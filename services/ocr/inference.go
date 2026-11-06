package ocr

import (
	"context"
	"image"

	"github.com/caitun/inferkit/services/imagetiming"
	ort "github.com/yalue/onnxruntime_go"
)

func (s *Service) detect(ctx context.Context, img *image.RGBA) ([]Box, error) {
	done := imagetiming.Measure(ctx, imagetiming.DetectionWait)
	release, err := s.execution.acquire(ctx, s.execution.cpus)
	done(err)
	if err != nil {
		return nil, err
	}
	defer release()
	shape := detectionShape(img, s.config.DetectionMaxSide)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.OCRDetection(int(shape[3]), int(shape[2]), 0, 0)
	}
	var boxes []Box
	err = s.detector.withTensor(ctx, shape, func(dst []float32) error {
		finished := imagetiming.Measure(ctx, imagetiming.DetectionPreprocess)
		prepareErr := fillDetection(ctx, img, shape, dst)
		finished(prepareErr)
		return prepareErr
	}, func(values []float32, output ort.Shape) error {
		finished := imagetiming.Measure(ctx, imagetiming.DetectionPostprocess)
		var postErr error
		boxes, postErr = detectionBoxes(ctx, values, output, img.Rect.Dx(), img.Rect.Dy(), s.config)
		finished(postErr)
		return postErr
	})
	return boxes, err
}
