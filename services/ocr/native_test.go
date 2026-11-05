package ocr

import (
	"bytes"
	"context"
	"errors"
	"image"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func nativeConfiguration(tb testing.TB) (Config, string) {
	tb.Helper()
	dir := os.Getenv("OCR_TEST_DIR")
	if dir == "" {
		tb.Skip("set OCR_TEST_DIR to a directory with det.onnx, rec.onnx, dict.txt and sample.png")
	}
	c := Configuration(viper.New())
	c.DetectionModel = filepath.Join(dir, "det.onnx")
	c.RecognitionModel = filepath.Join(dir, "rec.onnx")
	c.Dictionary = filepath.Join(dir, "dict.txt")
	c.Library = os.Getenv("OCR_TEST_LIBRARY")
	return c, dir
}

func nativeService(tb testing.TB) (*Service, string) {
	tb.Helper()
	c, dir := nativeConfiguration(tb)
	return newNativeService(tb, c), dir
}

func newNativeService(tb testing.TB, c Config) *Service {
	tb.Helper()
	return newNativeServiceWithPlan(tb, c, automaticPlan(c, resource.Detect()))
}

func newNativeServiceWithPlan(tb testing.TB, c Config, plan executionPlan) *Service {
	tb.Helper()
	s, err := newWithPlan(c, plan)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			tb.Error(closeErr)
		}
	})
	return s
}

func legacyPlan(threads int) executionPlan {
	return executionPlan{cpus: threads, detectionThreads: threads, recognitionThreads: threads, lineWorkers: 1, images: 1, requests: 4}
}

func TestNativeMediumRecognition(t *testing.T) {
	s, dir := nativeService(t)
	data := nativeRead(t, dir, "sample.png")
	result, err := s.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(result.Text)
	if !strings.Contains(result.Text, "BOARDING") || !strings.Contains(result.Text, "PASS") || !strings.Contains(result.Text, "登机牌") {
		t.Fatalf("unexpected text: %q", result.Text)
	}
	if result.Width != 896 || result.Height != 528 || len(result.Lines) < 10 {
		t.Fatalf("incomplete result: %+v", result)
	}
}

func TestNativeConcurrentRequestsAndCancellation(t *testing.T) {
	s, dir := nativeService(t)
	data := nativeRead(t, dir, "sample.png")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Recognize(ctx, imageinput.Source{Reader: bytes.NewReader(data)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request: %v", err)
	}
	group := xutil.ErrGroup(t.Context(), "ocr_native_test").Limit(2)
	for range 2 {
		group.Go(func(ctx context.Context) error {
			_, recognizeErr := s.Recognize(ctx, imageinput.Source{Reader: bytes.NewReader(data)})
			return recognizeErr
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Recognize(t.Context(), imageinput.Source{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed request: %v", err)
	}
}

func TestNativeSharedEnvironment(t *testing.T) {
	s, dir := nativeService(t)
	if filterDir := os.Getenv("IMAGE_FILTER_TEST_MODEL"); filterDir != "" {
		v := viper.New()
		v.Set("image.filter.model", filterDir)
		v.Set("image.filter.library", os.Getenv("OCR_TEST_LIBRARY"))
		filter, initErr := imagefilter.New(imagefilter.Configuration(v))
		if initErr != nil {
			t.Fatal(initErr)
		}
		if closeErr := filter.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}

	data := nativeRead(t, dir, "sample.png")
	if _, err := s.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
		t.Fatal(err)
	}
}

func nativeRead(tb testing.TB, dir, name string) []byte {
	tb.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		tb.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			tb.Error(closeErr)
		}
	}()
	data, err := root.ReadFile(name)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func TestNativeSupportedFormats(t *testing.T) {
	s, _ := nativeService(t)
	for _, format := range []string{"jpeg", "webp", "png", "avif"} {
		data := nativeRead(t, "../imagefilter/testdata", "sample."+format)
		if _, err := s.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
			t.Fatalf("%s: %v", format, err)
		}
	}
}

func TestNativeRunCancellationAndReuse(t *testing.T) {
	s, _ := nativeService(t)
	input, shape, err := detectionTensor(t.Context(), image.NewRGBA(image.Rect(0, 0, 960, 960)), 960)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, _, err = s.detector.run(ctx, input, shape); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("native termination: %v", err)
	}
	if _, _, err = s.detector.run(t.Context(), input, shape); err != nil {
		t.Fatalf("reuse after termination: %v", err)
	}
}

func TestNativeCloseCancelsDownload(t *testing.T) {
	s, _ := nativeService(t)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { close(started); <-r.Context().Done() }))
	t.Cleanup(server.Close)
	finished := make(chan error, 1)
	group := xutil.WaitGroup("ocr_close_test")
	group.Go(t.Context(), func(ctx context.Context) {
		_, err := s.Recognize(ctx, imageinput.Source{URL: server.URL})
		finished <- err
	})
	<-started
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	group.Wait()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("shutdown did not cancel download: %v", err)
	}
}

