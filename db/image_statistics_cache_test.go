package db

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitun/inferkit/services/imagereview"
	"github.com/caitun/inferkit/xutil"
)

func statisticsFixture(total uint64) ImageStatistics {
	return ImageStatistics{Total: total, Classes: []ImageClassCount{{Label: imageStatisticsPhotoLabel, Count: total}}}
}

type statisticsRefreshGate struct {
	entered chan struct{}
	release chan struct{}
	calls   atomic.Uint64
}

func newStatisticsRefreshGate() *statisticsRefreshGate {
	return &statisticsRefreshGate{entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *statisticsRefreshGate) load(ctx context.Context) (ImageStatistics, error) {
	number := g.calls.Add(1)
	if number == 1 {
		close(g.entered)
		if err := waitStatisticsFixture(ctx, g.release); err != nil {
			return ImageStatistics{}, err
		}
	}
	return statisticsFixture(number), nil
}

func TestImageStatisticsCacheReusesIsolatesAndExpires(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	var calls uint64
	load := func(context.Context) (ImageStatistics, error) { calls++; return statisticsFixture(calls), nil }
	first, err := cache.get(t.Context(), load)
	if err != nil {
		t.Fatal(err)
	}
	first.Classes[0].Count = 99
	cached, err := cache.get(t.Context(), load)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || cached.Classes[0].Count != 1 {
		t.Fatal("cache snapshot exposed mutable data", calls, cached)
	}
	cache.mu.Lock()
	cache.expires = time.Now().Add(-time.Second)
	cache.mu.Unlock()
	fresh, err := cache.get(t.Context(), load)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Total != 2 || calls != 2 {
		t.Fatal("expired snapshot was reused", fresh, calls)
	}
}

func TestImageStatisticsCacheCoalescesConcurrentReaders(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	gate := newStatisticsRefreshGate()
	results := make(chan error, 21)
	group := xutil.WaitGroup("image_statistics_coalescing_test")
	task := func(ctx context.Context) {
		value, err := cache.get(ctx, gate.load)
		if err == nil && value.Total != 1 {
			err = ErrImageQuery
		}
		results <- err
	}
	group.Go(t.Context(), task)
	<-gate.entered
	for range 20 {
		group.Go(t.Context(), task)
	}
	close(gate.release)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if gate.calls.Load() != 1 {
		t.Fatal("concurrent cache miss duplicated aggregate SQL", gate.calls.Load())
	}
}

func TestImageStatisticsCacheDiscardsPreAnnotationRefresh(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	gate := newStatisticsRefreshGate()
	values := make(chan ImageStatistics, 1)
	group := xutil.WaitGroup("image_statistics_invalidation_test").Go(t.Context(), func(ctx context.Context) {
		value, err := cache.get(ctx, gate.load)
		if err != nil {
			t.Error(err)
		}
		values <- value
	})
	<-gate.entered
	cache.invalidate()
	close(gate.release)
	group.Wait()
	result := <-values
	if result.Total != 2 || gate.calls.Load() != 2 {
		t.Fatal("pre-commit aggregate was published after invalidation", result, gate.calls.Load())
	}
	cached, err := cache.get(t.Context(), gate.load)
	if err != nil || cached.Total != 2 || gate.calls.Load() != 2 {
		t.Fatal("fresh result not cached", cached, err)
	}
}

func TestImageStatisticsCacheDoesNotCacheFailures(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	if _, err := cache.get(t.Context(), func(context.Context) (ImageStatistics, error) { return ImageStatistics{}, ErrImageQuery }); !errors.Is(err, ErrImageQuery) {
		t.Fatal(err)
	}
	value, err := cache.get(t.Context(), func(context.Context) (ImageStatistics, error) { return statisticsFixture(3), nil })
	if err != nil || value.Total != 3 {
		t.Fatal("failed refresh poisoned cache", value, err)
	}
}

func TestImageStatisticsCacheCancelledWaiterLeavesRefreshOwned(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	_, call, _ := cache.begin()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.get(ctx, func(context.Context) (ImageStatistics, error) {
		t.Error("waiter stole refresh")
		return ImageStatistics{}, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cache.finish(call, statisticsFixture(1), nil)
	value, err := cache.get(t.Context(), func(context.Context) (ImageStatistics, error) {
		t.Error("owner's result was lost")
		return ImageStatistics{}, nil
	})
	if err != nil || value.Total != 1 {
		t.Fatal(value, err)
	}
}

func TestImageStatisticsCachePanicDoesNotStrandRefresh(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	assertStatisticsLoaderPanic(t, cache)
	value, err := cache.get(t.Context(), func(context.Context) (ImageStatistics, error) { return statisticsFixture(1), nil })
	if err != nil || value.Total != 1 {
		t.Fatal("panic stranded future readers", value, err)
	}
}

func assertStatisticsLoaderPanic(t *testing.T, cache *imageStatisticsCache) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Error("expected loader panic")
		}
	}()
	if _, err := cache.get(t.Context(), func(context.Context) (ImageStatistics, error) { panic("statistics loader fixture") }); err != nil {
		t.Fatal(err)
	}
}

func TestImageStatisticsCacheEmptyResultIsCached(t *testing.T) {
	t.Parallel()
	cache := new(imageStatisticsCache)
	calls := 0
	load := func(context.Context) (ImageStatistics, error) { calls++; return statisticsFixture(0), nil }
	for range 2 {
		if _, err := cache.get(t.Context(), load); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("empty dataset caused repeated aggregate queries", calls)
	}
}

func BenchmarkImageStatisticsCacheHit(b *testing.B) {
	cache := new(imageStatisticsCache)
	calls := 0
	load := func(context.Context) (ImageStatistics, error) {
		calls++
		return buildImageStatistics(imagereview.FilterLabels(), nil), nil
	}
	if _, err := cache.get(b.Context(), load); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := cache.get(b.Context(), load); err != nil {
			b.Fatal(err)
		}
	}
	if calls != 1 {
		b.Fatal("warm cache issued another load", calls)
	}
}

func waitStatisticsFixture(ctx context.Context, release <-chan struct{}) error {
	select {
	case <-ctx.Done():
		return errors.Join(ErrImageStorage, ctx.Err())
	case <-release:
		return nil
	}
}
