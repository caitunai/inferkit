package ocr

import (
	"context"
	"errors"
	"image"
	"time"

	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/xutil"
	ort "github.com/yalue/onnxruntime_go"
)

type lineBatch struct {
	lines   []lineGeometry
	indices []int
}

func (s *Service) batches(boxes []Box) ([]lineBatch, error) {
	size := s.batchLimit()
	if s.recognizer.provider == cpuProvider || s.recognizer.provider == openVINOProvider {
		// Keep enough CPU jobs runnable before grouping lines into larger GEMMs.
		size = min(size, max(1, len(boxes)/s.plan.lineWorkers))
	}
	groups := make(map[int]int)
	var batches []lineBatch
	for index, box := range boxes {
		g, err := recognitionGeometry(box, s.config.RecognitionHeight, s.config.RecognitionMaxWidth)
		if err != nil {
			return nil, err
		}
		// Keep batched dense CTC output below the existing per-run budget even
		// for exports whose time axis downsamples less than PP-OCRv6 does.
		limit := min(size, max(1, maxOutputElements/(g.width*max(1, len(s.dictionary)))))
		group, ok := groups[g.width]
		if !ok || len(batches[group].lines) >= limit {
			group = len(batches)
			groups[g.width] = group
			batches = append(batches, lineBatch{})
		}
		batches[group].lines = append(batches[group].lines, g)
		batches[group].indices = append(batches[group].indices, index)
	}
	return batches, nil
}

func (s *Service) batchLimit() int {
	if s.recognizer.input[0] > 0 || s.recognizer.output[0] > 0 {
		return 1
	}
	return max(1, min(s.config.RecognitionBatchSize, s.plan.batchSize))
}

func (s *Service) recognizeBoxes(ctx context.Context, img *image.RGBA, boxes []Box) (Result, error) {
	done := imagetiming.Measure(ctx, imagetiming.Recognition)
	batches, err := s.batches(boxes)
	if err != nil {
		done(err)
		return Result{}, err
	}
	lines := make([]Line, len(boxes))
	group := xutil.ErrGroup(ctx, "ocr_recognize_batches").Limit(s.plan.lineWorkers)
	for _, batch := range batches {
		group.Go(func(ctx context.Context) error { return s.recognizeBatch(ctx, img, batch, lines) })
	}
	err = group.Wait()
	done(err)
	if err != nil {
		return Result{}, errors.Join(ErrLineRecognition, err)
	}
	done = imagetiming.Measure(ctx, imagetiming.Result)
	result := s.result(img, lines)
	done(nil)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.OCRReturned(len(result.Lines))
	}
	return result, nil
}

func (s *Service) recognizeBatch(ctx context.Context, img *image.RGBA, batch lineBatch, lines []Line) error {
	done := imagetiming.Measure(ctx, imagetiming.RecognitionWait)
	release, err := s.execution.acquireBatch(ctx, len(batch.lines))
	done(err)
	if err != nil {
		return err
	}
	defer release()
	shape := batch.lines[0].shape(len(batch.lines))
	return s.recognizer.withTensor(ctx, shape, func(dst []float32) error {
		finished := imagetiming.Measure(ctx, imagetiming.RecognitionPreprocess)
		prepareErr := fillBatch(ctx, img, batch, dst)
		finished(prepareErr)
		return prepareErr
	}, func(values []float32, output ort.Shape) error {
		finished := imagetiming.Measure(ctx, imagetiming.CTC)
		decodeErr := decodeBatch(values, output, s.dictionary, batch, lines)
		finished(decodeErr)
		return decodeErr
	})
}

func fillBatch(ctx context.Context, img *image.RGBA, batch lineBatch, dst []float32) error {
	length := len(dst) / len(batch.lines)
	for i, g := range batch.lines {
		if err := fillRecognition(ctx, img, g, dst[i*length:(i+1)*length]); err != nil {
			return err
		}
	}
	return nil
}

func decodeBatch(values []float32, shape ort.Shape, dictionary []string, batch lineBatch, lines []Line) error {
	if len(shape) != 3 || shape[0] != int64(len(batch.lines)) || len(values)%len(batch.lines) != 0 {
		return ErrInference
	}
	length := len(values) / len(batch.lines)
	one := ort.Shape{1, shape[1], shape[2]}
	for i, g := range batch.lines {
		text, confidence, err := decodeCTC(values[i*length:(i+1)*length], one, dictionary)
		if err != nil {
			return err
		}
		lines[batch.indices[i]] = Line{Text: text, Confidence: confidence, Box: g.box}
	}
	return nil
}

func recordRun(ctx context.Context, shape ort.Shape, elapsed time.Duration) {
	if trace := imagetiming.From(ctx); trace != nil {
		trace.OCRBatch(int(shape[0]), int(shape[3]), elapsed)
	}
}
