//go:build linux || darwin

package onnx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompilationCacheIdentity(t *testing.T) {
	t.Parallel()
	key := compilationCacheKey([]byte("model"), "ort1", "os1", "")
	if key != compilationCacheKey([]byte("model"), "ort1", "os1", "ALL") {
		t.Fatal("default compute units changed cache identity")
	}
	for _, other := range []string{
		compilationCacheKey([]byte("new weights"), "ort1", "os1", "ALL"),
		compilationCacheKey([]byte("model"), "ort2", "os1", "ALL"),
		compilationCacheKey([]byte("model"), "ort1", "os2", "ALL"),
		compilationCacheKey([]byte("model"), "ort1", "os1", "CPUOnly"),
	} {
		if other == key || !isCompilationKey(other) {
			t.Fatal("different model or runtime reused cache identity")
		}
	}
}

func cachedModel(t *testing.T, root, model string, now time.Time) *CompilationCache {
	t.Helper()
	key := compilationCacheKey([]byte(model), "ort", "os", "ALL")
	cache, err := acquireCompilationCache(root, key, now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := cache.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return cache
}

func TestCompilationCacheBuildAndLifetimeLocks(t *testing.T) {
	t.Parallel()
	root, now := t.TempDir(), time.Now()
	cache := cachedModel(t, root, "same", now)
	key := filepath.Base(cache.Directory())
	if _, err := acquireCompilationCache(root, key, now); !errors.Is(err, ErrCache) {
		t.Fatalf("parallel compilation was not rejected: %v", err)
	}
	if err := cache.Finish(); err != nil {
		t.Fatal(err)
	}
	reused := cachedModel(t, root, "same", now)
	if reused.Directory() != cache.Directory() {
		t.Fatal("compiled directory was not reused")
	}
	oldRoot, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := oldRoot.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if removed, removeErr := removeCompilationEntry(oldRoot, key); removed || removeErr != nil {
		t.Fatalf("active cache was removed: %v %v", removed, removeErr)
	}
}

func TestCompilationCacheCapacityProtectsActiveEntries(t *testing.T) {
	t.Parallel()
	root, now := t.TempDir(), time.Now()
	for i := range compilationCacheEntries {
		cachedModel(t, root, string(rune('a'+i)), now)
	}
	key := compilationCacheKey([]byte("ninth"), "ort", "os", "ALL")
	if _, err := acquireCompilationCache(root, key, now); !errors.Is(err, ErrCache) {
		t.Fatalf("active cache capacity exceeded: %v", err)
	}
}

func TestCompilationCacheExpiryAndEviction(t *testing.T) {
	t.Parallel()
	root, now := t.TempDir(), time.Now()
	expired := cachedModel(t, root, "old", now.Add(-2*compilationCacheTTL))
	if err := expired.Close(); err != nil {
		t.Fatal(err)
	}
	active := cachedModel(t, root, "active-old", now.Add(-2*compilationCacheTTL))
	for i := range compilationCacheEntries + 1 {
		cache := cachedModel(t, root, string(rune('a'+i)), now.Add(time.Duration(i)*time.Second))
		if err := cache.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(expired.Directory()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired entry retained: %v", err)
	}
	if _, err := os.Stat(active.Directory()); err != nil {
		t.Fatal("expired but active entry removed", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != compilationCacheEntries+1 {
		t.Fatalf("cache directory count is unbounded: %d %v", len(entries), err)
	}
}

func TestCompilationCacheInterruptedCreation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	key := compilationCacheKey([]byte("partial"), "ort", "os", "ALL")
	if err := os.Mkdir(filepath.Join(root, key), 0o700); err != nil {
		t.Fatal(err)
	}
	cache := cachedModel(t, root, "partial", time.Now())
	if filepath.Base(cache.Directory()) != key {
		t.Fatal("interrupted directory was not recovered")
	}
}
