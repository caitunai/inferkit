package imagefilter

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

var environment struct {
	library string
	mu      sync.Mutex
	users   int
}

type engine struct {
	session *ort.DynamicAdvancedSession
	slots   chan *tensorSlot
	all     []*tensorSlot
}

type tensorSlot struct {
	input   *ort.Tensor[float32]
	output  *ort.Tensor[float32]
	options *ort.RunOptions
}

func acquireEnvironment(library string) error {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	if library == "" {
		library = defaultLibrary()
	}
	if environment.users > 0 {
		if library != environment.library {
			return ErrConfig
		}
		environment.users++
		return nil
	}
	ort.SetSharedLibraryPath(library)
	if err := ort.InitializeEnvironment(); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	environment.library = library
	environment.users = 1
	return nil
}

func releaseEnvironment() error {
	environment.mu.Lock()
	defer environment.mu.Unlock()
	environment.users--
	if environment.users > 0 {
		return nil
	}
	if err := ort.DestroyEnvironment(); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	return nil
}

func defaultLibrary() string {
	switch runtime.GOOS {
	case "windows":
		return "onnxruntime.dll"
	case "darwin":
		for _, path := range []string{"/opt/homebrew/opt/onnxruntime/lib/libonnxruntime.dylib", "/usr/local/opt/onnxruntime/lib/libonnxruntime.dylib"} {
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
		return "libonnxruntime.dylib"
	default:
		return "libonnxruntime.so"
	}
}

func newEngine(c Config, m *manifest, data []byte) (*engine, error) {
	if err := acquireEnvironment(c.Library); err != nil {
		return nil, err
	}
	e := &engine{slots: make(chan *tensorSlot, c.Concurrency)}
	if err := e.initialize(c, m, data); err != nil {
		return nil, errors.Join(err, e.close())
	}
	return e, nil
}

func (e *engine) initialize(c Config, m *manifest, data []byte) (returnErr error) {
	options, err := ort.NewSessionOptions()
	if err != nil {
		return errors.Join(ErrRuntime, err)
	}
	defer func() { returnErr = errors.Join(returnErr, options.Destroy()) }()
	if err = configureSession(options, c.Threads); err != nil {
		return err
	}
	e.session, err = ort.NewDynamicAdvancedSessionWithONNXData(data, []string{m.Input.Name}, []string{m.Output.Name}, options)
	if err != nil {
		return errors.Join(ErrRuntime, err)
	}
	for range c.Concurrency {
		slot, slotErr := newTensorSlot(m)
		if slotErr != nil {
			return slotErr
		}
		e.all = append(e.all, slot)
		e.slots <- slot
	}
	return nil
}

func configureSession(o *ort.SessionOptions, threads int) error {
	setters := []func() error{
		func() error { return o.SetIntraOpNumThreads(threads) },
		func() error { return o.SetInterOpNumThreads(1) },
		func() error { return o.SetExecutionMode(ort.ExecutionModeSequential) },
		func() error { return o.SetGraphOptimizationLevel(ort.GraphOptimizationLevelEnableAll) },
		func() error { return o.AddSessionConfigEntry("session.intra_op.allow_spinning", "0") },
	}
	for _, set := range setters {
		if err := set(); err != nil {
			return errors.Join(ErrRuntime, err)
		}
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
	for _, s := range e.all {
		errs = append(errs, s.close())
	}
	errs = append(errs, releaseEnvironment())
	if err := errors.Join(errs...); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	return nil
}

func (e *engine) run(ctx context.Context, s *tensorSlot) ([]float32, error) {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if err := s.options.Terminate(); err != nil {
			log.Error().Err(err).Msg("terminate image inference")
		}
	})
	err := e.session.RunWithOptions([]ort.Value{s.input}, []ort.Value{s.output}, s.options)
	// Terminate must finish before resetting/reusing or freeing the run options.
	if !stop() {
		<-done
	}
	err = errors.Join(err, s.options.UnsetTerminate(), ctx.Err())
	if err != nil {
		return nil, errors.Join(ErrInference, err)
	}
	return s.output.GetData(), nil
}
