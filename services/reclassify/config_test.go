package reclassify

import (
	"context"
	"errors"
	"testing"
)

func TestZeroIntervalDefaultAndBounds(t *testing.T) {
	t.Parallel()
	service := testService(t, &testStore{}, successDetector())
	if interval := service.Status().State.IntervalMS; interval != 0 {
		t.Fatal("new task has an artificial delay", interval)
	}
	for _, interval := range []int{0, 1, 500, 1000, MaxIntervalMS} {
		if !validInterval(interval) {
			t.Fatal("valid interval rejected", interval)
		}
	}
	for _, interval := range []int{-1, MaxIntervalMS + 1} {
		if validInterval(interval) {
			t.Fatal("out-of-range interval accepted", interval)
		}
	}
}

func TestZeroIntervalHonorsCancellation(t *testing.T) {
	t.Parallel()
	if err := wait(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := wait(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatal("zero-delay processing ignored cancellation", err)
	}
}
