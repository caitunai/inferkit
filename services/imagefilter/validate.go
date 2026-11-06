package imagefilter

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"

	"github.com/caitun/inferkit/services/imagepipeline"
)

// ErrValidation means fixture probabilities or image preprocessing differ from the export baseline.
var ErrValidation = errors.New("image filter fixture validation failed")

// Adjacent normalized bytes can differ by an extra float32 rounding unit.
const tensorTolerance = float64(imagepipeline.MaxPixelError)/pixelMax + 1.0/(1<<23)

type validationFile struct {
	Samples []validationSample `json:"samples"`
	ATol    float64            `json:"atol"`
	RTol    float64            `json:"rtol"`
}

type validationSample struct {
	SourceFile    string    `json:"source_file"`
	InputFile     string    `json:"input_file"`
	Probabilities []float32 `json:"onnx_probabilities"`
	ClassID       int       `json:"class_id"`
}

// Validation reports both inference-only and full image-pipeline comparisons.
type Validation struct {
	Source                   string  `json:"source"`
	TensorMaxError           float64 `json:"tensor_max_error"`
	ProbabilityMaxError      float64 `json:"probability_max_error"`
	ImageProbabilityMaxError float64 `json:"image_probability_max_error"`
	TensorTolerance          float64 `json:"tensor_tolerance"`
	TensorExact              bool    `json:"tensor_exact"`
	Passed                   bool    `json:"passed"`
}

// Validate compares all exported fixtures without caching their results.
func (s *Service) Validate(ctx context.Context) ([]Validation, error) {
	return s.WithBackend(Libvips).Validate(ctx)
}

// Validate compares fixtures using the selected backend without relaxing tolerances.
func (d Detector) Validate(ctx context.Context) ([]Validation, error) {
	if _, err := ParseBackend(string(d.backend)); err != nil {
		return nil, err
	}
	s := d.service
	if err := s.admit(); err != nil {
		return nil, err
	}
	defer func() { <-s.requests; s.active.Done() }()
	var file validationFile
	if err := readJSON(filepath.Join(s.config.Model, "validation.json"), &file); err != nil {
		return nil, err
	}
	if err := file.validate(); err != nil {
		return nil, err
	}
	results := make([]Validation, 0, len(file.Samples))
	var resultErr error
	for _, sample := range file.Samples {
		result, err := d.validateSample(ctx, sample, file)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		if !result.Passed {
			resultErr = ErrValidation
		}
	}
	return results, resultErr
}

func (s *Service) fixture(path string) ([]byte, error) {
	if !filepath.IsLocal(path) {
		return nil, ErrValidation
	}
	data, err := os.ReadFile(filepath.Join(s.config.Model, path)) //nolint:gosec // Operator-controlled fixture path is validated as local before joining the trusted model directory.
	if err != nil {
		return nil, errors.Join(ErrValidation, err)
	}
	return data, nil
}

func (d Detector) validateSample(ctx context.Context, sample validationSample, file validationFile) (Validation, error) {
	s := d.service
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	input, err := s.fixture(sample.InputFile)
	if err != nil {
		return Validation{}, err
	}
	if len(input) != inputLength*4 {
		return Validation{}, ErrValidation
	}
	select {
	case <-ctx.Done():
		return Validation{}, errors.Join(ErrValidation, ctx.Err())
	case slot := <-s.engine.slots:
		defer func() { s.engine.slots <- slot }()
		return d.compareSample(ctx, slot, input, sample, file)
	}
}

func (d Detector) compareSample(ctx context.Context, slot *tensorSlot, input []byte, sample validationSample, file validationFile) (Validation, error) {
	s := d.service
	tensor := slot.input.GetData()
	for i := range tensor {
		tensor[i] = math.Float32frombits(binary.LittleEndian.Uint32(input[i*4:]))
	}
	probabilities, err := s.engine.run(ctx, slot)
	if err != nil {
		return Validation{}, err
	}
	raw, err := s.result(probabilities)
	if err != nil {
		return Validation{}, err
	}
	data, err := s.fixture(sample.SourceFile)
	if err != nil {
		return Validation{}, err
	}
	if err = d.prepare(ctx, data, tensor); err != nil {
		return Validation{}, err
	}
	maxError := tensorDifference(tensor, input)
	probabilities, err = s.engine.run(ctx, slot)
	if err != nil {
		return Validation{}, err
	}
	imageResult, err := s.result(probabilities)
	if err != nil {
		return Validation{}, err
	}
	rawError, rawOK := probabilityDifference(raw, sample, file)
	imageError, imageOK := probabilityDifference(imageResult, sample, file)
	return Validation{
		Source: sample.SourceFile, TensorMaxError: maxError, ProbabilityMaxError: rawError,
		ImageProbabilityMaxError: imageError, TensorTolerance: tensorTolerance,
		TensorExact: maxError == 0, Passed: rawOK && imageOK && maxError <= tensorTolerance,
	}, nil
}

func tensorDifference(tensor []float32, input []byte) float64 {
	var maximum float64
	for i, value := range tensor {
		want := math.Float32frombits(binary.LittleEndian.Uint32(input[i*4:]))
		maximum = max(maximum, math.Abs(float64(value-want)))
	}
	return maximum
}

func probabilityDifference(result Result, sample validationSample, file validationFile) (float64, bool) {
	if len(result.Probabilities) != len(sample.Probabilities) {
		return 0, false
	}
	passed := result.ClassID == sample.ClassID
	var maximum float64
	for i, p := range result.Probabilities {
		want := float64(sample.Probabilities[i])
		delta := math.Abs(float64(p) - want)
		maximum = max(maximum, delta)
		passed = passed && delta <= file.ATol+file.RTol*math.Abs(want)
	}
	return maximum, passed
}

func (v validationFile) validate() error {
	if len(v.Samples) == 0 || len(v.Samples) > 1024 || v.ATol < 0 || v.ATol > 0.001 || v.RTol < 0 || v.RTol > 0.01 {
		return ErrValidation
	}
	return nil
}
