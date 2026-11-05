package imagefilter

import (
	"bytes"
	"context"
	"errors"
	"image"
	"sync/atomic"
	"testing"

	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func TestParallelResizeParity(t *testing.T) {
	t.Parallel()
	for _, alpha := range []bool{false, true} {
		source := profileImage()
		if alpha {
			for i := 3; i < len(source.Pix); i += 4 {
				source.Pix[i] = byte((i / 4) % 256)
			}
		}
		assertResizeWorkers(t, source)
	}
}

func assertResizeWorkers(t *testing.T, source *image.NRGBA) {
	t.Helper()
	want, err := resizeRGBA(t.Context(), toRGBA(source), imageSize, profileOutputHeight, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, workers := range []int{2, 4, 9} {
		got, resizeErr := resizeRGBA(t.Context(), toRGBA(source), imageSize, profileOutputHeight, workers)
		if resizeErr != nil {
			t.Fatal(resizeErr)
		}
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("parallel resize changed pixels with %d workers", workers)
		}
	}
}

func TestPreparationLeaseCancellationReturnsSlots(t *testing.T) {
	t.Parallel()
	e := &engine{slots: make(chan *tensorSlot, 4)}
	for range 3 {
		e.slots <- nil
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := resizeWithBudget(ctx, profileImage(), imageSize, profileOutputHeight, e.reservePreparation)
	if !errors.Is(err, context.Canceled) || len(e.slots) != 3 {
		t.Fatalf("lease not released: %v slots=%d", err, len(e.slots))
	}
	lease := e.reservePreparation()
	if len(lease.held) != 3 || len(e.slots) != 0 {
		t.Fatal("wrong idle budget")
	}
	lease.release()
}

func TestRowsPanicDrainsAndBoundsWorkers(t *testing.T) {
	t.Parallel()
	var active atomic.Int64
	err := forRows(t.Context(), 128, 3, func(row int) {
		if n := active.Add(1); n > 3 {
			t.Errorf("worker bound exceeded: %d", n)
		}
		defer active.Add(-1)
		if row == 64 {
			panic("row failure")
		}
	})
	if !errors.Is(err, xutil.ErrPanic) || active.Load() != 0 {
		t.Fatalf("workers not drained: %v", err)
	}
}

func TestNativeBypassCacheRunsEveryRequest(t *testing.T) {
	c := nativeConfig(t)
	c.BypassCache = true
	s := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	data := nativeImage(t, s)
	var want Result
	for range 3 {
		result, err := s.Check(t.Context(), Source{Reader: bytes.NewReader(data)})
		if err != nil {
			t.Fatal(err)
		}
		want = result
	}
	group := xutil.ErrGroup(t.Context(), "image_filter_bypass_test").Limit(min(6, s.plan.requests))
	for range 6 {
		group.Go(func(ctx context.Context) error {
			got, err := s.Check(ctx, Source{Reader: bytes.NewReader(data)})
			if err == nil {
				assertClassificationParity(t, got, want)
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	if s.engine.runs.Load() != 9 || len(s.cache.pending) != 0 || len(s.cache.items) != 0 {
		t.Fatal("request was cached or coalesced")
	}
}
