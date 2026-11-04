package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/queue/job"
	"github.com/caitun/inferkit/xutil"
)

var errTestJob = errors.New("test job failed")

type controlledJob struct {
	parseError error
	runs       *atomic.Int32
	name       string
	failures   int32
}

func (j *controlledJob) GetJobName() string                   { return j.name }
func (j *controlledJob) GetJobTopic() string                  { return "default" }
func (j *controlledJob) GetJobData() (message.Payload, error) { return []byte("{}"), nil }
func (j *controlledJob) ParseJob(message.Payload) (job.Job, error) {
	if j.parseError != nil {
		return nil, j.parseError
	}
	return &controlledJob{name: j.name, failures: j.failures, runs: j.runs}, nil
}

func (j *controlledJob) RunJob(context.Context) error {
	if j.runs.Add(1) <= j.failures {
		return errTestJob
	}
	return nil
}

func TestDispatchRetriesThenSucceeds(t *testing.T) {
	setQueueTestConfig(t)
	var runs atomic.Int32
	register(&controlledJob{name: "retry-test", failures: 2, runs: &runs})

	if err := dispatch(t.Context(), "default", "retry-test", "id", []byte("{}")); err != nil {
		t.Fatalf("dispatch() error = %v", err)
	}
	if got := runs.Load(); got != 3 {
		t.Fatalf("RunJob() calls = %d, want 3", got)
	}
}

func TestDispatchReturnsPermanentParseFailure(t *testing.T) {
	setQueueTestConfig(t)
	register(&controlledJob{name: "parse-test", parseError: errTestJob})

	err := dispatch(t.Context(), "default", "parse-test", "id", []byte("invalid"))
	if !errors.Is(err, ErrParseJob) || !errors.Is(err, job.ErrPermanent) {
		t.Fatalf("dispatch() error = %v, want parse and permanent errors", err)
	}
}

func TestDispatchExhaustsBoundedRetries(t *testing.T) {
	setQueueTestConfig(t)
	var runs atomic.Int32
	register(&controlledJob{name: "failure-test", failures: 10, runs: &runs})

	err := dispatch(t.Context(), "default", "failure-test", "id", []byte("{}"))
	if !errors.Is(err, ErrRunJob) {
		t.Fatalf("dispatch() error = %v, want ErrRunJob", err)
	}
	if got := runs.Load(); got != 3 {
		t.Fatalf("RunJob() calls = %d, want 3", got)
	}
}

func TestRunMessageWorkerRecoversAndNacks(t *testing.T) {
	msg := message.NewMessage("panic-message", []byte("{}"))
	msg.Metadata.Set("name", "panic-test")
	group := xutil.WaitGroup("queue_message_worker_test")
	group.Go(t.Context(), func(context.Context) {
		runMessageWorker("default", msg, func() {
			panic("test panic")
		})
	})
	group.Wait()

	select {
	case <-msg.Nacked():
	default:
		t.Fatal("message was not nacked after worker panic")
	}
}

func TestDeadLetterMaxLengthsUsesSeparateCapacity(t *testing.T) {
	oldPrefix := viper.Get("queue.prefix")
	oldTopics := viper.Get("queue.topics")
	oldSuffix := viper.Get("queue.deadLetterSuffix")
	oldMaximum := viper.Get("queue.deadLetterMaxLength")
	viper.Set("queue.prefix", "test")
	viper.Set("queue.topics", []string{"default", "email"})
	viper.Set("queue.deadLetterSuffix", "dlq")
	viper.Set("queue.deadLetterMaxLength", 25)
	t.Cleanup(func() {
		viper.Set("queue.prefix", oldPrefix)
		viper.Set("queue.topics", oldTopics)
		viper.Set("queue.deadLetterSuffix", oldSuffix)
		viper.Set("queue.deadLetterMaxLength", oldMaximum)
	})

	maxLengths := deadLetterMaxLengths()
	if len(maxLengths) != 2 {
		t.Fatalf("deadLetterMaxLengths() size = %d, want 2", len(maxLengths))
	}
	for _, topic := range []string{"test:default.dlq", "test:email.dlq"} {
		if maximum := maxLengths[topic]; maximum != 25 {
			t.Fatalf("dead-letter max length for %q = %d, want 25", topic, maximum)
		}
	}
}

func setQueueTestConfig(t *testing.T) {
	t.Helper()
	oldRetries := viper.Get("queue.maxRetries")
	oldInitial := viper.Get("queue.retryInitialInterval")
	oldMaximum := viper.Get("queue.retryMaxInterval")
	viper.Set("queue.maxRetries", 2)
	viper.Set("queue.retryInitialInterval", time.Millisecond)
	viper.Set("queue.retryMaxInterval", time.Millisecond)
	t.Cleanup(func() {
		viper.Set("queue.maxRetries", oldRetries)
		viper.Set("queue.retryInitialInterval", oldInitial)
		viper.Set("queue.retryMaxInterval", oldMaximum)
	})
}
