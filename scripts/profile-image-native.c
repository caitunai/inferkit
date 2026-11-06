/* Sampling-only harness. Signposts mark the exact materialization interval;
 * production CGO code and image operations remain unchanged. */
#if defined(__linux__)
#define _GNU_SOURCE
#endif
#include <vips/vips.h>
#if defined(__APPLE__)
#include <os/signpost.h>
static os_log_t profile_log;
#endif

__attribute__((noinline))
static void *profile_native_eval(VipsImage *image, size_t *length) {
#if defined(__APPLE__)
    os_signpost_id_t identity = os_signpost_id_generate(profile_log);
    os_signpost_interval_begin(profile_log, identity, "native_eval");
#endif
    void *pixels = vips_image_write_to_memory(image, length);
#if defined(__APPLE__)
    os_signpost_interval_end(profile_log, identity, "native_eval");
#endif
    return pixels;
}

#define vips_image_write_to_memory profile_native_eval
#define main benchmark_main
#include "bench-image-native.c"
#undef main
#undef vips_image_write_to_memory

int main(int argc, char **argv) {
#if defined(__APPLE__)
    profile_log = os_log_create("com.inferkit.native-profile", OS_LOG_CATEGORY_POINTS_OF_INTEREST);
#endif
    return benchmark_main(argc, argv);
}
