package imagefilter

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"image"
	"math"
	"testing"

	"github.com/rs/zerolog"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/imagepipeline"
	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func TestPreprocessorConfiguration(t *testing.T) {
	t.Parallel()
	v := viper.New()
	v.Set("image.filter.model", "/model")
	want := Configuration(v)
	for _, backend := range []string{string(Libvips), backendGo, "unknown"} {
		v.Set("image.filter.preprocessBackend", backend)
		if got := Configuration(v); got != want {
			t.Fatal("removed configuration still selects a global backend")
		}
	}
	if _, err := ParseBackend("unknown"); !errors.Is(err, ErrConfig) {
		t.Fatal("unknown CLI backend accepted")
	}
}

func TestGoPreparationUsesGoJPEGAndAllFormats(t *testing.T) {
	t.Parallel()
	img, err := decodeImage(t.Context(), formatFixture(t, "sample.jpeg"), 48*32)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := img.(*image.YCbCr); !ok {
		t.Fatalf("Go JPEG path used a different decoder: %T", img)
	}
	for _, format := range fixtureFormats {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			assertGoPreparation(t, format)
		})
	}
}

func assertGoPreparation(t *testing.T, format string) {
	t.Helper()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	ctx, trace := imagetiming.New(output.WithContext(t.Context()))
	s := &Service{config: Config{MaxPixels: 48 * 32}, engine: &engine{slots: make(chan *tensorSlot)}}
	tensor := make([]float32, inputLength)
	if err := s.WithBackend(Go).prepare(ctx, formatFixture(t, "sample."+format), tensor); err != nil {
		t.Fatal(err)
	}
	for _, value := range tensor {
		if math.IsNaN(float64(value)) || value < 0 || value > 1 {
			t.Fatal("invalid Go tensor")
		}
	}
	trace.Finish(ctx, true, 0)
	var fields map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["decode_backend"] != backendGo || fields["image_format"] != format {
		t.Fatalf("wrong decoder: %+v", fields)
	}
	if _, exists := fields["native_pipeline_ms"]; exists {
		t.Fatal("Go backend executed native image pipeline")
	}
}

func TestGoPreparationCancellationAndTensorBounds(t *testing.T) {
	t.Parallel()
	data := formatFixture(t, "sample.jpeg")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := preprocessWithBudget(ctx, data, 48*32, make([]float32, inputLength), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if err := preprocessWithBudget(t.Context(), data, 48*32, nil, nil); !errors.Is(err, ErrPreprocess) {
		t.Fatalf("invalid destination accepted: %v", err)
	}
}

func TestPredictionIdentitySeparatesPreprocessors(t *testing.T) {
	t.Parallel()
	s := &Service{modelSHA256: "same-model"}
	native, comparison := s.WithBackend(Libvips), s.WithBackend(Go)
	if native.PredictionSHA256() != s.ModelSHA256() || native.service != comparison.service {
		t.Fatal("existing model identity changed")
	}
	if comparison.PredictionSHA256() == native.PredictionSHA256() || len(comparison.PredictionSHA256()) != 64 {
		t.Fatal("comparison collection identity is not isolated")
	}
}

func TestGoPreprocessorKeepsModelValidation(t *testing.T) {
	t.Parallel()
	m := &manifest{Preprocessing: expectedPreprocessing()}
	m.Preprocessing.Profile = goPreprocessProfile
	if err := m.Preprocessing.validate(); !errors.Is(err, ErrModel) {
		t.Fatal("comparison weakened model manifest validation")
	}
	if Go.preprocessProfile() == imagepipeline.Profile {
		t.Fatal("Go preprocessing misrepresented the training contract")
	}
}

func TestNativeGoComparisonInference(t *testing.T) {
	requireNativePipeline(t)
	c := nativeConfig(t)
	c.BypassCache = true
	s := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	detector := s.WithBackend(Go)
	for _, format := range fixtureFormats {
		data := formatFixture(t, "sample."+format)
		if _, err := detector.Check(t.Context(), Source{Reader: bytes.NewReader(data)}); err != nil {
			t.Fatal(err)
		}
	}
	results, err := detector.Validate(t.Context())
	if !errors.Is(err, ErrValidation) || len(results) == 0 {
		t.Fatalf("Go path incorrectly passed libvips fixture validation: %v", err)
	}
	if s.engine.runs.Load() <= uint64(len(fixtureFormats)) || len(s.cache.items) != 0 {
		t.Fatal("comparison did not execute uncached inference")
	}
}

func TestNativeMixedBackendCacheIsolation(t *testing.T) {
	c := nativeConfig(t)
	c.CacheEntries = 2
	s := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	data := formatFixture(t, "sample.jpeg")
	runMixedBackendRequests(t, s, data)
	if s.engine.runs.Load() != 2 || len(s.cache.items) != 2 || len(s.cache.pending) != 0 {
		t.Fatal("backends did not retain independent cached results")
	}
	if len(s.engine.slots) != s.plan.concurrency || len(s.requests) != 0 {
		t.Fatal("shared resource budget was not returned")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	assertClosedBackends(t, s)
}

func runMixedBackendRequests(t *testing.T, s *Service, data []byte) {
	t.Helper()
	group := xutil.ErrGroup(t.Context(), "image_filter_backend_cache_test").Limit(min(8, s.plan.requests))
	for i := range 8 {
		backend := Libvips
		if i%2 != 0 {
			backend = Go
		}
		group.Go(func(ctx context.Context) error {
			detector := s.WithBackend(backend)
			result, err := detector.Check(ctx, Source{Reader: bytes.NewReader(data)})
			if err == nil && result.PredictionSHA256 != detector.PredictionSHA256() {
				t.Error("cache or in-flight work leaked across backends")
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func assertClosedBackends(t *testing.T, s *Service) {
	t.Helper()
	for _, backend := range []Backend{Libvips, Go} {
		if _, err := s.WithBackend(backend).Check(t.Context(), Source{}); !errors.Is(err, ErrClosed) {
			t.Fatal("backend outlived its owning service")
		}
	}
}

func TestBackendsShareAdmission(t *testing.T) {
	t.Parallel()
	s := &Service{requests: make(chan struct{}, 1)}
	if err := s.admit(); err != nil {
		t.Fatal(err)
	}
	defer func() { <-s.requests; s.active.Done() }()
	for _, backend := range []Backend{Libvips, Go} {
		if _, err := s.WithBackend(backend).Check(t.Context(), Source{}); !errors.Is(err, ErrBusy) {
			t.Fatal("backend bypassed the shared admission limit")
		}
	}
}
