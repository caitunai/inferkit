//go:build goexperiment.simd && arm64 && !purego

package imagefilter

import "simd/archsimd"

func planarSIMDSupported() bool { return true }

func rgbGather(pixels, indices archsimd.Uint8x16) archsimd.Uint8x16 {
	return pixels.LookupOrZero(indices)
}
