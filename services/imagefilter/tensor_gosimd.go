//go:build goexperiment.simd && !purego

package imagefilter

import (
	"image"

	"github.com/caitun/inferkit/services/internal/vector"
)

func tensorBackendName() string { return planarBackendName() }

func tensorSIMD(img *image.NRGBA, dst []float32) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if !vector.Enabled() || w < 1 || h < 1 || w > imageSize || h > imageSize || len(dst) != inputLength {
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
	i := vector.BlendRGBA(src, red, green, blue)
	for ; i < len(src)/4; i++ {
		pixel := src[i*4:][:4]
		alpha := int(pixel[3])
		red[i] = whiteChannel(pixel[0], alpha)
		green[i] = whiteChannel(pixel[1], alpha)
		blue[i] = whiteChannel(pixel[2], alpha)
	}
}

func whiteChannel(value byte, alpha int) float32 {
	return float32((int(value)*alpha+pixelMax*(pixelMax-alpha)+127)/pixelMax) / float32(pixelMax)
}
