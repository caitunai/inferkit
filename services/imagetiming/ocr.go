package imagetiming

import (
	"context"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/rs/zerolog"
)

// OCRPlan records actual providers and bounded execution capacity, never text.
type OCRPlan struct {
	DetectionProvider, RecognitionProvider string
	DetectionThreads, RecognitionThreads   int
	LineWorkers, Images, Requests, Active  int
}

type ocrMetrics struct {
	runs                                                  []time.Duration
	plan                                                  OCRPlan
	candidates, boxes, returned, batches, lines, batchMax int
	widthMin, widthMax, reused, allocated                 int
	detectionWidth, detectionHeight                       int
}

// NewOCR owns one HTTP or CLI OCR summary.
func NewOCR(ctx context.Context) (context.Context, *Trace) {
	ctx, trace := New(ctx)
	trace.ocr = new(ocrMetrics)
	return ctx, trace
}

// EnsureOCR preserves HTTP timing while allowing direct callers to log once.
func EnsureOCR(ctx context.Context) (context.Context, *Trace, bool) {
	if trace := From(ctx); trace != nil && trace.ocr != nil {
		return ctx, trace, false
	}
	ctx, trace := NewOCR(ctx)
	return ctx, trace, true
}

// OCRCapacity updates the request's actual execution plan.
func (t *Trace) OCRCapacity(plan OCRPlan, tensor string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr != nil {
		t.ocr.plan, t.tensorBackend = plan, tensor
	}
}

// OCRDetection records detector dimensions and region counts.
func (t *Trace) OCRDetection(width, height, candidates, boxes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr != nil {
		t.ocr.detectionWidth, t.ocr.detectionHeight = width, height
		t.ocr.candidates, t.ocr.boxes = candidates, boxes
	}
}

// OCRBoxes records detection candidates before confidence filtering.
func (t *Trace) OCRBoxes(candidates, boxes int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr != nil {
		t.ocr.candidates, t.ocr.boxes = candidates, boxes
	}
}

// OCRBatch records a bounded number of run durations; durations may overlap.
func (t *Trace) OCRBatch(lines, width int, elapsed time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr == nil {
		return
	}
	m := t.ocr
	m.batches++
	m.lines += lines
	m.batchMax = max(m.batchMax, lines)
	if m.widthMin == 0 {
		m.widthMin = width
	}
	m.widthMin, m.widthMax = min(m.widthMin, width), max(m.widthMax, width)
	if len(m.runs) < 3000 {
		m.runs = append(m.runs, elapsed)
	}
}

// OCRBuffers counts reused versus newly allocated native tensor slots.
func (t *Trace) OCRBuffers(reused bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr == nil {
		return
	}
	if reused {
		t.ocr.reused++
	} else {
		t.ocr.allocated++
	}
}

// OCRReturned records only the count after confidence filtering.
func (t *Trace) OCRReturned(lines int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ocr != nil {
		t.ocr.returned = lines
	}
}

func (m *ocrMetrics) fields(event *zerolog.Event) {
	p := m.plan
	event.Str("detection_provider", p.DetectionProvider).Str("recognition_provider", p.RecognitionProvider).
		Int("detection_threads", p.DetectionThreads).Int("recognition_threads", p.RecognitionThreads).
		Int("line_workers", p.LineWorkers).Int("image_concurrency", p.Images).Int("max_requests", p.Requests).Int("active_requests", p.Active).
		Int("detection_width", m.detectionWidth).Int("detection_height", m.detectionHeight).
		Int("candidates", m.candidates).Int("boxes", m.boxes).Int("returned_lines", m.returned).
		Int("recognition_batches", m.batches).Int("recognition_lines", m.lines).Int("recognition_batch_max", m.batchMax).
		Int("recognition_width_min", m.widthMin).Int("recognition_width_max", m.widthMax).
		Int("buffer_reused", m.reused).Int("buffer_allocated", m.allocated)
	if len(m.runs) == 0 {
		return
	}
	slices.Sort(m.runs)
	for _, q := range []int{50, 95, 99} {
		i := int(math.Ceil(float64(len(m.runs))*float64(q)/100)) - 1
		event.Float64("recognition_run_p"+strconv.Itoa(q)+"_ms", milliseconds(m.runs[i]))
	}
	event.Float64("recognition_run_max_ms", milliseconds(m.runs[len(m.runs)-1]))
}
