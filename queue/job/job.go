package job

import (
	"context"
	"errors"

	"github.com/ThreeDotsLabs/watermill/message"
)

// ErrPermanent indicates permanent queue job failure.
var ErrPermanent = errors.New("permanent queue job failure")

// Job represents job data.
type Job interface {
	GetJobName() string
	GetJobTopic() string
	GetJobData() (message.Payload, error)
	ParseJob(data message.Payload) (Job, error)
	RunJob(ctx context.Context) error
}
