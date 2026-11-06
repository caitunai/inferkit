//go:build vips && cgo

#if defined(__linux__)
#define _GNU_SOURCE
#endif
#include "native_vips.h"
#include <vips/vips.h>
#include <libheif/heif.h>
#include <string.h>
#include <dlfcn.h>

#define PIPELINE_MAX_DIMENSION 32768

/* The hook observes public loader properties without changing its options.
 * Only this synchronous C invocation's thread can access the stack record;
 * other requests and libvips workers have independent or empty TLS. */
static GPrivate current_diagnostics = G_PRIVATE_INIT(NULL);
static gboolean observe_jpeg(GSignalInvocationHint *hint, guint count,
                             const GValue *values, gpointer unused) {
    (void)hint; (void)unused;
    PipelineDiagnostics *diagnostics = g_private_get(&current_diagnostics);
    if (!diagnostics || count < 1) return TRUE;
    GObject *object = g_value_get_object(&values[0]);
    if (!object || !g_str_has_prefix(G_OBJECT_TYPE_NAME(object), "VipsForeignLoadJpeg")) return TRUE;
    VipsImage *out = NULL;
    g_object_get(object, "shrink", &diagnostics->jpeg_shrink, "out", &out, NULL);
    diagnostics->jpeg_loads++;
    g_strlcpy(diagnostics->jpeg_loader, G_OBJECT_TYPE_NAME(object), sizeof diagnostics->jpeg_loader);
    if (out) {
        diagnostics->jpeg_width = vips_image_get_width(out);
        diagnostics->jpeg_height = vips_image_get_height(out);
        g_object_unref(out);
    }
    return TRUE;
}

static void finish_stage(PipelineDiagnostics *diagnostics, int stage, gint64 *started) {
    gint64 now = g_get_monotonic_time();
    diagnostics->elapsed_us[stage] += now - *started;
    *started = now;
}

typedef struct {
    struct heif_context *context;
    struct heif_image_handle *handle;
    struct heif_image *image;
} AvifSource;

struct PipelineControl { gint cancelled; };
PipelineControl *pipeline_control_new(void) { return g_new0(PipelineControl, 1); }
void pipeline_cancel(PipelineControl *control) { g_atomic_int_set(&control->cancelled, 1); }
static int cancelled(void *data) { return g_atomic_int_get(&((PipelineControl *)data)->cancelled); }
static void progress(VipsImage *image, VipsProgress *unused, void *data) {
    (void)unused;
    if (cancelled(data)) vips_image_set_kill(image, TRUE);
}

const char *pipeline_jpeg_library(void) {
    Dl_info library, jpeg;
    if (!dladdr((void *)vips_init, &library)) return "unknown";
    void *handle = dlopen(library.dli_fname, RTLD_LAZY | RTLD_LOCAL);
    if (!handle) return "unknown";
    void *symbol = dlsym(handle, "jpeg_CreateDecompress");
    const char *name = symbol && dladdr(symbol, &jpeg) ? jpeg.dli_fname : "unknown";
    dlclose(handle);
    return name;
}

static uint32_t read_be32(const unsigned char *p) {
    return ((uint32_t)p[0] << 24) | ((uint32_t)p[1] << 16) | ((uint32_t)p[2] << 8) | p[3];
}

static int bounded(int width, int height, uint64_t limit) {
    return width > 0 && height > 0 && width <= PIPELINE_MAX_DIMENSION &&
        height <= PIPELINE_MAX_DIMENSION && (uint64_t)width <= limit / (uint64_t)height;
}

/* Restrict buffer auto-detection before invoking libvips. In particular, SVG,
 * PDF, HEIC and arbitrary ImageMagick loaders are not public API inputs. */
static const char *input_format(const unsigned char *p, size_t n) {
    if (n >= 8 && !memcmp(p, "\x89PNG\r\n\x1a\n", 8)) return "png";
    if (n >= 3 && p[0] == 255 && p[1] == 216 && p[2] == 255) return "jpeg";
    if (n >= 12 && !memcmp(p, "RIFF", 4) && !memcmp(p + 8, "WEBP", 4)) return "webp";
    if (n >= 16 && !memcmp(p + 4, "ftyp", 4)) {
        uint32_t length = read_be32(p);
        if (length < 16 || length > n) return NULL;
        if (!memcmp(p + 8, "avif", 4) || !memcmp(p + 8, "avis", 4)) return "avif";
        for (size_t i = 16; i + 4 <= length; i += 4)
            if (!memcmp(p + i, "avif", 4) || !memcmp(p + i, "avis", 4)) return "avif";
    }
    return NULL;
}

