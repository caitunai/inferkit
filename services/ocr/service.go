package ocr

import (
	"cmp"
	"context"
	"errors"
	"image"
	"strings"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/onnx"
	"github.com/caitun/inferkit/services/resource"
)

// Line contains one recognized region in orientation-corrected image coordinates.
type Line struct {
	Text       string  `json:"text"`
	Box        Box     `json:"box"`
	Confidence float64 `json:"confidence"`
}

// Result returns reading-order lines and their newline-joined text.
type Result struct {
	Text   string `json:"text"`
	Lines  []Line `json:"lines"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Service owns detection and recognition sessions and bounds concurrent requests and inference.
// Images and recognized text are request-local and are not cached or persisted.
type Service struct {
	execution  *executor
	calls      map[*call]struct{}
	detector   *model
	recognizer *model
	http       *resty.Client
	lease      *onnx.Lease
	slots      chan struct{}
	closeErr   error
	dictionary []string
	config     Config
	plan       executionPlan
	mu         sync.Mutex
	active     sync.WaitGroup
	closeOnce  sync.Once
	closed     bool
}

// New validates deployment assets and initializes the model sessions once.
func New(c Config) (*Service, error) {
	return NewContext(context.Background(), c)
}

// NewContext completes finite OCR startup work before the caller publishes it.
func NewContext(ctx context.Context, c Config) (*Service, error) {
	return newWithPlanContext(ctx, c, automaticPlan(c, resource.Detect()))
}

func newWithPlan(c Config, plan executionPlan) (*Service, error) {
	return newWithPlanContext(context.Background(), c, plan)
}

func newWithPlanContext(ctx context.Context, c Config, plan executionPlan) (*Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrModel, err)
	}

	if err := c.validate(); err != nil {
		return nil, err
	}
	dictionary, err := loadDictionary(c.Dictionary, c.DictionarySHA256)
	if err != nil {
		return nil, err
	}
	lease, err := onnx.Acquire(c.Library)
	if err != nil {
		return nil, errors.Join(ErrModel, err)
	}
	s := &Service{calls: make(map[*call]struct{}), lease: lease, config: c, dictionary: dictionary, plan: plan, execution: newExecutor(plan), slots: make(chan struct{}, plan.images)}
	if err = s.loadModels(); err != nil {
		return nil, errors.Join(err, s.closeResources())
	}
	if err = s.warmup(ctx); err != nil {
		return nil, errors.Join(err, s.closeResources())
	}
	s.http = imageinput.NewDownloader(imageinput.Limits{Timeout: c.Timeout, MaxRequests: plan.requests, Concurrency: plan.images})
	log.Info().Str("detection_provider", s.detector.provider).Str("recognition_provider", s.recognizer.provider).Str("image_decoder", imageinput.NativeDecoder).Int("cpus", plan.cpus).Int64("memory_bytes", plan.memory).Int("detection_threads", plan.detectionThreads).Int("recognition_threads", plan.recognitionThreads).Int("line_workers", plan.lineWorkers).Int("image_concurrency", plan.images).Int("max_requests", plan.requests).Msg("OCR automatic resource plan")
	return s, nil
}

func (s *Service) loadModels() error {
	c := s.config
	det := modelSettings{
		role: "detection",
		path: c.DetectionModel, pin: c.DetectionSHA256, threads: s.plan.detectionThreads, slots: 1, cache: c.CoreMLCache,
		backend: onnx.Backend{Provider: cmp.Or(c.DetectionProvider, c.Provider), ComputeUnits: c.CoreMLComputeUnits},
		input:   ort.Shape{1, 3, -1, -1}, output: ort.Shape{1, 1, -1, -1},
	}
	rec := modelSettings{
		role: "recognition",
		path: c.RecognitionModel, pin: c.RecognitionSHA256, threads: s.plan.recognitionThreads, slots: s.plan.lineWorkers, cache: c.CoreMLCache,
		backend: onnx.Backend{Provider: cmp.Or(c.RecognitionProvider, c.Provider), ComputeUnits: c.CoreMLComputeUnits},
		input:   ort.Shape{1, 3, int64(c.RecognitionHeight), -1}, output: ort.Shape{1, -1, int64(len(s.dictionary))},
	}
	var err error
	s.detector, err = loadModel(det)
	if err != nil {
		return err
	}
	s.recognizer, err = loadModel(rec)
	if err != nil {
		return err
	}
	if s.detector.input[2] > 0 || s.detector.input[3] > 0 || s.recognizer.input[3] > 0 {
		return ErrModel
	}
	return nil
}

// Limits exposes request bounds to the HTTP adapter.
func (s *Service) Limits() (int64, time.Duration, int) {
	return s.config.MaxBytes, s.config.Timeout, s.plan.requests
}

type call struct{ cancel context.CancelFunc }

func (s *Service) admit(ctx context.Context) (context.Context, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, ErrClosed
	}
	if len(s.calls) >= s.plan.requests {
		return nil, nil, ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	request := &call{cancel: cancel}
	s.calls[request] = struct{}{}
	s.active.Add(1)
	finish := func() {
		cancel()
		s.mu.Lock()
		delete(s.calls, request)
		s.mu.Unlock()
		s.active.Done()
	}
	return ctx, finish, nil
}

// Recognize reads, decodes and processes one image within the caller's deadline.
func (s *Service) Recognize(ctx context.Context, source imageinput.Source) (result Result, returnErr error) {
	ctx, trace, owner := imagetiming.EnsureOCR(ctx)
	if owner {
		defer func() { trace.Finish(ctx, returnErr == nil, 0) }()
	}
	done := imagetiming.Measure(ctx, imagetiming.Admission)
	admitted, finish, err := s.admit(ctx)
	done(err)
	if err != nil {
		return Result{}, err
	}
	defer finish()
	ctx = admitted
	s.traceCapacity(trace)
	done = imagetiming.Measure(ctx, imagetiming.SlotWait)
	select {
	case s.slots <- struct{}{}:
		done(nil)
		defer func() { <-s.slots }()
	case <-ctx.Done():
		done(ctx.Err())
		return Result{}, errors.Join(ErrInference, ctx.Err())
	}
	img, err := s.readImage(ctx, source)
	if err != nil {
		return Result{}, err
	}
	return s.infer(ctx, img)
}

func (s *Service) infer(ctx context.Context, img *image.RGBA) (Result, error) {
	boxes, err := s.detect(ctx, img)
	if err != nil {
		return Result{}, err
	}
	return s.recognizeBoxes(ctx, img, boxes)
}

func (s *Service) result(img *image.RGBA, lines []Line) Result {
	result := Result{Width: img.Bounds().Dx(), Height: img.Bounds().Dy(), Lines: make([]Line, 0, len(lines))}
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		if line.Text == "" || line.Confidence < s.config.MinConfidence {
			continue
		}
		result.Lines = append(result.Lines, line)
		texts = append(texts, line.Text)
	}
	result.Text = strings.Join(texts, "\n")
	return result
}

// Close rejects new requests, drains admitted work and releases native resources.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		for request := range s.calls {
			request.cancel()
		}
		s.mu.Unlock()
		s.active.Wait()
		s.closeErr = s.closeResources()
	})
	return s.closeErr
}

func (s *Service) closeResources() error {
	var errs []error
	if s.http != nil {
		s.http.GetClient().CloseIdleConnections()
	}
	for _, m := range []*model{s.recognizer, s.detector} {
		if m != nil {
			errs = append(errs, m.close())
		}
	}
	errs = append(errs, s.lease.Close())
	if err := errors.Join(errs...); err != nil {
		return errors.Join(ErrInference, err)
	}
	return nil
}
