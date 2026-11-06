//go:build linux || darwin

package imagefilter

import (
	"testing"
	"time"
)

func TestNativeCoreMLCompilationCacheReuse(t *testing.T) {
	c := coreMLConfig(t)
	c.Provider, c.CoreMLCache = coreMLProvider, true
	first := validatedCoreMLCache(t, c)
	second := validatedCoreMLCache(t, c)
	if first != second {
		t.Fatal("identical model did not reuse its compiled directory")
	}
}

func validatedCoreMLCache(t *testing.T, c Config) string {
	t.Helper()
	s, err := NewContext(t.Context(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	if s.engine.managed.CacheDirectory() == "" {
		t.Skip("system CoreML cache unavailable or in use by a concurrent builder")
	}
	if _, err = s.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return s.engine.managed.CacheDirectory()
}

// BenchmarkCoreMLStartup separates session startup from the first user Run.
// Run with -benchtime=10x -count=3; it excludes HTTP and image preprocessing.
func BenchmarkCoreMLStartup(b *testing.B) {
	c := coreMLConfig(b)
	c.Provider = coreMLProvider
	for _, cache := range []bool{false, true} {
		for _, runs := range []int{0, 3} {
			c.CoreMLCache, c.WarmupRuns = cache, runs
			name := "uncached"
			if cache {
				name = "cached"
			}
			if runs > 0 {
				name += "/warm"
			} else {
				name += "/cold"
			}
			b.Run(name, func(b *testing.B) { benchmarkStartup(b, c) })
		}
	}
}

func benchmarkStartup(b *testing.B, c Config) {
	b.Helper()
	var startup time.Duration
	var latencies []time.Duration
	for b.Loop() {
		created, first := firstInference(b, c)
		startup += created
		latencies = append(latencies, first)
	}
	b.StopTimer()
	b.ReportMetric(float64(startup)/float64(len(latencies))/float64(time.Millisecond), "startup-ms")
	reportFilterLatencies(b, latencies)
}

func firstInference(b *testing.B, c Config) (time.Duration, time.Duration) {
	b.Helper()
	start := time.Now()
	s, err := NewContext(b.Context(), c)
	if err != nil {
		b.Fatal(err)
	}
	created := time.Since(start)
	slot := <-s.engine.slots
	for i := range slot.input.GetData() {
		slot.input.GetData()[i] = 1
	}
	start = time.Now()
	output, runErr := s.engine.run(b.Context(), slot)
	first := time.Since(start)
	validErr := validateProbabilities(output)
	s.engine.slots <- slot
	closeErr := s.Close()
	if runErr != nil || validErr != nil || closeErr != nil {
		b.Fatal(runErr, validErr, closeErr)
	}
	return created, first
}