func TestNativeRunOutputOwnership(t *testing.T) {
	s, _ := nativeService(t)
	input := make([]float32, 3*32*32)
	values, shape, err := s.detector.run(t.Context(), input, ort.Shape{1, 3, 32, 32})
	if err != nil {
		t.Fatal(err)
	}
	want := slices.Clone(values)
	runtime.GC()
	if _, _, err = s.detector.run(t.Context(), input, ort.Shape{1, 3, 32, 32}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(values, want) || len(shape) != 4 {
		t.Fatal("returned Go data or shape was invalidated by native cleanup or the next run")
	}
}

func TestNativeAutomaticExecutionParity(t *testing.T) {
	config, dir := nativeConfiguration(t)
	data := nativeRead(t, dir, "sample.png")
	single := newNativeServiceWithPlan(t, config, legacyPlan(4))
	want, err := single.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	if err = single.Close(); err != nil {
		t.Fatal(err)
	}
	parallel := newNativeService(t, config)
	got, err := parallel.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	assertResultParity(t, got, want)
}

func assertResultParity(t *testing.T, got, want Result) {
	t.Helper()
	if got.Text != want.Text || got.Width != want.Width || got.Height != want.Height || len(got.Lines) != len(want.Lines) {
		t.Fatal("thread setting changed recognized text, dimensions or line count")
	}
	for i, line := range got.Lines {
		if line.Text != want.Lines[i].Text || line.Box != want.Lines[i].Box || math.Abs(line.Confidence-want.Lines[i].Confidence) > 1e-5 {
			t.Fatalf("thread setting changed line %d: %+v vs %+v", i, line, want.Lines[i])
		}
	}
}

func TestNativeAutomaticBurstBeyondFour(t *testing.T) {
	s, _ := nativeService(t)
	if s.plan.requests < 6 {
		t.Skip("effective CPU/memory budget admits fewer than six calls")
	}
	image := nativeRead(t, "../imagefilter/testdata", "sample.avif")
	group := xutil.ErrGroup(t.Context(), "ocr_auto_burst_test").Limit(6)
	for range 6 {
		group.Go(func(ctx context.Context) error {
			_, err := s.Recognize(ctx, imageinput.Source{Reader: bytes.NewReader(image)})
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLineFailureDrainsAndAllowsReuse(t *testing.T) {
	s, dir := nativeService(t)
	img := image.NewRGBA(image.Rect(0, 0, 1000, 20))
	width := float64(img.Bounds().Dx() - 1)
	boxes := []Box{{{}, {X: 50}, {X: 50, Y: 19}, {Y: 19}}, {{}, {X: width}, {X: width, Y: 19}, {Y: 19}}}
	if _, err := s.recognizeBoxes(t.Context(), img, boxes); !errors.Is(err, imageinput.ErrTooLarge) {
		t.Fatalf("line width classification lost: %v", err)
	}
	data := nativeRead(t, dir, "sample.png")
	if _, err := s.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
		t.Fatalf("line failure leaked permits or poisoned native state: %v", err)
	}
}
