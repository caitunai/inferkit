package queue

import (
	"net"
	"os"
	"testing"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/queue/job"
	projectredis "github.com/caitun/inferkit/redis"
)

//nolint:cyclop,funlen,gocognit // This end-to-end test keeps one setup and assertion lifecycle visible.
func TestPublisherBoundsStreamLengthIntegration(t *testing.T) {
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR is not set")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort() error = %v", err)
	}
	viper.Set("redis.host", host)
	viper.Set("redis.port", port)
	viper.Set("redis.db", 15)
	viper.Set("queue.prefix", "queue-integration")
	viper.Set("queue.streamMaxLength", 1000)
	viper.Set("queue.deadLetterMaxLength", 10)
	ctx := t.Context()
	if operationErr := projectredis.Init(ctx); operationErr != nil {
		t.Fatalf("redis.Init() error = %v", operationErr)
	}
	if operationErr := projectredis.GetClient().FlushDB(ctx).Err(); operationErr != nil {
		t.Fatalf("FlushDB() error = %v", operationErr)
	}
	if operationErr := Init(); operationErr != nil {
		t.Fatalf("Init() error = %v", operationErr)
	}
	t.Cleanup(func() {
		if operationErr := Close(); operationErr != nil {
			t.Errorf("queue.Close() cleanup error = %v", operationErr)
		}
		if operationErr := projectredis.GetClient().FlushDB(ctx).Err(); operationErr != nil {
			t.Errorf("FlushDB() cleanup error = %v", operationErr)
		}
		if operationErr := projectredis.Close(); operationErr != nil {
			t.Errorf("redis.Close() cleanup error = %v", operationErr)
		}
	})

	for number := range 500 {
		if operationErr := Publish(ctx, &job.Example{Number: number}); operationErr != nil {
			t.Fatalf("Publish() error = %v", operationErr)
		}
	}
	length, err := projectredis.GetClient().XLen(ctx, queueTopic("default")).Result()
	if err != nil {
		t.Fatalf("XLen() error = %v", err)
	}
	if length != 500 {
		t.Fatalf("ordinary stream length = %d, want 500", length)
	}

	for range 500 {
		deadLetter := message.NewMessage(watermill.NewUUID(), []byte("failed"))
		deadLetter.SetContext(ctx)
		if operationErr := publishMessage("default."+deadLetterSuffix(), deadLetter); operationErr != nil {
			t.Fatalf("publishMessage(DLQ) error = %v", operationErr)
		}
	}
	deadLetterLength, err := projectredis.GetClient().XLen(
		ctx,
		queueTopic("default."+deadLetterSuffix()),
	).Result()
	if err != nil {
		t.Fatalf("XLen(DLQ) error = %v", err)
	}
	if deadLetterLength > 200 {
		t.Fatalf("DLQ stream length = %d, want at most 200 after approximate trim", deadLetterLength)
	}
}
