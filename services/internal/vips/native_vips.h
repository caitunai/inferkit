#ifndef INFERKIT_SHARED_VIPS_H
#define INFERKIT_SHARED_VIPS_H
#include <stddef.h>
#include <stdint.h>

typedef struct {
    void *pixels;
    size_t length;
    int width, height, status;
    char format[8];
    char reason[256];
} PipelineResult;
typedef struct PipelineControl PipelineControl;
int pipeline_init(void);
const char *pipeline_version(void);
const char *pipeline_jpeg_library(void);
PipelineControl *pipeline_control_new(void);
void pipeline_cancel(PipelineControl *control);
PipelineResult pipeline_normalize(const void *data, size_t length, uint64_t max_pixels, int size, PipelineControl *control);
void pipeline_free(void *pixels);
#endif
