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

	"github.com/caitun/inferkit/services/imagepipeline"
)

func requireNativePipeline(tb testing.TB) {
	tb.Helper()
	if !imagepipeline.Enabled {
		tb.Skip("native preprocessing requires CGO_ENABLED=1 and -tags vips")
	}
	if err := imagepipeline.Available(); err != nil {
		tb.Fatal(err)
	}
}

func TestNativePreprocessingNeverFallsBack(t *testing.T) {
	t.Parallel()
	if imagepipeline.Enabled {
		t.Skip("this check exercises builds without the native image pipeline")
	}
	err := preprocess(t.Context(), formatFixture(t, fixturePNG), 48*32, make([]float32, inputLength))
	if !errors.Is(err, ErrConfig) || !errors.Is(err, imagepipeline.ErrUnavailable) {
		t.Fatalf("new model silently fell back to another preprocessor: %v", err)
	}
}

func TestPreprocessAlphaPaddingAndLimits(t *testing.T) {
	requireNativePipeline(t)
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

func TestPNGEXIFPreprocessing(t *testing.T) {
	requireNativePipeline(t)
	t.Parallel()
	// Stay inside the canvas so orientation is checked independently of the
	// thumbnailer's axis-order-dependent interpolation rounding.
	img := image.NewNRGBA(image.Rect(0, 0, 320, 213))
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
	if err := preprocess(t.Context(), data, 320*213, got); err != nil {
		t.Fatal(err)
	}
	oriented := toRGBA(rotatedFixtureImage(img))
	var rotated bytes.Buffer
	if err := png.Encode(&rotated, oriented); err != nil {
		t.Fatal(err)
	}
	if err := preprocess(t.Context(), rotated.Bytes(), 320*213, want); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatal("EXIF rotation differs from physically rotated image")
	}
}

func testTIFF(orientation uint16) []byte {
	data := make([]byte, 26)
	copy(data, []byte{'I', 'I', 42, 0, 8, 0, 0, 0, 1, 0, 0x12, 1, 3, 0, 1, 0, 0, 0})
	binary.LittleEndian.PutUint16(data[18:], orientation)
	return data
}

func rotatedFixtureImage(img image.Image) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, img.Bounds().Dy(), img.Bounds().Dx()))
	for y := range dst.Bounds().Dy() {
		for x := range dst.Bounds().Dx() {
			dst.Set(x, y, img.At(y, img.Bounds().Dy()-1-x))
		}
	}
	return dst
}
