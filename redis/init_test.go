package redis

import (
	"errors"
	"testing"

	goredis "github.com/redis/go-redis/v9"
)

func TestCloseDisconnectsRedisClient(t *testing.T) {
	client := installTestClient(t)

	if err := Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Ping(t.Context()).Err(); !errors.Is(err, goredis.ErrClosed) {
		t.Fatalf("Ping() error = %v, want redis.ErrClosed", err)
	}
	if err := Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
}

func TestBorrowedClientDoesNotCloseSharedRedisClient(t *testing.T) {
	client := installTestClient(t)
	borrowed := GetBorrowedClient()

	if err := borrowed.Close(); err != nil {
		t.Fatalf("borrowed Close() error = %v", err)
	}
	if err := Close(); err != nil {
		t.Fatalf("owner Close() error = %v", err)
	}
	if err := client.Ping(t.Context()).Err(); !errors.Is(err, goredis.ErrClosed) {
		t.Fatalf("Ping() error = %v, want redis.ErrClosed", err)
	}
}

func installTestClient(t *testing.T) *goredis.Client {
	t.Helper()
	client := goredis.NewClient(&goredis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() {
		if err := client.Close(); err != nil && !errors.Is(err, goredis.ErrClosed) {
			t.Errorf("Client.Close() cleanup error = %v", err)
		}
	})
	rdbMutex.Lock()
	rdb = client
	rdbMutex.Unlock()
	return client
}
