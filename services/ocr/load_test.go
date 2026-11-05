//go:build linux || darwin

package ocr

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/xutil"
)

// BenchmarkOCRLoad measures complete requests under one and multiple callers.
// The mixed corpus includes a dense document and a small, empty-text AVIF.
func BenchmarkOCRLoad(b *testing.B) {
	for _, callers := range []int{1, 4} {
		b.Run("callers_"+strconv.Itoa(callers), func(b *testing.B) { benchmarkOCRLoad(b, callers, false) })
	}
}

func benchmarkOCRLoad(b *testing.B, callers int, baseline bool) {
	b.Helper()
	config, dir := nativeConfiguration(b)
	var service *Service
	if baseline {
		service = newNativeServiceWithPlan(b, config, legacyPlan(4))
	} else {
		service = newNativeService(b, config)
	}
	images := [][]byte{nativeRead(b, dir, "sample.png"), nativeRead(b, "../imagefilter/testdata", "sample.avif")}
	if _, err := service.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(images[0])}); err != nil {
		b.Fatal(err)
	}
	durations := make([]time.Duration, 0)
	b.ReportAllocs()
	cpuStart, err := processCPUTime()
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		batch, loadErr := runOCRLoad(b.Context(), service, images, callers)
		if loadErr != nil {
			b.Fatal(loadErr)
		}
		durations = append(durations, batch...)
	}
	b.StopTimer()
	reportOCRLatencies(b, durations)
	b.ReportMetric(float64(len(durations))/b.Elapsed().Seconds(), "images/s")
	cpuEnd, err := processCPUTime()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(100*float64(cpuEnd-cpuStart)/float64(b.Elapsed()), "cpu-%")
}

func runOCRLoad(ctx context.Context, service *Service, images [][]byte, callers int) ([]time.Duration, error) {
	durations := make([]time.Duration, callers)
	group := xutil.ErrGroup(ctx, "ocr_load_test").Limit(callers)
	for index := range callers {
		group.Go(func(ctx context.Context) error {
			start := time.Now()
			_, err := service.Recognize(ctx, imageinput.Source{Reader: bytes.NewReader(images[index%len(images)])})
			durations[index] = time.Since(start)
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return nil, errors.Join(ErrInference, err)
	}
	return durations, nil
}

func reportOCRLatencies(b *testing.B, durations []time.Duration) {
	b.Helper()
	slices.Sort(durations)
	for name, quantile := range map[string]float64{"p50-ms": 0.5, "p95-ms": 0.95, "p99-ms": 0.99} {
		index := max(0, int(math.Ceil(float64(len(durations))*quantile))-1)
		b.ReportMetric(float64(durations[index])/float64(time.Millisecond), name)
	}
}

func BenchmarkOCRComparison(b *testing.B) {
	for _, baseline := range []bool{true, false} {
		name := "automatic"
		if baseline {
			name = "serial4"
		}
		b.Run(name, func(b *testing.B) {
			for _, callers := range []int{1, 4} {
				b.Run("callers_"+strconv.Itoa(callers), func(b *testing.B) { benchmarkOCRLoad(b, callers, baseline) })
			}
		})
	}
}

func processCPUTime() (time.Duration, error) {
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		return 0, errors.Join(ErrInference, err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano()), nil
}
