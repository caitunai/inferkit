package imagefilter

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strconv"
	"testing"
)

const (
	profileWidth        = 1129
	profileHeight       = 774
	profileOutputHeight = 219
)

func profileImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, profileWidth, profileHeight))
	for y := range profileHeight {
		for x := range profileWidth {
			img.SetNRGBA(x, y, color.NRGBA{R: byte((x*x + y) % 256), G: byte((x + y*y) % 256), B: byte((x * y) % 256), A: pixelMax})
		}
	}
	return img
}

func profilePNG(tb testing.TB, shift byte) []byte {
	tb.Helper()
	img := profileImage()
	img.Pix[0] = shift
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		tb.Fatal(err)
	}
	return out.Bytes()
}

func BenchmarkPrepareSized(b *testing.B) {
	source := profileImage()
	for _, step := range []string{"all", "convert", "resize", "tensor"} {
		b.Run(step, func(b *testing.B) {
			tensor := make([]float32, inputLength)
			small := image.NewNRGBA(image.Rect(0, 0, imageSize, profileOutputHeight))
			b.ReportAllocs()
			for b.Loop() {
				switch step {
				case "all":
					writeTensor(resizeForBenchmark(b, toRGBA(source), 1), tensor)
				case "convert":
					toRGBA(source)
				case "resize":
					resizeForBenchmark(b, source, 1)
				case "tensor":
					writeTensor(small, tensor)
				}
			}
		})
	}
}

func resizeForBenchmark(b *testing.B, source *image.NRGBA, workers int) *image.NRGBA {
	b.Helper()
	resized, err := resizeRGBA(b.Context(), source, imageSize, profileOutputHeight, workers)
	if err != nil {
		b.Fatal(err)
	}
	return resized
}

func BenchmarkParallelResize(b *testing.B) {
	source := profileImage()
	for _, workers := range []int{1, 2, 4, 9} {
		b.Run(strconv.Itoa(workers), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				resizeForBenchmark(b, source, workers)
			}
		})
	}
}
