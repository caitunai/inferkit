// Package imagetiming collects bounded, request-local image pipeline diagnostics.
package imagetiming

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Stage identifies a fixed pipeline step, never a user-supplied label.
type Stage uint8

// Preprocess contains native or Go image preparation and Tensor. NativePipeline
// contains NativeHeader, NativePrepare, NativeEval and NativeCleanup.
const (
	Admission Stage = iota
	HTTPParse
	SourceRead
	Hash
	CacheLookup
	CacheWait
	SlotWait
	Decode
	Preprocess
	NativePipeline
	NativeHeader
	NativePrepare
	NativeEval
	NativeCleanup
	Convert
	Resize
	Premultiply
	ResizeHorizontal
	ResizeVertical
	Unpremultiply
	Tensor
	Inference
	Result
	CacheStore
	Persist
	HTTPResponse
	HTTPCleanup
	Opaque
	DetectionWait
	DetectionPreprocess
	DetectionInference
	DetectionPostprocess
	Recognition
	RecognitionWait
	RecognitionPreprocess
	RecognitionInference
	CTC
	stageCount
)

var stageNames = [stageCount]string{
	"admission", "http_parse", "source_read", "hash", "cache_lookup", "cache_wait", "slot_wait",
	"decode", "preprocess", "native_pipeline", "native_header", "native_prepare", "native_eval", "native_cleanup", "convert", "resize", "premultiply", "resize_horizontal", "resize_vertical", "unpremultiply", "tensor", "inference", "result", "cache_store", "persist", "http_response", "http_cleanup",
	"opaque", "detection_wait", "detection_preprocess", "detection_inference", "detection_postprocess", "recognition_wall", "recognition_wait_sum", "recognition_preprocess_sum", "recognition_inference_sum", "ctc_sum",
}

type contextKey struct{}

// Trace belongs to one HTTP request or CLI detection. It never stores image data,
// URLs, hashes, credentials or error messages. The mutex also permits shared callers.
type Trace struct {
	ocr           *ocrMetrics
	started       time.Time
	source        string
	cache         string
	persistence   string
	failed        string
	pixels        string
	format        string
	provider      string
	decoder       string
	resizeBackend string
	jpegLoader    string
	preprocessor  string
	profile       string
	tensorBackend string
	durations     [stageCount]time.Duration
	seen          [stageCount]bool
	jpegMultiscan bool
	bytes         int
	width         int
	height        int
	active        int
	capacity      int
	workers       int
	threads       int
	resizeWorkers int
	jpegShrink    int
	jpegWidth     int
	jpegHeight    int
	jpegBuilds    int
	attempt       uint64
	mu            sync.Mutex
}

// New starts a separate trace even if its parent has a trace.
func New(ctx context.Context) (context.Context, *Trace) {
	t := &Trace{started: time.Now(), cache: "unreached", persistence: "not_applicable"}
	return context.WithValue(ctx, contextKey{}, t), t
}

// Ensure reuses an HTTP-owned trace or creates a CLI-owned one.
func Ensure(ctx context.Context) (context.Context, *Trace, bool) {
	if trace := From(ctx); trace != nil {
		return ctx, trace, false
	}
	ctx, trace := New(ctx)
	return ctx, trace, true
}

// From returns nil when diagnostics have not been enabled for this operation.
func From(ctx context.Context) *Trace {
	trace, ok := ctx.Value(contextKey{}).(*Trace)
	if !ok {
		return nil
	}
	return trace
}

// Measure starts a step; its returned function must be called before proceeding.
// Observing a failure records its stage without duplicating error logging.
func Measure(ctx context.Context, stage Stage) func(error) {
	trace := From(ctx)
	if trace == nil || stage >= stageCount {
		return func(error) {}
	}
	started := time.Now()
	return func(err error) {
		elapsed := time.Since(started)
		trace.mu.Lock()
		defer trace.mu.Unlock()
		trace.durations[stage] += elapsed
		trace.seen[stage] = true
		if err != nil && trace.failed == "" {
			trace.failed = stageNames[stage]
		}
	}
}

// Input records encoded byte count and a fixed source kind.
func (t *Trace) Input(kind string, size int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.source, t.bytes = kind, size
}

// Image records orientation-corrected decoded dimensions.
func (t *Trace) Image(width, height int, pixels string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.width, t.height = width, height
	t.pixels = pixels
}

// Format records the codec selected from the actual encoded bytes.
func (t *Trace) Format(format string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.format = format
}

