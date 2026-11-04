package queue

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill-redisstream/pkg/redisstream"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/queue/job"
	"github.com/caitun/inferkit/redis"
	"github.com/caitun/inferkit/xutil"
)

const (
	defaultQueueShutdownTimeout = 5 * time.Second
	defaultStreamMaxLength      = int64(100000)
	defaultMaxRetries           = 3
	defaultRetryInitialInterval = 250 * time.Millisecond
	defaultRetryMaxInterval     = 2 * time.Second
	defaultNackResendInterval   = time.Second
	defaultSubscriberBlockTime  = time.Second //nolint:goconst // Redis blocking and retry intervals are independently configurable.
	defaultDeadLetterSuffix     = "dead-letter"
	defaultDeadLetterMaxLength  = int64(10000)
	defaultConsumerConcurrency  = 1
	defaultClaimInterval        = 30 * time.Second
	defaultClaimBatchSize       = int64(100)
	defaultMaxIdleTime          = 5 * time.Minute
	defaultConsumerCheck        = time.Minute
	defaultConsumerTimeout      = 5 * time.Minute //nolint:goconst // Consumer lifetime and idle-claim thresholds are independently configurable.
)

var (
	publisher   *redisstream.Publisher
	subscriber  *redisstream.Subscriber
	clientMutex sync.RWMutex
	jobsMutex   sync.RWMutex
	jobs        = make(map[string]job.Job)
	// ErrRedisStream indicates create redis stream client failed.
	ErrRedisStream = errors.New("create redis stream client failed")
	// ErrCloseRedisStream indicates close redis stream client failed.
	ErrCloseRedisStream = errors.New("close redis stream client failed")
	// ErrQueueShutdownTimeout indicates queue shutdown timeout.
	ErrQueueShutdownTimeout = errors.New("queue shutdown timeout")
	// ErrJobHandlerNotFound indicates queue job handler not found.
	ErrJobHandlerNotFound = errors.New("queue job handler not found")
	// ErrRunJob indicates run queue job failed.
	ErrRunJob = errors.New("run queue job failed")
	// ErrParseJob indicates parse queue job failed.
	ErrParseJob = errors.New("parse queue job failed")
	// ErrSubscribeTopic indicates subscribe queue topic failed.
	ErrSubscribeTopic = errors.New("subscribe queue topic failed")
	// ErrListenerStopped indicates queue listener stopped unexpectedly.
	ErrListenerStopped = errors.New("queue listener stopped unexpectedly")
	// ErrPublishDeadLetter indicates publish queue dead letter failed.
	ErrPublishDeadLetter = errors.New("publish queue dead letter failed")
	// ErrJobPanic indicates queue job panicked.
	ErrJobPanic = errors.New("queue job panicked")
	// ErrRetryCanceled indicates queue job retry canceled.
	ErrRetryCanceled = errors.New("queue job retry canceled")
	// ErrSubscriberRunning indicates queue subscriber is already running.
	ErrSubscriberRunning = errors.New("queue subscriber is already running")
)

// Init initializes package resources.
func Init() error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	if publisher != nil {
		return nil
	}
	var err error
	publisher, err = redisstream.NewPublisher(
		redisstream.PublisherConfig{
			Client:        redis.GetBorrowedClient(),
			Marshaller:    redisstream.DefaultMarshallerUnmarshaller{},
			Maxlens:       deadLetterMaxLengths(),
			DefaultMaxlen: streamMaxLength(),
		},
		NewLogger(),
	)
	if err != nil {
		log.Error().
			Err(err).
			Str("method", "Init").
			Str("package", "queue").
			Msg("create queue publisher failed")
		return errors.Join(ErrRedisStream, err)
	}
	return nil
}

