//go:build !vips || !cgo

package vips

import (
	"context"
	"image"
)

// Ready reports whether the shared native runtime can be initialized.
func Ready() bool { return false }

// Decode is unavailable when native image processing was not compiled.
func Decode(_ []byte, _ image.Config, _ string) (image.Image, bool) { return nil, false }

// Enabled identifies whether this build includes the native image pipeline.
const Enabled = false

// Available verifies the native dependency contract before model initialization.
func Available() error { return ErrUnavailable }

// Version reports the linked libvips version, or empty when unavailable.
func Version() string { return "" }

// JPEGLibrary is empty when native image processing was not compiled.
func JPEGLibrary() string { return "" }

// Normalize requires libvips; a Go fallback would violate the model contract.
func Normalize(_ context.Context, _ []byte, _ int64, _ int) (*Pixels, error) {
	return nil, ErrUnavailable
}