static int multipicture_jpeg(const unsigned char *p, size_t n) {
    size_t offset = 2;
    while (offset + 2 <= n && p[offset] == 255) {
        while (offset < n && p[offset] == 255) offset++;
        if (offset == n) return 0;
        unsigned int marker = p[offset++];
        if (marker == 0xDA || marker == 0xD9) return 0;
        if (marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8)) continue;
        if (offset + 2 > n) return 0;
        size_t size = ((size_t)p[offset] << 8) | p[offset + 1];
        if (size < 2 || size > n - offset) return 0;
        if (marker == 0xE2 && size >= 6 && !memcmp(p + offset + 2, "MPF\0", 4)) return 1;
        offset += size;
    }
    return 0;
}

static int animated(const unsigned char *p, size_t n, const char *format) {
    if (!strcmp(format, "jpeg")) return multipicture_jpeg(p, n);
    if (!strcmp(format, "png")) {
        for (size_t i = 8; i + 12 <= n;) {
            uint32_t size = read_be32(p + i);
            if (size > n - i - 12) return 0; /* The real loader reports corruption. */
            if (!memcmp(p + i + 4, "acTL", 4)) return 1;
            i += (size_t)size + 12;
        }
    }
    if (!strcmp(format, "webp") && n >= 21 && !memcmp(p + 12, "VP8X", 4))
        return (p[20] & 2) != 0;
    if (!strcmp(format, "avif")) {
        uint32_t size = read_be32(p);
        if (!memcmp(p + 8, "avis", 4)) return 1;
        for (size_t i = 16; i + 4 <= size; i += 4)
            if (!memcmp(p + i, "avis", 4)) return 1;
    }
    return 0;
}

int pipeline_init(void) {
    /* Ready owns process-wide libvips initialization and configuration. */
    if (vips_version(0) != 8 || vips_version(1) < 18) return -1;
#if LIBHEIF_HAVE_VERSION(1, 23, 0)
    if (heif_get_version_number() < 0x01170000) return -1;
    const struct heif_decoder_descriptor *descriptors[16];
    int count = heif_get_decoder_descriptors(heif_compression_AV1, descriptors, 16);
    for (int i = 0; i < count; i++) {
        const char *id = heif_decoder_descriptor_get_id_name(descriptors[i]);
        if (id && !strcmp(id, "dav1d")) {
            gpointer object_class = g_type_class_ref(VIPS_TYPE_OBJECT);
            guint signal = g_signal_lookup("postbuild", VIPS_TYPE_OBJECT);
            g_type_class_unref(object_class);
            return signal && g_signal_add_emission_hook(signal, 0, observe_jpeg, NULL, NULL) ? 0 : -1;
        }
    }
#endif
    return -1;
}

const char *pipeline_version(void) { return vips_version_string(); }
void pipeline_free(void *pixels) { g_free(pixels); }

static void avif_close(AvifSource *source) {
    if (source->image) heif_image_release(source->image);
    if (source->handle) heif_image_handle_release(source->handle);
    if (source->context) heif_context_free(source->context);
}

/* Native AVIF adapter: stock heifload cannot receive per-request security
 * limits or a decoder ID. Decode exactly once with bounded dav1d, then hand the
 * borrowed native plane to libvips. No decoded image passes through Go. */