// Start exposes the package's start value.
//
//nolint:cyclop,funlen // This bounded queue state machine keeps shutdown, acknowledgement, and retry ordering explicit.
func Start(ctx context.Context, subscriberID string) error {
	consumer := strings.TrimSpace(viper.GetString("queue.consumer.name"))
	if consumer == "" {
		consumer = "worker"
	}
	if subscriberID == "" {
		subscriberID = watermill.NewShortUUID()
	}
	streamSubscriber, err := redisstream.NewSubscriber(
		redisstream.SubscriberConfig{
			Client:                        redis.GetBorrowedClient(),
			Consumer:                      consumer + "-" + subscriberID,
			ConsumerGroup:                 viper.GetString("queue.consumer.group"),
			NackResendSleep:               configDuration("queue.consumer.nackResendInterval", defaultNackResendInterval),
			BlockTime:                     configDuration("queue.consumer.blockTime", defaultSubscriberBlockTime),
			ClaimInterval:                 configDuration("queue.consumer.claimInterval", defaultClaimInterval),
			ClaimBatchSize:                configPositiveInt64("queue.consumer.claimBatchSize", defaultClaimBatchSize),
			MaxIdleTime:                   configDuration("queue.consumer.maxIdleTime", defaultMaxIdleTime),
			CheckConsumersInterval:        configDuration("queue.consumer.checkConsumersInterval", defaultConsumerCheck),
			ConsumerTimeout:               configDuration("queue.consumer.consumerTimeout", defaultConsumerTimeout),
			DisableIndefiniteInitialBlock: true,
		},
		NewLogger(),
	)
	if err != nil {
		log.Error().
			Err(err).
			Str("method", "Init").
			Str("package", "queue").
			Msg("create queue subscriber failed")
		return errors.Join(ErrRedisStream, err)
	}
	clientMutex.Lock()
	if subscriber != nil {
		clientMutex.Unlock()
		return errors.Join(ErrSubscriberRunning, streamSubscriber.Close())
	}
	subscriber = streamSubscriber
	clientMutex.Unlock()

	SubscribeJob()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(quit)

	consumeCtx, cancelConsume := context.WithCancel(ctx)
	jobCtx, cancelJobs := context.WithCancel(ctx)
	defer cancelConsume()
	defer cancelJobs()
	subscriptions, err := subscribeTopics(consumeCtx, streamSubscriber)
	if err != nil {
		return errors.Join(err, Close())
	}
	stopConsuming := make(chan struct{})
	listeners := xutil.WaitGroup("queue_listeners")
	listenerErrs := make(chan error, len(subscriptions))
	for _, subscription := range subscriptions {
		listeners.Go(jobCtx, func(listenerContext context.Context) {
			listenTopic(listenerContext, stopConsuming, subscription, listenerErrs)
		})
	}

	var shutdownReason string
	var runErr error
	select {
	case sig := <-quit:
		shutdownReason = sig.String()
	case <-ctx.Done():
		shutdownReason = ctx.Err().Error()
	case runErr = <-listenerErrs:
		shutdownReason = runErr.Error()
	}
	close(stopConsuming)
	cancelConsume()

	shutdownErr := waitForListeners(ctx, listeners, queueShutdownTimeout())
	if shutdownErr != nil {
		cancelJobs()
	}
	closeErr := Close()
	log.Info().Str("reason", shutdownReason).Msg("stopped queue jobs")
	return errors.Join(runErr, shutdownErr, closeErr)
}

type topicSubscription struct {
	messages <-chan *message.Message
	topic    string
}

func subscribeTopics(
	ctx context.Context,
	streamSubscriber *redisstream.Subscriber,
) ([]topicSubscription, error) {
	topics := configuredTopics()
	subscriptions := make([]topicSubscription, 0, len(topics))
	for _, topic := range topics {
		messages, err := streamSubscriber.Subscribe(ctx, queueTopic(topic))
		if err != nil {
			return nil, errors.Join(ErrSubscribeTopic, err)
		}
		subscriptions = append(subscriptions, topicSubscription{topic: topic, messages: messages})
	}
	return subscriptions, nil
}

func queueShutdownTimeout() time.Duration {
	return configDuration("queue.shutdownTimeout", defaultQueueShutdownTimeout)
}

func streamMaxLength() int64 {
	maximum := viper.GetInt64("queue.streamMaxLength")
	if maximum <= 0 {
		return defaultStreamMaxLength
	}
	return maximum
}

func deadLetterMaxLength() int64 {
	maximum := viper.GetInt64("queue.deadLetterMaxLength")
	if maximum <= 0 {
		return defaultDeadLetterMaxLength
	}
	return maximum
}

func deadLetterMaxLengths() map[string]int64 {
	maximum := deadLetterMaxLength()
	maxLengths := make(map[string]int64, len(configuredTopics()))
	for _, topic := range configuredTopics() {
		maxLengths[queueTopic(topic+"."+deadLetterSuffix())] = maximum
	}
	return maxLengths
}

