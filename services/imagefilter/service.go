package imagefilter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/resource"
)

// Result retains model label order and the caller's optional source URL.
type Result struct {
	PredictionSHA256 string    `json:"-"`
	URL              string    `json:"url,omitempty"`
	SHA256           string    `json:"sha256"`
	Label            string    `json:"label"`
	Labels           []string  `json:"labels"`
	Probabilities    []float32 `json:"probabilities"`
	ClassID          int       `json:"class_id"`
	Confidence       float32   `json:"confidence"`
}

// Service owns a single model session. Call Close after stopping HTTP ingress.
type Service struct {
	engine      *engine
	http        *resty.Client
	cache       *resultStore
	requests    chan struct{}
	closeErr    error
	labels      []string
	modelSHA256 string
	config      Config
	plan        executionPlan
	mu          sync.Mutex
	active      sync.WaitGroup
	closeOnce   sync.Once
	closed      bool
}

// New verifies the package and creates one reusable inference session.
func New(c Config) (*Service, error) {
	return NewContext(context.Background(), c)
}

// NewContext completes bounded startup warmup before publishing the service.
func NewContext(ctx context.Context, c Config) (*Service, error) {
	return newWithPlanContext(ctx, c, automaticPlan(c, resource.Detect()))
}

func newWithPlan(c Config, plan executionPlan) (*Service, error) {
	return newWithPlanContext(context.Background(), c, plan)
}

func newWithPlanContext(ctx context.Context, c Config, plan executionPlan) (*Service, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrRuntime, err)
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	m, data, err := loadModel(c.Model)
	if err != nil {
		return nil, err
	}
	if err = validatePreprocessor(m); err != nil {
		return nil, err
	}
	e, err := newEngine(ctx, c, plan, m, data)
	if err != nil {
		return nil, err
	}
	service := &Service{
		engine: e, http: newDownloader(c, plan), config: c, plan: plan, labels: slices.Clone(m.Labels), modelSHA256: m.SHA256,
		requests: make(chan struct{}, plan.requests), cache: newResultStore(c),
	}
	for _, backend := range []Backend{Libvips, Go} {
		event := log.Info()
		backend.logPreprocessor(event)
		event.Str("model_preprocess_profile", m.Preprocessing.Profile).Str("prediction_sha256", service.WithBackend(backend).PredictionSHA256()).Str("execution_provider", e.provider).Str("coreml_compute_units", c.CoreMLComputeUnits).Int("cpus", plan.cpus).Int64("memory_bytes", plan.memory).Int("threads", plan.threads).Int("image_concurrency", plan.concurrency).Int("max_requests", plan.requests).Int("intra_spin_us", plan.spinMicros).Int("dynamic_block_base", plan.dynamicBlockBase).Bool("cache_bypass", c.BypassCache).Int("cache_entries", c.CacheEntries).Str("model_sha256", m.SHA256).Str("onnxruntime_version", ort.GetVersion()).Msg("image filter automatic resource plan")
	}
	return service, nil
}

// ModelSHA256 identifies the loaded model without rereading its deployment files.
func (s *Service) ModelSHA256() string { return s.modelSHA256 }

// Close rejects new work, waits for admitted calls and frees all native resources once.
func (s *Service) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.active.Wait()
		s.http.GetClient().CloseIdleConnections()
		s.closeErr = s.engine.close()
	})
	return s.closeErr
}

// Limits exposes the effective body, operation and admission bounds to HTTP adapters.
func (s *Service) Limits() (int64, time.Duration, int) {
	return s.config.MaxBytes, s.config.Timeout, s.plan.requests
}

func (s *Service) admit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	select {
	case s.requests <- struct{}{}:
		s.active.Add(1)
		return nil
	default:
		return ErrBusy
	}
}

// Check prefers file bytes, hashes the actual content, and coalesces identical in-flight images.
// A canceled owner fails its shared attempt; waiting callers may retry independently.
func (s *Service) Check(ctx context.Context, src Source) (Result, error) {
	return s.WithBackend(Libvips).Check(ctx, src)
}

