//go:build goexperiment.simd && (arm64 || amd64) && !purego

package ocr

import "simd/archsimd"

func tensorBackendName() string {
	if simdSupported() {
		return "go_simd"
	}
	return "go"
}

func validProbabilityVector(v archsimd.Float32x4) bool {
	mask := v.GreaterEqual(archsimd.BroadcastFloat32x4(0)).And(v.LessEqual(archsimd.BroadcastFloat32x4(1)))
	return mask.ToInt32x4().ReduceMax() == -1
}

func probabilitiesValid(values []float32) bool {
	if !simdSupported() {
		return scalarProbabilitiesValid(values)
	}
	i := 0
	for ; i+4 <= len(values); i += 4 {
		if !validProbabilityVector(archsimd.LoadFloat32x4(values[i:])) {
			return false
		}
	}
	return scalarProbabilitiesValid(values[i:])
}

func probabilityArgmax(values []float32) (int, bool) {
	if !simdSupported() || len(values) < 4 {
		return scalarProbabilityArgmax(values)
	}
	maxima := archsimd.BroadcastFloat32x4(-1)
	positions := archsimd.LoadInt32x4([]int32{0, 1, 2, 3})
	indices := positions
	step := archsimd.BroadcastInt32x4(4)
	i := 0
	for ; i+4 <= len(values); i += 4 {
		v := archsimd.LoadFloat32x4(values[i:])
		if !validProbabilityVector(v) {
			return 0, false
		}
		better := v.Greater(maxima)
		indices = positions.IfElse(better, indices)
		maxima = v.IfElse(better, maxima)
		positions = positions.Add(step)
	}
	maximum := maxima.ReduceMax()
	first := indices.IfElse(maxima.Equal(archsimd.BroadcastFloat32x4(maximum)), archsimd.BroadcastInt32x4(1<<30)).ReduceMin()
	best := int(first)
	tail, valid := scalarProbabilityArgmax(values[i:])
	if i < len(values) {
		if !valid {
			return 0, false
		}
		if values[i+tail] > maximum {
			best = i + tail
		}
	}
	return best, true
}
