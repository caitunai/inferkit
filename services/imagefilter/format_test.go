package imagefilter

import (
	"bytes"
	"embed"
	"encoding/binary"
	"errors"
	"math"
	"slices"
	"testing"
)

//go:embed testdata/*
var imageFormatFixtures embed.FS

const fixturePNG = "sample.png"

var fixtureFormats = [...]string{"png", "jpeg", "webp", "avif"}

func formatFixture(tb testing.TB, name string) []byte {
	tb.Helper()
	data, err := imageFormatFixtures.ReadFile("testdata/" + name)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

func TestSupportedImageFormats(t *testing.T) {
	requireNativePipeline(t)
	t.Parallel()
	for _, name := range []string{"sample.jpeg", fixturePNG, "sample.webp", "sample.avif", "sample10.avif"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assertFormatPreprocessing(t, name)
		})
	}
}

func assertFormatPreprocessing(t *testing.T, name string) {
	t.Helper()
	data := formatFixture(t, name)
	img, err := decodeImage(t.Context(), data, 48*32)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 48 || img.Bounds().Dy() != 32 {
		t.Fatalf("wrong bounds: %v", img.Bounds())
	}
	tensor := make([]float32, inputLength)
	if err = preprocess(t.Context(), data, 48*32, tensor); err != nil {
		t.Fatal(err)
	}
	for _, value := range tensor {
		if math.IsNaN(float64(value)) || value < 0 || value > 1 {
			t.Fatalf("invalid tensor value: %v", value)
		}
	}
}

func TestLosslessFormatsAndAVIFRotation(t *testing.T) {
	requireNativePipeline(t)
	t.Parallel()
	cases := []struct{ source, reference string }{
		{"sample.webp", fixturePNG},
		{"sample.avif", fixturePNG},
		{"rotated.avif", "rotated.png"},
	}
	for _, test := range cases {
		got, want := make([]float32, inputLength), make([]float32, inputLength)
		if err := preprocess(t.Context(), formatFixture(t, test.source), 48*32, got); err != nil {
			t.Fatal(err)
		}
		if err := preprocess(t.Context(), formatFixture(t, test.reference), 48*32, want); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s tensor differs from %s", test.source, test.reference)
		}
	}
}

func TestAVIFLimitsMalformedAndGenericBrand(t *testing.T) {
	t.Parallel()
	data := formatFixture(t, "sample.avif")
	if _, err := decodeImage(t.Context(), data, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pixel limit: %v", err)
	}
	if _, err := decodeImage(t.Context(), data[:32], 48*32); !errors.Is(err, ErrInput) {
		t.Fatalf("truncated AVIF: %v", err)
	}
	// MIAF's generic major brand still carries the avif compatible brand.
	generic := slices.Clone(data)
	copy(generic[8:12], "mif1")
	if _, err := decodeImage(t.Context(), generic, 48*32); err != nil {
		t.Fatalf("generic major brand: %v", err)
	}
	// A small ispe header must not bypass the coded AV1 frame allocation bound.
	deceptive := slices.Clone(data)
	position := bytes.Index(deceptive, []byte("ispe"))
	if position < 0 {
		t.Fatal("fixture is missing ispe")
	}
	binary.BigEndian.PutUint32(deceptive[position+8:], 1)
	binary.BigEndian.PutUint32(deceptive[position+12:], 1)
	if _, err := decodeImage(t.Context(), deceptive, 1); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("coded frame allocation limit: %v", err)
	}
}
