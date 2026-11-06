// Package imagepipeline normalizes compressed images entirely in libvips.
package imagepipeline

import "github.com/caitun/inferkit/services/internal/vips"

var (
	// ErrUnavailable means this build or its native libraries cannot implement the model profile.
	ErrUnavailable = vips.ErrUnavailable
	// ErrInput identifies an invalid or unsupported compressed image.
	ErrInput = vips.ErrInput
	// ErrTooLarge identifies an image exceeding the configured dimensions or allocation limit.
	ErrTooLarge = vips.ErrTooLarge
	// ErrMultiple identifies animation or multiple image pages.
	ErrMultiple = vips.ErrMultiple
)

// Profile identifies the exact training operations, rather than a generic resize algorithm.
const Profile = "libvips_thumbnail_rgb_320_v2"

// MaxPixelError allows one native rounding level between CPU architectures.
// Tensor conversion itself must remain bit-exact with float32(byte) / 255.
const MaxPixelError = 1

// WorkingBytesPerPixel reserves decoder, colour and native intermediate memory.
const WorkingBytesPerPixel int64 = 48

// VersionError explains why a model and a native runtime cannot be paired.
type VersionError struct {
	Expected string
	Actual   string
}

func (e *VersionError) Error() string {
	return "libvips version mismatch: model=" + e.Expected + " runtime=" + e.Actual
}

// DecodeError retains a bounded native-library cause for operational diagnostics.
type DecodeError = vips.DecodeError

// Header describes the source before thumbnailing, with orientation applied to dimensions.
type Header = vips.Header

// Diagnostics exposes native stage measurements and observed JPEG loader properties.
type Diagnostics = vips.Diagnostics

// Pixels owns a native buffer of interleaved RGB bytes. Call Close after consuming Data.
// The fixed-size canvas and all image operations are produced in libvips.
type Pixels = vips.Pixels
