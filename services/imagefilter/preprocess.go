package imagefilter

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/draw"
	_ "image/jpeg" // Register supported image decoders.
	_ "image/png"
	"math"
	"slices"

	"github.com/gen2brain/gav1d/av1"
	"github.com/gen2brain/gav1d/avif"

	_ "golang.org/x/image/webp" // Register WebP, including lossless alpha images.
)

const formatAVIF = "avif"

func preprocess(ctx context.Context, data []byte, maxPixels int64, dst []float32) error {
	img, err := decodeImage(data, maxPixels)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return errors.Join(ErrInput, err)
	}
	b := img.Bounds()
	scale := min(float64(imageSize)/float64(b.Dx()), float64(imageSize)/float64(b.Dy()))
	w := max(1, min(imageSize, int(math.RoundToEven(float64(b.Dx())*scale))))
	h := max(1, min(imageSize, int(math.RoundToEven(float64(b.Dy())*scale))))
	rgba := toRGBA(img)
	resized := resizeRGBA(rgba, w, h)
	writeTensor(resized, dst)
	return nil
}

func decodeImage(data []byte, maxPixels int64) (image.Image, error) {
	config, format, err := imageConfig(data)
	if err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"jpeg", "png", "webp", formatAVIF}, format) {
		return nil, ErrInput
	}
	if err = validateDimensions(config, maxPixels); err != nil {
		return nil, err
	}
	img, err := decodePixels(data, format, maxPixels)
	if err != nil {
		return nil, err
	}
	if orientation := exifOrientation(data, format); orientation != 1 {
		img = orientedImage{source: img, orientation: orientation}
	}
	return img, nil
}

func imageConfig(data []byte) (image.Config, string, error) {
	// AVIF may use the generic mif1/msf1 major brand. Let its container parser
	// validate the compatible brands and AV1 primary item instead of suffixes.
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		config, err := avif.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return image.Config{}, "", errors.Join(ErrInput, err)
		}
		return config, formatAVIF, nil
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", errors.Join(ErrInput, err)
	}
	return config, format, nil
}

func decodePixels(data []byte, format string, maxPixels int64) (image.Image, error) {
	if format == formatAVIF {
		// Also bound coded AV1 frames/tiles, whose dimensions may differ from
		// the container header. Container transforms are applied exactly once.
		img, err := avif.Decode(bytes.NewReader(data), avif.Options{AutoRotate: true, FrameSizeLimit: int(maxPixels)})
		if errors.Is(err, av1.ErrFrameTooLarge) {
			return nil, errors.Join(ErrTooLarge, err)
		}
		if err != nil {
			return nil, errors.Join(ErrInput, err)
		}
		return img, nil
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, errors.Join(ErrInput, err)
	}
	return img, nil
}

func validateDimensions(config image.Config, maxPixels int64) error {
	if config.Width < 1 || config.Height < 1 || config.Width > maxImageDimension || config.Height > maxImageDimension {
		return ErrTooLarge
	}
	if int64(config.Width) > maxPixels/int64(config.Height) {
		return ErrTooLarge
	}
	return nil
}

// Pillow filters 8-bit premultiplied RGBA, clips between separable passes,
// then unpremultiplies with integer division before compositing onto white.
func toRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	dst := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

func resizeRGBA(src *image.NRGBA, w, h int) *image.NRGBA {
	if src.Bounds().Dx() == w && src.Bounds().Dy() == h {
		return src
	}
	premultiply(src)
	resized := resampleAxis(src, w, false)
	resized = resampleAxis(resized, h, true)
	unpremultiply(resized)
	return resized
}

func premultiply(src *image.NRGBA) {
	for i := 0; i < len(src.Pix); i += 4 {
		for c := range channels {
			src.Pix[i+c] = byte((int(src.Pix[i+c])*int(src.Pix[i+3]) + 127) / pixelMax) //nolint:gosec // Product of two bytes divided by 255 is in [0,255].
		}
	}
}

func unpremultiply(resized *image.NRGBA) {
	for i := 0; i < len(resized.Pix); i += 4 {
		a := int(resized.Pix[i+3])
		if a == 0 || a == pixelMax {
			continue
		}
		for c := range channels {
			resized.Pix[i+c] = byte(min(pixelMax, int(resized.Pix[i+c])*pixelMax/a)) //nolint:gosec // Nonnegative byte product clamped to 255.
		}
	}
}

func writeTensor(img *image.NRGBA, dst []float32) {
	for i := range dst {
		dst[i] = 1
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	offsetX, offsetY := (imageSize-w)/2, (imageSize-h)/2
	for y := range h {
		for x := range w {
			i := img.PixOffset(x, y)
			a := int(img.Pix[i+3])
			for c := range channels {
				value := (int(img.Pix[i+c])*a + pixelMax*(pixelMax-a) + 127) / pixelMax
				dst[(c*imageSize+y+offsetY)*imageSize+x+offsetX] = float32(value) / float32(pixelMax)
			}
		}
	}
}
