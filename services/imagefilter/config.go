// Package imagefilter owns CPU image classification, input handling and bounded reuse.
package imagefilter

import (
	"errors"
	"time"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/onnx"
	"github.com/spf13/viper"
)

const (
	backendGo   = "go"
	imageSize   = 320
	channels    = 3
	pixelMax    = 255
	inputLength = channels * imageSize * imageSize
)

var (
	// ErrSource identifies failures reading an image source.
	ErrSource = errors.New("read image source failed")
	// ErrConfig indicates invalid resource limits or model configuration.
	ErrConfig = errors.New("invalid image filter configuration")
	// ErrModel indicates an incompatible or damaged deployment package.
	ErrModel = errors.New("invalid image filter model package")
	// ErrRuntime indicates native runtime initialization or cleanup failure.
	ErrRuntime = errors.New("image filter runtime failed")
	// ErrInput indicates an unreadable or unsupported image.
	ErrInput = imageinput.ErrInput
	// ErrTooLarge indicates an encoded size or decoded pixel limit violation.
	ErrTooLarge = imageinput.ErrTooLarge
	// ErrURL indicates an unsupported URL syntax or protocol.
	ErrURL = imageinput.ErrURL
	// ErrDNS identifies failed or empty image-host DNS resolution.
	ErrDNS = imageinput.ErrDNS
	// ErrPreprocess identifies decoding, orientation, resizing or tensor preparation failures.
	ErrPreprocess = errors.New("image preprocessing failed")
	// ErrDownload indicates an unsuccessful HTTP fetch.
	ErrDownload = imageinput.ErrDownload
	// ErrBusy indicates that all admission slots are occupied.
	ErrBusy = errors.New("image filter capacity exhausted")
	// ErrClosed indicates that shutdown has started.
	ErrClosed = errors.New("image filter is closed")
	// ErrInference indicates a failed or invalid model prediction.
	ErrInference = errors.New("image inference failed")
)

// Config specifies model assets, input bounds and result cache settings.
type Config struct {
	Model              string
	Library            string
	Provider           string
	CoreMLComputeUnits string
	Timeout            time.Duration
	CacheTTL           time.Duration
	MaxBytes           int64
	MaxPixels          int64
	CacheEntries       int
	BypassCache        bool
}

// Configuration reads image.filter after the root command merges configuration.
func Configuration(v *viper.Viper) Config {
	c := Config{
		Model: v.GetString("image.filter.model"), Library: v.GetString("image.filter.library"),
		Timeout: 30 * time.Second, CacheTTL: 5 * time.Minute,
		MaxBytes: 10 << 20, MaxPixels: 20_000_000,
		CacheEntries: 1024,
	}
	readLimits(v, &c)
	return c
}

func readLimits(v *viper.Viper, c *Config) {
	c.Provider = v.GetString("image.filter.provider")
	c.CoreMLComputeUnits = v.GetString("image.filter.coremlComputeUnits")
	c.BypassCache = v.GetBool("image.filter.bypassCache")
	if v.IsSet("image.filter.cacheEntries") {
		c.CacheEntries = v.GetInt("image.filter.cacheEntries")
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
	return errors.Join(c.validateMemory(), (onnx.Backend{Provider: c.Provider, ComputeUnits: c.CoreMLComputeUnits}).Validate())
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
