package cache

import (
	"errors"
	"net"
	"os"
	"testing"
	"time"

	"github.com/spf13/viper"

	projectredis "github.com/caitun/inferkit/redis"
)

//nolint:cyclop,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestCacheIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	Close()
	if operationErr := projectredis.Close(); operationErr != nil {
		t.Fatalf("redis.Close() error = %v", operationErr)
	}
	viper.Set("redis.host", host)
	viper.Set("redis.port", port)
	viper.Set("redis.db", 14)
	viper.Set("redis.prefix", "cache-integration")
	ctx := t.Context()
	if operationErr := projectredis.Init(ctx); operationErr != nil {
		t.Fatalf("redis.Init() error = %v", operationErr)
	}
	if operationErr := projectredis.GetClient().FlushDB(ctx).Err(); operationErr != nil {
		t.Fatalf("FlushDB() error = %v", operationErr)
	}
	InitCache()
	t.Cleanup(func() {
		Close()
		if operationErr := projectredis.GetClient().FlushDB(ctx).Err(); operationErr != nil {
			t.Errorf("FlushDB() cleanup error = %v", operationErr)
		}
		if operationErr := projectredis.Close(); operationErr != nil {
			t.Errorf("redis.Close() cleanup error = %v", operationErr)
		}
	})

	if operationErr := PutString(ctx, "key", "value", time.Minute); operationErr != nil {
		t.Fatalf("PutString() error = %v", operationErr)
	}
	value, err := GetString(ctx, "key")
	if err != nil || value != "value" {
		t.Fatalf("GetString() = %q, %v, want value, nil", value, err)
	}
	if operationErr := Delete(ctx, "key"); operationErr != nil {
		t.Fatalf("Delete() error = %v", operationErr)
	}
	if _, operationErr := GetString(ctx, "key"); !errors.Is(operationErr, ErrCacheMiss) {
		t.Fatalf("GetString(deleted) error = %v, want ErrCacheMiss", operationErr)
	}
}
