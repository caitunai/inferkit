package imagefilter

import (
	"context"
	"image"
	"math"

	"github.com/caitun/inferkit/services/imagetiming"
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
	if input < 1 || output < 1 {
		return nil
	}
	scale := float64(input) / float64(output)
	filterScale := max(1, scale)
	support := 3 * filterScale
	result := make([]filterWeights, output)
	// Coefficients remain immutable, but share one allocation. Reuse the scratch
	// values instead of allocating two small slices for every output coordinate.
	maxTaps := min(input, int(math.Ceil(2*support))+2)
	scratch := make([]float64, maxTaps)
	arena := make([]int, output*maxTaps)
	for i := range output {
		center := (float64(i) + 0.5) * scale
		start := max(0, int(center-support+0.5))
		end := min(input, int(center+support+0.5))
		values := scratch[:end-start]
		var total float64
		for j := range values {
			values[j] = lanczos((float64(j+start) - center + 0.5) / filterScale)
			total += values[j]
		}
		offset := i * maxTaps
		coefficients := arena[offset : offset+len(values) : offset+len(values)]
		for j, v := range values {
			coefficients[j] = int(math.Round(v / total * (1 << coefficientBits)))
		}
		result[i] = filterWeights{start: start, coefficients: coefficients}
	}
	return result
}

// Each output row is independent; each pixel retains its original tap order.
type resamplePlan struct {
	source, target *image.NRGBA
	kernel         *resampleKernel
	filter         []filterWeights
	vertical       bool
}

func resampleAxis(ctx context.Context, src *image.NRGBA, size int, vertical bool, workers int) (result *image.NRGBA, returnErr error) {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	input, outW, outH := w, size, h
	if vertical {
		input, outW, outH = h, w, size
	}
	if input == size {
		return src, nil
	}
	stage := imagetiming.ResizeHorizontal
	if vertical {
		stage = imagetiming.ResizeVertical
	}
	done := imagetiming.Measure(ctx, stage)
	defer func() { done(returnErr) }()
	dst := image.NewNRGBA(image.Rect(0, 0, outW, outH))
	plan := resamplePlan{source: src, target: dst, filter: weights(input, size), vertical: vertical}
	plan.kernel = prepareResampleKernel(plan.filter)
	if trace := imagetiming.From(ctx); trace != nil {
		backend := resizeBackendName()
		if plan.kernel == nil {
			backend = backendGo
		}
		trace.ResizeBackend(backend)
	}
	if err := forRows(ctx, outH, workers, plan.renderRow); err != nil {
		return nil, err
	}
	return dst, nil
}

func (p *resamplePlan) renderRow(row int) {
	if p.kernel != nil {
		p.renderSIMDRow(row)
		return
	}
	p.renderScalarRow(row)
}

func (p *resamplePlan) renderScalarRow(row int) {
	if p.vertical {
		p.verticalRow(row)
		return
	}
	source, target := row*p.source.Stride, row*p.target.Stride
	for x, filter := range p.filter {
		filterPixel(p.source.Pix, p.target.Pix, source+filter.start*4, target+x*4, 4, filter.coefficients)
	}
}

func (p *resamplePlan) verticalRow(row int) {
	filter := p.filter[row]
	source, target := filter.start*p.source.Stride, row*p.target.Stride
	for x := range p.target.Bounds().Dx() {
		filterPixel(p.source.Pix, p.target.Pix, source+x*4, target+x*4, p.source.Stride, filter.coefficients)
	}
}

func filterPixel(src, dst []byte, source, destination, step int, coefficients []int) {
	const rounding = 1 << (coefficientBits - 1)
	red, green, blue, alpha := rounding, rounding, rounding, rounding
	// Traverse each tap once for all four channels. Keeping separate integer
	// accumulators preserves Pillow's per-channel accumulation and rounding.
	for k, coefficient := range coefficients {
		pixel := src[source+k*step:][:4]
		red += int(pixel[0]) * coefficient
		green += int(pixel[1]) * coefficient
		blue += int(pixel[2]) * coefficient
		alpha += int(pixel[3]) * coefficient
	}
	pixel := dst[destination:][:4]
	pixel[0] = filteredByte(red)
	pixel[1] = filteredByte(green)
	pixel[2] = filteredByte(blue)
	pixel[3] = filteredByte(alpha)
}

func filteredByte(sum int) byte {
	return byte(max(0, min(pixelMax, sum>>coefficientBits)))
}
