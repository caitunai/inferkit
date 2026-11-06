//go:build goexperiment.simd && !purego

package vector

import (
	"encoding/binary"
	"simd"
)

const (
	pixelMax     = 255
	rgbChannels  = 3
	rgbaChannels = 4
)

// NormalizeRGB writes complete SIMD batches of interleaved RGB into planes.
// It returns the number of pixels written; callers own scalar tail handling.
func NormalizeRGB(src []byte, red, green, blue []float32) int {
	mask := simd.BroadcastUint32s(pixelMax)
	width := mask.Len()
	packed := make([]uint32, width)
	i := 0
	// One look-ahead byte permits unaligned 32-bit loads for packed RGB.
	for ; (i+width)*rgbChannels < len(src); i += width {
		for lane := range width {
			packed[lane] = binary.LittleEndian.Uint32(src[(i+lane)*rgbChannels:])
		}
		pixels := simd.LoadUint32s(packed)
		normalize(pixels.And(mask), red[i:])
		normalize(pixels.ShiftAllRight(8).And(mask), green[i:])
		normalize(pixels.ShiftAllRight(16).And(mask), blue[i:])
	}
	return i
}

func normalize(values simd.Uint32s, dst []float32) {
	values.BitsToInt32().ConvertToFloat32().Div(simd.BroadcastFloat32s(pixelMax)).Store(dst)
}

// BlendRGBA composites complete SIMD batches onto white using exact integers.
func BlendRGBA(src []byte, red, green, blue []float32) int {
	mask := simd.BroadcastUint32s(pixelMax)
	width := mask.Len()
	i := 0
	for ; i+width <= len(src)/rgbaChannels; i += width {
		pixels := simd.LoadUint8s(src[i*rgbaChannels:]).ReshapeToUint32s()
		alpha := pixels.ShiftAllRight(24)
		blend(pixels.And(mask), alpha, red[i:])
		blend(pixels.ShiftAllRight(8).And(mask), alpha, green[i:])
		blend(pixels.ShiftAllRight(16).And(mask), alpha, blue[i:])
	}
	return i
}

func blend(color, alpha simd.Uint32s, dst []float32) {
	white := simd.BroadcastUint32s(pixelMax)
	sum := color.Mul(alpha).Add(white.Sub(alpha).Mul(white)).Add(simd.BroadcastUint32s((pixelMax - 1) / 2))
	// Exact floor(sum/255) for this bounded byte-compositing range.
	value := sum.Add(simd.BroadcastUint32s(1)).Add(sum.ShiftAllRight(8)).ShiftAllRight(8)
	normalize(value, dst)
}

// FilterRGBA computes guarded 32-bit filter sums, leaving rounding to the caller.
func FilterRGBA(src []byte, source, step int, coefficients []int32, rounding int32) [rgbaChannels]int32 {
	sum := simd.BroadcastInt32s(rounding)
	for k, coefficient := range coefficients {
		pixel := src[source+k*step:][:rgbaChannels]
		channels, _ := simd.LoadInt32sPart([]int32{int32(pixel[0]), int32(pixel[1]), int32(pixel[2]), int32(pixel[3])})
		sum = channels.Mul(simd.BroadcastInt32s(coefficient)).Add(sum)
	}
	var result [rgbaChannels]int32
	sum.StorePart(result[:])
	return result
}
