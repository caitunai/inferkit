//go:build linux || darwin

package imagefilter

import (
	"image"
	"math"
	"os"
	"slices"
	"strconv"
	"testing"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/resource"
)

func TestLegacyUserImagesPreserveScalarTensor(t *testing.T) {
	for format, data := range workloadImages(t) {
		t.Run(format, func(t *testing.T) {
			want := userScalarTensor(t, data)
			got := make([]float32, inputLength)
			if err := preprocessWithBudget(t.Context(), data, 20_000_000, got, nil); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(want, got) {
				t.Fatal("optimized processing changed the original image tensor")
			}
		})
	}
}

func userScalarTensor(t *testing.T, data []byte) []float32 {
	t.Helper()
	img, format, decoder, err := imageinput.DecodeWithBackend(data, 20_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if opaque, ok := img.(interface{ Opaque() bool }); ok {
		t.Logf("format=%s decoder=%s opaque=%v", format, decoder, opaque.Opaque())
	}
	b := img.Bounds()
	scale := min(float64(imageSize)/float64(b.Dx()), float64(imageSize)/float64(b.Dy()))
	w, h := max(1, int(math.RoundToEven(float64(b.Dx())*scale))), max(1, int(math.RoundToEven(float64(b.Dy())*scale)))
	rgba := toRGBA(img)
	if b.Dx() != w || b.Dy() != h {
		premultiply(rgba)
		rgba = scalarAxisReference(t, rgba, w, false)
		rgba = scalarAxisReference(t, rgba, h, true)
		unpremultiplyPixels(rgba.Pix)
	}
	tensor := make([]float32, inputLength)
	writeTensorScalar(rgba, tensor)
	return tensor
}

func scalarAxisReference(t *testing.T, src *image.NRGBA, size int, vertical bool) *image.NRGBA {
	t.Helper()
	w, h, input := size, src.Bounds().Dy(), src.Bounds().Dx()
	if vertical {
		w, h, input = src.Bounds().Dx(), size, src.Bounds().Dy()
	}
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	plan := resamplePlan{source: src, target: dst, filter: weights(input, size), vertical: vertical}
	if err := forRows(t.Context(), h, 1, plan.renderScalarRow); err != nil {
		t.Fatal(err)
	}
	return dst
}

func workloadImages(tb testing.TB) map[string][]byte {
	tb.Helper()
	directory := os.Getenv("IMAGE_FILTER_TEST_IMAGES")
	if directory == "" {
		tb.Skip("set IMAGE_FILTER_TEST_IMAGES to a directory containing test.{png,jpeg,webp,avif}")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			tb.Error(closeErr)
		}
	})
	images := make(map[string][]byte)
	for _, format := range fixtureFormats {
		data, readErr := root.ReadFile("test." + format)
		if readErr != nil {
			tb.Fatal(readErr)
		}
		images[format] = data
	}
	return images
}

func BenchmarkUserPipeline(b *testing.B) {
	c := nativeConfig(b)
	c.BypassCache = true
	c.Provider = os.Getenv("IMAGE_FILTER_TEST_PROVIDER")
	images := workloadImages(b)
	for _, format := range fixtureFormats {
		for _, callers := range []int{1, 8} {
			b.Run(format+"/"+strconv.Itoa(callers), func(b *testing.B) {
				s := nativeServiceWithPlan(b, c, automaticPlan(c, resource.Detect()))
				benchmarkFilterImages(b, s, [][]byte{images[format]}, callers)
			})
		}
	}
}

func BenchmarkUserDecode(b *testing.B) {
	images := workloadImages(b)
	for _, format := range fixtureFormats {
		b.Run(format, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := imageinput.Decode(images[format], 20_000_000); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
