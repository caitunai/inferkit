// Package onnx owns the process-wide native environment and cancellation-safe runs.
package onnx

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"

	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"
)

var (
	// ErrConfig identifies incompatible runtime configuration.
	ErrConfig = errors.New("invalid ONNX runtime configuration")
	// ErrRuntime identifies native initialization, inference or cleanup failures.
	ErrRuntime = errors.New("ONNX runtime failed")
)

var environment struct {
	library string
	mu      sync.Mutex
	users   int
}

// Lease keeps the native environment alive until Close is called.
type Lease struct {
	err  error
	once sync.Once
}

// Acquire reuses the environment only when all users agree on the shared library.
func Acquire(library string) (*Lease, error) {
	if err := acquire(library); err != nil {
		return nil, err
	}
	return &Lease{}, nil
}

// Close releases one lease exactly once.
func (l *Lease) Close() error { l.once.Do(func() { l.err = release() }); return l.err }

func acquire(library string) error {
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

func release() error {
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
	case platformDarwin:
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

// Configure applies bounded CPU session settings.
func Configure(o *ort.SessionOptions, threads int) error {
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

// Run propagates cancellation to native inference and waits for termination before reuse.
func Run(ctx context.Context, session *ort.DynamicAdvancedSession, inputs, outputs []ort.Value, options *ort.RunOptions) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		if err := options.Terminate(); err != nil {
			log.Error().Err(err).Msg("terminate ONNX inference")
		}
	})
	err := session.RunWithOptions(inputs, outputs, options)
	if !stop() {
		<-done
	}
	if err = errors.Join(err, options.UnsetTerminate(), ctx.Err()); err != nil {
		return errors.Join(ErrRuntime, err)
	}
	return nil
}
