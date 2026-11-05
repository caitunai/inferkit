package imagepipeline

import (
	"context"
	"errors"

	"github.com/caitun/inferkit/services/internal/vips"
)

// Enabled identifies whether this build includes the native image pipeline.
const Enabled = vips.Enabled

// Available verifies the native dependency contract before model initialization.
func Available() error { return errors.Join(vips.Available()) }

// Version reports the libvips library actually linked into this process.
func Version() string { return vips.Version() }

// JPEGLibrary reports the decoder dependency resolved from libvips itself.
func JPEGLibrary() string { return vips.JPEGLibrary() }

// Normalize borrows input synchronously; the caller owns the returned native pixels.
func Normalize(ctx context.Context, data []byte, maxPixels int64, size int) (*Pixels, error) {
	pixels, err := vips.Normalize(ctx, data, maxPixels, size)
	return pixels, errors.Join(err)
}
