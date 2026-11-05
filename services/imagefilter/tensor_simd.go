//go:build cgo && (arm64 || amd64) && !purego

package imagefilter

/*
#include <stdint.h>
#if defined(__aarch64__)
#include <arm_neon.h>
static void tensor_channel(uint16x8_t color, uint16x8_t alpha, float *out) {
    uint16x8_t sum = vmlaq_u16(vdupq_n_u16(127), color, alpha);
    sum = vmlaq_n_u16(sum, vsubq_u16(vdupq_n_u16(255), alpha), 255);
    // Exact floor(sum / 255) for 0 <= sum <= 65152, without integer division.
    sum = vshrq_n_u16(vaddq_u16(vaddq_u16(sum, vdupq_n_u16(1)), vshrq_n_u16(sum, 8)), 8);
    vst1q_f32(out, vdivq_f32(vcvtq_f32_u32(vmovl_u16(vget_low_u16(sum))), vdupq_n_f32(255)));
    vst1q_f32(out + 4, vdivq_f32(vcvtq_f32_u32(vmovl_u16(vget_high_u16(sum))), vdupq_n_f32(255)));
}
static int tensor_vector(const uint8_t *src, float *r, float *g, float *b, int width) {
    int x = 0;
    for (; x + 8 <= width; x += 8) {
        uint8x8x4_t pixel = vld4_u8(src + x * 4);
        uint16x8_t alpha = vmovl_u8(pixel.val[3]);
        tensor_channel(vmovl_u8(pixel.val[0]), alpha, r + x);
        tensor_channel(vmovl_u8(pixel.val[1]), alpha, g + x);
        tensor_channel(vmovl_u8(pixel.val[2]), alpha, b + x);
    }
    return x;
}
#else
#include <emmintrin.h>
static void tensor_channel(__m128i color, __m128i alpha, float *out) {
    __m128i sum = _mm_add_epi32(_mm_mullo_epi16(color, alpha), _mm_set1_epi32(127));
    sum = _mm_add_epi32(sum, _mm_mullo_epi16(_mm_sub_epi32(_mm_set1_epi32(255), alpha), _mm_set1_epi32(255)));
    sum = _mm_srli_epi32(_mm_add_epi32(_mm_add_epi32(sum, _mm_set1_epi32(1)), _mm_srli_epi32(sum, 8)), 8);
    _mm_storeu_ps(out, _mm_div_ps(_mm_cvtepi32_ps(sum), _mm_set1_ps(255)));
}
static int tensor_vector(const uint8_t *src, float *r, float *g, float *b, int width) {
    int x = 0;
    for (; x + 4 <= width; x += 4) {
        __m128i pixel = _mm_loadu_si128((const __m128i*)(src + x * 4));
        __m128i mask = _mm_set1_epi32(255), alpha = _mm_srli_epi32(pixel, 24);
        tensor_channel(_mm_and_si128(pixel, mask), alpha, r + x);
        tensor_channel(_mm_and_si128(_mm_srli_epi32(pixel, 8), mask), alpha, g + x);
        tensor_channel(_mm_and_si128(_mm_srli_epi32(pixel, 16), mask), alpha, b + x);
    }
    return x;
}
#endif
static void tensor_write(const uint8_t *src, int stride, int width, int height,
                         float *dst, int size) {
    int plane = size * size;
    for (int i = 0; i < 3 * plane; i++) dst[i] = 1;
    int ox = (size - width) / 2, oy = (size - height) / 2;
    for (int y = 0; y < height; y++) {
        const uint8_t *row = src + y * stride;
        float *r = dst + (y + oy) * size + ox;
        float *g = r + plane, *b = g + plane;
        int x = tensor_vector(row, r, g, b, width);
        for (; x < width; x++) {
            int a = row[x * 4 + 3];
            r[x] = (float)((row[x * 4] * a + 255 * (255 - a) + 127) / 255) / 255;
            g[x] = (float)((row[x * 4 + 1] * a + 255 * (255 - a) + 127) / 255) / 255;
            b[x] = (float)((row[x * 4 + 2] * a + 255 * (255 - a) + 127) / 255) / 255;
        }
    }
}
*/
import "C"

import (
	"image"
	"unsafe"
)

// This call borrows Go buffers synchronously. The C function retains no pointers,
// allocates no memory and uses only baseline NEON / SSE2 instructions.
func tensorSIMD(img *image.NRGBA, dst []float32) bool {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w < 1 || h < 1 || w > imageSize || h > imageSize || len(dst) != inputLength || len(img.Pix) < (h-1)*img.Stride+w*4 {
		return false
	}
	C.tensor_write((*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(img.Pix))), C.int(img.Stride), C.int(w), C.int(h), (*C.float)(unsafe.Pointer(unsafe.SliceData(dst))), imageSize)
	return true
}

func tensorBackendName() string { return backendSIMD }
