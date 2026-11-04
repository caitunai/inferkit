package imagefilter

import (
	"image"
	"math"
)

const coefficientBits = 22

type filterWeights struct {
	coefficients []int
	start        int
}

func lanczos(x float64) float64 {
	if x == 0 {
		return 1
	}
	if x < -3 || x >= 3 {
		return 0
	}
	x *= math.Pi
	return (math.Sin(x) / x) * (math.Sin(x/3) / (x / 3))
}

func weights(input, output int) []filterWeights {
	scale := float64(input) / float64(output)
	filterScale := max(1, scale)
	support := 3 * filterScale
	result := make([]filterWeights, output)
	for i := range output {
		center := (float64(i) + 0.5) * scale
		start := max(0, int(center-support+0.5))
		end := min(input, int(center+support+0.5))
		values := make([]float64, end-start)
		var total float64
		for j := range values {
			values[j] = lanczos((float64(j+start) - center + 0.5) / filterScale)
			total += values[j]
		}
		coefficients := make([]int, len(values))
		for j, v := range values {
			coefficients[j] = int(math.Round(v / total * (1 << coefficientBits)))
		}
		result[i] = filterWeights{start: start, coefficients: coefficients}
	}
	return result
}

// Both axes share one strided filtering implementation. Intermediate bytes are
// deliberately clipped and rounded to match Pillow's 8-bit separable filter.
func resampleAxis(src *image.NRGBA, size int, vertical bool) *image.NRGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	input, lines, step := w, h, 4
	outW, outH := size, h
	if vertical {
		input, lines, step = h, w, src.Stride
		outW, outH = w, size
	}
	if input == size {
		return src
	}
	dst := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	filter := weights(input, size)
	for line := range lines {
		for i, f := range filter {
			source, destination := line*src.Stride+f.start*step, line*dst.Stride+i*4
			if vertical {
				source, destination = line*4+f.start*step, i*dst.Stride+line*4
			}
			filterPixel(src.Pix, dst.Pix, source, destination, step, f.coefficients)
		}
	}
	return dst
}

func filterPixel(src, dst []byte, source, destination, step int, coefficients []int) {
	for c := range 4 {
		sum := 1 << (coefficientBits - 1)
		for k, coefficient := range coefficients {
			sum += int(src[source+k*step+c]) * coefficient
		}
		dst[destination+c] = byte(max(0, min(pixelMax, sum>>coefficientBits)))
	}
}
