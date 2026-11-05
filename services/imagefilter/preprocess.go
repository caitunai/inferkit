package imagefilter

import (
	"context"
	"errors"
	"image"
	"math"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
)

func preprocess(ctx context.Context, data []byte, maxPixels int64, dst []float32) error {
	return preprocessNative(ctx, data, maxPixels, dst)
}

func preprocessWithBudget(ctx context.Context, data []byte, maxPixels int64, dst []float32, reserve func() preparationLease) error {
	done := imagetiming.Measure(ctx, imagetiming.Decode)
	img, err := decodeImage(ctx, data, maxPixels)
	if err != nil {
		done(err)
		return err
	}
	if err = ctx.Err(); err != nil {
		done(err)
		return errors.Join(ErrInput, err)
	}
	done(nil)
	b := img.Bounds()
	if trace := imagetiming.From(ctx); trace != nil {
		trace.Image(b.Dx(), b.Dy(), pixelType(img))
	}
	done = imagetiming.Measure(ctx, imagetiming.Preprocess)
	scale := min(float64(imageSize)/float64(b.Dx()), float64(imageSize)/float64(b.Dy()))
	w := max(1, min(imageSize, int(math.RoundToEven(float64(b.Dx())*scale))))
	h := max(1, min(imageSize, int(math.RoundToEven(float64(b.Dy())*scale))))
	err = prepareTensor(ctx, img, w, h, dst, reserve)
	done(err)
	return err
}

func prepareTensor(ctx context.Context, img image.Image, w, h int, dst []float32, reserve func() preparationLease) error {
	done := imagetiming.Measure(ctx, imagetiming.Convert)
	rgba := decodedPixels(img)
	done(nil)
	done = imagetiming.Measure(ctx, imagetiming.Resize)
	resized, err := resizeWithBudget(ctx, rgba, w, h, reserve)
	done(err)
	if err != nil {
		return err
	}
	done = imagetiming.Measure(ctx, imagetiming.Tensor)
	writeTensor(resized, dst)
	done(nil)
	return nil
}

func decodeImage(ctx context.Context, data []byte, maxPixels int64) (image.Image, error) {
	img, format, decoder, err := imageinput.DecodeWithBackend(data, maxPixels)
	if trace := imagetiming.From(ctx); trace != nil {
		trace.Format(format)
		trace.Decoder(decoder)
	}
	if err != nil {
		return nil, errors.Join(ErrPreprocess, err)
	}
	return img, nil
}

// Pillow filters 8-bit premultiplied RGBA, clips between separable passes,
// then unpremultiplies with integer division before compositing onto white.
func resizeRGBA(ctx context.Context, src *image.NRGBA, w, h, workers int) (*image.NRGBA, error) {
	if src.Bounds().Dx() == w && src.Bounds().Dy() == h {
		return src, nil
	}
	if err := timedImageRows(ctx, src, workers, premultiplyPixels, imagetiming.Premultiply); err != nil {
		return nil, err
	}
	resized, err := resampleAxis(ctx, src, w, false, workers)
	if err != nil {
		return nil, err
	}
	resized, err = resampleAxis(ctx, resized, h, true, workers)
	if err != nil {
		return nil, err
	}
	if err = timedImageRows(ctx, resized, workers, unpremultiplyPixels, imagetiming.Unpremultiply); err != nil {
		return nil, err
	}
	return resized, nil
}

func premultiply(src *image.NRGBA) {
	premultiplyPixels(src.Pix)
}

func premultiplyPixels(pixels []byte) {
	for i := 0; i < len(pixels); i += 4 {
		if pixels[i+3] == pixelMax {
			continue
		}
		for c := range channels {
			pixels[i+c] = byte((int(pixels[i+c])*int(pixels[i+3]) + 127) / pixelMax) //nolint:gosec // Product of two bytes divided by 255 is in [0,255].
		}
	}
}

func unpremultiplyPixels(pixels []byte) {
	for i := 0; i < len(pixels); i += 4 {
		a := int(pixels[i+3])
		if a == 0 || a == pixelMax {
			continue
		}
		for c := range channels {
			pixels[i+c] = byte(min(pixelMax, int(pixels[i+c])*pixelMax/a)) //nolint:gosec // Nonnegative byte product clamped to 255.
		}
	}
}

func writeTensor(img *image.NRGBA, dst []float32) {
	if !tensorSIMD(img, dst) {
		writeTensorScalar(img, dst)
	}
}

func writeTensorScalar(img *image.NRGBA, dst []float32) {
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
