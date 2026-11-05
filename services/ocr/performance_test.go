package ocr

import (
	"bytes"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/resource"
)

// BenchmarkRecognize includes image decoding, native detection, all text lines
// and result assembly. Fixed iteration counts make before/after runs comparable.
func BenchmarkRecognize(b *testing.B) {
	for _, threads := range []int{0, 1, 2, 4} {
		name := "automatic"
		if threads > 0 {
			name = "serial_threads_" + strconv.Itoa(threads)
		}
		b.Run(name, func(b *testing.B) { benchmarkRecognize(b, threads) })
	}
}

func benchmarkRecognize(b *testing.B, threads int) {
	b.Helper()
	c, dir := nativeConfiguration(b)
	plan := automaticPlan(c, resource.Detect())
	if threads > 0 {
		plan = legacyPlan(threads)
	}
	s := newNativeServiceWithPlan(b, c, plan)
	data := nativeRead(b, dir, "sample.png")
	if _, err := s.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
		b.Fatal(err)
	}
	durations := make([]time.Duration, 0)
	b.ReportAllocs()
	for b.Loop() {
		start := time.Now()
		if _, err := s.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
			b.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(durations)
	for name, quantile := range map[string]float64{"p50-ms": 0.5, "p95-ms": 0.95, "p99-ms": 0.99} {
		index := max(0, int(math.Ceil(float64(len(durations))*quantile))-1)
		b.ReportMetric(float64(durations[index])/float64(time.Millisecond), name)
	}
}
