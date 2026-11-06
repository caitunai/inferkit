package ocr

import (
	"errors"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// Retention is bounded independently of dynamic input/output allocation limits.
const retainedTensorBytes = 32 << 20

type tensorPool struct {
	idle  []*tensorSlot
	mu    sync.Mutex
	limit int
}

type tensorSlot struct {
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	options *ort.RunOptions
	inputs  []ort.Value
	outputs []ort.Value
	shape   ort.Shape
	key     [4]int64
}

func (p *tensorPool) acquire(shape ort.Shape) (*tensorSlot, bool, error) {
	if len(shape) != 4 {
		return nil, false, ErrModel
	}
	key := [4]int64(shape)
	p.mu.Lock()
	for i, slot := range p.idle {
		if slot.key == key {
			p.take(i)
			p.mu.Unlock()
			return slot, true, nil
		}
	}
	var old *tensorSlot
	if len(p.idle) > 0 {
		old = p.take(0)
	}
	p.mu.Unlock()
	if old != nil {
		if err := old.close(); err != nil {
			return nil, false, err
		}
	}
	slot, err := newTensorSlot(shape)
	return slot, false, err
}

// take preserves last-release ordering; caller holds the pool mutex.
func (p *tensorPool) take(index int) *tensorSlot {
	slot := p.idle[index]
	copy(p.idle[index:], p.idle[index+1:])
	p.idle[len(p.idle)-1] = nil
	p.idle = p.idle[:len(p.idle)-1]
	return slot
}

func newTensorSlot(shape ort.Shape) (*tensorSlot, error) {
	s := &tensorSlot{outputs: make([]ort.Value, 1), key: [4]int64(shape)}
	var err error
	s.input, err = ort.NewEmptyTensor[float32](shape)
	if err == nil {
		s.options, err = ort.NewRunOptions()
	}
	if err != nil {
		return nil, errors.Join(ErrInference, err, s.close())
	}
	s.inputs = []ort.Value{s.input}
	return s, nil
}

func (p *tensorPool) release(slot *tensorSlot, failed bool) error {
	clear(slot.input.GetData())
	bytes := len(slot.input.GetData()) * 4
	if slot.output != nil {
		clear(slot.output.GetData())
		bytes += len(slot.output.GetData()) * 4
	}
	p.mu.Lock()
	if failed || bytes > retainedTensorBytes || len(p.idle) >= p.limit {
		p.mu.Unlock()
		return slot.close()
	}
	p.idle = append(p.idle, slot)
	p.mu.Unlock()
	return nil
}

func (p *tensorPool) close() error {
	var errs []error
	for _, slot := range p.idle {
		errs = append(errs, slot.close())
	}
	p.idle = nil
	return errors.Join(errs...)
}

func (s *tensorSlot) close() error {
	var errs []error
	if s.input != nil {
		errs = append(errs, s.input.Destroy())
	}
	if len(s.outputs) > 0 && s.outputs[0] != nil {
		errs = append(errs, s.outputs[0].Destroy())
	}
	if s.options != nil {
		errs = append(errs, s.options.Destroy())
	}
	return errors.Join(errs...)
}
