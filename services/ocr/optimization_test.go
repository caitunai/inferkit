package ocr

import (
	"context"
	"errors"
	"image"
	"math"
	"slices"
	"testing"

	"github.com/spf13/viper"
	ort "github.com/yalue/onnxruntime_go"
)

func TestModelSizeAndIndependentProviders(t *testing.T) {
	t.Parallel()
	v := viper.New()
	v.Set("image.ocr.modelSize", "small")
	v.Set("image.ocr.provider", "auto")
	v.Set("image.ocr.detectionProvider", coreMLProvider)
	v.Set("image.ocr.recognitionProvider", cpuProvider)
	c := Configuration(v)
	if c.validate() != nil || c.DetectionModel != "models/pp-ocrv6-small/det.onnx" || c.Dictionary != "models/pp-ocrv6-small/dict.txt" || c.DetectionProvider != coreMLProvider || c.RecognitionProvider != cpuProvider {
		t.Fatalf("model family/provider selection lost: %+v", c)
	}
	v.Set("image.ocr.recognitionModel", "custom.onnx")
	if Configuration(v).RecognitionModel != "custom.onnx" {
		t.Fatal("custom model path was not preserved")
	}
	for key, value := range map[string]any{"modelSize": "tiny", "detectionProvider": "invalid", "recognitionProvider": "invalid", "warmupRuns": 11, "recognitionBatchSize": 17} {
		bad := viper.New()
		bad.Set("image.ocr."+key, value)
		if err := Configuration(bad).validate(); !errors.Is(err, ErrConfig) {
			t.Fatalf("invalid %s accepted: %v", key, err)
		}
	}
}

func TestOpenVINOIndependentStageSelection(t *testing.T) {
	t.Parallel()
	for _, providers := range [][2]string{{cpuProvider, openVINOProvider}, {openVINOProvider, cpuProvider}, {openVINOProvider, openVINOProvider}} {
		v := viper.New()
		v.Set("image.ocr.detectionProvider", providers[0])
		v.Set("image.ocr.recognitionProvider", providers[1])
		c := Configuration(v)
		if err := c.validate(); err != nil {
			t.Fatal(err)
		}
		if c.DetectionProvider != providers[0] || c.RecognitionProvider != providers[1] {
			t.Fatal("independent OpenVINO provider selection lost")
		}
	}
}

func TestSIMDProbabilitySemantics(t *testing.T) {
	t.Parallel()
	for _, length := range []int{0, 1, 3, 4, 5, 17, 18710} {
		values := make([]float32, length)
		for i := range values {
			values[i] = float32((i*17+i/7)%8) / 8
		}
		assertProbabilityKernels(t, values)
		for _, invalid := range []float32{float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1)), -0.1, 1.1} {
			for i := range min(length, 17) {
				changed := slices.Clone(values)
				changed[i] = invalid
				assertProbabilityKernels(t, changed)
			}
		}
	}
	assertProbabilityKernels(t, []float32{0, 1, 1, 0, 1, 1, 0, 1, 1})
}

func assertProbabilityKernels(t *testing.T, values []float32) {
	t.Helper()
	want, wantValid := scalarProbabilityArgmax(values)
	got, gotValid := probabilityArgmax(values)
	if got != want || gotValid != wantValid || probabilitiesValid(values) != scalarProbabilitiesValid(values) {
		t.Fatalf("SIMD changed validation or first maximum: %v %v vs %v %v", got, gotValid, want, wantValid)
	}
}

func TestEqualWidthBatchingAndPixelContract(t *testing.T) {
	t.Parallel()
	c := Configuration(viper.New())
	c.RecognitionBatchSize = 4
	s := &Service{config: c, plan: executionPlan{lineWorkers: 10, batchSize: 4}, recognizer: &model{input: ort.Shape{-1, 3, 48, -1}, output: ort.Shape{-1, -1, 4}}}
	short := Box{{}, {X: 50}, {X: 50, Y: 20}, {Y: 20}}
	long := Box{{}, {X: 200}, {X: 200, Y: 20}, {Y: 20}}
	boxes := []Box{short, long, short, short, long, short, short}
	batches, err := s.batches(boxes)
	if err != nil || len(batches) != 3 {
		t.Fatalf("equal-width grouping failed: %v %v", batches, err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 300, 50))
	for i := range img.Pix {
		img.Pix[i] = byte(i % 256)
	}
	for _, batch := range batches {
		assertBatchPixels(t, img, batch)
	}
	s.recognizer.input[0] = 1
	fixed, fixedErr := s.batches(boxes)
	if fixedErr != nil || len(fixed) != len(boxes) {
		t.Fatal("fixed-batch custom model lost singleton fallback")
	}
}

func assertBatchPixels(t *testing.T, img *image.RGBA, batch lineBatch) {
	t.Helper()
	g := batch.lines[0]
	data := make([]float32, int(g.shape(len(batch.lines)).FlattenedSize()))
	for i := range data {
		data[i] = 99
	}
	if err := fillBatch(t.Context(), img, batch, data); err != nil {
		t.Fatal(err)
	}
	length := len(data) / len(batch.lines)
	for i, line := range batch.lines {
		want, _, err := recognitionTensor(t.Context(), img, line.box, line.height, 2048)
		if err != nil || !slices.Equal(want, data[i*length:(i+1)*length]) {
			t.Fatal("batch changed sampling, padding or normalization", err)
		}
	}
}

func TestBatchCTCOrderAndInvalidRows(t *testing.T) {
	t.Parallel()
	batch := lineBatch{lines: make([]lineGeometry, 2), indices: []int{2, 0}}
	lines := make([]Line, 3)
	values := []float32{0, 1, 0, 0, 0, 1}
	shape := ort.Shape{2, 1, 3}
	if err := decodeBatch(values, shape, []string{"", "A", "B"}, batch, lines); err != nil || lines[0].Text != "B" || lines[2].Text != "A" {
		t.Fatal("batch changed reading order", err)
	}
	values[4] = float32(math.NaN())
	if !errors.Is(decodeBatch(values, shape, []string{"", "A", "B"}, batch, lines), ErrInference) {
		t.Fatal("batch accepted invalid probabilities")
	}
}

func TestCanceledOCRStartup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	s, err := NewContext(ctx, Configuration(viper.New()))
	if s != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled startup continued", err)
	}
}
