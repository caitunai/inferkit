package ocr

import (
	"context"
	"errors"

	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/onnx"
)

const maxOutputElements = 32_000_000

func createSession(data []byte, threads int, input, output string, backend onnx.Backend) (*ort.DynamicAdvancedSession, string, error) {
	session, provider, err := backend.Create(data, []string{input}, []string{output}, func(options *ort.SessionOptions) error { return onnx.Configure(options, threads) })
	if err != nil {
		return nil, "", errors.Join(ErrModel, err)
	}
	return session, provider, nil
}

// run lets ONNX allocate dynamic outputs rather than guessing dimensions or
// probing error messages. The binding already copies native output to a Go-owned
// buffer; retaining that slice after Destroy avoids copying the whole tensor again.
func (m *model) run(ctx context.Context, data []float32, shape ort.Shape) (values []float32, dims ort.Shape, returnErr error) {
	if !shapeMatches(m.input, shape) {
		return nil, nil, ErrModel
	}
	input, err := ort.NewTensor(shape, data)
	if err != nil {
		return nil, nil, errors.Join(ErrInference, err)
	}
	defer func() { returnErr = errors.Join(returnErr, input.Destroy()) }()
	options, err := ort.NewRunOptions()
	if err != nil {
		return nil, nil, errors.Join(ErrInference, err)
	}
	defer func() { returnErr = errors.Join(returnErr, options.Destroy()) }()
	outputs := make([]ort.Value, 1)
	defer func() {
		if outputs[0] != nil {
			returnErr = errors.Join(returnErr, outputs[0].Destroy())
		}
	}()
	if err = onnx.Run(ctx, m.session, []ort.Value{input}, outputs, options); err != nil {
		return nil, nil, errors.Join(ErrInference, err)
	}
	result, ok := outputs[0].(*ort.Tensor[float32])
	if !ok {
		return nil, nil, ErrInference
	}
	// Bound the output consumed by postprocessing. onnxruntime_go v1.38
	// owns the returned Go slice; Destroy releases the C handle, not its buffer.
	if len(result.GetData()) > maxOutputElements {
		return nil, nil, ErrInference
	}
	values = result.GetData()
	return values, result.GetShape(), nil
}

func shapeMatches(expected, actual ort.Shape) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i, want := range expected {
		if want > 0 && want != actual[i] {
			return false
		}
	}
	return true
}
