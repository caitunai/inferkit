//go:build goexperiment.simd && !purego

package vector

import (
	"math"
	"simd"
	"slices"
)

// Enabled reports actual hardware execution, including GODEBUG=simd=0 fallback.
func Enabled() bool { return !simd.Emulated() }

func probabilityMask(v simd.Float32s) simd.Mask32s {
	return v.GreaterEqual(simd.BroadcastFloat32s(0)).And(v.LessEqual(simd.BroadcastFloat32s(1)))
}

func all(mask simd.Mask32s, scratch []int32) bool {
	mask.ToInt32s().Store(scratch)
	return !slices.Contains(scratch, 0)
}

// ValidProbabilities rejects NaN, infinities and values outside [0,1].
func ValidProbabilities(values []float32) bool {
	zero := simd.BroadcastFloat32s(0)
	width := zero.Len()
	valid := probabilityMask(zero)
	i := 0
	for ; i+width <= len(values); i += width {
		valid = valid.And(probabilityMask(simd.LoadFloat32s(values[i:])))
	}
	if !all(valid, make([]int32, width)) {
		return false
	}
	for _, value := range values[i:] {
		if !(value >= 0 && value <= 1) {
			return false
		}
	}
	return true
}

// Argmax fuses validation and argmax, preserving the first index on ties.
func Argmax(values []float32) (int, bool) {
	if len(values) == 0 {
		return 0, false
	}
	maxima := simd.BroadcastFloat32s(-1)
	width := maxima.Len()
	if len(values) < width || len(values) > math.MaxInt32 || width > math.MaxInt32 {
		return argmaxTail(values, 0, 0)
	}
	lanes := make([]int32, width)
	position := int32(0)
	for i := range width {
		lanes[i] = position
		position++
	}
	positions := simd.LoadInt32s(lanes)
	indices := positions
	step := simd.BroadcastInt32s(position)
	valid := probabilityMask(simd.BroadcastFloat32s(0))
	i := 0
	for ; i+width <= len(values); i += width {
		v := simd.LoadFloat32s(values[i:])
		valid = valid.And(probabilityMask(v))
		better := v.Greater(maxima)
		indices = positions.IfElse(better, indices)
		maxima = v.IfElse(better, maxima)
		positions = positions.Add(step)
	}
	if !all(valid, lanes) {
		return 0, false
	}
	maxValues := make([]float32, width)
	maxima.Store(maxValues)
	indices.Store(lanes)
	return argmaxTail(values, i, firstMaximum(maxValues, lanes))
}

func firstMaximum(values []float32, indices []int32) int {
	best := 0
	for i, value := range values {
		if value > values[best] || value == values[best] && indices[i] < indices[best] {
			best = i
		}
	}
	return int(indices[best])
}

func argmaxTail(values []float32, start, best int) (int, bool) {
	for i := start; i < len(values); i++ {
		value := values[i]
		if !(value >= 0 && value <= 1) {
			return 0, false
		}
		if value > values[best] {
			best = i
		}
	}
	return best, true
}
