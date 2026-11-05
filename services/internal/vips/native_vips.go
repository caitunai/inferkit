//go:build vips && cgo

package vips

/*
#cgo pkg-config: vips libheif
#cgo linux LDFLAGS: -ldl
#include "native_vips.h"
*/
import "C"

import (
	"context"
	"errors"
	"sync"
	"unsafe"
)

var initialize = sync.OnceValue(func() bool {
	return Ready() && C.pipeline_init() == 0
})

// Enabled identifies whether this build includes the native image pipeline.
const Enabled = true

// Available verifies the native dependency contract before model initialization.
func Available() error {
	if !initialize() {
		return ErrUnavailable
	}
	return nil
}

// Version reports the libvips library actually linked into this process.
func Version() string { return C.GoString(C.pipeline_version()) }

// JPEGLibrary reports the decoder dependency resolved from libvips itself.
func JPEGLibrary() string { return C.GoString(C.pipeline_jpeg_library()) }

// Normalize borrows the input synchronously and owns the returned native pixels.
func Normalize(ctx context.Context, data []byte, maxPixels int64, size int) (*Pixels, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrInput, err)
	}
	if len(data) == 0 || maxPixels < 1 || maxPixels > 100_000_000 || size < 1 || size > 320 {
		return nil, ErrInput
	}
	if err := Available(); err != nil {
		return nil, err
	}
	control, release := cancellation(ctx)
	defer release()
	result := C.pipeline_normalize(unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)), C.uint64_t(maxPixels), C.int(size), control)
	if err := pipelineError(int(result.status)); err != nil {
		return nil, errors.Join(err, nativeCause(result), ctx.Err())
	}
	memory := result.pixels
	pixels := &Pixels{
		Header:  Header{Format: C.GoString(&result.format[0]), Width: int(result.width), Height: int(result.height)},
		Data:    unsafe.Slice((*byte)(memory), int(result.length)),
		release: func() { C.pipeline_free(memory) },
	}
	if err := ctx.Err(); err != nil {
		pixels.Close()
		return nil, errors.Join(ErrInput, err)
	}
	return pixels, nil
}

func nativeCause(result C.PipelineResult) error {
	if reason := C.GoString(&result.reason[0]); reason != "" {
		return &DecodeError{Reason: reason}
	}
	return nil
}

func cancellation(ctx context.Context) (*C.PipelineControl, func()) {
	control := C.pipeline_control_new()
	finished := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { C.pipeline_cancel(control); close(finished) })
	return control, func() {
		if !stop() {
			<-finished
		}
		C.pipeline_free(unsafe.Pointer(control))
	}
}

func pipelineError(status int) error {
	switch status {
	case 0:
		return nil
	case 2:
		return ErrTooLarge
	case 3:
		return errors.Join(ErrInput, ErrMultiple)
	case 4:
		return ErrUnavailable
	default:
		return ErrInput
	}
}
