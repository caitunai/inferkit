//go:build goexperiment.simd && (arm64 || amd64) && !purego

package imagefilter

import (
	"image"
	"simd/archsimd"
)

func tensorBackendName() string { return planarBackendName() }

func tensorSIMD(img *image.NRGBA, dst []float32) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if !planarSIMDSupported() || w < 1 || h < 1 || w > imageSize || h > imageSize || len(dst) != inputLength {
		return false
	}
	for i := range dst {
		dst[i] = 1
	}
	ox, oy := (imageSize-w)/2, (imageSize-h)/2
	plane := imageSize * imageSize
	for y := range h {
		offset := (y+oy)*imageSize + ox
		writeRGBARow(img.Pix[y*img.Stride:][:w*4], dst[offset:], dst[plane+offset:], dst[2*plane+offset:])
	}
	return true
}

func writeRGBARow(src []byte, red, green, blue []float32) {
	mask := archsimd.BroadcastUint32x4(pixelMax)
	i := 0
	for ; i+4 <= len(src)/4; i += 4 {
		pixels := archsimd.LoadUint8x16(src[i*4:]).ReshapeToUint32s()
		alpha := pixels.ShiftAllRight(24)
		blendFour(pixels.And(mask), alpha, red[i:])
		blendFour(pixels.ShiftAllRight(8).And(mask), alpha, green[i:])
		blendFour(pixels.ShiftAllRight(16).And(mask), alpha, blue[i:])
	}
	for ; i < len(src)/4; i++ {
		pixel := src[i*4:][:4]
		alpha := int(pixel[3])
		red[i] = whiteChannel(pixel[0], alpha)
		green[i] = whiteChannel(pixel[1], alpha)
		blue[i] = whiteChannel(pixel[2], alpha)
	}
}

func blendFour(color, alpha archsimd.Uint32x4, dst []float32) {
	white := archsimd.BroadcastUint32x4(pixelMax)
	sum := color.Mul(alpha).Add(white.Sub(alpha).Mul(white)).Add(archsimd.BroadcastUint32x4(127))
	// Exact floor(sum/255) for [0,65152]; constant shifts require only AVX
	// on amd64, unlike variable per-lane shifts which require AVX2.
	value := sum.Add(archsimd.BroadcastUint32x4(1)).Add(sum.ShiftAllRight(8)).ShiftAllRight(8)
	value.BitsToInt32().ConvertToFloat32().Div(archsimd.BroadcastFloat32x4(pixelMax)).Store(dst)
}

func whiteChannel(value byte, alpha int) float32 {
	return float32((int(value)*alpha+pixelMax*(pixelMax-alpha)+127)/pixelMax) / float32(pixelMax)
}