func configuredTopics() []string {
	topics := viper.GetStringSlice("queue.topics")
	if len(topics) == 0 {
		return []string{"default"}
	}
	return topics
}

func maxRetries() int {
	retries := viper.GetInt("queue.maxRetries")
	if retries < 0 {
		return 0
	}
	if !viper.IsSet("queue.maxRetries") {
		return defaultMaxRetries
	}
	return retries
}

func deadLetterSuffix() string {
	suffix := strings.TrimSpace(viper.GetString("queue.deadLetterSuffix"))
	if suffix == "" {
		return defaultDeadLetterSuffix
	}
	return suffix
}

func configDuration(key string, fallback time.Duration) time.Duration {
	value := viper.GetDuration(key)
	if value <= 0 {
		return fallback
	}
	return value
}

func configPositiveInt64(key string, fallback int64) int64 {
	value := viper.GetInt64(key)
	if value <= 0 {
		return fallback
	}
	return value
}

func consumerConcurrency() int {
	concurrency := viper.GetInt("queue.consumer.concurrency")
	if concurrency <= 0 {
		return defaultConsumerConcurrency
	}
	return concurrency
}

func waitForListeners(ctx context.Context, listeners *xutil.Group, timeout time.Duration) error {
	done := make(chan struct{})
	xutil.Go(ctx, "queue_listener_waiter", func(context.Context) {
		listeners.Wait()
		close(done)
	})
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-timer.C:
		return ErrQueueShutdownTimeout
	}
}

//nolint:cyclop,gocognit // This bounded queue state machine keeps shutdown, acknowledgement, and retry ordering explicit.
func listenTopic(
	jobCtx context.Context,
	stop <-chan struct{},
	subscription topicSubscription,
	listenerErrs chan<- error,
) {
	defer func() {
		if e := recover(); e != nil {
			log.Error().
				Str("topic", subscription.topic).
				Str("reason", fmt.Sprintf("recover: %v", e)).
				Bytes("stack", debug.Stack()).
				Msg("panic when listen topic")
			select {
			case listenerErrs <- ErrListenerStopped:
			default:
			}
		}
	}()
	log.Info().
		Str("topic", subscription.topic).
		Str("prefix", viper.GetString("queue.prefix")).
		Msg("subscribe topic successfully")
	workers := make(chan struct{}, consumerConcurrency())
	workerGroup := xutil.WaitGroup("queue_message_workers")

listenLoop:
	for {
		select {
		case <-stop:
			break listenLoop
		case msg, ok := <-subscription.messages:
			if !ok {
				select {
				case listenerErrs <- ErrListenerStopped:
				default:
				}
				break listenLoop
			}
			if msg == nil {
				continue
			}
			select {
			case workers <- struct{}{}:
			case <-stop:
				msg.Nack()
				break listenLoop
			}
			workerGroup.Go(jobCtx, func(workerContext context.Context) {
				defer func() { <-workers }()
				runMessageWorker(subscription.topic, msg, func() {
					handleMessage(workerContext, subscription.topic, msg)
				})
			})
		}
	}
	workerGroup.Wait()
}

func runMessageWorker(topic string, msg *message.Message, handle func()) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error().
				Str("topic", topic).
				Str("job_name", msg.Metadata.Get("name")).
				Str("job_id", msg.UUID).
				Str("reason", fmt.Sprint(recovered)).
				Bytes("stack", debug.Stack()).
				Msg("queue message worker panicked")
			msg.Nack()
		}
	}()
	handle()
}

func handleMessage(ctx context.Context, topic string, msg *message.Message) {
	err := dispatch(ctx, topic, msg.Metadata.Get("name"), msg.UUID, msg.Payload)
	if err == nil {
		msg.Ack()
		return
	}
	if ctx.Err() != nil {
		msg.Nack()
		return
	}
	if deadLetterErr := publishDeadLetter(ctx, topic, msg, err); deadLetterErr != nil {
		log.Error().Err(deadLetterErr).Str("topic", topic).Str("job_id", msg.UUID).
			Msg("publish dead letter failed; message remains pending")
		msg.Nack()
		return
	}
	msg.Ack()
}

