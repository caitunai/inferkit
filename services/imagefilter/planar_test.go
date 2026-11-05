package imagefilter

import (
	"slices"
	"testing"
)

func TestRGBTensorExact(t *testing.T) {
	t.Parallel()
	for _, count := range []int{0, 1, 3, 4, 7, 15, 16, 17, 31, 256, inputLength} {
		src := make([]byte, count*channels)
		for i := range src {
			src[i] = byte(i % 256)
		}
		got, want := make([]float32, count*channels), make([]float32, count*channels)
		writeRGBTensorScalar(src, want)
		writeRGBTensor(src, got)
		if !slices.Equal(got, want) {
			t.Fatalf("SIMD normalization changed float32 division at size %d", count)
		}
	}
}

func BenchmarkRGBTensor(b *testing.B) {
	src := make([]byte, inputLength)
	for i := range src {
		src[i] = byte(i % 256)
	}
	dst := make([]float32, inputLength)
	for _, kernel := range []struct {
		write func([]byte, []float32)
		name  string
	}{{writeRGBTensorScalar, "scalar"}, {writeRGBTensor, planarBackendName()}} {
		b.Run(kernel.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				kernel.write(src, dst)
			}
		})
	}
}
