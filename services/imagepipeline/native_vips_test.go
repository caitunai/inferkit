//go:build vips && cgo

package imagepipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/caitun/inferkit/xutil"
)

const testPixelMax = 255

func fixtureRoot(t *testing.T, directory string) *os.Root {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return root
}

func fixtureBytes(t *testing.T, root *os.Root, name string) []byte {
	t.Helper()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	return fixtureBytes(t, fixtureRoot(t, "../imagefilter/testdata"), name)
}

func TestNativeFormatsAndLimits(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"png", "jpeg", "webp", "avif"} {
		t.Run(format, func(t *testing.T) { t.Parallel(); assertNativeFormat(t, format) })
	}
}

func assertNativeFormat(t *testing.T, format string) {
	t.Helper()
	data := fixture(t, "sample."+format)
	pixels, err := Normalize(t.Context(), data, 48*32, 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	assertNativeDiagnostics(t, pixels.Diagnostics)
	if pixels.Header.Format != format || pixels.Header.Width != 48 || pixels.Header.Height != 32 || len(pixels.Data) != 320*320*3 {
		t.Fatalf("invalid metadata: %+v", pixels.Header)
	}
	if _, err = Normalize(t.Context(), data, 1, 320); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("pixel bound: %v", err)
	}
	pixels.Close()
	if pixels.Data != nil {
		t.Fatal("close retained borrowed native memory")
	}
}

func assertNativeDiagnostics(t *testing.T, stats Diagnostics) {
	t.Helper()
	if stats.Header < 0 || stats.Prepare < 0 || stats.Eval <= 0 || stats.Cleanup < 0 {
		t.Fatalf("invalid native stage durations: %+v", stats)
	}
}

func TestJPEGObservedShrink(t *testing.T) {
	t.Parallel()
	for _, factor := range []int{1, 2, 4, 8} {
		width := 320
		if factor > 1 {
			width *= factor * 2
		}
		t.Run(strconv.Itoa(factor), func(t *testing.T) {
			t.Parallel()
			assertJPEGShrink(t, width, factor)
		})
	}
}

func assertJPEGShrink(t *testing.T, width, factor int) {
	t.Helper()
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, 64)), nil); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	pixels, err := Normalize(t.Context(), encoded.Bytes(), int64(width*64), 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	stats := pixels.Diagnostics
	assertNativeDiagnostics(t, stats)
	if stats.JPEGShrink != factor || stats.JPEGWidth != width/factor || stats.JPEGHeight != 64/factor {
		t.Fatalf("loader observation does not match its decoded dimensions: %+v", stats)
	}
	if stats.JPEGLoader != "VipsForeignLoadJpegBuffer" || stats.JPEGLoadBuilds < 2 || stats.JPEGMultiscan {
		t.Fatalf("missing or incorrect actual JPEG loader properties: %+v", stats)
	}
	if sum := stats.Header + stats.Prepare + stats.Eval + stats.Cleanup; sum > time.Since(started) {
		t.Fatalf("native stage durations exceed the enclosing operation: %v", sum)
	}
	t.Logf("shrink=%d decoded=%dx%d builds=%d header=%v prepare=%v eval=%v cleanup=%v", stats.JPEGShrink, stats.JPEGWidth, stats.JPEGHeight, stats.JPEGLoadBuilds, stats.Header, stats.Prepare, stats.Eval, stats.Cleanup)
}

func TestAVIFCodedDimensionsCannotBypassLimit(t *testing.T) {
	t.Parallel()
	data := fixture(t, "sample.avif")
	position := bytes.Index(data, []byte("ispe"))
	if position < 0 {
		t.Fatal("missing ispe")
	}
	binary.BigEndian.PutUint32(data[position+8:], 1)
	binary.BigEndian.PutUint32(data[position+12:], 1)
	if _, err := Normalize(t.Context(), data, 1, 320); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("coded-frame size escaped native limit: %v", err)
	}
}

func TestRejectAPNGBeforePixelDecode(t *testing.T) {
	t.Parallel()
	data := fixture(t, "sample.png")
	chunk := make([]byte, 20)
	binary.BigEndian.PutUint32(chunk, 8)
	copy(chunk[4:], "acTL")
	binary.BigEndian.PutUint32(chunk[8:], 2)
	animated := append(bytes.Clone(data[:33]), chunk...)
	animated = append(animated, data[33:]...)
	if _, err := Normalize(t.Context(), animated, 48*32, 320); !errors.Is(err, ErrMultiple) {
		t.Fatalf("animation must fail in header preflight: %v", err)
	}
}

func TestRejectMultipictureJPEGBeforePixelDecode(t *testing.T) {
	t.Parallel()
	data := fixture(t, "sample.jpeg")
	marker := []byte{255, 226, 0, 6, 'M', 'P', 'F', 0}
	data = append(append(bytes.Clone(data[:2]), marker...), data[2:]...)
	if _, err := Normalize(t.Context(), data, 48*32, 320); !errors.Is(err, ErrMultiple) {
		t.Fatalf("MPO metadata must be rejected before pixel decode: %v", err)
	}
}