// Check uses this adapter's backend and the service's shared admission budget.
func (d Detector) Check(ctx context.Context, src Source) (result Result, returnErr error) {
	if _, err := ParseBackend(string(d.backend)); err != nil {
		return Result{}, err
	}
	s := d.service
	ctx, trace, owner := imagetiming.Ensure(ctx)
	if owner {
		defer func() { trace.Finish(ctx, returnErr == nil, 0) }()
	}
	trace.Input(sourceKind(src), 0)
	trace.Preprocessing(string(d.backend), d.backend.preprocessProfile(), d.backend.tensorBackend())
	done := imagetiming.Measure(ctx, imagetiming.Admission)
	err := s.admit()
	done(err)
	trace.Capacity(len(s.requests), s.plan.requests, s.plan.concurrency, s.plan.threads)
	if err != nil {
		return Result{}, err
	}
	defer func() { <-s.requests; s.active.Done() }()
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	return d.checkSource(ctx, src)
}

func (d Detector) checkSource(ctx context.Context, src Source) (Result, error) {
	s := d.service
	data, err := s.readSource(ctx, src)
	if err != nil {
		return Result{}, err
	}
	done := imagetiming.Measure(ctx, imagetiming.Hash)
	if err = ctx.Err(); err != nil {
		done(err)
		return Result{}, errors.Join(ErrInput, err)
	}
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	done(nil)
	result, err := d.checkContent(ctx, key, data)
	if err != nil {
		return Result{}, err
	}
	result.URL = src.URL
	result.Labels = slices.Clone(result.Labels)
	result.Probabilities = slices.Clone(result.Probabilities)
	return result, nil
}

func (d Detector) checkContent(ctx context.Context, key string, data []byte) (Result, error) {
	s := d.service
	trace := imagetiming.From(ctx)
	if s.config.BypassCache {
		trace.Cache("bypass")
		return d.classify(ctx, key, data)
	}
	done := imagetiming.Measure(ctx, imagetiming.CacheLookup)
	cacheKey := d.backend.cacheKey(key)
	result, call, owner := s.cache.begin(cacheKey)
	done(nil)
	if call == nil {
		trace.Cache("hit")
		return result, nil
	}
	if !owner {
		trace.Cache("shared")
		return awaitResult(ctx, call)
	}
	trace.Cache("miss")
	result, err := d.classify(ctx, key, data)
	done = imagetiming.Measure(ctx, imagetiming.CacheStore)
	s.cache.finish(cacheKey, call, result, err)
	done(nil)
	return result, err
}

func (d Detector) classify(ctx context.Context, key string, data []byte) (Result, error) {
	result, err := d.infer(ctx, data)
	result.SHA256 = key
	result.PredictionSHA256 = d.PredictionSHA256()
	return result, err
}

func awaitResult(ctx context.Context, call *pendingCall) (result Result, returnErr error) {
	done := imagetiming.Measure(ctx, imagetiming.CacheWait)
	defer func() { done(returnErr) }()
	select {
	case <-ctx.Done():
		return Result{}, errors.Join(ErrInference, ctx.Err())
	case <-call.done:
		return call.result, call.err
	}
}

func (d Detector) infer(ctx context.Context, data []byte) (Result, error) {
	s := d.service
	done := imagetiming.Measure(ctx, imagetiming.SlotWait)
	select {
	case <-ctx.Done():
		done(ctx.Err())
		return Result{}, errors.Join(ErrInference, ctx.Err())
	case slot := <-s.engine.slots:
		done(nil)
		defer func() { s.engine.slots <- slot }()
		if err := d.prepare(ctx, data, slot.input.GetData()); err != nil {
			return Result{}, errors.Join(ErrPreprocess, err)
		}
		done = imagetiming.Measure(ctx, imagetiming.Inference)
		probabilities, err := s.engine.run(ctx, slot)
		done(err)
		if err != nil {
			return Result{}, err
		}
		done = imagetiming.Measure(ctx, imagetiming.Result)
		result, err := s.result(probabilities)
		done(err)
		return result, err
	}
}

func (s *Service) result(probabilities []float32) (Result, error) {
	if len(probabilities) != len(s.labels) {
		return Result{}, ErrInference
	}
	if err := validateProbabilities(probabilities); err != nil {
		return Result{}, err
	}
	best := 0
	for i, p := range probabilities {
		if p > probabilities[best] {
			best = i
		}
	}
	return Result{
		Label: s.labels[best], ClassID: best, Confidence: probabilities[best],
		Labels: s.labels, Probabilities: slices.Clone(probabilities),
	}, nil
}
