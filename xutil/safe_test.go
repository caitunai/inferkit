package xutil

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	errTestFailure = errors.New("test failure")
	errTestPanic   = errors.New("test panic")
)

func TestGoRecoversAndPublishesPanic(t *testing.T) {
	var output bytes.Buffer
	previousLogger := log.Logger
	log.Logger = zerolog.New(&output)
	t.Cleanup(func() {
		log.Logger = previousLogger
	})

	panicEvents := make(chan error, 1)
	unsubscribe := OnPanic("independent_task", func(_ context.Context, name string, err error) {
		if name != "independent_task" {
			t.Errorf("panic task name = %q, want independent_task", name)
		}
		panicEvents <- err
	})
	t.Cleanup(unsubscribe)

	ctx := log.Logger.WithContext(t.Context())
	Go(ctx, "independent_task", func(context.Context) {
		panic(errTestPanic)
	})

	select {
	case err := <-panicEvents:
		if !errors.Is(err, ErrPanic) || !errors.Is(err, errTestPanic) {
			t.Fatalf("panic error = %v, want ErrPanic and original error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for panic notification")
	}

	logged := output.String()
	if !strings.Contains(logged, "independent_task") || !strings.Contains(logged, "stack") {
		t.Fatalf("panic log = %s, want task name and stack", logged)
	}
}

func TestWaitGroupChainsAndWaitsAfterPanic(t *testing.T) {
	var completed atomic.Int32
	group := WaitGroup("worker_group")
	returned := group.
		Go(t.Context(), func(context.Context) {
			panic("worker panic")
		}).
		Go(t.Context(), func(context.Context) {
			completed.Add(1)
		})

	if returned != group {
		t.Fatal("Go() did not return the original group")
	}
	group.Wait()
	if completed.Load() != 1 {
		t.Fatalf("completed tasks = %d, want 1", completed.Load())
	}
}

func TestErrGroupReturnsPanicAndCancelsContext(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	group := ErrGroup(t.Context(), "error_group").Limit(2)
	returned := group.Go(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	<-started
	returned = returned.Go(func(context.Context) error {
		panic(errTestPanic)
	})

	if returned != group {
		t.Fatal("Go() did not return the original error group")
	}
	err := group.Wait()
	if !errors.Is(err, ErrPanic) || !errors.Is(err, errTestPanic) {
		t.Fatalf("Wait() error = %v, want ErrPanic and original error", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("panic did not cancel the error-group context")
	}
}

func TestErrGroupReturnsErrorAndCancelsContext(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	group := ErrGroup(t.Context(), "failing_group")
	group.Go(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		return ctx.Err()
	})
	<-started
	group.Go(func(context.Context) error {
		return errTestFailure
	})

	if err := group.Wait(); !errors.Is(err, errTestFailure) {
		t.Fatalf("Wait() error = %v, want test failure", err)
	}
	select {
	case <-canceled:
	default:
		t.Fatal("task error did not cancel the error-group context")
	}
}

func TestPanicSubscriptionsMatchAndUnsubscribe(t *testing.T) {
	var exactCount atomic.Int32
	var allCount atomic.Int32
	var otherCount atomic.Int32
	unsubscribeExact := OnPanic("subscribed_group", func(context.Context, string, error) {
		exactCount.Add(1)
	})
	unsubscribeAll := OnPanic(All, func(context.Context, string, error) {
		allCount.Add(1)
	})
	unsubscribeOther := OnPanic("other_group", func(context.Context, string, error) {
		otherCount.Add(1)
	})
	t.Cleanup(unsubscribeOther)

	panicWaitGroup(t.Context(), "subscribed_group")
	if exactCount.Load() != 1 || allCount.Load() != 1 || otherCount.Load() != 0 {
		t.Fatalf(
			"subscription counts = exact:%d all:%d other:%d, want 1, 1, 0",
			exactCount.Load(),
			allCount.Load(),
			otherCount.Load(),
		)
	}

	unsubscribeExact()
	unsubscribeExact()
	unsubscribeAll()
	panicWaitGroup(t.Context(), "subscribed_group")
	if exactCount.Load() != 1 || allCount.Load() != 1 {
		t.Fatalf("unsubscribed handlers were called: exact:%d all:%d", exactCount.Load(), allCount.Load())
	}
}

func TestPanickingSubscriberDoesNotBlockOthers(t *testing.T) {
	var notified atomic.Bool
	unsubscribePanicking := OnPanic("handler_group", func(context.Context, string, error) {
		panic("handler panic")
	})
	unsubscribeHealthy := OnPanic("handler_group", func(context.Context, string, error) {
		notified.Store(true)
	})
	t.Cleanup(unsubscribePanicking)
	t.Cleanup(unsubscribeHealthy)

	panicWaitGroup(t.Context(), "handler_group")
	if !notified.Load() {
		t.Fatal("healthy panic subscriber was not called")
	}
}

func panicWaitGroup(ctx context.Context, name string) {
	WaitGroup(name).
		Go(ctx, func(context.Context) {
			panic("test panic")
		}).
		Wait()
}
