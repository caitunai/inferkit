package cache

import (
	"context"
	"errors"
	"testing"
	"time"

	projectredis "github.com/caitun/inferkit/redis"
	"github.com/caitun/inferkit/xutil"
)

func TestPutStringRejectsInvalidInputs(t *testing.T) {
	tests := []struct {
		want error
		name string
		key  string
		ttl  time.Duration
	}{
		{name: "empty key", key: "", ttl: time.Second, want: ErrInvalidCacheKey},
		{name: "zero ttl", key: "key", ttl: 0, want: ErrInvalidCacheTTL},
		{name: "sub-second ttl", key: "key", ttl: time.Millisecond, want: ErrInvalidCacheTTL},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := PutString(t.Context(), test.key, "value", test.ttl)
			if !errors.Is(err, test.want) {
				t.Fatalf("PutString() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestConcurrentInitializationReturnsOneClient(t *testing.T) {
	Close()
	t.Cleanup(func() {
		Close()
		if err := projectredis.Close(); err != nil {
			t.Errorf("redis.Close() cleanup error = %v", err)
		}
	})

	const goroutines = 50
	clients := make(chan any, goroutines)
	group := xutil.WaitGroup("cache_initialization_test")
	for range goroutines {
		group.Go(t.Context(), func(context.Context) {
			clients <- GetClient()
		})
	}
	group.Wait()
	close(clients)

	first := <-clients
	for client := range clients {
		if client != first {
			t.Fatal("GetClient() returned different cache clients")
		}
	}
}
