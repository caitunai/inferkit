//go:build !goexperiment.simd || purego

package imagefilter

import "image"

func tensorSIMD(_ *image.NRGBA, _ []float32) bool { return false }

func tensorBackendName() string { return backendGo }
