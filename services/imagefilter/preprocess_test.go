package imagefilter

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"slices"
	"testing"
)

func TestPreprocessAlphaPaddingAndLimits(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: pixelMax, A: 128})
	img.SetNRGBA(1, 0, color.NRGBA{})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	tensor := make([]float32, inputLength)
	if err := preprocess(t.Context(), buffer.Bytes(), 2, tensor); err != nil {
		t.Fatal(err)
	}
	if tensor[0] != 1 || tensor[imageSize*imageSize] != 1 {
		t.Fatal("padding must be white")
	}
	if tensor[100*imageSize] != 1 || tensor[imageSize*imageSize+100*imageSize] >= 1 {
		t.Fatal("red alpha must be composited on white")
	}
	if err := preprocess(t.Context(), buffer.Bytes(), 1, tensor); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pixel limit: %v", err)
	}
	if err := preprocess(t.Context(), []byte("invalid"), 2, tensor); !errors.Is(err, ErrInput) {
		t.Fatalf("decode error: %v", err)
	}
}

func TestOrientationAllTransforms(t *testing.T) {
	t.Parallel()
	img := image.NewGray(image.Rect(0, 0, 2, 3))
	copy(img.Pix, []byte{1, 2, 3, 4, 5, 6})
	expected := [][]byte{
		{1, 2, 3, 4, 5, 6},
		{2, 1, 4, 3, 6, 5},
		{6, 5, 4, 3, 2, 1},
		{5, 6, 3, 4, 1, 2},
		{1, 3, 5, 2, 4, 6},
		{5, 3, 1, 6, 4, 2},
		{6, 4, 2, 5, 3, 1},
		{2, 4, 6, 1, 3, 5},
	}
	for index, want := range expected {
		oriented := orientedImage{source: img, orientation: uint16(index + 1)}
		var got []byte
		for y := range oriented.Bounds().Dy() {
			for x := range oriented.Bounds().Dx() {
				r, _, _, _ := oriented.At(x, y).RGBA()
				got = append(got, byte(r>>8)) //nolint:gosec // Color.RGBA returns channels in [0,65535]; shifting by eight fits a byte.
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("orientation %d: %v, want %v", index+1, got, want)
		}
	}
}

func TestExtremeImageDimensionsAreBounded(t *testing.T) {
	t.Parallel()
	config := image.Config{Width: maxImageDimension + 1, Height: 1}
	if err := validateDimensions(config, 100_000_000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("extreme aspect ratio: %v", err)
	}
}

func testTIFF(orientation uint16) []byte {
	data := make([]byte, 26)
	copy(data, []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0})
	binary.LittleEndian.PutUint16(data[18:], orientation)
	return data
}

func TestEXIFContainers(t *testing.T) {
	t.Parallel()
	tiff := testTIFF(6)
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe1, 0, 34}
	jpeg = append(jpeg, []byte("Exif\x00\x00")...)
	jpeg = append(jpeg, tiff...)
	pngData := make([]byte, 16)
	binary.BigEndian.PutUint32(pngData[8:], 26)
	copy(pngData[12:], "eXIf")
	pngData = slices.Concat(pngData, tiff)
	webp := make([]byte, 20)
	copy(webp[12:], "EXIF")
	binary.LittleEndian.PutUint32(webp[16:], 26)
	webp = slices.Concat(webp, tiff)
	for format, data := range map[string][]byte{"jpeg": jpeg, "png": pngData, "webp": webp} {
		if got := exifOrientation(data, format); got != 6 {
			t.Errorf("%s orientation = %d", format, got)
		}
		if got := exifOrientation(data[:len(data)-10], format); got != 1 {
			t.Errorf("%s truncated metadata = %d", format, got)
		}
	}
}

func TestPNGEXIFPreprocessing(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	img.SetNRGBA(0, 0, color.NRGBA{R: pixelMax, A: pixelMax})
	var source bytes.Buffer
	if err := png.Encode(&source, img); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 12)
	binary.BigEndian.PutUint32(chunk, 26)
	copy(chunk[4:], "eXIf")
	chunk = append(chunk[:8], testTIFF(6)...)
	chunk = binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
	data := slices.Concat(source.Bytes()[:33], chunk, source.Bytes()[33:])
	got, want := make([]float32, inputLength), make([]float32, inputLength)
	if err := preprocess(t.Context(), data, 6, got); err != nil {
		t.Fatal(err)
	}
	oriented := toRGBA(orientedImage{source: img, orientation: 6})
	var rotated bytes.Buffer
	if err := png.Encode(&rotated, oriented); err != nil {
		t.Fatal(err)
	}
	if err := preprocess(t.Context(), rotated.Bytes(), 6, want); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatal("EXIF rotation differs from physically rotated image")
	}
}