//nolint:gocognit // This bounded queue state machine keeps shutdown, acknowledgement, and retry ordering explicit.
func dispatch(ctx context.Context, topic, name, id string, data message.Payload) (returnErr error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Error().Str("topic", topic).Str("job_name", name).Str("job_id", id).
				Str("reason", fmt.Sprint(recovered)).Bytes("stack", debug.Stack()).Msg("queue job panicked")
			returnErr = errors.Join(ErrRunJob, ErrJobPanic)
		}
	}()
	jobsMutex.RLock()
	j, ok := jobs[name]
	jobsMutex.RUnlock()
	if !ok {
		log.Error().
			Str("topic", topic).
			Str("job_name", name).
			Int("payload_bytes", len(data)).
			Msg("job handler not found")
		return errors.Join(ErrJobHandlerNotFound, job.ErrPermanent)
	}
	parsed, err := j.ParseJob(data)
	if err != nil {
		log.Error().
			Err(err).
			Str("topic", topic).
			Str("job_name", name).
			Int("payload_bytes", len(data)).
			Msg("parse job data failed")
		return errors.Join(ErrParseJob, job.ErrPermanent, err)
	}
	for attempt := 0; ; attempt++ {
		err = parsed.RunJob(ctx)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return errors.Join(ErrRunJob, ctx.Err(), err)
		}
		if errors.Is(err, job.ErrPermanent) || attempt >= maxRetries() {
			log.Error().Err(err).Str("topic", topic).Str("job_name", name).
				Str("job_id", id).Int("attempt", attempt+1).Msg("job handler run failed")
			return errors.Join(ErrRunJob, err)
		}
		if retryErr := waitRetry(ctx, retryDelay(id, attempt)); retryErr != nil {
			return errors.Join(ErrRunJob, retryErr)
		}
	}
	log.Info().
		Str("topic", topic).
		Str("name", name).
		Str("job_id", id).
		Msg("run job successfully")
	return nil
}

func publishDeadLetter(ctx context.Context, topic string, original *message.Message, cause error) error {
	payload := append(message.Payload(nil), original.Payload...)
	deadLetter := message.NewMessage(watermill.NewUUID(), payload)
	deadLetter.Metadata.Set("name", original.Metadata.Get("name"))
	deadLetter.Metadata.Set("original_message_id", original.UUID)
	deadLetter.Metadata.Set("failure", classifyFailure(cause))
	deadLetter.SetContext(ctx)
	if err := publishMessage(topic+"."+deadLetterSuffix(), deadLetter); err != nil {
		return errors.Join(ErrPublishDeadLetter, err)
	}
	return nil
}

func classifyFailure(err error) string {
	switch {
	case errors.Is(err, ErrJobHandlerNotFound):
		return "handler_not_found"
	case errors.Is(err, ErrParseJob):
		return "invalid_payload"
	default:
		return "retry_exhausted"
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.Join(ErrRetryCanceled, ctx.Err())
	case <-timer.C:
		return nil
	}
}

func retryDelay(messageID string, attempt int) time.Duration {
	delay := configDuration("queue.retryInitialInterval", defaultRetryInitialInterval)
	maximum := configDuration("queue.retryMaxInterval", defaultRetryMaxInterval)
	if delay > maximum {
		delay = maximum
	}
	for i := 0; i < attempt && delay < maximum; i++ {
		if delay > maximum/2 {
			delay = maximum
			break
		}
		delay *= 2
	}
	hasher := fnv.New32a()
	if _, err := fmt.Fprintf(hasher, "%s:%d", messageID, attempt); err != nil {
		return delay
	}
	jitterPercent := int64(80 + hasher.Sum32()%41)
	return time.Duration(int64(delay) * jitterPercent / 100)
}

// Close stops Watermill clients without closing the shared Redis connection.
func Close() error {
	clientMutex.Lock()
	defer clientMutex.Unlock()

	streamSubscriber := subscriber
	streamPublisher := publisher
	subscriber = nil
	publisher = nil

	var subscriberErr error
	if streamSubscriber != nil {
		subscriberErr = streamSubscriber.Close()
	}
	var publisherErr error
	if streamPublisher != nil {
		publisherErr = streamPublisher.Close()
	}
	closeErr := errors.Join(subscriberErr, publisherErr)
	if closeErr != nil {
		return errors.Join(ErrCloseRedisStream, closeErr)
	}
	return nil
}

func register(j job.Job) {
	jobsMutex.Lock()
	defer jobsMutex.Unlock()
	jobs[j.GetJobName()] = j
}
