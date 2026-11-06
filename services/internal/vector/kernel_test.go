//go:build goexperiment.simd && !purego

package vector

import (
	"math"
	"slices"
	"testing"
)

func TestProbabilityContract(t *testing.T) {
	t.Parallel()
	for _, size := range []int{0, 1, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 18710} {
		values := make([]float32, size)
		for i := range values {
			values[i] = float32((i*17+i/7)%8) / 8
		}
		assertProbability(t, values)
		assertInvalidProbabilities(t, values)
	}
	// Equal maxima across SIMD chunks and in the scalar tail keep the first.
	assertProbability(t, []float32{0, 1, 1, 0, 1, 1, 0, 1, 1})
}

func assertInvalidProbabilities(t *testing.T, values []float32) {
	t.Helper()
	limit := min(len(values), 33)
	positions := make([]int, 0, limit+1)
	for i := range limit {
		positions = append(positions, i)
	}
	if len(values) > len(positions) {
		positions = append(positions, len(values)-1)
	}
	for _, invalid := range []float32{-0.1, 1.1, float32(math.NaN()), float32(math.Inf(1)), float32(math.Inf(-1))} {
		for _, i := range positions {
			original := values[i]
			values[i] = invalid
			assertProbability(t, values)
			values[i] = original
		}
	}
}

func assertProbability(t *testing.T, values []float32) {
	t.Helper()
	want, valid := referenceArgmax(values)
	got, gotValid := Argmax(values)
	if gotValid != valid || valid && got != want {
		t.Fatalf("argmax changed first maximum or validation: %d/%v vs %d/%v", got, gotValid, want, valid)
	}
	if ValidProbabilities(values) != (len(values) == 0 || valid) {
		t.Fatal("probability validation changed")
	}
}

func referenceArgmax(values []float32) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	best := 0
	for i, value := range values {
		if !(value >= 0 && value <= 1) {
			return 0, false
		}
		if value > values[best] {
			best = i
		}
	}
	return best, true
}

func TestRGBAndAlphaExact(t *testing.T) {
	t.Parallel()
	for _, count := range []int{0, 1, 3, 4, 5, 7, 8, 9, 15, 16, 17, 31, 32, 33, 257, 102400} {
		assertPixels(t, count, false)
		assertPixels(t, count, true)
	}
}

func assertPixels(t *testing.T, count int, alpha bool) {
	t.Helper()
	stride := rgbChannels
	if alpha {
		stride = rgbaChannels
	}
	src := make([]byte, count*stride)
	for i := range src {
		src[i] = byte((i*37 + i/11) % 256)
	}
	planes := [rgbChannels][]float32{}
	for i := range planes {
		planes[i] = slices.Repeat([]float32{-99}, count+1)
	}
	var written int
	if alpha {
		written = BlendRGBA(src, planes[0], planes[1], planes[2])
	} else {
		written = NormalizeRGB(src, planes[0], planes[1], planes[2])
	}
	assertPixelPlanes(t, src, planes, written, stride)
}

func assertPixelPlanes(t *testing.T, src []byte, planes [rgbChannels][]float32, written, stride int) {
	t.Helper()
	for channel, plane := range planes {
		for i := range written {
			if plane[i] != referenceChannel(src[i*stride:][:stride], channel) {
				t.Fatal("SIMD changed a channel or integer alpha rounding")
			}
		}
		if slices.ContainsFunc(plane[written:], func(value float32) bool { return value != -99 }) {
			t.Fatal("SIMD wrote beyond complete batches")
		}
	}
}

func referenceChannel(pixel []byte, channel int) float32 {
	value := int(pixel[channel])
	if len(pixel) == rgbaChannels {
		a := int(pixel[3])
		value = (value*a + pixelMax*(pixelMax-a) + 127) / pixelMax
	}
	return float32(value) / pixelMax
}

func TestFilterChannelsAndSignedTaps(t *testing.T) {
	t.Parallel()
	src := make([]byte, 64)
	for i := range src {
		src[i] = byte(i*3 + 1)
	}
	coefficients := []int32{1024, -512, 512}
	for _, step := range []int{4, 7, 16} {
		got := FilterRGBA(src, 2, step, coefficients, 512)
		for channel := range got {
			want := int32(512)
			for k, coefficient := range coefficients {
				want += int32(src[2+k*step+channel]) * coefficient
			}
			if got[channel] != want {
				t.Fatal("SIMD changed signed filtering or channel order")
			}
		}
	}
}

func BenchmarkArgmax(b *testing.B) {
	values := make([]float32, 18710)
	values[12345] = 1
	for _, variant := range []struct {
		kernel func([]float32) (int, bool)
		name   string
	}{{kernel: Argmax, name: "portable"}, {kernel: referenceArgmax, name: "scalar"}} {
		b.Run(variant.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				got, valid := variant.kernel(values)
				if !valid || got != 12345 {
					b.Fatal("incorrect argmax")
				}
			}
		})
	}
}
