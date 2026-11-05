//go:build vips && cgo

package vips

/*
#include <vips/vips.h>
#include <stdlib.h>

static int input_vips_init(void) {
    if (vips_init("inferkit") != 0) return -1;
    // Request slots own concurrency and memory. Do not keep image operations
    // alive in the process-wide VIPS cache or start a CPU pool per request.
    vips_cache_set_max(0);
    vips_cache_set_max_mem(0);
    vips_concurrency_set(1);
    return 0;
}

static void* input_vips_decode(void *data, size_t length, int codec,
                              int width, int height, size_t *length_out) {
    VipsImage *source = NULL, *rgb = NULL, *rgba = NULL;
    void *pixels = NULL;
    int status;
    switch (codec) {
    case 1: status = vips_jpegload_buffer(data, length, &source,
                 "access", VIPS_ACCESS_SEQUENTIAL, "fail_on", VIPS_FAIL_ON_ERROR, NULL); break;
    default: status = vips_webpload_buffer(data, length, &source,
                 "access", VIPS_ACCESS_SEQUENTIAL, "fail_on", VIPS_FAIL_ON_ERROR, NULL); break;

    }
    if (status != 0) goto done;
    // Do not apply ICC transforms, autorotation, shrunk decoding or 16-bit
    // truncation. Unhandled layouts go back through the existing decoder.
    if (source->Xsize != width || source->Ysize != height ||
        source->BandFmt != VIPS_FORMAT_UCHAR ||
        (source->Type != VIPS_INTERPRETATION_sRGB && source->Type != VIPS_INTERPRETATION_B_W)) goto done;
    if (vips_colourspace(source, &rgb, VIPS_INTERPRETATION_sRGB, NULL) != 0) goto done;
    if (rgb->Bands == 3) {
        if (vips_addalpha(rgb, &rgba, NULL) != 0) goto done;
    } else if (rgb->Bands == 4) {
        rgba = g_object_ref(rgb);
    } else goto done;
    pixels = vips_image_write_to_memory(rgba, length_out);
done:
    VIPS_UNREF(rgba);
    VIPS_UNREF(rgb);
    VIPS_UNREF(source);
    vips_error_clear();
    vips_thread_shutdown();
    return pixels;
}
*/
import "C"

import (
	"image"
	"sync"
	"unsafe"
)

var initializeVips = sync.OnceValue(func() bool { return C.input_vips_init() == 0 })

// Ready serializes process-wide libvips initialization across image users.
func Ready() bool { return initializeVips() }

// Decode retains the original-size JPEG/WebP decoder used by OCR image input.
func Decode(data []byte, config image.Config, format string) (image.Image, bool) {
	codecs := map[string]int{"jpeg": 1, "webp": 2}
	codec, supported := codecs[format]
	if !supported || len(data) == 0 || !initializeVips() {
		return nil, false
	}

	var length C.size_t
	// The synchronous C call retains data only until all lazy pixels have been
	// materialized; no Go pointer survives the call or is stored in a cache.
	pixels := C.input_vips_decode(unsafe.Pointer(unsafe.SliceData(data)), C.size_t(len(data)), C.int(codec), C.int(config.Width), C.int(config.Height), &length)
	if pixels == nil {
		return nil, false
	}
	defer C.g_free(C.gpointer(pixels))
	expected := config.Width * config.Height * 4
	if uint64(length) != uint64(expected) {
		return nil, false
	}
	img := image.NewNRGBA(image.Rect(0, 0, config.Width, config.Height))
	copy(img.Pix, unsafe.Slice((*byte)(pixels), expected))
	return img, true
}
