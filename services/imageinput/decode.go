// Package imageinput provides bounded image loading and orientation-aware decoding.
package imageinput

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg" // Register supported decoders.
	"image/png"
	"io"
	"slices"

	"github.com/gen2brain/gav1d/av1"
	"github.com/gen2brain/gav1d/avif"
	fastpng "github.com/woozymasta/png"
	_ "golang.org/x/image/webp" // Register WebP decoding.
)

// MaxDimension bounds allocations for extremely thin images.
const (
	MaxDimension = 32768
	formatAVIF   = "avif"
	formatPNG    = "png"
	formatJPEG   = "jpeg"
	formatWebP   = "webp"
	pngSignature = "\x89PNG\r\n\x1a\n"
)

// Decode validates encoded dimensions before allocating decoded pixels.
func Decode(data []byte, maxPixels int64) (image.Image, error) {
	img, _, err := DecodeWithFormat(data, maxPixels)
	return img, err
}

// DecodeWithFormat also returns the validated codec name for request diagnostics.
func DecodeWithFormat(data []byte, maxPixels int64) (image.Image, string, error) {
	img, format, _, err := DecodeWithBackend(data, maxPixels)
	return img, format, err
}

// DecodeWithBackend reports the decoder actually used, including native fallback.
func DecodeWithBackend(data []byte, maxPixels int64) (image.Image, string, string, error) {
	config, format, err := imageConfig(data)
	if err != nil {
		return nil, format, "", err
	}
	if !slices.Contains([]string{formatJPEG, formatPNG, formatWebP, formatAVIF}, format) {
		return nil, format, "", ErrInput
	}
	if err = validateDimensions(config, maxPixels); err != nil {
		return nil, format, "", err
	}
	backend := "go"
	img, native := nativeDecode(data, config, format)
	if native {
		backend = NativeDecoder
	} else {
		img, err = decodePixels(data, format, maxPixels)
	}
	if err != nil {
		return nil, format, "", err
	}
	if orientation := exifOrientation(data, format); orientation != 1 {
		img = orientedImage{source: img, orientation: orientation}
	}
	return img, format, backend, nil
}

func imageConfig(data []byte) (image.Config, string, error) {
	if bytes.HasPrefix(data, []byte(pngSignature)) {
		return configuration(data, formatPNG, png.DecodeConfig)
	}
	// AVIF may use the generic mif1/msf1 major brand. Let its container parser
	// validate the compatible brands and AV1 primary item instead of suffixes.
	if len(data) >= 12 && string(data[4:8]) == "ftyp" {
		return configuration(data, formatAVIF, avif.DecodeConfig)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, "", errors.Join(ErrInput, err)
	}
	return config, format, nil
}

func configuration(data []byte, format string, decode func(io.Reader) (image.Config, error)) (image.Config, string, error) {
	config, err := decode(bytes.NewReader(data))
	if err != nil {
		return image.Config{}, format, errors.Join(ErrInput, err)
	}
	return config, format, nil
}

func decodePixels(data []byte, format string, maxPixels int64) (image.Image, error) {
	if format == formatPNG {
		img, err := fastpng.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, errors.Join(ErrInput, err)
		}
		return img, nil
	}
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
	if config.Width < 1 || config.Height < 1 || config.Width > MaxDimension || config.Height > MaxDimension {
		return ErrTooLarge
	}
	if int64(config.Width) > maxPixels/int64(config.Height) {
		return ErrTooLarge
	}
	return nil
}
