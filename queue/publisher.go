package queue

import (
	"context"
	"errors"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/queue/job"
)

var (
	// ErrPublishTopicMessage indicates publish message to topic failed.
	ErrPublishTopicMessage = errors.New("publish message to topic failed")
	// ErrPublisherNotReady indicates queue publisher is not initialized.
	ErrPublisherNotReady = errors.New("queue publisher is not initialized")
	// ErrEncodeJob indicates encode queue job failed.
	ErrEncodeJob = errors.New("encode queue job failed")
	// ErrInvalidJob indicates queue job is nil.
	ErrInvalidJob = errors.New("queue job is nil")
)

// Publish performs the publish operation.
func Publish(ctx context.Context, j job.Job) error {
	if j == nil {
		return errors.Join(ErrPublishTopicMessage, ErrInvalidJob)
	}
	payload, err := j.GetJobData()
	if err != nil {
		return errors.Join(ErrPublishTopicMessage, ErrEncodeJob, err)
	}
	msg := message.NewMessage(watermill.NewUUID(), payload)
	msg.Metadata.Set("name", j.GetJobName())
	msg.SetContext(ctx)
	return publishMessage(j.GetJobTopic(), msg)
}

func publishMessage(topic string, msg *message.Message) error {
	clientMutex.RLock()
	defer clientMutex.RUnlock()

	if publisher == nil {
		return ErrPublisherNotReady
	}
	if topic == "" {
		topic = "default"
	}
	if err := publisher.Publish(queueTopic(topic), msg); err != nil {
		return errors.Join(ErrPublishTopicMessage, err)
	}
	return nil
}

func queueTopic(topic string) string {
	topicPrefix := viper.GetString("queue.prefix")
	if topicPrefix == "" {
		return topic
	}
	return topicPrefix + ":" + topic
}
