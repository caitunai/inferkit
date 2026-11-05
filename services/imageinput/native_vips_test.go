//go:build vips && cgo

package imageinput

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"testing"

	"github.com/caitun/inferkit/xutil"
)

func TestVipsRetainsGoPNGAndBounds(t *testing.T) {
	t.Parallel()
	for _, model := range []color.Model{color.NRGBAModel, color.GrayModel, color.NRGBA64Model} {
		source := image.NewNRGBA(image.Rect(0, 0, 59, 37))
		for i := range source.Pix {
			source.Pix[i] = byte(i % 256)
		}
		var buffer bytes.Buffer
		if err := png.Encode(&buffer, convertedImage{source, model}); err != nil {
			t.Fatal(err)
		}
		assertVipsPNG(t, buffer.Bytes())
	}
}

type convertedImage struct {
	image.Image
	model color.Model
}

func (i convertedImage) ColorModel() color.Model { return i.model }

func assertVipsPNG(t *testing.T, data []byte) {
	t.Helper()
	want, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	got, _, decoder, err := DecodeWithBackend(data, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if decoder != "go" {
		t.Fatal("PNG should retain the Go decoder")
	}
	assertPNGColors(t, got, want)

	if _, err = Decode(data, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatal("native decoder bypassed pixel bound")
	}
}

func TestVipsConcurrentDecode(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, image.NewRGBA(image.Rect(0, 0, 59, 37)), nil); err != nil {
		t.Fatal(err)
	}
	data := buffer.Bytes()
	group := xutil.ErrGroup(t.Context(), "vips_decode_test").Limit(8)
	for range 8 {
		group.Go(func(_ context.Context) error { assertVipsDecode(t, data); return nil })
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkNativeDecode(b *testing.B) {
	data := pngBenchmarkData(b)
	for format, input := range map[string][]byte{formatJPEG: jpegBenchmarkData(b, data), formatWebP: webpBenchmarkData(b, data)} {
		b.Run(format+"/go", func(b *testing.B) { benchmarkDecodeBackend(b, input, format, false) })
		b.Run(format+"/vips", func(b *testing.B) { benchmarkDecodeBackend(b, input, format, true) })
	}
}

func jpegBenchmarkData(b *testing.B, data []byte) []byte {
	b.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		b.Fatal(err)
	}
	var buffer bytes.Buffer
	if err = jpeg.Encode(&buffer, img, &jpeg.Options{Quality: 90}); err != nil {
		b.Fatal(err)
	}
	return buffer.Bytes()
}

func benchmarkDecodeBackend(b *testing.B, input []byte, format string, native bool) {
	b.Helper()
	config, _, err := imageConfig(input)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if native {
			if _, ok := nativeDecode(input, config, format); !ok {
				b.Fatal("VIPS decoder unavailable")
			}
		} else if _, decodeErr := decodePixels(input, format, 1_000_000); decodeErr != nil {
			b.Fatal(decodeErr)
		}
	}
}

func webpBenchmarkData(b *testing.B, data []byte) []byte {
	b.Helper()
	root, err := os.OpenRoot(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			b.Error(closeErr)
		}
	})
	if err = root.WriteFile("source.png", data, 0o600); err != nil {
		b.Fatal(err)
	}
	command := exec.CommandContext(b.Context(), "vips", "copy", "source.png", "target.webp")
	command.Dir = root.Name()
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		b.Fatalf("libvips-tools required to generate WebP: %v: %s", commandErr, output)
	}
	encoded, err := root.ReadFile("target.webp")
	if err != nil {
		b.Fatal(err)
	}
	return encoded
}

func assertPNGColors(t *testing.T, got, want image.Image) {
	t.Helper()
	if got.Bounds() != want.Bounds() {
		t.Fatal("native PNG dimensions differ")
	}
	for y := range want.Bounds().Dy() {
		for x := range want.Bounds().Dx() {
			gr, gg, gb, ga := got.At(x, y).RGBA()
			wr, wg, wb, wa := want.At(x, y).RGBA()
			if [4]uint32{gr, gg, gb, ga} != [4]uint32{wr, wg, wb, wa} {
				t.Fatalf("native PNG colors differ at %d,%d", x, y)
			}
		}
	}
}

func assertVipsDecode(t *testing.T, data []byte) {
	t.Helper()
	img, _, decoder, err := DecodeWithBackend(data, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if decoder != NativeDecoder || img.Bounds().Empty() {
		t.Fatalf("native decoder was not used: %s", decoder)
	}
	if _, err = Decode(data, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatal("native decoder bypassed pixel limit")
	}
}

func TestVipsWebPDecode(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../imagefilter/testdata/sample.webp")
	if err != nil {
		t.Fatal(err)
	}
	assertVipsDecode(t, data)
	want, err := decodePixels(data, formatWebP, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	got, _, _, err := DecodeWithBackend(data, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	assertPNGColors(t, got, want)
}
