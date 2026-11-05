//go:build vips && cgo

package vips_test

import (
	"bytes"
	"image"
	"image/jpeg"
	"strconv"
	"testing"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagepipeline"
)

// Both public entry points must safely share initialization in the same process.
func TestConcurrentPublicEntryPoints(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 48, 32)), nil); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	for i := range 16 {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			if i%2 == 0 {
				assertSharedDecode(t, data)
				return
			}
			assertSharedNormalize(t, data)
		})
	}
}

func assertSharedDecode(t *testing.T, data []byte) {
	t.Helper()
	decoded, _, backend, err := imageinput.DecodeWithBackend(data, 48*32)
	if err != nil {
		t.Fatal(err)
	}
	if backend != "libvips" || decoded.Bounds() != image.Rect(0, 0, 48, 32) {
		t.Fatalf("unexpected shared decoder result: %s %v", backend, decoded.Bounds())
	}
}

func assertSharedNormalize(t *testing.T, data []byte) {
	t.Helper()
	pixels, err := imagepipeline.Normalize(t.Context(), data, 48*32, 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	if len(pixels.Data) != 320*320*3 || pixels.Header.Width != 48 || pixels.Header.Height != 32 {
		t.Fatalf("unexpected shared preprocessing result: %d %v", len(pixels.Data), pixels.Header)
	}
}
