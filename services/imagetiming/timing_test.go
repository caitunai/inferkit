package imagetiming

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"testing"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/xutil"
)

func TestTraceOwnership(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	ctx, trace, owner := Ensure(output.WithContext(t.Context()))
	if !owner {
		t.Fatal("standalone invocation did not own its trace")
	}
	_, same, owner := Ensure(ctx)
	if owner || same != trace {
		t.Fatal("nested invocation created duplicate trace")
	}
	_, separate := New(ctx)
	if separate == trace {
		t.Fatal("separate request reused parent trace")
	}
}

func TestConcurrentMeasurements(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	ctx, trace := New(output.WithContext(t.Context()))
	group := xutil.ErrGroup(ctx, "image_timing_test").Limit(4)
	for range 16 {
		group.Go(func(ctx context.Context) error {
			done := Measure(ctx, SourceRead)
			trace.Input("reader", 123)
			done(nil)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	trace.Finish(ctx, true, 0)
	var fields map[string]any
	if err := json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["image_bytes"] != float64(123) || fields["message"] != "image filter timing" {
		t.Fatalf("missing metrics: %+v", fields)
	}
	if elapsed, ok := fields["source_read_ms"].(float64); !ok || elapsed < 0 {
		t.Fatalf("invalid elapsed time: %+v", fields)
	}
}

func TestUnobservedOperationsAreNoOps(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	if From(ctx) != nil {
		t.Fatal("unexpected request trace")
	}
	Measure(ctx, Decode)(nil)
	ctx, trace := New(ctx)
	Measure(ctx, stageCount)(nil)
	if trace.seen[Decode] {
		t.Fatal("operation without trace was attributed to new request")
	}
}
