//go:build goexperiment.simd && amd64 && !purego

package imagefilter

import "simd/archsimd"

// These 128-bit Go intrinsics require AVX. Older CPUs retain scalar conversion;
// native image decoding and ONNX Runtime perform their own CPU dispatch.
func planarSIMDSupported() bool { return archsimd.X86.AVX() }

func rgbGather(pixels, indices archsimd.Uint8x16) archsimd.Uint8x16 {
	return pixels.PermuteOrZero(indices.BitsToInt8())
}
