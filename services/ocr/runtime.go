package ocr

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/onnx"
	ort "github.com/yalue/onnxruntime_go"
)

const maxOutputElements = 32_000_000

// run returns owned data for callers that retain it across runs.
func (m *model) run(ctx context.Context, data []float32, shape ort.Shape) (values []float32, dims ort.Shape, returnErr error) {
	if len(data) != int(shape.FlattenedSize()) {
		return nil, nil, ErrModel
	}
	err := m.withTensor(ctx, shape, func(dst []float32) error { copy(dst, data); return nil }, func(output []float32, outputShape ort.Shape) error {
		values, dims = slices.Clone(output), slices.Clone(outputShape)
		return nil
	})
	return values, dims, err
}

// withTensor holds exclusive ownership through postprocessing. Idle buffers are cleared.
func (m *model) withTensor(ctx context.Context, shape ort.Shape, prepare func([]float32) error, consume func([]float32, ort.Shape) error) (returnErr error) {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrInference, err)
	}
	if !shapeMatches(m.input, shape) {
		return ErrModel
	}
	slot, reused, err := m.pool.acquire(shape)
	if err != nil {
		return err
	}
	recordBuffer(ctx, reused)
	defer func() { returnErr = errors.Join(returnErr, m.pool.release(slot, returnErr != nil)) }()
	if err = prepare(slot.input.GetData()); err != nil {
		return err
	}
	stage := imagetiming.DetectionInference
	if len(m.output) == 3 {
		stage = imagetiming.RecognitionInference
	}
	done := imagetiming.Measure(ctx, stage)
	start := time.Now()
	err = m.runSlot(ctx, slot)
	done(err)
	if len(m.output) == 3 {
		recordRun(ctx, shape, time.Since(start))
	}
	if err != nil {
		return err
	}
	return consume(slot.output.GetData(), slot.shape)
}

func (m *model) runSlot(ctx context.Context, slot *tensorSlot) error {
	hadOutput := slot.output != nil
	err := onnx.Run(ctx, m.session, slot.inputs, slot.outputs, slot.options)
	if err != nil && hadOutput && ctx.Err() == nil {
		// Compatible custom models may have data-dependent output dimensions.
		// Retry once with shape discovery, without parsing native error strings.
		if closeErr := slot.output.Destroy(); closeErr != nil {
			return errors.Join(ErrInference, err, closeErr)
		}
		slot.output, slot.outputs[0] = nil, nil
		err = onnx.Run(ctx, m.session, slot.inputs, slot.outputs, slot.options)
	}
	if err != nil {
		return errors.Join(ErrInference, err)
	}
	output, ok := slot.outputs[0].(*ort.Tensor[float32])
	if !ok {
		return ErrInference
	}
	slot.output = output
	if len(output.GetData()) > maxOutputElements {
		return ErrInference
	}
	slot.shape = output.GetShape()
	return nil
}

func shapeMatches(expected, actual ort.Shape) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i, want := range expected {
		if actual[i] < 1 || want > 0 && want != actual[i] {
			return false
		}
	}
	return true
}
