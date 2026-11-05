//go:build cgo && (arm64 || amd64) && !purego && (!goexperiment.simd || !arm64)

package imagefilter

/*
#include <stdint.h>
#include <string.h>
enum { RESAMPLE_COEFFICIENT_BITS = 22 };
#if defined(__aarch64__)
#include <arm_neon.h>
static uint32_t filter_vector(const uint8_t *src, int step, const int32_t *coefs, int count) {
    int32x4_t sum = vdupq_n_s32(1 << (RESAMPLE_COEFFICIENT_BITS - 1));
    for (int k = 0; k < count; k++) {
        uint32_t pixel;
        memcpy(&pixel, src + k * step, 4);
        uint16x8_t wide = vmovl_u8(vcreate_u8(pixel));
        int32x4_t channels = vreinterpretq_s32_u32(vmovl_u16(vget_low_u16(wide)));
        sum = vmlaq_n_s32(sum, channels, coefs[k]);
    }
    int16x4_t value = vqmovn_s32(vshrq_n_s32(sum, RESAMPLE_COEFFICIENT_BITS));
    uint8x8_t packed = vqmovun_s16(vcombine_s16(value, vdup_n_s16(0)));
    return vget_lane_u32(vreinterpret_u32_u8(packed), 0);
}
#else
#include <emmintrin.h>
static __m128i multiply32(__m128i x, __m128i y) {
    __m128i even = _mm_mul_epu32(x, y);
    __m128i odd = _mm_mul_epu32(_mm_srli_si128(x, 4), _mm_srli_si128(y, 4));
    even = _mm_shuffle_epi32(even, _MM_SHUFFLE(2, 0, 2, 0));
    odd = _mm_shuffle_epi32(odd, _MM_SHUFFLE(2, 0, 2, 0));
    return _mm_unpacklo_epi32(even, odd);
}
static uint32_t filter_vector(const uint8_t *src, int step, const int32_t *coefs, int count) {
    __m128i sum = _mm_set1_epi32(1 << (RESAMPLE_COEFFICIENT_BITS - 1)), zero = _mm_setzero_si128();
    for (int k = 0; k < count; k++) {
        uint32_t pixel;
        memcpy(&pixel, src + k * step, 4);
        __m128i channels = _mm_cvtsi32_si128((int32_t)pixel);
        channels = _mm_unpacklo_epi16(_mm_unpacklo_epi8(channels, zero), zero);
        sum = _mm_add_epi32(sum, multiply32(channels, _mm_set1_epi32(coefs[k])));
    }
    __m128i value = _mm_packs_epi32(_mm_srai_epi32(sum, RESAMPLE_COEFFICIENT_BITS), zero);
    return (uint32_t)_mm_cvtsi128_si32(_mm_packus_epi16(value, zero));
}
#endif

static void resample_row(const uint8_t *src, uint8_t *dst, int src_stride,
                         int dst_stride, int width, int row, int vertical,
                         const int32_t *coefs, const int32_t *offsets,
                         const int32_t *starts) {
    for (int x = 0; x < width; x++) {
        int filter = vertical ? row : x;
        int first = offsets[filter], count = offsets[filter + 1] - first;
        int source = vertical ? starts[filter] * src_stride + x * 4 : row * src_stride + starts[filter] * 4;
        uint32_t value = filter_vector(src + source, vertical ? src_stride : 4, coefs + first, count);
        memcpy(dst + row * dst_stride + x * 4, &value, 4);
    }
}
*/
import "C"

import "unsafe"

func resizeBackendName() string { return backendSIMD }

// One synchronous call per output row, never per tap or pixel. All slice-backed
// pointers remain owned by the plan and are not retained after the call.
func (p *resamplePlan) renderSIMDRow(row int) {
	vertical := C.int(0)
	if p.vertical {
		vertical = 1
	}
	k := p.kernel
	C.resample_row((*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(p.source.Pix))), (*C.uint8_t)(unsafe.Pointer(unsafe.SliceData(p.target.Pix))),
		C.int(p.source.Stride), C.int(p.target.Stride), C.int(p.target.Bounds().Dx()), C.int(row), vertical,
		(*C.int32_t)(unsafe.Pointer(unsafe.SliceData(k.coefficients))), (*C.int32_t)(unsafe.Pointer(unsafe.SliceData(k.offsets))), (*C.int32_t)(unsafe.Pointer(unsafe.SliceData(k.starts))))
}
