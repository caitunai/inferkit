/* Experimental executable only: reuse the production lifecycle with narrowly
 * wrapped public operations. It is never compiled into the Go application. */
#if defined(__linux__)
#define _GNU_SOURCE
#endif
#include <vips/vips.h>
#include <stdarg.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/resource.h>

static char variant = 'A';

static int trial_thumbnail(void *data, size_t length, VipsImage **out, int size, ...) {
    va_list options;
    va_start(options, size);
    int status;
    const unsigned char *bytes = data;
    if (variant == 'B' && length >= 3 && bytes[0] == 255 && bytes[1] == 216 && bytes[2] == 255) {
        VipsImage *source = NULL;
        status = vips_jpegload_buffer(data, length, &source, "shrink", 4,
            "access", VIPS_ACCESS_SEQUENTIAL, "fail_on", VIPS_FAIL_ON_ERROR, NULL);
        if (!status) status = vips_thumbnail_image(source, out, size, "height", size,
            "size", VIPS_SIZE_BOTH, "crop", VIPS_INTERESTING_NONE,
            "no_rotate", FALSE, "linear", FALSE, NULL);
        VIPS_UNREF(source);
    } else {
        VipsBlob *blob = vips_blob_new(NULL, data, length);
        status = vips_call_split("thumbnail_buffer", options, blob, out, size);
        vips_area_unref(VIPS_AREA(blob));
    }
    va_end(options);
    return status;
}

static int trial_colourspace(VipsImage *in, VipsImage **out, VipsInterpretation space, ...) {
    if (variant == 'D' && space == VIPS_INTERPRETATION_sRGB &&
        in->Type == space && in->BandFmt == VIPS_FORMAT_UCHAR && in->Bands == 3) {
        *out = g_object_ref(in);
        return 0;
    }
    va_list options;
    va_start(options, space);
    int status = vips_call_split("colourspace", options, in, out, space);
    va_end(options);
    return status;
}

static int trial_cast(VipsImage *in, VipsImage **out, VipsBandFormat format, ...) {
    if (variant == 'D' && in->BandFmt == format) {
        *out = g_object_ref(in);
        return 0;
    }
    va_list options;
    va_start(options, format);
    int status = vips_call_split("cast", options, in, out, format);
    va_end(options);
    return status;
}

#define vips_thumbnail_buffer trial_thumbnail
#define vips_colourspace trial_colourspace
#define vips_cast trial_cast
#include "../services/internal/vips/native_vips.c"
#undef vips_thumbnail_buffer
#undef vips_colourspace
#undef vips_cast

typedef struct {
    GMutex mutex;
    GCond condition;
    int ready;
    gboolean start;
} Gate;

typedef struct {
    Gate *gate;
    const void *data;
    size_t length;
    gint64 *times;
    gint64 stages[PIPELINE_STAGES];
    int samples;
    int failed;
} Worker;

static PipelineResult normalize(const void *data, size_t length) {
    PipelineControl *control = pipeline_control_new();
    PipelineResult result = pipeline_normalize(data, length, 20000000, 320, control);
    pipeline_free(control);
    return result;
}

static gpointer measure(gpointer argument) {
    Worker *worker = argument;
    g_mutex_lock(&worker->gate->mutex);
    worker->gate->ready++;
    g_cond_broadcast(&worker->gate->condition);
    while (!worker->gate->start) g_cond_wait(&worker->gate->condition, &worker->gate->mutex);
    g_mutex_unlock(&worker->gate->mutex);
    for (int i = 0; i < worker->samples; i++) {
        gint64 started = g_get_monotonic_time();
        PipelineResult result = normalize(worker->data, worker->length);
        if (result.status) { worker->failed = 1; return NULL; }
        pipeline_free(result.pixels);
        worker->times[i] = g_get_monotonic_time() - started;
        for (int s = 0; s < PIPELINE_STAGES; s++) worker->stages[s] += result.diagnostics.elapsed_us[s];
    }
    return NULL;
}

static int compare(const void *a, const void *b) {
    gint64 left = *(const gint64 *)a, right = *(const gint64 *)b;
    return (left > right) - (left < right);
}

static gint64 percentile(gint64 *times, int count, int percent) {
    return times[(count * percent + 99) / 100 - 1];
}

static gint64 cpu_time(void) {
    struct rusage usage;
    if (getrusage(RUSAGE_SELF, &usage)) return 0;
    return (usage.ru_utime.tv_sec + usage.ru_stime.tv_sec) * 1000000LL +
        usage.ru_utime.tv_usec + usage.ru_stime.tv_usec;
}

