// Package reclassify owns one durable, explicitly started serial reclassification job.
package reclassify

import (
	"context"
	"errors"
	"time"

	"github.com/caitun/inferkit/services/imagefilter"
)

// Task errors preserve ownership, storage and transition classifications.
var (
	ErrBusy        = errors.New("image reclassification already owned by another worker")
	ErrState       = errors.New("invalid image reclassification transition")
	ErrCheckpoint  = errors.New("image reclassification checkpoint unavailable")
	ErrStorage     = errors.New("image reclassification database unavailable")
	ErrModel       = errors.New("image reclassification model changed")
	ErrDetection   = errors.New("historical image detection failed")
	ErrUnavailable = errors.New("image reclassification unavailable")
)

// Task states are persisted and exposed to the maintenance console.
const (
	Idle              = "idle"
	Running           = "running"
	Pausing           = "pausing"
	Paused            = "paused"
	Cancelling        = "cancelling"
	Cancelled         = "cancelled"
	Completed         = "completed"
	checkpointVersion = 1
	MaxBatch          = 50
	maxFailures       = 50
)

// Item contains only source identity and prediction fields, never annotations.
type Item struct {
	URL         string
	SHA256      string
	URLSHA256   string
	ModelSHA256 string
	ID          uint
}

// Snapshot fixes the historical primary-key ceiling at job creation.
type Snapshot struct {
	MaxID uint   `json:"max_id"`
	Total uint64 `json:"total"`
}

// Writer owns one session-level database lock; no transaction spans image I/O.
// Its pinned connection performs every query, including on small shared pools.
type Writer interface {
	Snapshot(context.Context) (Snapshot, error)
	Next(context.Context, uint, uint, int) ([]Item, error)
	Apply(context.Context, Item, imagefilter.Result) (bool, error)
	Verify(context.Context) error
	Close(context.Context) error
}

// Repository provides exclusive database ownership for a run.
type Repository interface {
	AcquireReclassification(context.Context) (Writer, error)
}

// Detector reuses the process model and checks historical source identity.
type Detector interface {
	ModelSHA256() string
	Recheck(context.Context, imagefilter.Source, string) (imagefilter.Result, error)
}

// Failure retains a bounded record ID and safe reason code.
type Failure struct {
	Code string `json:"code"`
	ID   uint   `json:"id"`
}

// State contains no URLs, original pixels, API credentials or recognized text.
type State struct {
	StartedAt           time.Time `json:"started_at,omitzero"`
	UpdatedAt           time.Time `json:"updated_at,omitzero"`
	Status              string    `json:"status"`
	ModelSHA256         string    `json:"model_sha256,omitempty"`
	Reason              string    `json:"reason,omitempty"`
	Failures            []Failure `json:"failures"`
	Snapshot            Snapshot  `json:"snapshot"`
	JobID               uint64    `json:"job_id,omitzero"`
	Cursor              uint      `json:"cursor"`
	Succeeded           uint64    `json:"succeeded"`
	Failed              uint64    `json:"failed"`
	Skipped             uint64    `json:"skipped"`
	ConsecutiveFailures int       `json:"consecutive_failures"`
	IntervalMS          int       `json:"interval_ms"`
	Version             int       `json:"version"`
}

// View combines durable progress with current process availability.
type View struct {
	CurrentModel string `json:"current_model"`
	State        State  `json:"state"`
	CurrentID    uint   `json:"current_id"`
	Available    bool   `json:"available"`
	Active       bool   `json:"active"`
}

// Start requires explicit overwrite confirmation and the expected model.
type Start struct {
	ModelSHA256 string `json:"model_sha256"`
	IntervalMS  int    `json:"interval_ms"`
	Confirm     bool   `json:"confirm_overwrite"`
}
