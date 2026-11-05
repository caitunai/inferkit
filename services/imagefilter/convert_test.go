package imagefilter

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"testing"
)

func referenceNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func TestOpaqueRGBAConversionAndOwnership(t *testing.T) {
	t.Parallel()
	parent := image.NewRGBA(image.Rect(-5, 9, 40, 30))
	draw.Draw(parent, parent.Bounds(), &image.Uniform{C: color.RGBA{R: 17, G: 253, B: 201, A: pixelMax}}, image.Point{}, draw.Src)
	for _, rect := range []image.Rectangle{parent.Bounds(), image.Rect(-2, 11, 17, 23)} {
		img := parent.SubImage(rect)
		got, want := toRGBA(img), referenceNRGBA(img)
		if !bytes.Equal(got.Pix, want.Pix) || got.Bounds() != want.Bounds() {
			t.Fatal("opaque/subimage conversion differs")
		}
		got.Pix[0] = 0
		if !bytes.Equal(toRGBA(img).Pix, want.Pix) {
			t.Fatal("converted output aliases source image")
		}
	}
}

func TestTransparentRGBAConversion(t *testing.T) {
	t.Parallel()
	img := image.NewRGBA(image.Rect(0, 0, 256, 1))
	for alpha := range 256 {
		img.SetRGBA(alpha, 0, color.RGBA{R: byte(alpha), G: byte(alpha / 2), A: byte(alpha)})
	}
	if !bytes.Equal(toRGBA(img).Pix, referenceNRGBA(img).Pix) {
		t.Fatal("transparent RGBA arithmetic changed")
	}
}

func TestYCbCrConversionParity(t *testing.T) {
	t.Parallel()
	for _, ratio := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410} {
		img := conversionYCbCr(image.Rect(-7, -5, 31, 26), ratio)
		assertYCbCrParity(t, img)
		assertYCbCrParity(t, img.SubImage(image.Rect(-3, -1, 20, 19)))
	}
}

func assertYCbCrParity(t *testing.T, img image.Image) {
	t.Helper()
	got, want := toRGBA(img), referenceNRGBA(img)
	if !bytes.Equal(got.Pix, want.Pix) || got.Bounds() != want.Bounds() {
		t.Fatal("YCbCr conversion differs from draw.Src")
	}
}

func conversionYCbCr(rect image.Rectangle, ratio image.YCbCrSubsampleRatio) *image.YCbCr {
	img := image.NewYCbCr(rect, ratio)
	for i := range img.Y {
		img.Y[i] = byte((i*19 + i/7) % 256)
	}
	for i := range img.Cb {
		img.Cb[i], img.Cr[i] = byte((i*31)%256), byte((i*47+117)%256)
	}
	return img
}

func BenchmarkConversion(b *testing.B) {
	rgb := image.NewRGBA(image.Rect(0, 0, 1070, 680))
	draw.Draw(rgb, rgb.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	images := []struct {
		source image.Image
		name   string
	}{
		{source: rgb, name: "rgba"},
		{source: conversionYCbCr(rgb.Bounds(), image.YCbCrSubsampleRatio420), name: "ycbcr"},
	}
	for _, input := range images {
		b.Run(input.name, func(b *testing.B) { benchmarkConversionModes(b, input.source) })
	}
}

func benchmarkConversionModes(b *testing.B, source image.Image) {
	b.Helper()
	modes := []struct {
		convert func(image.Image) *image.NRGBA
		name    string
	}{
		{convert: referenceNRGBA, name: "standard"},
		{convert: toRGBA, name: "optimized"},
	}
	for _, mode := range modes {
		b.Run(mode.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				mode.convert(source)
			}
		})
	}
}
