package imageinput

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	fastpng "github.com/woozymasta/png"
)

func TestPNGDecoderStandardCorpusParity(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("Go toolchain unavailable")
	}
	output, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Join(strings.TrimSpace(string(output)), "src/image/png/testdata"))
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("Go distribution omits PNG corpus")
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	files, err := fs.Glob(root.FS(), "*.png")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		data, readErr := root.ReadFile(file)
		if readErr != nil {
			t.Fatal(readErr)
		}
		t.Run(file, func(t *testing.T) { t.Parallel(); assertPNGParity(t, data) })
	}
}

func assertPNGParity(t *testing.T, data []byte) {
	t.Helper()
	want, stdErr := png.Decode(bytes.NewReader(data))
	got, fastErr := fastpng.Decode(bytes.NewReader(data))
	if (stdErr == nil) != (fastErr == nil) {
		t.Fatalf("decoder acceptance differs: standard=%v fast=%v", stdErr, fastErr)
	}
	if stdErr != nil {
		return
	}
	if got.Bounds() != want.Bounds() || !bytes.Equal(pngPixels(t, got), pngPixels(t, want)) {
		t.Fatal("PNG pixel bytes differ")
	}

	if palette, ok := want.(*image.Paletted); ok {
		assertPalettePixels(t, got, palette)
	}
}

func assertPalettePixels(t *testing.T, got image.Image, want *image.Paletted) {
	t.Helper()
	for y := range want.Bounds().Dy() {
		for x := range want.Bounds().Dx() {
			gr, gg, gb, ga := got.At(x, y).RGBA()
			wr, wg, wb, wa := want.At(x, y).RGBA()
			if gr != wr || gg != wg || gb != wb || ga != wa {
				t.Fatal("PNG palette differs")
			}
		}
	}
}

func pngPixels(t *testing.T, img image.Image) []byte {
	t.Helper()
	switch img := img.(type) {
	case *image.RGBA:
		return img.Pix
	case *image.NRGBA:
		return img.Pix
	case *image.RGBA64:
		return img.Pix
	case *image.NRGBA64:
		return img.Pix
	case *image.Gray:
		return img.Pix
	case *image.Gray16:
		return img.Pix
	case *image.Paletted:
		return img.Pix
	default:
		t.Fatal("unexpected PNG pixel storage")
		return nil
	}
}

func pngBenchmarkData(tb testing.TB) []byte {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1070, 680))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte((i/4)%256), byte((i/31)%256), byte((i/17)%256), 255
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		tb.Fatal(err)
	}
	return out.Bytes()
}

func TestPNGDecoderLimitsAndChecksum(t *testing.T) {
	t.Parallel()
	data := pngBenchmarkData(t)
	if _, err := Decode(data, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("PNG pixel limit bypassed: %v", err)
	}
	oversized := bytes.Clone(data)
	binary.BigEndian.PutUint32(oversized[16:20], MaxDimension+1)
	binary.BigEndian.PutUint32(oversized[29:33], crc32.ChecksumIEEE(oversized[12:29]))
	if _, err := Decode(oversized, 100_000_000); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("PNG side limit bypassed: %v", err)
	}
	position := bytes.Index(data, []byte("IDAT"))
	if position < 0 {
		t.Fatal("PNG missing IDAT")
	}
	data[position+4] ^= 1
	if _, err := Decode(data, 1_000_000); !errors.Is(err, ErrInput) {
		t.Fatalf("corrupt PNG accepted: %v", err)
	}
}

func FuzzPNGDecoderParity(f *testing.F) {
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 5, 7))); err != nil {
		f.Fatal(err)
	}
	f.Add(out.Bytes())
	f.Fuzz(func(t *testing.T, data []byte) {
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil || config.Width <= 0 || config.Height <= 0 || config.Width > MaxDimension || config.Height > MaxDimension || int64(config.Width)*int64(config.Height) > 1_000_000 {
			return
		}
		assertPNGParity(t, data)
	})
}

func BenchmarkPNGDecode(b *testing.B) {
	data := pngBenchmarkData(b)
	for _, decoder := range []struct {
		decode func(io.Reader) (image.Image, error)
		name   string
	}{{png.Decode, "standard"}, {fastpng.Decode, "fast"}} {
		b.Run(decoder.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := decoder.decode(bytes.NewReader(data)); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
