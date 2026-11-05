package ocr

import (
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
	"github.com/caitun/inferkit/services/onnx"
	"github.com/caitun/inferkit/services/resource"
	"github.com/caitun/inferkit/xutil"
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
	return newWithPlan(c, automaticPlan(c, resource.Detect()))
}

func newWithPlan(c Config, plan executionPlan) (*Service, error) {
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
	s.http = imageinput.NewDownloader(imageinput.Limits{Timeout: c.Timeout, MaxRequests: plan.requests, Concurrency: plan.images})
	log.Info().Str("detection_provider", s.detector.provider).Str("recognition_provider", s.recognizer.provider).Str("image_decoder", imageinput.NativeDecoder).Int("cpus", plan.cpus).Int64("memory_bytes", plan.memory).Int("detection_threads", plan.detectionThreads).Int("recognition_threads", plan.recognitionThreads).Int("line_workers", plan.lineWorkers).Int("image_concurrency", plan.images).Int("max_requests", plan.requests).Msg("OCR automatic resource plan")
	return s, nil
}

func (s *Service) loadModels() error {
	c := s.config
	backend := onnx.Backend{Provider: c.Provider, ComputeUnits: c.CoreMLComputeUnits}
	var err error
	s.detector, err = loadModel(c.DetectionModel, c.DetectionSHA256, s.plan.detectionThreads, backend, ort.Shape{1, 3, -1, -1}, ort.Shape{1, 1, -1, -1})
	if err != nil {
		return err
	}
	s.recognizer, err = loadModel(c.RecognitionModel, c.RecognitionSHA256, s.plan.recognitionThreads, backend, ort.Shape{1, 3, int64(c.RecognitionHeight), -1}, ort.Shape{1, -1, int64(len(s.dictionary))})
	if err != nil {
		return err
	}
	// A fixed width/height export cannot serve variable-size OCR inputs.
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
func (s *Service) Recognize(ctx context.Context, source imageinput.Source) (Result, error) {
	ctx, finish, err := s.admit(ctx)
	if err != nil {
		return Result{}, err
	}
	defer finish()
	// Reserve before decoding so decoded images stay within the automatic memory budget.
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-ctx.Done():
		return Result{}, errors.Join(ErrInference, ctx.Err())
	}
	data, err := imageinput.Read(ctx, s.http, source, s.config.MaxBytes)
	if err != nil {
		return Result{}, errors.Join(ErrSource, err)
	}
	img, err := imageinput.Decode(data, s.config.MaxPixels)
	if err != nil {
		return Result{}, errors.Join(ErrSource, err)
	}
	if err = ctx.Err(); err != nil {
		return Result{}, errors.Join(ErrInference, err)
	}
	return s.infer(ctx, opaqueImage(img))
}

func (s *Service) infer(ctx context.Context, img *image.RGBA) (Result, error) {
	boxes, err := s.detect(ctx, img)
	if err != nil {
		return Result{}, err
	}
	return s.recognizeBoxes(ctx, img, boxes)
}

func (s *Service) detect(ctx context.Context, img *image.RGBA) ([]Box, error) {
	release, err := s.execution.acquire(ctx, s.execution.cpus)
	if err != nil {
		return nil, err
	}
	defer release()
	input, shape, err := detectionTensor(ctx, img, s.config.DetectionMaxSide)
	if err != nil {
		return nil, err
	}
	values, output, err := s.detector.run(ctx, input, shape)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	return detectionBoxes(ctx, values, output, b.Dx(), b.Dy(), s.config)
}

func (s *Service) recognizeBoxes(ctx context.Context, img *image.RGBA, boxes []Box) (Result, error) {
	lines := make([]Line, len(boxes))
	group := xutil.ErrGroup(ctx, "ocr_recognize_lines").Limit(s.plan.lineWorkers)
	for index, box := range boxes {
		group.Go(func(ctx context.Context) error {
			line, err := s.recognizeLine(ctx, img, box)
			lines[index] = line
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return Result{}, errors.Join(ErrLineRecognition, err)
	}
	return s.result(img, lines), nil
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

func (s *Service) recognizeLine(ctx context.Context, img *image.RGBA, box Box) (Line, error) {
	release, err := s.execution.acquireLine(ctx)
	if err != nil {
		return Line{}, err
	}
	defer release()
	input, shape, err := recognitionTensor(ctx, img, box, s.config.RecognitionHeight, s.config.RecognitionMaxWidth)
	if err != nil {
		return Line{}, err
	}
	values, output, err := s.recognizer.run(ctx, input, shape)
	if err != nil {
		return Line{}, err
	}
	text, confidence, err := decodeCTC(values, output, s.dictionary)
	if err != nil {
		return Line{}, err
	}
	return Line{Text: text, Confidence: confidence, Box: box}, nil
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
			errs = append(errs, m.session.Destroy())
		}
	}
	errs = append(errs, s.lease.Close())
	if err := errors.Join(errs...); err != nil {
		return errors.Join(ErrInference, err)
	}
	return nil
}
