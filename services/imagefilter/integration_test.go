package imagefilter

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func nativeConfig(tb testing.TB) Config {
	tb.Helper()
	dir := os.Getenv("IMAGE_FILTER_TEST_MODEL")
	if dir == "" {
		tb.Skip("set IMAGE_FILTER_TEST_MODEL to run native model tests")
	}
	v := viper.New()
	v.Set("image.filter.model", dir)
	v.Set("image.filter.library", os.Getenv("IMAGE_FILTER_TEST_LIBRARY"))
	return Configuration(v)
}

func TestNativeSupportedFormatSources(t *testing.T) {
	s := nativeService(t)
	formats := fixtureFormats
	files := make(map[string][]byte, len(formats))
	for _, format := range formats {
		files["/sample."+format] = formatFixture(t, "sample."+format)
	}
	server := imageFixtureServer(t, files)
	// Permit only this test fixture transport to reach localhost. Production
	// public-address validation remains covered by TestURLSafety.
	s.http.SetTransport(server.Client().Transport)
	for _, format := range formats {
		t.Run(format, func(t *testing.T) {
			assertImageSourceParity(t, s, files["/sample."+format], server.URL+"/sample."+format)
		})
	}
}

func imageFixtureServer(t *testing.T, files map[string][]byte) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := files[r.URL.Path]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		if _, err := w.Write(data); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func assertImageSourceParity(t *testing.T, s *Service, data []byte, url string) {
	t.Helper()
	fromFile, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	fromURL, err := s.Check(t.Context(), Source{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.SHA256 != fromURL.SHA256 || fromFile.Label != fromURL.Label || fromFile.Confidence != fromURL.Confidence {
		t.Fatalf("file/URL result differs: %+v %+v", fromFile, fromURL)
	}
}

func nativeService(tb testing.TB) *Service {
	tb.Helper()
	c := nativeConfig(tb)
	return nativeServiceWithPlan(tb, c, automaticPlan(c, resource.Detect()))
}

func nativeServiceWithPlan(tb testing.TB, c Config, plan executionPlan) *Service {
	tb.Helper()
	service, err := newWithPlan(c, plan)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			tb.Error(closeErr)
		}
	})
	return service
}

func nativeImage(tb testing.TB, s *Service) []byte {
	tb.Helper()
	return nativeFixture(tb, s, "000_source.png")
}

func nativeFixture(tb testing.TB, s *Service, name string) []byte {
	tb.Helper()
	root, err := os.OpenRoot(filepath.Join(s.config.Model, "fixtures"))
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

func TestNativeFixtures(t *testing.T) {
	s := nativeService(t)
	results, err := s.Validate(t.Context())
	if err != nil {
		t.Fatalf("fixture comparison: %+v: %v", results, err)
	}
	for _, result := range results {
		if !result.Passed || result.TensorMaxError > tensorTolerance {
			t.Errorf("fixture mismatch: %+v", result)
		}
	}
}

func TestNativeCacheIsolationAndFileURLPrecedence(t *testing.T) {
	s := nativeService(t)
	data := nativeImage(t, s)
	first, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(data), URL: "https://127.0.0.1/metadata"})
	if err != nil {
		t.Fatal(err)
	}
	// Hold every native tensor: a cache hit must complete without running inference.
	var held []*tensorSlot
	for range s.plan.concurrency {
		held = append(held, <-s.engine.slots)
	}
	defer func() {
		for _, slot := range held {
			s.engine.slots <- slot
		}
	}()
	want := first.Confidence
	first.Labels[0], first.Probabilities[0] = "mutated", -1
	second, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(data), URL: "https://example.com/other"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Confidence != want || second.Labels[0] == "mutated" || second.Probabilities[0] < 0 || second.URL != "https://example.com/other" {
		t.Fatalf("mutable cache or URL leakage: %+v", second)
	}
}

func TestNativeConcurrentAndCancellation(t *testing.T) {
	s := nativeService(t)
	data := nativeImage(t, s)
	errorsChannel := make(chan error, s.plan.requests)
	group := xutil.WaitGroup("image_filter_concurrent_test")
	for range s.plan.requests {
		group.Go(t.Context(), func(ctx context.Context) {
			_, err := s.Check(ctx, Source{Reader: bytes.NewReader(data)})
			errorsChannel <- err
		})
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Error(err)
		}
	}
	testNativeCancellation(t, s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(data)}); !errors.Is(err, ErrClosed) {
		t.Fatalf("call after shutdown: %v", err)
	}
}

func testNativeCancellation(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	slot := <-s.engine.slots
	defer func() { s.engine.slots <- slot }()
	if _, err := s.engine.run(ctx, slot); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := s.engine.run(t.Context(), slot); err != nil {
		t.Fatalf("canceled run poisoned tensor slot: %v", err)
	}
}

func TestAdmissionAndShutdown(t *testing.T) {
	t.Parallel()
	s := &Service{requests: make(chan struct{}, 1)}
	if err := s.admit(); err != nil {
		t.Fatal(err)
	}
	if err := s.admit(); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected backpressure: %v", err)
	}
	<-s.requests
	s.active.Done()
	s.closed = true
	if err := s.admit(); !errors.Is(err, ErrClosed) {
		t.Fatalf("expected shutdown rejection: %v", err)
	}
}

func BenchmarkCheck(b *testing.B) {
	for _, cached := range []bool{false, true} {
		name := "inference"
		if cached {
			name = "cached"
		}
		b.Run(name, func(b *testing.B) { benchmarkCheck(b, cached) })
	}
}

func benchmarkCheck(b *testing.B, cached bool) {
	b.Helper()
	c := nativeConfig(b)
	if !cached {
		c.CacheEntries = 0
	}
	s, err := New(c)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Error(closeErr)
		}
	})
	data := nativeImage(b, s)
	b.ReportAllocs()
	for b.Loop() {
		if _, checkErr := s.Check(b.Context(), Source{Reader: bytes.NewReader(data)}); checkErr != nil {
			b.Fatal(checkErr)
		}
	}
}