static VipsImage *avif_source(const void *data, size_t length, uint64_t limit,
                             VipsImage *header, AvifSource *owner, int *status, PipelineControl *control) {
#if LIBHEIF_HAVE_VERSION(1, 23, 0)
    owner->context = heif_context_alloc();
    if (!owner->context) return NULL;
    struct heif_security_limits *limits = heif_context_get_security_limits(owner->context);
    limits->max_image_size_pixels = limit;
    /* Small images still need fixed-size codec and colour-conversion tables. */
    limits->max_memory_block_size = limit * 16 + (64ULL << 20);
    limits->max_total_memory = limit * 32 + length * 2 + (64ULL << 20);
    limits->max_sequence_frames = 1;
    heif_context_set_max_decoding_threads(owner->context, 0);
    struct heif_error error = heif_context_read_from_memory_without_copy(owner->context, data, length, NULL);
    if (error.code != heif_error_Ok) goto failed;
    if (heif_context_get_number_of_top_level_images(owner->context) != 1) { *status = 3; return NULL; }
    error = heif_context_get_primary_image_handle(owner->context, &owner->handle);
    if (error.code != heif_error_Ok) goto failed;
    if (!bounded(heif_image_handle_get_ispe_width(owner->handle),
                 heif_image_handle_get_ispe_height(owner->handle), limit)) { *status = 2; return NULL; }
    struct heif_decoding_options *options = heif_decoding_options_alloc();
    if (!options) return NULL;
    options->decoder_id = "dav1d";
    options->strict_decoding = 1;
    options->num_codec_threads = 1;
    options->cancel_decoding = cancelled;
    options->progress_user_data = control;
    int alpha = heif_image_handle_has_alpha_channel(owner->handle);
    int depth = heif_image_handle_get_luma_bits_per_pixel(owner->handle);
    if (depth < 1 || depth > 16) { heif_decoding_options_free(options); return NULL; }
    int wide = depth > 8;
    enum heif_chroma chroma = alpha ? heif_chroma_interleaved_RGBA : heif_chroma_interleaved_RGB;
#if G_BYTE_ORDER == G_LITTLE_ENDIAN
    if (wide) chroma = alpha ? heif_chroma_interleaved_RRGGBBAA_LE : heif_chroma_interleaved_RRGGBB_LE;
#else
    if (wide) chroma = alpha ? heif_chroma_interleaved_RRGGBBAA_BE : heif_chroma_interleaved_RRGGBB_BE;
#endif
    error = heif_decode_image(owner->handle, &owner->image, heif_colorspace_RGB, chroma, options);
    heif_decoding_options_free(options);
    if (error.code != heif_error_Ok) goto failed;
    int width = heif_image_get_width(owner->image, heif_channel_interleaved);
    int height = heif_image_get_height(owner->image, heif_channel_interleaved);
    if (!bounded(width, height, limit)) { *status = 2; return NULL; }
    int stride = 0, bands = alpha ? 4 : 3, bytes = wide ? 2 : 1;
    const uint8_t *plane = heif_image_get_plane_readonly(owner->image, heif_channel_interleaved, &stride);
    if (!plane || stride < width * bands * bytes || stride % bytes) return NULL;
    VipsImage *raw = vips_image_new_from_memory(plane, (size_t)stride * height,
        stride / bytes, height, 1, wide ? VIPS_FORMAT_USHORT : VIPS_FORMAT_UCHAR);
    VipsImage *strip = NULL, *folded = NULL, *rgb = NULL;
    if (raw && !vips_extract_area(raw, &strip, 0, 0, width * bands, height, NULL) &&
        !vips_bandfold(strip, &folded, "factor", bands, NULL))
        vips_copy(folded, &rgb, "interpretation", wide ? VIPS_INTERPRETATION_RGB16 : VIPS_INTERPRETATION_sRGB, NULL);
    VIPS_UNREF(folded); VIPS_UNREF(strip); VIPS_UNREF(raw);
    /* libheif returns right-aligned 10/12-bit samples. Match heifload's
     * full-range RGB16 convention using lazy libvips operations. */
    if (rgb && wide && depth < 16) {
        VipsImage *scaled = NULL, *full_range = NULL;
        if (!vips_linear1(rgb, &scaled, (double)(1U << (16 - depth)), 0.0, NULL))
            vips_cast(scaled, &full_range, VIPS_FORMAT_USHORT, NULL);
        VIPS_UNREF(scaled); VIPS_UNREF(rgb);
        rgb = full_range;
    }
    if (rgb && vips_image_get_typeof(header, VIPS_META_ORIENTATION)) {
        int orientation = 1;
        if (!vips_image_get_int(header, VIPS_META_ORIENTATION, &orientation))
            vips_image_set_int(rgb, VIPS_META_ORIENTATION, orientation);
    }
    if (rgb && vips_image_get_typeof(header, VIPS_META_ICC_NAME)) {
        const void *profile = NULL;
        size_t profile_length = 0;
        if (!vips_image_get_blob(header, VIPS_META_ICC_NAME, &profile, &profile_length))
            vips_image_set_blob_copy(rgb, VIPS_META_ICC_NAME, profile, profile_length);
    }
    return rgb;
failed:
    vips_error("avif", "%s", error.message);
    if (error.subcode == heif_suberror_Security_limit_exceeded) *status = 2;
#else
    (void)data; (void)length; (void)limit; (void)header; (void)owner; (void)control;
    *status = 4;
#endif
    return NULL;
}

static VipsImage *thumbnail(const void *data, size_t length, uint64_t limit, int size,
                            const char *format, VipsImage *header, AvifSource *owner, int *status, PipelineControl *control) {
    VipsImage *small = NULL, *source = NULL;
    if (!strcmp(format, "avif")) {
        source = avif_source(data, length, limit, header, owner, status, control);
        if (source) vips_thumbnail_image(source, &small, size, "height", size,
            "size", VIPS_SIZE_BOTH, "crop", VIPS_INTERESTING_NONE, "no_rotate", FALSE, "linear", FALSE, NULL);
        VIPS_UNREF(source);
    } else {
        vips_thumbnail_buffer((void *)data, length, &small, size, "height", size,
            "size", VIPS_SIZE_BOTH, "crop", VIPS_INTERESTING_NONE,
            "no_rotate", FALSE, "linear", FALSE, "fail_on", VIPS_FAIL_ON_ERROR, NULL);
    }
    return small;
}

