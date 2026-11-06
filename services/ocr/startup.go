package ocr

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

const startupWarmupTimeout = 30 * time.Second

func (s *Service) warmup(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, startupWarmupTimeout)
	defer cancel()
	start := time.Now()
	for i := range s.config.WarmupRuns {
		if err := s.warmupDetection(ctx); err != nil {
			return err
		}
		batch := 1
		if i%2 == 1 {
			batch = s.batchLimit()
		}
		if err := s.warmupRecognition(ctx, batch); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrModel, err)
	}
	log.Info().Int("warmup_runs", s.config.WarmupRuns).Int("recognition_batch_limit", s.batchLimit()).
		Float64("warmup_ms", float64(time.Since(start))/float64(time.Millisecond)).
		Bool("detection_coreml_cache", s.detector.managed.CacheDirectory() != "").Bool("recognition_coreml_cache", s.recognizer.managed.CacheDirectory() != "").
		Str("tensor_backend", tensorBackendName()).Msg("OCR startup warmup complete")
	return nil
}

func (s *Service) warmupDetection(ctx context.Context) error {
	side := int64(s.config.DetectionMaxSide)
	return s.detector.withTensor(ctx, ort.Shape{1, 3, side, side}, func(dst []float32) error { clear(dst); return nil }, func(values []float32, shape ort.Shape) error {
		if !validDetectionOutput(values, shape, s.config.DetectionMaxSide) {
			return ErrInference
		}
		return nil
	})
}

func (s *Service) warmupRecognition(ctx context.Context, batch int) error {
	return s.recognizer.withTensor(ctx, ort.Shape{int64(batch), 3, int64(s.config.RecognitionHeight), 320}, func(dst []float32) error { clear(dst); return nil }, func(values []float32, shape ort.Shape) error {
		if len(shape) != 3 || shape[0] != int64(batch) || shape[1] < 1 || shape[2] != int64(len(s.dictionary)) || shape.FlattenedSize() != int64(len(values)) || !validProbabilities(values) {
			return ErrInference
		}
		return nil
	})
}
