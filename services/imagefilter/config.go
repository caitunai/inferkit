// Package imagefilter owns CPU image classification, input handling and bounded reuse.
package imagefilter

import (
	"errors"
	"time"

	"github.com/spf13/viper"
)

const (
	imageSize   = 320
	channels    = 3
	pixelMax    = 255
	inputLength = channels * imageSize * imageSize
	// Bound Lanczos coefficient memory even for extreme, thin image dimensions.
	maxImageDimension = 32768
)

var (
	// ErrConfig indicates invalid resource limits or model configuration.
	ErrConfig = errors.New("invalid image filter configuration")
	// ErrModel indicates an incompatible or damaged deployment package.
	ErrModel = errors.New("invalid image filter model package")
	// ErrRuntime indicates native runtime initialization or cleanup failure.
	ErrRuntime = errors.New("image filter runtime failed")
	// ErrInput indicates an unreadable or unsupported image.
	ErrInput = errors.New("invalid image input")
	// ErrTooLarge indicates an encoded size or decoded pixel limit violation.
	ErrTooLarge = errors.New("image exceeds configured limit")
	// ErrURL indicates an unsupported URL or non-public destination.
	ErrURL = errors.New("invalid or forbidden image URL")
	// ErrDownload indicates an unsuccessful HTTP fetch.
	ErrDownload = errors.New("image download failed")
	// ErrBusy indicates that all admission slots are occupied.
	ErrBusy = errors.New("image filter capacity exhausted")
	// ErrClosed indicates that shutdown has started.
	ErrClosed = errors.New("image filter is closed")
	// ErrInference indicates a failed or invalid model prediction.
	ErrInference = errors.New("image inference failed")
)

// Config bounds CPU, network and memory use for one process-wide service.
type Config struct {
	Model        string
	Library      string
	Timeout      time.Duration
	CacheTTL     time.Duration
	MaxBytes     int64
	MaxPixels    int64
	Concurrency  int
	MaxRequests  int
	Threads      int
	CacheEntries int
}

// Configuration reads image.filter after the root command merges configuration.
func Configuration(v *viper.Viper) Config {
	c := Config{
		Model: v.GetString("image.filter.model"), Library: v.GetString("image.filter.library"),
		Timeout: 30 * time.Second, CacheTTL: 5 * time.Minute,
		MaxBytes: 10 << 20, MaxPixels: 20_000_000,
		Concurrency: 2, MaxRequests: 8, Threads: 1, CacheEntries: 1024,
	}
	readLimits(v, &c)
	return c
}

func readLimits(v *viper.Viper, c *Config) {
	ints := map[string]*int{
		"concurrency": &c.Concurrency, "maxRequests": &c.MaxRequests,
		"threads": &c.Threads, "cacheEntries": &c.CacheEntries,
	}
	for key, dst := range ints {
		if v.IsSet("image.filter." + key) {
			*dst = v.GetInt("image.filter." + key)
		}
	}
	for key, dst := range map[string]*int64{"maxBytes": &c.MaxBytes, "maxPixels": &c.MaxPixels} {
		if v.IsSet("image.filter." + key) {
			*dst = v.GetInt64("image.filter." + key)
		}
	}
	for key, dst := range map[string]*time.Duration{"timeout": &c.Timeout, "cacheTTL": &c.CacheTTL} {
		if v.IsSet("image.filter." + key) {
			*dst = v.GetDuration("image.filter." + key)
		}
	}
}

func (c Config) validate() error {
	if c.Model == "" || c.Timeout <= 0 || c.Timeout > 5*time.Minute {
		return ErrConfig
	}
	if c.CacheTTL <= 0 || c.CacheTTL > 24*time.Hour {
		return ErrConfig
	}
	return errors.Join(c.validateCPU(), c.validateMemory())
}

func (c Config) validateCPU() error {
	if c.Concurrency < 1 || c.Concurrency > 64 || c.MaxRequests < c.Concurrency || c.MaxRequests > 1024 {
		return ErrConfig
	}
	if c.Threads < 1 || c.Threads > 256 {
		return ErrConfig
	}
	return nil
}

func (c Config) validateMemory() error {
	if c.MaxBytes < 1 || c.MaxBytes > 100<<20 || c.MaxPixels < 1 || c.MaxPixels > 100_000_000 {
		return ErrConfig
	}
	if c.CacheEntries < 0 || c.CacheEntries > 100_000 {
		return ErrConfig
	}
	return nil
}
