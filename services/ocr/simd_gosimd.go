//go:build goexperiment.simd && !purego

package ocr

import "github.com/caitun/inferkit/services/internal/vector"

func tensorBackendName() string {
	if vector.Enabled() {
		return "go_simd"
	}
	return "go"
}

func probabilitiesValid(values []float32) bool {
	if !vector.Enabled() {
		return scalarProbabilitiesValid(values)
	}
	return vector.ValidProbabilities(values)
}

func probabilityArgmax(values []float32) (int, bool) {
	if !vector.Enabled() {
		return scalarProbabilityArgmax(values)
	}
	return vector.Argmax(values)
}
