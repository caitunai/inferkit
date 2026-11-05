//go:build !cgo || (!arm64 && !amd64) || purego

package imagefilter

import "image"

func tensorSIMD(_ *image.NRGBA, _ []float32) bool { return false }

func tensorBackendName() string { return backendGo }
