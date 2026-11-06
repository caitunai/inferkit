//go:build linux || darwin

package imagefilter

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

	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func BenchmarkFilterLoad(b *testing.B) {
	c := nativeConfig(b)
	c.CacheEntries = 0
	plans := []struct {
		name string
		plan executionPlan
	}{{"legacy", executionPlan{cpus: 2, threads: 1, concurrency: 2, requests: 8}}, {"automatic", automaticPlan(c, resource.Detect())}}
	for _, item := range plans {
		for _, callers := range []int{1, 8} {
			b.Run(item.name+"/callers_"+strconv.Itoa(callers), func(b *testing.B) {
				service := nativeServiceWithPlan(b, c, item.plan)
				benchmarkFilterLoad(b, service, callers)
			})
		}
	}
}

func benchmarkFilterLoad(b *testing.B, service *Service, callers int) {
	b.Helper()
	images := make([][]byte, 8)
	for i := range images {
		data := nativeFixture(b, service, "00"+strconv.Itoa(i)+"_source.png")
		images[i] = data
	}
	benchmarkFilterImages(b, service, images, callers)
}

type imageDetector interface {
	Check(context.Context, Source) (Result, error)
}

func benchmarkFilterImages(b *testing.B, service imageDetector, images [][]byte, callers int) {
	b.Helper()
	for _, data := range images {
		if _, err := service.Check(b.Context(), Source{Reader: bytes.NewReader(data)}); err != nil {
			b.Fatal(err)
		}
	}
	latencies := make([]time.Duration, 0)
	cpuStart := filterCPUTime(b)
	b.ReportAllocs()
	for b.Loop() {
		times, err := filterLoad(b.Context(), service, images, callers)
		if err != nil {
			b.Fatal(err)
		}
		latencies = append(latencies, times...)
	}
	b.StopTimer()
	cpu := filterCPUTime(b) - cpuStart
	reportFilterLatencies(b, latencies)
	b.ReportMetric(float64(len(latencies))/b.Elapsed().Seconds(), "images/s")
	b.ReportMetric(100*float64(cpu)/float64(b.Elapsed()), "cpu-%")
}

func BenchmarkFilterSizedLoad(b *testing.B) {
	c := nativeConfig(b)
	c.CacheEntries = 0
	cpus := resource.Detect().CPUs
	images := make([][]byte, 8)
	for i := range images {
		images[i] = profilePNG(b, byte(i))
	}
	candidates := []int{1, min(2, cpus), min(3, cpus), min(4, cpus), cpus}
	slices.Sort(candidates)
	for _, threads := range slices.Compact(candidates) {
		workers := max(1, cpus-threads+1)
		plan := executionPlan{cpus: cpus, threads: threads, concurrency: workers, requests: max(8, workers+cpus)}
		for _, callers := range []int{1, 8} {
			b.Run("threads_"+strconv.Itoa(threads)+"/callers_"+strconv.Itoa(callers), func(b *testing.B) {
				service := nativeServiceWithPlan(b, c, plan)
				benchmarkFilterImages(b, service, images, callers)
			})
		}
	}
}

func filterLoad(ctx context.Context, service imageDetector, images [][]byte, callers int) ([]time.Duration, error) {
	times := make([]time.Duration, callers)
	group := xutil.ErrGroup(ctx, "image_filter_load_test").Limit(callers)
	for i := range callers {
		group.Go(func(ctx context.Context) error {
			start := time.Now()
			_, err := service.Check(ctx, Source{Reader: bytes.NewReader(images[i%len(images)])})
			times[i] = time.Since(start)
			if err != nil {
				return errors.Join(ErrInference, err)
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, errors.Join(ErrInference, err)
	}
	return times, nil
}

func filterCPUTime(b *testing.B) time.Duration {
	b.Helper()
	var usage unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &usage); err != nil {
		b.Fatal(err)
	}
	return time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
}

func reportFilterLatencies(b *testing.B, latencies []time.Duration) {
	b.Helper()
	slices.Sort(latencies)
	for _, q := range []int{50, 95, 99} {
		index := max(0, int(math.Ceil(float64(len(latencies))*float64(q)/100))-1)
		b.ReportMetric(float64(latencies[index])/float64(time.Millisecond), "p"+strconv.Itoa(q)+"-ms")
	}
}

type namedExecutionPlan struct {
	name string
	plan executionPlan
}

func inferencePlans(c Config) []namedExecutionPlan {
	base := automaticPlan(c, resource.Detect())
	base.spinMicros, base.dynamicBlockBase = 0, 0
	dynamic, spin, both := base, base, base
	dynamic.dynamicBlockBase = 4
	spin.spinMicros = 1000
	both.spinMicros, both.dynamicBlockBase = 1000, 4
	return []namedExecutionPlan{{name: "park", plan: base}, {name: "dynamic4", plan: dynamic}, {name: "bounded_spin", plan: spin}, {name: "bounded_spin_dynamic4", plan: both}}
}

func BenchmarkInferencePolicies(b *testing.B) {
	c := nativeConfig(b)
	for _, policy := range inferencePlans(c) {
		b.Run(policy.name, func(b *testing.B) { benchmarkNativeInference(b, c, policy.plan) })
	}
}

func benchmarkNativeInference(b *testing.B, c Config, plan executionPlan) {
	b.Helper()

	s := nativeServiceWithPlan(b, c, plan)
	slot := <-s.engine.slots
	defer func() { s.engine.slots <- slot }()
	if err := preprocess(b.Context(), nativeImage(b, s), c.MaxPixels, slot.input.GetData()); err != nil {
		b.Fatal(err)
	}
	for range 5 {
		if _, err := s.engine.run(b.Context(), slot); err != nil {
			b.Fatal(err)
		}
	}
	latencies := make([]time.Duration, 0)
	cpuStart := filterCPUTime(b)
	b.ReportAllocs()
	for b.Loop() {
		start := time.Now()
		if _, err := s.engine.run(b.Context(), slot); err != nil {
			b.Fatal(err)
		}
		latencies = append(latencies, time.Since(start))
	}
	b.StopTimer()
	reportFilterLatencies(b, latencies)
	b.ReportMetric(100*float64(filterCPUTime(b)-cpuStart)/float64(b.Elapsed()), "cpu-%")
	idleStart := filterCPUTime(b)
	time.Sleep(50 * time.Millisecond)
	b.ReportMetric(float64(filterCPUTime(b)-idleStart)/float64(time.Millisecond), "idle-cpu-ms/50ms")
}

func BenchmarkFilterPolicies(b *testing.B) {
	c := nativeConfig(b)
	c.BypassCache = true
	images := make([][]byte, 8)
	for i := range images {
		images[i] = profilePNG(b, byte(i))
	}
	for _, policy := range inferencePlans(c) {
		for _, callers := range []int{1, 8} {
			b.Run(policy.name+"/callers_"+strconv.Itoa(callers), func(b *testing.B) {
				s := nativeServiceWithPlan(b, c, policy.plan)
				benchmarkFilterImages(b, s, images, callers)
			})
		}
	}
}