// Execution records the registered provider, which may retain CPU subgraphs.
func (t *Trace) Execution(provider string) { t.mu.Lock(); defer t.mu.Unlock(); t.provider = provider }

// Decoder records the actual decoder, including fallback to Go.
func (t *Trace) Decoder(decoder string) { t.mu.Lock(); defer t.mu.Unlock(); t.decoder = decoder }

// Preprocessing identifies the selected path even for cache hits.
func (t *Trace) Preprocessing(backend, profile, tensor string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.preprocessor, t.profile, t.tensorBackend = backend, profile, tensor
}

// Native records measured C stages. Deferred image work can occur in Prepare or Eval.
func (t *Trace) Native(header, prepare, eval, cleanup time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for offset, duration := range [...]time.Duration{header, prepare, eval, cleanup} {
		stage := int(NativeHeader) + offset
		t.durations[stage] += duration
		t.seen[stage] = true
	}
}

// JPEG records the last observed loader's shrink and output dimensions, never a prediction.
func (t *Trace) JPEG(loader string, shrink, width, height, builds int, multiscan bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.jpegLoader, t.jpegShrink, t.jpegWidth, t.jpegHeight = loader, shrink, width, height
	t.jpegBuilds, t.jpegMultiscan = builds, multiscan
}

// Attempt records a session-wide inference invocation number, not a cache hit.
func (t *Trace) Attempt(number uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.attempt = number
}

// ResizeWorkers records the actual caller slots used for this preparation.
func (t *Trace) ResizeWorkers(workers int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.resizeWorkers = workers
}

// ResizeBackend records the row kernel actually selected after its range guard.
func (t *Trace) ResizeBackend(backend string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.resizeBackend != "" && t.resizeBackend != backend {
		t.resizeBackend = "mixed"
		return
	}
	t.resizeBackend = backend
}

// Capacity snapshots the service's current admission and execution capacity.
func (t *Trace) Capacity(active, capacity, workers, threads int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.active, t.capacity, t.workers, t.threads = active, capacity, workers, threads
}

// Cache records hit, miss or shared-in-flight behavior.
func (t *Trace) Cache(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cache = state
}

// Persistence records whether the URL result was saved or why it was skipped.
func (t *Trace) Persistence(state string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.persistence = state
}

// Finish emits one structured summary. status is zero for non-HTTP callers.
// Milliseconds are explicit and independent of zerolog's global duration unit.
func (t *Trace) Finish(ctx context.Context, success bool, status int) {
	elapsed := time.Since(t.started)
	t.mu.Lock()
	defer t.mu.Unlock()
	event := log.Ctx(ctx).Info()
	t.summary(event, success, status, elapsed)
	message := "image filter timing"
	if t.ocr != nil {
		t.ocr.fields(event)
		message = "OCR timing"
	} else {
		t.imageFields(event)
	}
	for stage, duration := range t.durations {
		if t.seen[stage] {
			event = event.Float64(stageNames[stage]+"_ms", milliseconds(duration))
		}
	}
	event.Msg(message)
}

func (t *Trace) summary(event *zerolog.Event, success bool, status int, elapsed time.Duration) {
	event.Bool("success", success).Int("status", status).
		Float64("total_ms", milliseconds(elapsed)).Str("source_type", t.source).Str("failed_stage", t.failed).
		Int("image_bytes", t.bytes).Int("image_width", t.width).Int("image_height", t.height).
		Str("decode_backend", t.decoder).Str("pixel_type", t.pixels).Str("image_format", t.format).
		Str("tensor_backend", t.tensorBackend)
}

func (t *Trace) imageFields(event *zerolog.Event) {
	event.Str("cache_state", t.cache).Str("persist_state", t.persistence).
		Str("execution_provider", t.provider).Str("resize_backend", t.resizeBackend).
		Uint64("inference_attempt", t.attempt).Int("resize_workers", t.resizeWorkers).
		Int("active_requests", t.active).Int("max_requests", t.capacity).
		Int("image_concurrency", t.workers).Int("threads", t.threads)
	event.Str("preprocess_backend", t.preprocessor).Str("preprocess_profile", t.profile)
	if t.jpegLoader != "" {
		event.Str("jpeg_loader", t.jpegLoader).Int("jpeg_shrink_on_load", t.jpegShrink).
			Int("jpeg_decode_width", t.jpegWidth).Int("jpeg_decode_height", t.jpegHeight).
			Int("jpeg_loader_builds", t.jpegBuilds).Bool("jpeg_multiscan", t.jpegMultiscan)
	}
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
