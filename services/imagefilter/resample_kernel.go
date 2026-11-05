package imagefilter

import "math"

type resampleKernel struct {
	coefficients []int32
	offsets      []int32
	starts       []int32
}

// The SIMD accumulator is signed 32-bit. Prove the full absolute tap bound,
// including rounding, before narrowing anything; unusual filters stay scalar.
func safeFilter(filter filterWeights) bool {
	bound := int64(1 << (coefficientBits - 1))
	for _, coefficient := range filter.coefficients {
		value := int64(coefficient)
		if value < -math.MaxInt32 || value > math.MaxInt32 {
			return false
		}
		if value < 0 {
			value = -value
		}
		bound += value * pixelMax
		if bound > math.MaxInt32 {
			return false
		}
	}
	return filter.start >= 0 && filter.start <= math.MaxInt32
}

func prepareResampleKernel(filters []filterWeights) *resampleKernel {
	if resizeBackendName() == backendGo || len(filters) == 0 {
		return nil
	}
	count := 0
	for _, filter := range filters {
		if !safeFilter(filter) || len(filter.coefficients) == 0 || len(filter.coefficients) > math.MaxInt32-count {
			return nil
		}
		count += len(filter.coefficients)
	}
	kernel := &resampleKernel{coefficients: make([]int32, 0, count), offsets: make([]int32, 1, len(filters)+1), starts: make([]int32, 0, len(filters))}
	for _, filter := range filters {
		kernel.starts = append(kernel.starts, int32(filter.start)) //nolint:gosec // safeFilter bounds the start before narrowing.
		for _, coefficient := range filter.coefficients {
			kernel.coefficients = append(kernel.coefficients, int32(coefficient)) //nolint:gosec // safeFilter proves the coefficient and complete accumulator range.
		}
		kernel.offsets = append(kernel.offsets, int32(len(kernel.coefficients))) //nolint:gosec // Total coefficient count was bounded by MaxInt32 above.
	}
	return kernel
}
