package imageinput

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"slices"
	"testing"
)

func TestComparisonRejectsMultipleImageHeaders(t *testing.T) {
	t.Parallel()
	mpf := []byte("\xff\xd8\xff\xff\xe2\x00\x06MPF\x00")
	pngData := slices.Concat([]byte(pngSignature), []byte{0, 0, 0, 0}, []byte("acTL"), make([]byte, 4))
	webp := make([]byte, 21)
	copy(webp[12:], "VP8X")
	webp[20] = 2
	avifData := make([]byte, 20)
	binary.BigEndian.PutUint32(avifData, 20)
	copy(avifData[8:], "avif")
	copy(avifData[16:], "avis")
	for format, data := range map[string][]byte{formatJPEG: mpf, formatPNG: pngData, formatWebP: webp, formatAVIF: avifData} {
		if !multipleImages(data, format) {
			t.Fatalf("multiple-image header accepted: %s", format)
		}
		for size := range data {
			multipleImages(data[:size], format)
		}
	}
}

func TestGoDecoderRejectsMultipleBeforePixels(t *testing.T) {
	t.Parallel()
	src := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	var jpegData, pngData bytes.Buffer
	if err := jpeg.Encode(&jpegData, src, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pngData, src); err != nil {
		t.Fatal(err)
	}
	jpegMulti := slices.Concat(jpegData.Bytes()[:2], []byte("\xff\xe2\x00\x06MPF\x00"), jpegData.Bytes()[2:])
	pngMulti := slices.Concat(pngData.Bytes()[:33], []byte{0, 0, 0, 0, 'a', 'c', 'T', 'L', 0, 0, 0, 0}, pngData.Bytes()[33:])
	for _, data := range [][]byte{jpegMulti, pngMulti} {
		if _, _, _, err := DecodeGoWithBackend(data, 6); !errors.Is(err, ErrInput) {
			t.Fatalf("multiple pictures decoded: %v", err)
		}
	}
	img, format, backend, err := DecodeGoWithBackend(jpegData.Bytes(), 6)
	if err != nil || format != formatJPEG || backend != "go" {
		t.Fatalf("Go JPEG decode failed: %s %s %v", format, backend, err)
	}
	if _, ok := img.(*image.YCbCr); !ok {
		t.Fatalf("unexpected JPEG implementation: %T", img)
	}
}

func TestJPEGSegmentsPreserveEXIFWithPadding(t *testing.T) {
	t.Parallel()
	exif := slices.Concat([]byte("\xff\xd8\xff\x01\xff\xff\xe1\x00\x22Exif\x00\x00"), testTIFF(6))
	if orientation := exifOrientation(exif, formatJPEG); orientation != 6 {
		t.Fatalf("padded EXIF marker lost: %d", orientation)
	}
	for size := range exif {
		jpegEXIF(exif[:size])
	}
}
