package redis

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/xutil"
)

//nolint:cyclop,funlen,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestRedisOperationsIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	ctx := t.Context()
	client := goredis.NewClient(&goredis.Options{Addr: addr, DB: 13})
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
	if err := client.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("FlushDB() error = %v", err)
	}
	oldPrefix := viper.GetString("redis.prefix")
	viper.Set("redis.prefix", "redis-integration")
	rdbMutex.Lock()
	rdb = client
	rdbMutex.Unlock()
	t.Cleanup(func() {
		viper.Set("redis.prefix", oldPrefix)
		if err := client.FlushDB(ctx).Err(); err != nil {
			t.Errorf("FlushDB() cleanup error = %v", err)
		}
		if err := Close(); err != nil {
			t.Errorf("Close() cleanup error = %v", err)
		}
	})

	if err := client.Set(ctx, WithPrefix("expiring"), "value", 0).Err(); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := SetExpire(ctx, "expiring", 2*time.Second); err != nil {
		t.Fatalf("SetExpire() error = %v", err)
	}
	if ttl := client.PTTL(ctx, WithPrefix("expiring")).Val(); ttl <= 0 || ttl > 2*time.Second {
		t.Fatalf("PTTL() = %v, want (0, 2s]", ttl)
	}
	if err := SetExpire(ctx, "missing", time.Second); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("SetExpire(missing) error = %v, want ErrKeyNotFound", err)
	}

	const goroutines = 16
	const increments = 50
	group := xutil.WaitGroup("redis_increment_test")
	errorsChannel := make(chan error, goroutines)
	for range goroutines {
		group.Go(ctx, func(taskContext context.Context) {
			for range increments {
				if _, err := Increment(taskContext, "counter", time.Minute); err != nil {
					errorsChannel <- err
					return
				}
			}
		})
	}
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		t.Fatalf("Increment() error = %v", err)
	}
	if value := client.Get(ctx, WithPrefix("counter")).Val(); value != "800" {
		t.Fatalf("counter = %q, want 800", value)
	}
	if ttl := client.PTTL(ctx, WithPrefix("counter")).Val(); ttl <= 0 {
		t.Fatalf("counter PTTL() = %v, want positive", ttl)
	}

	if err := RightPushWithLimitExpired(
		ctx,
		"bounded-list",
		[]string{"one", "two", "three", "four"},
		3,
		time.Minute,
	); err != nil {
		t.Fatalf("RightPushWithLimitExpired() error = %v", err)
	}
	var values []string
	if err := GetListAllElements(ctx, "bounded-list", &values); err != nil {
		t.Fatalf("GetListAllElements() error = %v", err)
	}
	if len(values) != 3 || values[0] != "two" || values[2] != "four" {
		t.Fatalf("bounded list = %#v, want [two three four]", values)
	}
}