/* Preserve training order: thumbnail -> sRGB -> uchar -> flatten -> embed.
 * Return the small RGB canvas directly; tensor packing belongs to Go SIMD. */
static VipsImage *canvas_graph(VipsImage *small, int size) {
    VipsImage *rgb = NULL, *bytes = NULL, *flat = NULL, *opaque = NULL, *canvas = NULL;
    VipsArrayDouble *white = vips_array_double_newv(3, 255.0, 255.0, 255.0);
    if (vips_colourspace(small, &rgb, VIPS_INTERPRETATION_sRGB, NULL) ||
        vips_cast(rgb, &bytes, VIPS_FORMAT_UCHAR, NULL)) goto done;
    if (vips_image_hasalpha(bytes)) {
        if (vips_flatten(bytes, &flat, "background", white, "max_alpha", 255.0, NULL)) goto done;
    } else flat = g_object_ref(bytes);
    if (flat->Bands != 3 || flat->Xsize < 1 || flat->Ysize < 1 || flat->Xsize > size || flat->Ysize > size) goto done;
    if (vips_cast(flat, &opaque, VIPS_FORMAT_UCHAR, NULL)) goto done;
    if (vips_embed(opaque, &canvas, (size - flat->Xsize) / 2, (size - flat->Ysize) / 2,
        size, size, "extend", VIPS_EXTEND_BACKGROUND, "background", white, NULL)) goto done;
done:
    VIPS_UNREF(opaque); VIPS_UNREF(flat); VIPS_UNREF(bytes); VIPS_UNREF(rgb);
    vips_area_unref((VipsArea *)white);
    return canvas;
}

PipelineResult pipeline_normalize(const void *data, size_t length, uint64_t limit, int size, PipelineControl *control) {
    PipelineResult result = {0};
    PipelineResult *out = &result;
    out->status = 1;
    PipelineDiagnostics *diagnostics = &out->diagnostics;
    g_private_set(&current_diagnostics, diagnostics);
    gint64 started = g_get_monotonic_time();
    int stage = PIPELINE_HEADER;
    VipsImage *header = NULL, *small = NULL, *canvas = NULL;
    AvifSource owner = {0};
    const char *format = input_format(data, length);
    if (!format || cancelled(control)) goto done;
    g_strlcpy(out->format, format, sizeof out->format);
    if (animated(data, length, format)) { out->status = 3; goto done; }
    header = vips_image_new_from_buffer(data, length, "", "access", VIPS_ACCESS_SEQUENTIAL,
        "fail_on", VIPS_FAIL_ON_ERROR, NULL);
    if (!header) goto done;
    if (vips_image_get_n_pages(header) != 1) { out->status = 3; goto done; }
    if (!bounded(header->Xsize, header->Ysize, limit)) { out->status = 2; goto done; }
    int orientation = 1;
    if (vips_image_get_typeof(header, VIPS_META_ORIENTATION)) vips_image_get_int(header, VIPS_META_ORIENTATION, &orientation);
    out->width = orientation >= 5 && orientation <= 8 ? header->Ysize : header->Xsize;
    out->height = orientation >= 5 && orientation <= 8 ? header->Xsize : header->Ysize;
    if (!strcmp(format, "jpeg") && vips_image_get_typeof(header, "jpeg-multiscan"))
        vips_image_get_int(header, "jpeg-multiscan", &diagnostics->jpeg_multiscan);
    finish_stage(diagnostics, stage, &started);
    stage = PIPELINE_PREPARE;
    small = thumbnail(data, length, limit, size, format, header, &owner, &out->status, control);
    if (!small) goto done;
    canvas = canvas_graph(small, size);
    if (!canvas) goto done;
    vips_image_set_progress(canvas, TRUE);
    g_signal_connect(canvas, "eval", G_CALLBACK(progress), control);
    finish_stage(diagnostics, stage, &started);
    stage = PIPELINE_EVAL;
    out->pixels = vips_image_write_to_memory(canvas, &out->length);
    if (out->pixels && out->length == (size_t)size * size * 3) out->status = 0;
    else { g_free(out->pixels); out->pixels = NULL; }
done:
    finish_stage(diagnostics, stage, &started);
    VIPS_UNREF(canvas); VIPS_UNREF(small); VIPS_UNREF(header);
    avif_close(&owner);
    if (out->status) g_strlcpy(out->reason, vips_error_buffer(), sizeof out->reason);
    vips_error_clear();
    vips_thread_shutdown();
    finish_stage(diagnostics, PIPELINE_CLEANUP, &started);
    g_private_set(&current_diagnostics, NULL);
    return result;
}