static int run(const void *data, size_t length, int samples, int callers, PipelineDiagnostics diagnostics) {
    Gate gate = {0};
    g_mutex_init(&gate.mutex); g_cond_init(&gate.condition);
    Worker *workers = g_new0(Worker, callers);
    GThread **threads = g_new0(GThread *, callers);
    gint64 *times = g_new(gint64, samples * callers);
    for (int i = 0; i < callers; i++) {
        workers[i] = (Worker){.gate=&gate, .data=data, .length=length,
            .times=times+i*samples, .samples=samples};
        threads[i] = g_thread_new("native_trial", measure, &workers[i]);
    }
    g_mutex_lock(&gate.mutex);
    while (gate.ready != callers) g_cond_wait(&gate.condition, &gate.mutex);
    gint64 cpu_started = cpu_time(), started = g_get_monotonic_time();
    gate.start = TRUE;
    g_cond_broadcast(&gate.condition);
    g_mutex_unlock(&gate.mutex);
    int failed = 0;
    gint64 stages[PIPELINE_STAGES] = {0};
    for (int i = 0; i < callers; i++) {
        g_thread_join(threads[i]); failed |= workers[i].failed;
        for (int s = 0; s < PIPELINE_STAGES; s++) stages[s] += workers[i].stages[s];
    }
    gint64 elapsed = g_get_monotonic_time() - started, cpu = cpu_time() - cpu_started;
    int count = callers * samples;
    qsort(times, count, sizeof *times, compare);
    struct rusage usage;
    getrusage(RUSAGE_SELF, &usage);
    gint64 rss = usage.ru_maxrss;
#if !defined(__APPLE__)
    rss *= 1024;
#endif
    if (!failed) {
        printf("{\"samples\":%d,\"p50_ms\":%.6f,\"p95_ms\":%.6f,\"p99_ms\":%.6f,"
            "\"images_per_second\":%.4f,\"cpu_percent\":%.4f,\"peak_rss_bytes\":%lld,"
            "\"header_ms\":%.6f,\"prepare_ms\":%.6f,\"eval_ms\":%.6f,\"cleanup_ms\":%.6f,"
            "\"jpeg_shrink\":%d,\"jpeg_multiscan\":%s,\"jpeg_width\":%d,\"jpeg_height\":%d}\n",
            count, percentile(times,count,50)/1000.0, percentile(times,count,95)/1000.0,
            percentile(times,count,99)/1000.0, count*1000000.0/elapsed, cpu*100.0/elapsed,
            (long long)rss, stages[0]/(1000.0*count), stages[1]/(1000.0*count),
            stages[2]/(1000.0*count), stages[3]/(1000.0*count), diagnostics.jpeg_shrink,
            diagnostics.jpeg_multiscan ? "true" : "false", diagnostics.jpeg_width, diagnostics.jpeg_height);
    }
    g_free(times); g_free(threads); g_free(workers);
    g_cond_clear(&gate.condition); g_mutex_clear(&gate.mutex);
    return failed;
}

/* VARIANT THREADS INPUT SAMPLES CALLERS RGB_OUTPUT
 * A/C/E are deliberately identical: production already implements C and E. */
int main(int argc, char **argv) {
    if (argc != 7 || !strchr("ABCDE", argv[1][0])) return 2;
    variant = argv[1][0];
    int threads = atoi(argv[2]), samples = atoi(argv[4]), callers = atoi(argv[5]);
    if (threads < 1 || threads > 8 || samples < 1 || samples > 10000 || callers < 1 || callers > 8) return 2;
    if (vips_init("inferkit-native-trial")) return 3;
    vips_cache_set_max(0); vips_cache_set_max_mem(0);
    vips_concurrency_set(threads);
    if (pipeline_init()) return 3;
    gchar *data = NULL; gsize length = 0;
    if (!g_file_get_contents(argv[3], &data, &length, NULL)) return 4;
    PipelineDiagnostics diagnostics = {0};
    for (int i = 0; i < 5; i++) {
        PipelineResult result = normalize(data, length);
        if (result.status) { fprintf(stderr, "%s\n", result.reason); return 5; }
        diagnostics = result.diagnostics;
        if (i == 4 && !g_file_set_contents(argv[6], result.pixels, result.length, NULL)) return 6;
        pipeline_free(result.pixels);
    }
    int status = run(data, length, samples, callers, diagnostics);
    g_free(data);
    return status;
}