func TestNativeParallelCancellationAndOwnership(t *testing.T) {
	t.Parallel()
	data := fixture(t, "sample.png")
	group := xutil.ErrGroup(t.Context(), "native_pipeline_test").Limit(8)
	for range 8 {
		group.Go(func(ctx context.Context) error {
			pixels, err := Normalize(ctx, data, 48*32, 320)
			if err != nil {
				return err
			}
			defer pixels.Close()
			if len(pixels.Data) != 320*320*3 {
				return ErrInput
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Normalize(ctx, data, 48*32, 320); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestSmallAlphaAndCentering(t *testing.T) {
	t.Parallel()
	img := image.NewNRGBA(image.Rect(0, 0, 320, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: testPixelMax, A: 128})
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, img); err != nil {
		t.Fatal(err)
	}
	pixels, err := Normalize(t.Context(), encoded.Bytes(), 320, 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	position := 159 * 320 * 3
	if pixels.Data[0] != testPixelMax || pixels.Data[position] != testPixelMax || pixels.Data[position+1] != 127 || pixels.Data[position+2] != 127 {
		t.Fatal("alpha must be flattened after thumbnailing; odd padding must use floor centering")
	}
}

func TestExportedNormalizedPixels(t *testing.T) {
	directory := os.Getenv("IMAGE_FILTER_TEST_MODEL")
	if directory == "" {
		t.Skip("set IMAGE_FILTER_TEST_MODEL for exported libvips pixel fixtures")
	}
	root := fixtureRoot(t, directory)
	data := fixtureBytes(t, root, "validation.json")
	var report struct {
		Samples []struct {
			Source     string `json:"source_file"`
			Normalized string `json:"normalized_file"`
		} `json:"samples"`
	}
	if decodeErr := json.Unmarshal(data, &report); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	for _, sample := range report.Samples {
		t.Run(sample.Source, func(t *testing.T) { compareExportedPixels(t, root, sample.Source, sample.Normalized) })
	}
}

func compareExportedPixels(t *testing.T, root *os.Root, source, normalized string) {
	t.Helper()
	pixels, err := Normalize(t.Context(), fixtureBytes(t, root, source), 20_000_000, 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	img, err := png.Decode(bytes.NewReader(fixtureBytes(t, root, normalized)))
	if err != nil {
		t.Fatal(err)
	}
	count, maxError := compareRGB(t, pixels.Data, img)
	if maxError > MaxPixelError {
		t.Fatalf("%d RGB bytes differ; maximum %d exceeds native tolerance", count, maxError)
	}
	t.Logf("changed RGB bytes=%d maximum error=%d", count, maxError)
}

func compareRGB(t *testing.T, pixels []byte, reference image.Image) (int, int) {
	t.Helper()
	count, maximum := 0, 0
	for i, value := range pixels {
		offset := i / 3
		y, x := offset/320, offset%320
		r, g, b, _ := reference.At(x, y).RGBA()
		expected := [3]uint32{r, g, b}[i%3] >> 8
		delta := int(value) - int(expected)
		if delta != 0 {
			if count < 8 {
				t.Logf("x=%d y=%d channel=%d got=%d want=%d", x, y, i%3, value, expected)
			}
			count++
			maximum = max(maximum, delta, -delta)
		}
	}
	return count, maximum
}

func TestNativeDeadlineCleanupDoesNotPoisonNextCall(t *testing.T) {
	t.Parallel()
	data := fixture(t, "sample.avif")
	for range 16 {
		ctx, cancel := context.WithTimeout(t.Context(), time.Microsecond)
		pixels, err := Normalize(ctx, data, 48*32, 320)
		cancel()
		pixels.Close()
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("deadline identity lost: %v", err)
		}
	}
	pixels, err := Normalize(t.Context(), data, 48*32, 320)
	if err != nil {
		t.Fatalf("cancelled native call poisoned the next request: %v", err)
	}
	pixels.Close()
}

func TestHighBitDepthAVIFMatchesTrainingPipeline(t *testing.T) {
	t.Parallel()
	pixels, err := Normalize(t.Context(), fixture(t, "sample10.avif"), 48*32, 320)
	if err != nil {
		t.Fatal(err)
	}
	defer pixels.Close()
	expected := fixtureBytes(t, fixtureRoot(t, "testdata"), "sample10-normalized.png")
	img, err := png.Decode(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	count, maximum := compareRGB(t, pixels.Data, img)
	if maximum > MaxPixelError {
		t.Fatalf("10-bit AVIF differs from pyvips reference: changed=%d maximum=%d", count, maximum)
	}
}
