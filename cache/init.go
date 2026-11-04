package cache

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-redis/cache/v9"

	"github.com/caitun/inferkit/redis"
)

var (
	cli      *cache.Cache
	cliMutex sync.RWMutex
	// ErrPutString indicates put string in cache failed.
	ErrPutString = errors.New("put string in cache failed")
	// ErrGetString indicates get string from cache failed.
	ErrGetString = errors.New("get string from cache failed")
	// ErrDelete indicates delete cache key failed.
	ErrDelete = errors.New("delete cache key failed")
	// ErrCacheMiss indicates cache key not found.
	ErrCacheMiss = errors.New("cache key not found")
	// ErrInvalidCacheKey indicates cache key is empty.
	ErrInvalidCacheKey = errors.New("cache key is empty")
	// ErrInvalidCacheTTL indicates cache ttl must be at least one second.
	ErrInvalidCacheTTL = errors.New("cache ttl must be at least one second")
)

// InitCache performs the init cache operation.
func InitCache() {
	cliMutex.Lock()
	defer cliMutex.Unlock()
	if cli == nil {
		cli = newClient()
	}
}

// GetClient returns client.
func GetClient() *cache.Cache {
	cliMutex.RLock()
	client := cli
	cliMutex.RUnlock()
	if client != nil {
		return client
	}

	cliMutex.Lock()
	defer cliMutex.Unlock()
	if cli == nil {
		cli = newClient()
	}
	return cli
}

// PutString performs the put string operation.
func PutString(ctx context.Context, key, value string, ttl time.Duration) error {
	if key == "" {
		return errors.Join(ErrPutString, ErrInvalidCacheKey)
	}
	if ttl < time.Second {
		return errors.Join(ErrPutString, ErrInvalidCacheTTL)
	}
	if err := GetClient().Set(buildItem(ctx, key, value, ttl)); err != nil {
		return errors.Join(ErrPutString, err)
	}
	return nil
}

// GetString returns string.
func GetString(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", errors.Join(ErrGetString, ErrInvalidCacheKey)
	}
	var wanted string
	err := GetClient().Get(ctx, redis.WithPrefix(key), &wanted)
	if err == nil {
		return wanted, nil
	}
	if errors.Is(err, cache.ErrCacheMiss) {
		return "", errors.Join(ErrGetString, ErrCacheMiss)
	}
	return "", errors.Join(ErrGetString, err)
}

// Delete performs the delete operation.
func Delete(ctx context.Context, key string) error {
	if key == "" {
		return errors.Join(ErrDelete, ErrInvalidCacheKey)
	}
	if err := GetClient().Delete(ctx, redis.WithPrefix(key)); err != nil {
		return errors.Join(ErrDelete, err)
	}
	return nil
}

// Close releases the cache wrapper. The redis package owns the shared pool.
func Close() {
	cliMutex.Lock()
	cli = nil
	cliMutex.Unlock()
}

func buildItem(ctx context.Context, key string, value any, ttl time.Duration) *cache.Item {
	return &cache.Item{
		Ctx:   ctx,
		Key:   redis.WithPrefix(key),
		Value: value,
		TTL:   ttl,
	}
}

func newClient() *cache.Cache {
	return cache.New(&cache.Options{
		Redis: redis.GetClient(),
	})
}
