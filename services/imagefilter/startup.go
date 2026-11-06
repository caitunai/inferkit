package imagefilter

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/onnx"
)

const warmupTimeout = 30 * time.Second

func (e *engine) createSession(c Config, plan executionPlan, m *manifest, data []byte) error {
	backend := onnx.Backend{Provider: c.Provider, ComputeUnits: c.CoreMLComputeUnits}
	if c.CoreMLCache && backend.PreferredProvider() == coreMLProvider {
		e.prepareCompilationCache(data, &backend)
	}
	configure := func(options *ort.SessionOptions) error { return configureSession(options, plan) }
	initialBackend := backend
	if e.compilation != nil {
		// Retry CoreML without its derived cache before auto may fall back to CPU.
		initialBackend.Provider = coreMLProvider
	}
	start := time.Now()
	var err error
	e.session, e.provider, err = initialBackend.Create(data, []string{m.Input.Name}, []string{m.Output.Name}, configure)
	e.finishCompilation()
	if err != nil && e.compilation != nil {
		log.Warn().Err(err).Msg("cached CoreML initialization failed; retrying without compilation cache")
		if closeErr := e.compilation.Close(); closeErr != nil {
			return errors.Join(ErrRuntime, err, closeErr)
		}
		e.compilation = nil
		backend.CacheDirectory = ""
		e.session, e.provider, err = backend.Create(data, []string{m.Input.Name}, []string{m.Output.Name}, configure)
	}
	if err != nil {
		return errors.Join(ErrRuntime, err)
	}
	log.Info().Str("execution_provider", e.provider).Bool("coreml_cache", e.compilation != nil).Dur("session_create_ms", time.Since(start)).Msg("image filter session initialized")
	return nil
}

func (e *engine) finishCompilation() {
	if e.compilation == nil {
		return
	}
	if err := e.compilation.Finish(); err != nil {
		log.Warn().Err(err).Msg("finish CoreML compilation cache initialization")
	}
	if e.provider == "cpu" {
		if err := e.compilation.Close(); err != nil {
			log.Warn().Err(err).Msg("release unused CoreML compilation cache")
		}
		e.compilation = nil
	}
}

func (e *engine) prepareCompilationCache(data []byte, backend *onnx.Backend) {
	cache, err := onnx.AcquireCompilationCache(data, backend.ComputeUnits)
	if err != nil {
		log.Warn().Err(err).Msg("CoreML compilation cache unavailable; continuing without cache")
		return
	}
	e.compilation = cache
	backend.CacheDirectory = cache.Directory()
	log.Info().Str("directory", cache.Directory()).Msg("image filter CoreML compilation cache")
}

func (e *engine) warmup(ctx context.Context, runs int) error {
	ctx, cancel := context.WithTimeout(ctx, warmupTimeout)
	defer cancel()
	start := time.Now()
	slot := e.all[0] // Startup owns every slot; no caller can acquire one yet.
	if runs > 0 {
		input := slot.input.GetData()
		for i := range input {
			input[i] = 1 // An opaque white RGB canvas after normalization.
		}
	}
	for range runs {
		if err := onnx.Run(ctx, e.session, []ort.Value{slot.input}, []ort.Value{slot.output}, slot.options); err != nil {
			return errors.Join(ErrRuntime, err)
		}
		if err := validateProbabilities(slot.output.GetData()); err != nil {
			return errors.Join(ErrRuntime, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	log.Info().Str("execution_provider", e.provider).Int("warmup_runs", runs).Dur("warmup_ms", time.Since(start)).Msg("image filter startup warmup complete")
	return nil
}

func validateProbabilities(probabilities []float32) error {
	var sum float64
	for _, value := range probabilities {
		p := float64(value)
		if math.IsNaN(p) || math.IsInf(p, 0) || p < 0 || p > 1 {
			return ErrInference
		}
		sum += p
	}
	if math.Abs(sum-1) > 0.001 {
		return ErrInference
	}
	return nil
}
