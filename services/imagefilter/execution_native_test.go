package imagefilter

import (
	"bytes"
	"context"
	"errors"
	"math"
	"slices"
	"strconv"
	"testing"

	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func TestNativeAutomaticParallelParity(t *testing.T) {
	c := nativeConfig(t)
	c.CacheEntries = 0
	baseline := nativeServiceWithPlan(t, c, executionPlan{cpus: 2, threads: 1, concurrency: 2, requests: 8})
	automatic := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Detect()))
	images := make([][]byte, 10)
	want := make([]Result, len(images))
	for i := range images {
		images[i] = nativeFixture(t, baseline, "00"+strconv.Itoa(i)+"_source.png")
		result, err := baseline.Check(t.Context(), Source{Reader: bytes.NewReader(images[i])})
		if err != nil {
			t.Fatal(err)
		}
		want[i] = result
	}
	group := xutil.ErrGroup(t.Context(), "image_filter_auto_parity_test").Limit(min(len(images), automatic.plan.requests))
	for i, data := range images {
		group.Go(func(ctx context.Context) error {
			got, err := automatic.Check(ctx, Source{Reader: bytes.NewReader(data)})
			if err == nil {
				assertClassificationParity(t, got, want[i])
			}
			return err
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func assertClassificationParity(t *testing.T, got, want Result) {
	t.Helper()
	if got.SHA256 != want.SHA256 || got.ClassID != want.ClassID || got.Label != want.Label || !slices.Equal(got.Labels, want.Labels) || len(got.Probabilities) != len(want.Probabilities) {
		t.Errorf("classification changed: %+v vs %+v", got, want)
		return
	}
	for i, value := range got.Probabilities {
		if math.Abs(float64(value-want.Probabilities[i])) > 1e-6 {
			t.Errorf("probability %d changed: %v vs %v", i, value, want.Probabilities[i])
		}
	}
}

func TestNativeMemoryConstrainedThreadedExecution(t *testing.T) {
	c := nativeConfig(t)
	s := nativeServiceWithPlan(t, c, automaticPlan(c, resource.Budget{CPUs: 4, Memory: 1 << 30}))
	if _, err := s.Validate(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestNativeWaitingCancellationAndReuse(t *testing.T) {
	s := nativeService(t)
	data := nativeImage(t, s)
	held := make([]*tensorSlot, 0, s.plan.concurrency)
	for range s.plan.concurrency {
		held = append(held, <-s.engine.slots)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	group := xutil.ErrGroup(t.Context(), "image_filter_wait_cancel_test").Limit(1)
	group.Go(func(context.Context) error {
		close(started)
		_, err := s.infer(ctx, data)
		return err
	})
	<-started
	cancel()
	err := group.Wait()
	for _, slot := range held {
		s.engine.slots <- slot
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting cancellation lost: %v", err)
	}
	if _, err = s.Check(t.Context(), Source{Reader: bytes.NewReader(data)}); err != nil {
		t.Fatalf("reuse failed: %v", err)
	}
}
