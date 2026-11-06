//go:build !goexperiment.simd || (!arm64 && !amd64) || purego

package ocr

func tensorBackendName() string                      { return "go" }
func probabilityArgmax(values []float32) (int, bool) { return scalarProbabilityArgmax(values) }
func probabilitiesValid(values []float32) bool       { return scalarProbabilitiesValid(values) }
