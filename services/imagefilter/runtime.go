package imagefilter

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/onnx"
	ort "github.com/yalue/onnxruntime_go"
)

type engine struct {
	provider    string
	lease       *onnx.Lease
	compilation *onnx.CompilationCache
	session     *ort.DynamicAdvancedSession
	slots       chan *tensorSlot
	all         []*tensorSlot
	runs        atomic.Uint64
}

type tensorSlot struct {
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	options *ort.RunOptions
}

func newEngine(ctx context.Context, c Config, plan executionPlan, m *manifest, data []byte) (*engine, error) {
	lease, err := onnx.Acquire(c.Library)
	if err != nil {
		if errors.Is(err, onnx.ErrConfig) {
			return nil, errors.Join(ErrConfig, err)
		}
		return nil, errors.Join(ErrRuntime, err)
	}
	e := &engine{lease: lease, slots: make(chan *tensorSlot, plan.concurrency)}
	if err = e.initialize(c, plan, m, data); err != nil {
		return nil, errors.Join(err, e.close())
	}
	if err = e.warmup(ctx, c.WarmupRuns); err != nil {
		return nil, errors.Join(err, e.close())
	}
	return e, nil
}

func (e *engine) initialize(c Config, plan executionPlan, m *manifest, data []byte) error {
	if err := e.createSession(c, plan, m, data); err != nil {
		return err
	}

	for range plan.concurrency {
		slot, slotErr := newTensorSlot(m)
		if slotErr != nil {
			return slotErr
		}
		e.all = append(e.all, slot)
		e.slots <- slot
	}
	return nil
}

func newTensorSlot(m *manifest) (*tensorSlot, error) {
	s := new(tensorSlot)
	var err error
	s.input, err = ort.NewEmptyTensor[float32](ort.Shape(m.Input.Shape))
	if err == nil {
		s.output, err = ort.NewEmptyTensor[float32](ort.Shape(m.Output.Shape))
	}
	if err == nil {
		s.options, err = ort.NewRunOptions()
	}
	if err != nil {
		return nil, errors.Join(ErrRuntime, err, s.close())
	}
	return s, nil
}

func (s *tensorSlot) close() error {
	var errs []error
	if s.input != nil {
		errs = append(errs, s.input.Destroy())
	}
	if s.output != nil {
		errs = append(errs, s.output.Destroy())
	}
	if s.options != nil {
		errs = append(errs, s.options.Destroy())
	}
	return errors.Join(errs...)
}

func (e *engine) close() error {
	var errs []error
	if e.session != nil {
		errs = append(errs, e.session.Destroy())
	}
	if e.compilation != nil {
		errs = append(errs, e.compilation.Close())
	}
	for _, s := range e.all {
		errs = append(errs, s.close())
	}
	errs = append(errs, e.lease.Close())
	if err := errors.Join(errs...); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	return nil
}

func (e *engine) run(ctx context.Context, s *tensorSlot) ([]float32, error) {
	attempt := e.runs.Add(1)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.Attempt(attempt)
		trace.Execution(e.provider)
	}
	if err := onnx.Run(ctx, e.session, []ort.Value{s.input}, []ort.Value{s.output}, s.options); err != nil {
		return nil, errors.Join(ErrInference, err)
	}
	return s.output.GetData(), nil
}
