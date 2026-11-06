//go:build goexperiment.simd && amd64 && !purego

package ocr

import "simd/archsimd"

func simdSupported() bool { return archsimd.X86.AVX() }
