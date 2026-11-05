package ocr

import (
	"context"
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
)

func TestConfigurationAndDictionary(t *testing.T) {
	t.Parallel()
	v := viper.New()
	c := Configuration(v)
	if !strings.Contains(c.DetectionModel, "medium") || c.validate() != nil {
		t.Fatalf("default configuration: %+v", c)
	}
	v.Set("image.ocr.detectionModel", "custom-det.onnx")
	v.Set("image.ocr.recognitionModel", "custom-rec.onnx")
	v.Set("image.ocr.dictionary", "custom.txt")
	c = Configuration(v)
	if c.DetectionModel != "custom-det.onnx" || c.RecognitionModel != "custom-rec.onnx" || c.Dictionary != "custom.txt" {
		t.Fatal("deployment paths ignored")
	}
	for key, value := range map[string]any{"maxBytes": 0, "timeout": "0s", "detectionMaxSide": 31, "maxLines": 4000, "minConfidence": math.NaN()} {
		settings := viper.New()
		settings.Set("image.ocr."+key, value)
		if err := Configuration(settings).validate(); !errors.Is(err, ErrConfig) {
			t.Errorf("%s accepted: %v", key, err)
		}
	}
	testDictionary(t)
}

func testDictionary(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dict.txt")
	if err := os.WriteFile(path, []byte("中\r\nA\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tokens, err := loadDictionary(path, "")
	if err != nil || strings.Join(tokens, "|") != "|中|A| " {
		t.Fatalf("dictionary: %v %v", tokens, err)
	}
	if _, err = loadDictionary(path, strings.Repeat("0", 64)); !errors.Is(err, ErrModel) {
		t.Fatalf("checksum: %v", err)
	}
	if err = os.WriteFile(path, []byte("A\n\nB"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = loadDictionary(path, ""); !errors.Is(err, ErrModel) {
		t.Fatalf("empty token: %v", err)
	}
}

const testProbability = 0.9

func TestCTCBlankRepeatsAndInvalidOutput(t *testing.T) {
	t.Parallel()
	dict := []string{"", "中", "A", " "}
	values := make([]float32, 7*len(dict))
	for step, token := range []int{1, 1, 0, 1, 3, 2, 2} {
		values[step*len(dict)+token] = testProbability
	}
	text, confidence, err := decodeCTC(values, ort.Shape{1, 7, 4}, dict)
	if err != nil || text != "中中 A" || math.Abs(confidence-testProbability) > 1e-6 {
		t.Fatalf("CTC: %q %v %v", text, confidence, err)
	}
	for _, shape := range []ort.Shape{{1, 7, 3}, {2, 7, 4}, {1, 8, 4}, {1, 0, 4}} {
		if _, _, err = decodeCTC(values, shape, dict); !errors.Is(err, ErrInference) {
			t.Fatalf("accepted shape: %v", shape)
		}
	}
	values[0] = float32(math.NaN())
	if _, _, err = decodeCTC(values, ort.Shape{1, 7, 4}, dict); !errors.Is(err, ErrInference) {
		t.Fatalf("accepted NaN: %v", err)
	}
}

func TestDetectionCoordinatesAndCandidateLimit(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	values := make([]float32, 32*32)
	for y := 8; y <= 15; y++ {
		for x := 4; x <= 24; x++ {
			values[y*32+x] = testProbability
		}
	}
	boxes, err := detectionBoxes(t.Context(), values, ort.Shape{1, 1, 32, 32}, 320, 320, c)
	if err != nil || len(boxes) != 1 {
		t.Fatalf("boxes: %v %v", boxes, err)
	}
	if boxes[0][0].X >= 40 || boxes[0][2].X <= 240 {
		t.Fatalf("box not expanded and scaled: %v", boxes[0])
	}
	c.MaxCandidates = 1
	c.MaxLines = 1
	values[30*32+30] = testProbability
	if _, err = detectionBoxes(t.Context(), values, ort.Shape{1, 1, 32, 32}, 320, 320, c); !errors.Is(err, imageinput.ErrTooLarge) {
		t.Fatalf("candidate bound: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = detectionBoxes(ctx, values, ort.Shape{1, 1, 32, 32}, 320, 320, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("postprocess cancellation: %v", err)
	}
}

func TestPerspectiveAndBGRNormalization(t *testing.T) {
	t.Parallel()
	box := Box{{X: 2, Y: 3}, {X: 12, Y: 4}, {X: 11, Y: 14}, {X: 1, Y: 12}}
	transform := projective(box)
	for i, uv := range [4]Point{{}, {X: 1}, {X: 1, Y: 1}, {Y: 1}} {
		if p := transform.at(uv.X, uv.Y); distance(p, box[i]) > 1e-8 {
			t.Fatalf("corner %d: %v", i, p)
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := range 32 {
		for x := range 32 {
			img.SetRGBA(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	values, _, err := detectionTensor(t.Context(), img, 32)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(float64(values[0])-(-0.485/0.229)) > 1e-5 || values[2*32*32] < 2 {
		t.Fatal("detection does not use BGR ImageNet normalization")
	}
}

func TestRecognitionWidthAndPadding(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 1000, 20))
	width := float64(img.Bounds().Dx() - 1)
	box := Box{{}, {X: width}, {X: width, Y: 19}, {Y: 19}}
	if _, _, err := recognitionTensor(t.Context(), img, box, 48, 320); !errors.Is(err, imageinput.ErrTooLarge) {
		t.Fatalf("line width limit: %v", err)
	}
	box = Box{{}, {X: 50}, {X: 50, Y: 19}, {Y: 19}}
	values, shape, err := recognitionTensor(t.Context(), img, box, 48, 320)
	if err != nil || shape[3] != 320 {
		t.Fatalf("recognition shape: %v %v", shape, err)
	}
	if values[0] != -1 || values[319] != 0 {
		t.Fatal("recognition padding must be normalized zero")
	}
}

func TestDegenerateAndRotatedBoxes(t *testing.T) {
	t.Parallel()
	collapsed := Box{{}, {X: 10}, {X: 10}, {}}
	if _, _, err := recognitionTensor(t.Context(), image.NewRGBA(image.Rect(0, 0, 20, 20)), collapsed, 48, 320); !errors.Is(err, ErrInference) {
		t.Fatalf("accepted collapsed quadrilateral: %v", err)
	}
	points := []Point{{X: 0, Y: 10}, {X: 10, Y: 0}, {X: 30, Y: 20}, {X: 20, Y: 30}, {X: 10, Y: 10}}
	rect, ok := minimumRectangle(points)
	if !ok || math.Abs((rect.maxU-rect.minU)*(rect.maxV-rect.minV)-400) > 1e-7 {
		t.Fatalf("rotated minimum rectangle: %+v", rect)
	}
	if !validBox(rect.box(0)) {
		t.Fatal("rotated box corner order is invalid")
	}
}

func TestAdmissionBoundAndRelease(t *testing.T) {
	t.Parallel()
	service := &Service{calls: make(map[*call]struct{}), config: Configuration(viper.New())}
	service.plan.requests = 1
	ctx, finish, err := service.admit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.admit(t.Context()); !errors.Is(err, ErrBusy) {
		t.Fatalf("missing backpressure: %v", err)
	}
	finish()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("request context remains alive after completion")
	}
	_, release, err := service.admit(t.Context())
	if err != nil {
		t.Fatalf("capacity not returned: %v", err)
	}
	release()
	service.active.Wait()
	service.closed = true
	if _, _, err = service.admit(t.Context()); !errors.Is(err, ErrClosed) {
		t.Fatalf("accepted admission after shutdown: %v", err)
	}
}

func TestLegacyExecutionSettingsAreIgnored(t *testing.T) {
	t.Parallel()
	v := viper.New()
	for key, value := range map[string]int{"concurrency": 1, "threads": 4, "maxRequests": 4} {
		v.Set("image.ocr."+key, value)
	}
	got, want := Configuration(v), Configuration(viper.New())
	if got != want {
		t.Fatal("legacy execution settings still affect OCR configuration")
	}
}
