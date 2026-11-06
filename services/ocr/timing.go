package ocr

import (
	"context"
	"errors"
	"image"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
)

func recordBuffer(ctx context.Context, reused bool) {
	if trace := imagetiming.From(ctx); trace != nil {
		trace.OCRBuffers(reused)
	}
}

func (s *Service) traceCapacity(trace *imagetiming.Trace) {
	s.mu.Lock()
	active := len(s.calls)
	s.mu.Unlock()
	p := s.plan
	trace.OCRCapacity(imagetiming.OCRPlan{
		DetectionProvider: s.detector.provider, RecognitionProvider: s.recognizer.provider,
		DetectionThreads: p.detectionThreads, RecognitionThreads: p.recognitionThreads, LineWorkers: p.lineWorkers,
		Images: p.images, Requests: p.requests, Active: active,
	}, tensorBackendName())
}

func (s *Service) readImage(ctx context.Context, source imageinput.Source) (*image.RGBA, error) {
	kind := "url"
	if source.Reader != nil {
		kind = "reader"
	}
	trace := imagetiming.From(ctx)
	done := imagetiming.Measure(ctx, imagetiming.SourceRead)
	data, err := imageinput.Read(ctx, s.http, source, s.config.MaxBytes)
	done(err)
	trace.Input(kind, len(data))
	if err != nil {
		return nil, errors.Join(ErrSource, err)
	}
	done = imagetiming.Measure(ctx, imagetiming.Decode)
	img, format, backend, err := imageinput.DecodeWithBackend(data, s.config.MaxPixels)
	trace.Format(format)
	trace.Decoder(backend)
	if err != nil {
		done(err)
		return nil, errors.Join(ErrSource, err)
	}
	if err = ctx.Err(); err != nil {
		done(err)
		return nil, errors.Join(ErrInference, err)
	}
	done(nil)
	done = imagetiming.Measure(ctx, imagetiming.Opaque)
	opaque := opaqueImage(img)
	done(nil)
	trace.Image(opaque.Rect.Dx(), opaque.Rect.Dy(), "rgba")
	return opaque, nil
}
