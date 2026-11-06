package ocr

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/rs/zerolog"
)

func TestNativeRequestTiming(t *testing.T) {
	s, dir := nativeService(t)
	data := nativeRead(t, dir, "sample.png")
	for i := range 2 {
		var buffer bytes.Buffer
		logger := zerolog.New(&buffer).With().Str("requestID", "native-ocr-test").Logger()
		result, err := s.Recognize(logger.WithContext(t.Context()), imageinput.Source{Reader: bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		assertNativeTiming(t, buffer.Bytes(), len(result.Lines), i > 0)
		if strings.Contains(buffer.String(), "BOARDING") {
			t.Fatal("recognized text escaped into request log")
		}
	}
}

func assertNativeTiming(t *testing.T, data []byte, lines int, expectReuse bool) {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal("expected exactly one request summary", err)
	}
	if fields["message"] != "OCR timing" || fields["requestID"] != "native-ocr-test" || fields["success"] != true || fields["returned_lines"] != float64(lines) {
		t.Fatal("missing correlated OCR result summary", fields)
	}
	if reused, ok := fields["buffer_reused"].(float64); !ok || expectReuse && reused < 1 {
		t.Fatal("repeated image did not reuse buffers", fields["buffer_reused"])
	}
	assertNativeStageTimings(t, fields)
}

func assertNativeStageTimings(t *testing.T, fields map[string]any) {
	t.Helper()
	for _, name := range []string{"decode_ms", "detection_preprocess_ms", "detection_inference_ms", "detection_postprocess_ms", "recognition_wall_ms", "recognition_inference_sum_ms", "ctc_sum_ms", "recognition_run_p95_ms", "recognition_batches"} {
		value, ok := fields[name].(float64)
		if !ok || value <= 0 {
			t.Fatalf("missing native OCR stage %s: %v", name, fields[name])
		}
	}
}

// BenchmarkOCRStartup includes finite warmup in startup and reports the first
// real request separately. Repeated runs intentionally reuse compilation cache.
func BenchmarkOCRStartup(b *testing.B) {
	if !coreMLTestHost() || os.Getenv("OCR_TEST_COREML") != "1" {
		b.Skip("set OCR_TEST_COREML=1 on macOS ARM")
	}
	c, dir := nativeConfiguration(b)
	c.DetectionProvider, c.RecognitionProvider = coreMLProvider, cpuProvider
	path := os.Getenv("OCR_TEST_IMAGE")
	if path == "" {
		path = filepath.Join(dir, "sample.png")
	}
	data := nativeRead(b, filepath.Dir(path), filepath.Base(path))
	for _, cache := range []bool{false, true} {
		for _, runs := range []int{0, 3} {
			config := c
			config.CoreMLCache, config.WarmupRuns = cache, runs
			name := "cache_" + strconv.FormatBool(cache) + "/warmup_" + strconv.Itoa(runs)
			b.Run(name, func(b *testing.B) { benchmarkOCRStartup(b, config, data) })
		}
	}
}

func benchmarkOCRStartup(b *testing.B, c Config, data []byte) {
	b.Helper()
	var startup time.Duration
	var requests []time.Duration
	for b.Loop() {
		start := time.Now()
		s, err := NewContext(b.Context(), c)
		if err != nil {
			b.Fatal(err)
		}
		startup += time.Since(start)
		start = time.Now()
		_, runErr := s.Recognize(b.Context(), imageinput.Source{Reader: bytes.NewReader(data)})
		requests = append(requests, time.Since(start))
		closeErr := s.Close()
		if runErr != nil || closeErr != nil {
			b.Fatal(runErr, closeErr)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(startup)/float64(b.N)/float64(time.Millisecond), "startup-ms")
	reportOCRLatencies(b, requests)
}
