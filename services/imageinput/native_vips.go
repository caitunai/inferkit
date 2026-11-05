//go:build vips && cgo

package imageinput

import (
	"image"

	"github.com/caitun/inferkit/services/internal/vips"
)

// NativeReady serializes process-wide libvips initialization across image users.
func NativeReady() bool { return vips.Ready() }

func nativeDecode(data []byte, config image.Config, format string) (image.Image, bool) {
	return vips.Decode(data, config, format)
}

// NativeDecoder identifies optional image acceleration in this build.
const NativeDecoder = "libvips"

// ExtraDecodeBytesPerPixel reserves native buffers alongside Go-owned pixels.
const ExtraDecodeBytesPerPixel int64 = 8
