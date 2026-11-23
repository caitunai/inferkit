package db

import (
	"context"
	"errors"
	"sync"
	"time"
)

const imageStatisticsTTL = 30 * time.Second

type imageStatisticsCall struct {
	done       chan struct{}
	err        error
	value      ImageStatistics
	generation uint64
	stale      bool
}

// One entry and one request-owned refresh bound memory and database concurrency.
// The zero value is ready to use. Expiration is checked lazily on access.
type imageStatisticsCache struct {
	loading    *imageStatisticsCall
	expires    time.Time
	value      ImageStatistics
	generation uint64
	mu         sync.Mutex
}

type imageStatisticsLoader func(context.Context) (ImageStatistics, error)

func (c *imageStatisticsCache) get(ctx context.Context, load imageStatisticsLoader) (ImageStatistics, error) {
	for {
		value, retry, err := c.read(ctx, load)
		if !retry {
			return value, err
		}
	}
}

// Read one cache generation; concurrent invalidation requests another attempt.
func (c *imageStatisticsCache) read(ctx context.Context, load imageStatisticsLoader) (ImageStatistics, bool, error) {
	if err := ctx.Err(); err != nil {
		return ImageStatistics{}, false, errors.Join(ErrImageStorage, err)
	}
	cached, call, owner := c.begin()
	if call == nil {
		return cached, false, nil
	}
	if owner {
		c.refresh(ctx, call, load)
	}
	if err := waitImageStatistics(ctx, call); err != nil {
		return ImageStatistics{}, false, err
	}
	if call.stale || (errors.Is(call.err, context.Canceled) && ctx.Err() == nil) {
		return ImageStatistics{}, true, nil
	}
	return call.value.clone(), false, call.err
}

func (c *imageStatisticsCache) begin() (ImageStatistics, *imageStatisticsCall, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Now().Before(c.expires) {
		return c.value.clone(), nil, false
	}
	if c.loading != nil {
		return ImageStatistics{}, c.loading, false
	}
	call := &imageStatisticsCall{done: make(chan struct{}), generation: c.generation}
	c.loading = call
	return ImageStatistics{}, call, true
}

func (c *imageStatisticsCache) refresh(ctx context.Context, call *imageStatisticsCall, load imageStatisticsLoader) {
	finished := false
	// Leave no stuck refresh if the request's panic recovery unwinds this frame.
	defer func() {
		if !finished {
			c.finish(call, ImageStatistics{}, ErrImageStorage)
		}
	}()
	value, err := load(ctx)
	if err != nil {
		err = errors.Join(ErrImageStorage, err)
	}
	c.finish(call, value, err)
	finished = true
}

func (c *imageStatisticsCache) finish(call *imageStatisticsCall, value ImageStatistics, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	call.value, call.err = value.clone(), err
	call.stale = call.generation != c.generation
	if err == nil && !call.stale {
		c.value = value.clone()
		c.expires = time.Now().Add(imageStatisticsTTL)
	}
	c.loading = nil
	close(call.done)
}

func (c *imageStatisticsCache) invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.generation++
	c.value = ImageStatistics{}
	c.expires = time.Time{}
}

func waitImageStatistics(ctx context.Context, call *imageStatisticsCall) error {
	select {
	case <-ctx.Done():
		return errors.Join(ErrImageStorage, ctx.Err())
	case <-call.done:
		return nil
	}
}
