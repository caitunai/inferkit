package reclassify

import (
	"path/filepath"
	"time"

	"github.com/spf13/viper"
)

// Processing intervals bound the maintenance load between serial images.
const (
	DefaultIntervalMS = 0
	MinIntervalMS     = 0
	MaxIntervalMS     = 60_000
)

// Config bounds local checkpoint storage and background resource use.
type Config struct {
	Directory    string
	Timeout      time.Duration
	Batch        int
	FailureLimit int
}

// Configuration uses the existing persistent storage volume by default.
func Configuration(v *viper.Viper) Config {
	base := v.GetString("storage.path")
	if base == "" {
		base = "."
	}
	c := Config{Directory: filepath.Join(base, "image-reclassification"), Timeout: 30 * time.Second, Batch: 25, FailureLimit: 20}
	if v.IsSet("image.review.reclassify.directory") {
		c.Directory = v.GetString("image.review.reclassify.directory")
	}
	return c
}

func (c Config) validate() error {
	if c.Directory == "" || c.Batch < 1 || c.Batch > MaxBatch || c.Timeout <= 0 || c.Timeout > time.Minute || c.FailureLimit < 1 || c.FailureLimit > 100 {
		return ErrState
	}
	return nil
}

func validInterval(value int) bool { return value >= MinIntervalMS && value <= MaxIntervalMS }
