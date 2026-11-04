package imagefilter

import (
	"errors"
	"testing"
	"time"
)

func TestResultStoreCoalescingEvictionAndRetry(t *testing.T) {
	t.Parallel()
	store := newResultStore(Config{CacheEntries: 1, CacheTTL: time.Minute})
	_, owner, created := store.begin("first")
	if !created {
		t.Fatal("first request must own inference")
	}
	_, waiter, created := store.begin("first")
	if created || owner != waiter {
		t.Fatal("duplicate must join the same attempt")
	}
	store.finish("first", owner, Result{Label: "valid"}, nil)
	<-waiter.done
	if result, pending, _ := store.begin("first"); pending != nil || result.Label != "valid" {
		t.Fatal("result was not cached")
	}
	_, second, _ := store.begin("second")
	store.finish("second", second, Result{}, nil)
	_, retry, created := store.begin("first")
	if !created {
		t.Fatal("oldest result was not evicted")
	}
	store.finish("first", retry, Result{}, ErrInput)
	_, retry, created = store.begin("first")
	if !created {
		t.Fatal("failures must not be cached")
	}
	store.finish("first", retry, Result{}, ErrInput)
	if !errors.Is(retry.err, ErrInput) {
		t.Fatal("shared error identity lost")
	}
}

func TestResultStoreExpiryDoesNotEvictNewerSameKey(t *testing.T) {
	t.Parallel()
	store := newResultStore(Config{CacheEntries: 2, CacheTTL: time.Minute})
	_, first, _ := store.begin("same")
	store.finish("same", first, Result{}, nil)
	store.items["same"].expires = time.Time{}
	_, second, created := store.begin("same")
	if !created {
		t.Fatal("expired result was reused")
	}
	store.finish("same", second, Result{Label: "new"}, nil)
	_, other, _ := store.begin("other")
	store.finish("other", other, Result{}, nil)
	if result, pending, _ := store.begin("same"); pending != nil || result.Label != "new" {
		t.Fatal("stale FIFO slot removed the newer result")
	}
}
