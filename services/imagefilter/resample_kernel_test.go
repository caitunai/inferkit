package imagefilter

import (
	"bytes"
	"image"
	"math"
	"slices"
	"testing"
)

func TestWeightArenaPreservesCoefficients(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{1, imageSize}, {7, 3}, {1070, imageSize}, {680, 203}, {32768, imageSize}} {
		for index, got := range weights(size[0], size[1]) {
			want := independentWeight(size[0], size[1], index)
			if got.start != want.start || !slices.Equal(got.coefficients, want.coefficients) {
				t.Fatal("weight arena changed fixed-point coefficients")
			}
		}
	}
}

// Retain an independent allocation-based oracle for the established rounding.
func independentWeight(input, output, index int) filterWeights {
	ratio := float64(input) / float64(output)
	scale := max(1, ratio)
	center := (float64(index) + 0.5) * ratio
	start := max(0, int(center-3*scale+0.5))
	end := min(input, int(center+3*scale+0.5))
	values := make([]float64, end-start)
	var total float64
	for i := range values {
		values[i] = lanczos((float64(start+i) - center + 0.5) / scale)
		total += values[i]
	}
	result := filterWeights{start: start, coefficients: make([]int, len(values))}
	for i, value := range values {
		result.coefficients[i] = int(math.Round(value / total * (1 << coefficientBits)))
	}
	return result
}

func TestResampleKernelMatchesScalar(t *testing.T) {
	t.Parallel()
	for _, size := range [][3]int{{1070, 680, imageSize}, {61, 37, 19}, {7, 13, 41}, {1, 51, imageSize}, {129, 3, 1}, {32768, 1, imageSize}, {1, 32768, imageSize}} {
		source := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
		for i := range source.Pix {
			source.Pix[i] = byte((i*31 + i/13) % 256)
		}
		for _, vertical := range []bool{false, true} {
			assertResampleKernel(t, source, size[2], vertical)
		}
	}
}

func assertResampleKernel(t *testing.T, source *image.NRGBA, size int, vertical bool) {
	t.Helper()
	w, h, input := size, source.Bounds().Dy(), source.Bounds().Dx()
	if vertical {
		w, h, input = source.Bounds().Dx(), size, source.Bounds().Dy()
	}
	want, got := image.NewNRGBA(image.Rect(0, 0, w, h)), image.NewNRGBA(image.Rect(0, 0, w, h))
	plan := resamplePlan{source: source, target: want, filter: weights(input, size), vertical: vertical}
	if err := forRows(t.Context(), h, 4, plan.renderRow); err != nil {
		t.Fatal(err)
	}
	plan.target, plan.kernel = got, prepareResampleKernel(plan.filter)
	if resizeBackendName() != backendGo && plan.kernel == nil {
		t.Fatal("normal filter was unexpectedly rejected by SIMD guard")
	}
	if err := forRows(t.Context(), h, 4, plan.renderRow); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Fatalf("SIMD changed pixels: source=%v size=%d vertical=%v", source.Bounds(), size, vertical)
	}
}

func TestResampleKernelRejectsOverflow(t *testing.T) {
	t.Parallel()
	for _, filter := range []filterWeights{{coefficients: []int{math.MaxInt32}}, {coefficients: []int{1 << coefficientBits, -1 << coefficientBits, 1 << coefficientBits}}, {start: -1}} {
		if prepareResampleKernel([]filterWeights{filter}) != nil {
			t.Fatal("unsafe 32-bit filter must use scalar arithmetic")
		}
	}
}

func TestDecodedPixelsOwnsRequestBuffers(t *testing.T) {
	t.Parallel()
	straight := image.NewNRGBA(image.Rect(0, 0, 16, 9))
	if &decodedPixels(straight).Pix[0] != &straight.Pix[0] {
		t.Fatal("decoded NRGBA was copied")
	}
	opaque := image.NewRGBA(straight.Bounds())
	for i := 3; i < len(opaque.Pix); i += 4 {
		opaque.Pix[i] = pixelMax
	}
	if &decodedPixels(opaque).Pix[0] != &opaque.Pix[0] {
		t.Fatal("owned opaque layout was copied")
	}
	transparent := image.NewRGBA(straight.Bounds())
	if &decodedPixels(transparent).Pix[0] == &transparent.Pix[0] {
		t.Fatal("premultiplied transparent pixels were incorrectly reinterpreted")
	}
}
