package job

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/rs/zerolog/log"
)

var (
	// ErrEncodeExample indicates encode example job failed.
	ErrEncodeExample = errors.New("encode example job failed")
	// ErrDecodeExample indicates decode example job failed.
	ErrDecodeExample = errors.New("decode example job failed")
	// ErrRunExample indicates run example job failed.
	ErrRunExample = errors.New("run example job failed")
)

// Example represents example data.
type Example struct {
	Number int `json:"number"`
}

// GetJobName returns job name.
func (e *Example) GetJobName() string {
	return "example"
}

// GetJobTopic returns job topic.
func (e *Example) GetJobTopic() string {
	return "default"
}

// GetJobData returns job data.
func (e *Example) GetJobData() (message.Payload, error) {
	data, err := json.Marshal(e)
	if err != nil {
		return nil, errors.Join(ErrEncodeExample, err)
	}
	return data, nil
}

// ParseJob parses job.
func (e *Example) ParseJob(data message.Payload) (Job, error) {
	n := &Example{}
	if err := json.Unmarshal(data, n); err != nil {
		return nil, errors.Join(ErrDecodeExample, ErrPermanent, err)
	}
	return n, nil
}

// RunJob performs the run job operation.
func (e *Example) RunJob(ctx context.Context) error {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.Join(ErrRunExample, ctx.Err())
	case <-timer.C:
	}
	log.Ctx(ctx).Debug().Int("num", e.Number).Msg("run example job")
	return nil
}
