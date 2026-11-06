package imagetiming

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"
	"time"

	"github.com/caitun/inferkit/xutil"
	"github.com/rs/zerolog"
)

func TestOCRConcurrentAggregates(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	ctx, trace, owner := EnsureOCR(output.WithContext(t.Context()))
	if !owner {
		t.Fatal("direct OCR did not own trace")
	}
	_, same, secondOwner := EnsureOCR(ctx)
	if same != trace || secondOwner {
		t.Fatal("nested OCR did not reuse HTTP trace")
	}
	trace.OCRCapacity(OCRPlan{DetectionProvider: "coreml", RecognitionProvider: "cpu", LineWorkers: 4}, "go_simd")
	group := xutil.WaitGroup("ocr_timing_test")
	for range 4 {
		group.Go(ctx, func(_ context.Context) { trace.OCRBatch(2, 320, time.Millisecond); trace.OCRBuffers(true) })
	}
	group.Wait()
	trace.Finish(ctx, true, 200)
	var fields map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]float64{"recognition_batches": 4, "recognition_lines": 8, "recognition_batch_max": 2, "buffer_reused": 4, "recognition_run_p50_ms": 1} {
		if fields[name] != want {
			t.Fatalf("wrong OCR aggregate %s: %v", name, fields[name])
		}
	}
}
