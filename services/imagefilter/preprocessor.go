package imagefilter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/services/imagepipeline"
)

// The Go comparison path retains the earlier Pillow-compatible pass order and
// rounding. It does not claim the model's libvips training pixel contract.
const goPreprocessProfile = "go_pillow_lanczos_rgb_320_v1"

// Backend selects one request's image processing without changing the service.
type Backend string

const (
	// Libvips follows the model's native training preprocessing.
	Libvips Backend = "libvips"
	// Go uses Go decoding and the earlier Lanczos implementation.
	Go Backend = backendGo
)

// ParseBackend validates CLI input before opening the model or native session.
func ParseBackend(value string) (Backend, error) {
	backend := Backend(value)
	if backend != Libvips && backend != Go {
		return "", ErrConfig
	}
	return backend, nil
}

// Detector borrows one service's session, resource budget and bounded cache.
// Its backend is immutable; only the owning Service closes native resources.
type Detector struct {
	service     *Service
	backend     Backend
	expectedSHA string
	fresh       bool
}

// WithBackend creates a lightweight request adapter, not another model session.
func (s *Service) WithBackend(backend Backend) Detector {
	return Detector{service: s, backend: backend}
}

// Limits returns the shared service's request limits.
func (d Detector) Limits() (int64, time.Duration, int) { return d.service.Limits() }

func (b Backend) preprocessProfile() string {
	if b == Go {
		return goPreprocessProfile
	}
	return imagepipeline.Profile
}

func (b Backend) tensorBackend() string {
	if b == Go {
		return tensorBackendName()
	}
	return planarBackendName()
}

func validatePreprocessor(m *manifest) error {
	if err := imagepipeline.Available(); err != nil {
		return errors.Join(ErrConfig, err)
	}
	if m.Preprocessing.Version != imagepipeline.Version() {
		return errors.Join(ErrModel, &imagepipeline.VersionError{Expected: m.Preprocessing.Version, Actual: imagepipeline.Version()})
	}
	return nil
}

func (b Backend) logPreprocessor(event *zerolog.Event) {
	event.Str("preprocess_backend", string(b)).Str("preprocess_profile", b.preprocessProfile()).
		Bool("preprocess_matches_model", b == Libvips).Str("tensor_backend", b.tensorBackend())
	if b == Go {
		event.Str("image_decoder", backendGo).Str("resize_backend", resizeBackendName())
		return
	}
	event.Str("libvips_version", imagepipeline.Version()).Str("avif_decoder", "dav1d").
		Str("jpeg_library", imagepipeline.JPEGLibrary()).Str("resize_backend", string(Libvips)).Str("image_decoder", string(Libvips))
}

func (d Detector) prepare(ctx context.Context, data []byte, dst []float32) error {
	s := d.service
	if d.backend == Go {
		return preprocessWithBudget(ctx, data, s.config.MaxPixels, dst, s.engine.reservePreparation)
	}
	return preprocessNative(ctx, data, s.config.MaxPixels, dst)
}

// PredictionSHA256 namespaces collected comparison results without changing
// ModelSHA256. Existing libvips records keep their original model identity.
func (d Detector) PredictionSHA256() string {
	s := d.service
	if d.backend == Libvips {
		return s.modelSHA256
	}
	hash := sha256.Sum256([]byte(s.modelSHA256 + "\x00" + d.backend.preprocessProfile()))
	return hex.EncodeToString(hash[:])
}

func (b Backend) cacheKey(hash string) string {
	if b == Go {
		return backendGo + ":" + hash
	}
	return hash
}
