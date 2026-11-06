//go:build linux || darwin

package imagefilter

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"runtime"
	"strconv"
	"testing"

	"github.com/caitun/inferkit/services/onnx"
	"github.com/caitun/inferkit/services/resource"
	ort "github.com/yalue/onnxruntime_go"
)

func TestNativeWarmupCancellationAndReuse(t *testing.T) {
	c := nativeConfig(t)
	c.WarmupRuns = 3
	s := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	if s.engine.runs.Load() != 0 || len(s.engine.slots) != s.plan.concurrency {
		t.Fatal("startup consumed request attempts or leaked slots")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.engine.warmup(ctx, 3); !errors.Is(err, context.Canceled) {
		t.Fatalf("warmup cancellation lost: %v", err)
	}
	if _, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(nativeImage(t, s))}); err != nil {
		t.Fatalf("inference after warmup failed: %v", err)
	}
}

func coreMLConfig(tb testing.TB) Config {
	tb.Helper()
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" || os.Getenv("IMAGE_FILTER_TEST_COREML") != "1" {
		tb.Skip("set IMAGE_FILTER_TEST_COREML=1 on macOS ARM to test CoreML")
	}
	c := nativeConfig(tb)
	c.BypassCache = true
	return c
}

func TestNativeCoreMLParity(t *testing.T) {
	c := coreMLConfig(t)
	cpu := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	c.Provider = coreMLProvider
	accelerated := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	if accelerated.engine.provider != coreMLProvider {
		t.Fatal("CoreML was not registered")
	}
	var largest float64
	for _, data := range [][]byte{nativeImage(t, cpu), profilePNG(t, 0), formatFixture(t, "sample.jpeg")} {
		want, err := cpu.Check(t.Context(), Source{Reader: bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		got, err := accelerated.Check(t.Context(), Source{Reader: bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		largest = max(largest, compareBackendResult(t, got, want))
	}
	if largest > 0.001 {
		t.Fatalf("CoreML probability difference exceeds 0.001: %g", largest)
	}
	assertCoreMLConcurrency(t, accelerated, nativeImage(t, cpu))
	t.Logf("largest probability difference: %g", largest)
}

func assertCoreMLConcurrency(t *testing.T, s *Service, data []byte) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Check(ctx, Source{Reader: bytes.NewReader(data)}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := filterLoad(t.Context(), s, [][]byte{data}, 8); err != nil {
		t.Fatal(err)
	}
}

func TestNativeCoreMLAutoFallback(t *testing.T) {
	c := coreMLConfig(t)
	lease, err := onnx.Acquire(c.Library)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := lease.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	m, data, err := loadModel(c.Model)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	session, provider, err := (onnx.Backend{Provider: "auto"}).Create(data, []string{m.Input.Name}, []string{m.Output.Name}, func(options *ort.SessionOptions) error {
		calls++
		if calls == 1 {
			return ErrRuntime
		}
		return onnx.Configure(options, 2)
	})
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := session.Destroy(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if provider != "cpu" || calls != 2 {
		t.Fatal("auto did not retry with fresh CPU session options")
	}
}

func compareBackendResult(t *testing.T, got, want Result) float64 {
	t.Helper()
	if got.ClassID != want.ClassID || got.SHA256 != want.SHA256 {
		t.Fatal("CoreML classification differs")
	}
	var largest float64
	for i, value := range got.Probabilities {
		largest = max(largest, math.Abs(float64(value-want.Probabilities[i])))
	}
	return largest
}

func BenchmarkCoreML(b *testing.B) { benchmarkProviders(b, nil) }

func BenchmarkCoreMLSized(b *testing.B) {
	coreMLConfig(b)
	images := make([][]byte, 8)
	for i := range images {
		images[i] = profilePNG(b, byte(i))
	}
	benchmarkProviders(b, images)
}

func benchmarkProviders(b *testing.B, images [][]byte) {
	b.Helper()
	c := coreMLConfig(b)
	for _, provider := range []string{"cpu", coreMLProvider} {
		for _, callers := range []int{1, 8} {
			b.Run(provider+"/"+strconv.Itoa(callers), func(b *testing.B) {
				c.Provider = provider
				s := nativeServiceWithPlan(b, c, automaticPlan(c, resource.Detect()))
				if images == nil {
					benchmarkFilterLoad(b, s, callers)
				} else {
					benchmarkFilterImages(b, s, images, callers)
				}
			})
		}
	}
}
