package queue

import (
	"errors"
	"testing"

	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"

	"github.com/caitun/inferkit/queue/job"
	"github.com/caitun/inferkit/redis"
)

func TestCloseWatermillClientsKeepsSharedRedisOwnedByRedisPackage(t *testing.T) {
	t.Cleanup(func() {
		if err := Close(); err != nil {
			t.Errorf("Close() cleanup error = %v", err)
		}
		if err := redis.Close(); err != nil {
			t.Errorf("redis.Close() cleanup error = %v", err)
		}
	})

	if err := Init(); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	streamSubscriber, err := redisstream.NewSubscriber(
		redisstream.SubscriberConfig{Client: redis.GetBorrowedClient()},
		NewLogger(),
	)
	if err != nil {
		t.Fatalf("NewSubscriber() error = %v", err)
	}
	clientMutex.Lock()
	subscriber = streamSubscriber
	clientMutex.Unlock()

	if operationErr := Close(); operationErr != nil {
		t.Fatalf("Close() error = %v", operationErr)
	}
	if operationErr := Close(); operationErr != nil {
		t.Fatalf("second Close() error = %v", operationErr)
	}
	if operationErr := redis.Close(); operationErr != nil {
		t.Fatalf("redis.Close() error = %v", operationErr)
	}
	if operationErr := Publish(t.Context(), &job.Example{}); !errors.Is(operationErr, ErrPublisherNotReady) {
		t.Fatalf("Publish() error = %v, want ErrPublisherNotReady", operationErr)
	}
}
