package ocr

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
)

func TestAutomaticResourceBudget(t *testing.T) {
	t.Parallel()
	config := Configuration(viper.New())
	large := automaticPlan(config, resource.Budget{CPUs: 32, Memory: 64 << 30})
	if large.cpus != 32 || large.detectionThreads != 32 || large.recognitionThreads != 1 || large.lineWorkers != 32 {
		t.Fatalf("CPU budget remains fixed: %+v", large)
	}
	if large.images <= 1 || large.requests <= 4 {
		t.Fatalf("request limits remain fixed: %+v", large)
	}
}

func TestAutomaticSingleCPUBudget(t *testing.T) {
	t.Parallel()
	config := Configuration(viper.New())
	small := automaticPlan(config, resource.Budget{CPUs: 1, Memory: 1 << 30})
	if small.cpus != 1 || small.lineWorkers != 1 || small.images < 1 || small.requests <= small.images {
		t.Fatalf("invalid small budget: %+v", small)
	}
}

func TestAutomaticMemoryBudget(t *testing.T) {
	t.Parallel()
	config := Configuration(viper.New())
	large := automaticPlan(config, resource.Budget{CPUs: 32, Memory: 64 << 30})
	constrained := automaticPlan(config, resource.Budget{CPUs: 32, Memory: 768 << 20})
	if constrained.lineWorkers >= large.lineWorkers || constrained.images >= large.images {
		t.Fatal("memory does not constrain concurrent working sets")
	}
}

func TestExecutorCancellationReturnsAllPermits(t *testing.T) {
	t.Parallel()
	e := newExecutor(executionPlan{cpus: 2, lineWorkers: 2})
	release, err := e.acquire(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = e.acquireLine(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if len(e.lines) != 0 {
		t.Fatal("canceled line retained a memory slot")
	}
	release()
	lineRelease, err := e.acquireLine(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lineRelease()
	allRelease, err := e.acquire(t.Context(), 2)
	if err != nil {
		t.Fatal(err)
	}
	allRelease()
}

func TestExecutorConcurrentLineBound(t *testing.T) {
	t.Parallel()
	e := newExecutor(executionPlan{cpus: 2, lineWorkers: 2})
	var active atomic.Int64
	group := xutil.ErrGroup(t.Context(), "ocr_executor_test").Limit(8)
	for range 32 {
		group.Go(func(ctx context.Context) error {
			release, err := e.acquireLine(ctx)
			if err != nil {
				return err
			}
			defer release()
			if n := active.Add(1); n > 2 {
				t.Errorf("concurrent native runs exceeded budget: %d", n)
			}
			active.Add(-1)
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 || len(e.lines) != 0 {
		t.Fatal("executor did not drain")
	}
}
