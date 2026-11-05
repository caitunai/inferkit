//go:build !vips || !cgo

package imageinput

import "image"

// NativeReady reports whether this build can initialize libvips.
func NativeReady() bool { return false }

func nativeDecode(_ []byte, _ image.Config, _ string) (image.Image, bool) {
	return nil, false
}

// NativeDecoder identifies optional image acceleration in this build.
const NativeDecoder = "go"

// ExtraDecodeBytesPerPixel accounts for native buffers outside Go's heap.
const ExtraDecodeBytesPerPixel int64 = 0
