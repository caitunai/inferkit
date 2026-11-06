// Package vips owns the shared libvips CGO runtime, decoding and native pixel lifetime.
package vips

import (
	"errors"
	"time"
)

var (
	// ErrUnavailable means the native libraries cannot implement the model profile.
	ErrUnavailable = errors.New("image pipeline requires CGO, -tags vips, libvips >= 8.18 and libheif >= 1.23 with dav1d")
	// ErrInput identifies an invalid or unsupported compressed image.
	ErrInput = errors.New("invalid libvips image input")
	// ErrTooLarge identifies an image exceeding the configured dimensions or allocation limit.
	ErrTooLarge = errors.New("libvips image exceeds configured limit")
	// ErrMultiple identifies animation or multiple image pages.
	ErrMultiple = errors.New("animated or multipage images are unsupported")
)

// DecodeError retains a bounded native-library cause for operational diagnostics.
type DecodeError struct{ Reason string }

func (e *DecodeError) Error() string { return e.Reason }

// Header describes the source before thumbnailing, with orientation applied to dimensions.
type Header struct {
	Format string
	Width  int
	Height int
}

// Diagnostics reports non-overlapping native wall times and observed JPEG loader properties.
// Prepare can include eager decoding; Eval is only the final canvas materialization.
type Diagnostics struct {
	JPEGLoader     string
	Header         time.Duration
	Prepare        time.Duration
	Eval           time.Duration
	Cleanup        time.Duration
	JPEGShrink     int
	JPEGWidth      int
	JPEGHeight     int
	JPEGLoadBuilds int
	JPEGMultiscan  bool
}

// Pixels owns interleaved RGB bytes in native memory. Call Close after consuming Data.
type Pixels struct {
	release     func()
	Data        []byte
	Header      Header
	Diagnostics Diagnostics
}

// Close releases native pixels once. Pixels has one caller and is not shared.
func (p *Pixels) Close() {
	if p == nil || p.release == nil {
		return
	}
	p.release()
	p.release, p.Data = nil, nil
}
