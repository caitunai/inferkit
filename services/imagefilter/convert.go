package imagefilter

import (
	"image"
	"image/draw"
)

// decodedPixels consumes a request-local decoder result. Its pixels are never
// cached or shared; resizing may modify them after ownership is transferred.
// Keep toRGBA as the copying conversion for borrowed images and references.
func decodedPixels(img image.Image) *image.NRGBA {
	if source, ok := img.(*image.NRGBA); ok && source.Rect.Min == (image.Point{}) {
		return source
	}
	if source, ok := img.(*image.RGBA); ok && source.Rect.Min == (image.Point{}) && source.Opaque() {
		return &image.NRGBA{Pix: source.Pix, Stride: source.Stride, Rect: source.Rect}
	}
	return toRGBA(img)
}

// toRGBA returns an owned, straight-alpha buffer. Its layout is unchanged from
// draw.Src; opaque RGBA can be copied directly, and YCbCr uses the RGB fast path.
func toRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	switch source := img.(type) {
	case *image.RGBA:
		if source.Opaque() {
			copyOpaqueRGBA(dst, source)
			return dst
		}
	case *image.YCbCr:
		// YCbCr is opaque. RGB and straight-alpha RGB therefore have identical
		// bytes; this header enables the standard library's specialized converter.
		rgb := &image.RGBA{Pix: dst.Pix, Stride: dst.Stride, Rect: dst.Rect}
		draw.Draw(rgb, rgb.Bounds(), source, b.Min, draw.Src)
		return dst
	}
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func copyOpaqueRGBA(dst *image.NRGBA, src *image.RGBA) {
	b := src.Bounds()
	rowBytes := b.Dx() * 4
	for y := range b.Dy() {
		start := src.PixOffset(b.Min.X, b.Min.Y+y)
		copy(dst.Pix[y*dst.Stride:][:rowBytes], src.Pix[start:][:rowBytes])
	}
}

func pixelType(img image.Image) string {
	switch img.(type) {
	case *image.RGBA:
		return "rgba"
	case *image.NRGBA:
		return "nrgba"
	case *image.YCbCr:
		return "ycbcr"
	case *image.NYCbCrA:
		return "nycbcra"
	case *image.Gray:
		return "gray"
	case *image.Paletted:
		return "paletted"
	case *image.RGBA64, *image.NRGBA64, *image.Gray16:
		return "16bit"
	default:
		return "other"
	}
}
