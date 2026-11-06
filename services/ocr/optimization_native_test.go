package ocr

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caitun/inferkit/services/imageinput"
	ort "github.com/yalue/onnxruntime_go"
)

func TestNativeBatchAndBufferReuse(t *testing.T) {
	c, dir := nativeConfiguration(t)
	c.RecognitionBatchSize = 1
	single := newNativeService(t, c)
	c.RecognitionBatchSize = 4
	batched := newNativeService(t, c)
	data := nativeRead(t, dir, "sample.png")
	want, err := single.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		got, runErr := batched.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
		if runErr != nil {
			t.Fatal(runErr)
		}
		assertResultParity(t, got, want)
	}
	assertClearedPool(t, &batched.recognizer.pool)
}

func assertClearedPool(t *testing.T, pool *tensorPool) {
	t.Helper()
	if len(pool.idle) == 0 || len(pool.idle) > pool.limit {
		t.Fatal("tensor retention is unbounded or absent")
	}
	for _, slot := range pool.idle {
		assertZeroTensor(t, slot.input.GetData())
		if slot.output != nil {
			assertZeroTensor(t, slot.output.GetData())
		}
	}
}

func assertZeroTensor(t *testing.T, values []float32) {
	t.Helper()
	for _, value := range values {
		if value != 0 {
			t.Fatal("pooled tensor retained request data")
		}
	}
}

func TestNativeMixedProviders(t *testing.T) {
	if !coreMLTestHost() || os.Getenv("OCR_TEST_COREML") != "1" {
		t.Skip("set OCR_TEST_COREML=1 on macOS ARM")
	}
	c, dir := nativeConfiguration(t)
	c.DetectionProvider, c.RecognitionProvider = coreMLProvider, cpuProvider
	s := newNativeService(t, c)
	if s.detector.provider != coreMLProvider || s.recognizer.provider != cpuProvider {
		t.Fatal("independent providers ignored")
	}
	if c.CoreMLCache && s.recognizer.managed.CacheDirectory() != "" {
		t.Fatal("CPU model acquired a CoreML cache")
	}
	data := nativeRead(t, dir, "sample.png")
	if _, err := s.Recognize(t.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
		t.Fatal(err)
	}
}

func TestNativePooledOutputShapeRetry(t *testing.T) {
	s, _ := nativeService(t)
	input := make([]float32, 3*32*32)
	shape := ort.Shape{1, 3, 32, 32}
	if _, _, err := s.detector.run(t.Context(), input, shape); err != nil {
		t.Fatal(err)
	}
	slot := s.detector.pool.idle[0]
	if err := slot.output.Destroy(); err != nil {
		t.Fatal(err)
	}
	wrong, err := ort.NewEmptyTensor[float32](ort.Shape{1})
	if err != nil {
		t.Fatal(err)
	}
	slot.output, slot.outputs[0] = wrong, wrong
	if _, _, err = s.detector.run(t.Context(), input, shape); err != nil {
		t.Fatal("shape discovery retry failed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err = s.detector.run(ctx, input, shape); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled pooled run continued", err)
	}
}

func BenchmarkOCRPipeline(b *testing.B) {
	c, dir := nativeConfiguration(b)
	path := os.Getenv("OCR_TEST_IMAGE")
	if path == "" {
		path = filepath.Join(dir, "sample.png")
	}
	data := nativeRead(b, filepath.Dir(path), filepath.Base(path))
	for _, variant := range []string{cpuProvider, coreMLProvider, "hybrid"} {
		if variant != cpuProvider && (!coreMLTestHost()) {
			continue
		}
		b.Run(variant, func(b *testing.B) {
			config := c
			config.Provider = variant
			if variant == "hybrid" {
				config.Provider = "auto"
				config.DetectionProvider = coreMLProvider
				config.RecognitionProvider = cpuProvider
			}
			s := newNativeService(b, config)
			benchmarkPipeline(b, s, data)
		})
	}
}

func benchmarkPipeline(b *testing.B, s *Service, data []byte) {
	b.Helper()
	if _, err := s.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
		b.Fatal(err)
	}
	var timings []time.Duration
	b.ReportAllocs()
	for b.Loop() {
		start := time.Now()
		if _, err := s.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(data)}); err != nil {
			b.Fatal(err)
		}
		timings = append(timings, time.Since(start))
	}
	b.StopTimer()
	reportOCRLatencies(b, timings)
}
