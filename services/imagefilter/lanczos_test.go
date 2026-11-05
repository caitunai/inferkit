package imagefilter

import (
	"bytes"
	"image"
	"slices"
	"testing"
)

// The scalar reference keeps the established per-channel arithmetic as an oracle.
func scalarFilterPixel(src, dst []byte, source, destination, step int, coefficients []int) {
	for c := range 4 {
		sum := 1 << (coefficientBits - 1)
		for k, coefficient := range coefficients {
			sum += int(src[source+k*step+c]) * coefficient
		}
		dst[destination+c] = byte(max(0, min(pixelMax, sum>>coefficientBits)))
	}
}

func TestFusedFilterMatchesScalar(t *testing.T) {
	t.Parallel()
	source := make([]byte, 8<<10)
	for i := range source {
		source[i] = byte((i*17 + i/5) % 256)
	}
	for _, size := range [][2]int{{1, imageSize}, {7, 3}, {profileWidth, imageSize}, {profileHeight, profileOutputHeight}, {32768, imageSize}} {
		for _, filter := range weights(size[0], size[1]) {
			assertFilterRounding(t, source, filter.coefficients)
		}
	}
}

func assertFilterRounding(t *testing.T, source []byte, coefficients []int) {
	t.Helper()
	// Exercise contiguous horizontal taps and strided vertical taps.
	for _, step := range []int{4, 8} {
		got, want := make([]byte, 8), make([]byte, 8)
		scalarFilterPixel(source, want, 1, 2, step, coefficients)
		filterPixel(source, got, 1, 2, step, coefficients)
		if !bytes.Equal(got, want) {
			t.Fatalf("different channel rounding with step %d", step)
		}
	}
}

func TestPremultiplyFastPathMatchesScalar(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 256, 1))
	for i := range 256 {
		copy(img.Pix[i*4:], []byte{byte(i), pixelMax - byte(i), 177, byte(i)})
	}
	want := slices.Clone(img.Pix)
	for i := 0; i < len(want); i += 4 {
		for c := range channels {
			value := (int(want[i+c])*int(want[i+3]) + 127) / pixelMax
			if value < 0 || value > pixelMax {
				t.Fatal("reference premultiply overflow")
			}
			want[i+c] = byte(min(pixelMax, max(0, value)))
		}
	}
	premultiply(img)
	if !bytes.Equal(img.Pix, want) {
		t.Fatal("opaque fast path changed premultiplied values")
	}
}
