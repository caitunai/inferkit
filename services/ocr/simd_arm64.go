//go:build goexperiment.simd && arm64 && !purego

package ocr

func simdSupported() bool { return true }
