package imagefilter

import (
	"image"
	"slices"
	"testing"
)

func TestTensorAllAlphaAndChannelValues(t *testing.T) {
	t.Parallel()
	src := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	for alpha := range 256 {
		for value := range 256 {
			i := src.PixOffset(value, alpha)
			src.Pix[i], src.Pix[i+1], src.Pix[i+2], src.Pix[i+3] = byte(value), byte(255-value), byte((value+alpha)%256), byte(alpha)
		}
	}
	assertTensorParity(t, src)
	for _, width := range []int{1, 3, 7, 9, 219, imageSize} {
		img := image.NewNRGBA(image.Rect(0, 0, width+8, 29))
		for i := range img.Pix {
			img.Pix[i] = byte(i % 256)
		}
		// Non-contiguous rows exercise SIMD tails without reading beyond a row.
		view := &image.NRGBA{Pix: img.Pix, Stride: img.Stride, Rect: image.Rect(0, 0, width, 29)}
		assertTensorParity(t, view)
	}
}

func assertTensorParity(t *testing.T, img *image.NRGBA) {
	t.Helper()
	want, got := make([]float32, inputLength), make([]float32, inputLength)
	writeTensorScalar(img, want)
	writeTensor(img, got)
	if !slices.Equal(want, got) {
		t.Fatal("native tensor differs from scalar reference")
	}
}

func BenchmarkTensor(b *testing.B) {
	src := image.NewNRGBA(image.Rect(0, 0, imageSize, profileOutputHeight))
	for i := range src.Pix {
		src.Pix[i] = byte(i % 256)
	}
	dst := make([]float32, inputLength)
	for _, kernel := range []struct {
		write func(*image.NRGBA, []float32)
		name  string
	}{{writeTensorScalar, "scalar"}, {writeTensor, tensorBackendName()}} {
		b.Run(kernel.name, func(b *testing.B) {
			for b.Loop() {
				kernel.write(src, dst)
			}
		})
	}
}
