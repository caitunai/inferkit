package onnx

import (
	"errors"
	"sync"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

// Session owns a native session and its optional compilation-cache lease.
// The caller must keep its environment Lease alive until Close completes.
type Session struct {
	native   *ort.DynamicAdvancedSession
	cache    *CompilationCache
	err      error
	provider string
	once     sync.Once
}

// Open creates a session, retrying without derived cache before auto CPU fallback.
func (b Backend) Open(data []byte, inputs, outputs []string, cache bool, configure func(*ort.SessionOptions) error) (*Session, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	b.CacheDirectory = "" // Managed leases exclusively own directories used by Open.
	s := new(Session)
	initial := s.prepareCache(b, data, cache)
	var err error
	s.native, s.provider, err = initial.Create(data, inputs, outputs, configure)
	s.finishCache()
	if err != nil && s.cache != nil {
		log.Warn().Err(err).Msg("cached CoreML initialization failed; retrying without compilation cache")
		if closeErr := s.cache.Close(); closeErr != nil {
			return nil, errors.Join(ErrRuntime, err, closeErr)
		}
		s.cache = nil
		b.CacheDirectory = ""
		s.native, s.provider, err = b.Create(data, inputs, outputs, configure)
	}
	if err != nil {
		return nil, errors.Join(ErrRuntime, err, s.Close())
	}
	return s, nil
}

func (s *Session) prepareCache(b Backend, data []byte, enabled bool) Backend {
	if !enabled || b.PreferredProvider() != providerCoreML {
		return b
	}
	cache, err := AcquireCompilationCache(data, b.ComputeUnits)
	if err != nil {
		log.Warn().Err(err).Msg("CoreML compilation cache unavailable; continuing without cache")
		return b
	}
	s.cache = cache
	b.CacheDirectory, b.Provider = cache.Directory(), providerCoreML
	log.Info().Str("directory", cache.Directory()).Msg("ONNX CoreML compilation cache")
	return b
}

func (s *Session) finishCache() {
	if s.cache == nil {
		return
	}
	if err := s.cache.Finish(); err != nil {
		log.Warn().Err(err).Msg("finish CoreML compilation cache initialization")
	}
	if s.provider == providerCPU {
		if err := s.cache.Close(); err != nil {
			log.Warn().Err(err).Msg("release unused CoreML compilation cache")
		}
		s.cache = nil
	}
}

// Native returns the borrowed native handle; only this Session closes it.
func (s *Session) Native() *ort.DynamicAdvancedSession { return s.native }

// Provider reports the actual execution provider after initialization fallback.
func (s *Session) Provider() string { return s.provider }

// CacheDirectory is empty when compilation caching is inactive.
func (s *Session) CacheDirectory() string {
	if s.cache == nil {
		return ""
	}
	return s.cache.Directory()
}

// Close destroys native state before releasing the cache's live lease, once.
func (s *Session) Close() error {
	s.once.Do(func() {
		if s.native != nil {
			s.err = s.native.Destroy()
		}
		if s.cache != nil {
			s.err = errors.Join(s.err, s.cache.Close())
		}
		if s.err != nil {
			s.err = errors.Join(ErrRuntime, s.err)
		}
	})
	return s.err
}
