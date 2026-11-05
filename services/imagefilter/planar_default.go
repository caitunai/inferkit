//go:build !goexperiment.simd || (!arm64 && !amd64) || purego

package imagefilter

func writeRGBTensor(src []byte, dst []float32) { writeRGBTensorScalar(src, dst) }
func planarBackendName() string                { return backendGo }
