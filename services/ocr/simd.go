package ocr

import "math"

func scalarProbabilityArgmax(values []float32) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	best := 0
	for i, v := range values {
		if math.IsNaN(float64(v)) || v < 0 || v > 1 {
			return 0, false
		}
		if v > values[best] {
			best = i
		}
	}
	return best, true
}
