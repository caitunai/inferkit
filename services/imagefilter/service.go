package imagefilter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/rs/zerolog/log"
	ort "github.com/yalue/onnxruntime_go"

	"github.com/caitun/inferkit/services/imagepipeline"
	"github.com/caitun/inferkit/services/imagetiming"
	"github.com/caitun/inferkit/services/resource"
)

// Result retains model label order and the caller's optional source URL.
type Result struct {
	URL           string    `json:"url,omitempty"`
	SHA256        string    `json:"sha256"`
	Label         string    `json:"label"`
	Labels        []string  `json:"labels"`
	Probabilities []float32 `json:"probabilities"`
	ClassID       int       `json:"class_id"`
	Confidence    float32   `json:"confidence"`
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
	return newWithPlan(c, automaticPlan(c, resource.Detect()))
}

func newWithPlan(c Config, plan executionPlan) (*Service, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	m, data, err := loadModel(c.Model)
	if err != nil {
		return nil, err
	}
	if err = imagepipeline.Available(); err != nil {
		return nil, errors.Join(ErrConfig, err)
	}
	if m.Preprocessing.Version != imagepipeline.Version() {
		return nil, errors.Join(ErrModel, &imagepipeline.VersionError{Expected: m.Preprocessing.Version, Actual: imagepipeline.Version()})
	}
	e, err := newEngine(c, plan, m, data)
	if err != nil {
		return nil, err
	}
	log.Info().Str("preprocess_profile", imagepipeline.Profile).Str("libvips_version", imagepipeline.Version()).Str("avif_decoder", "dav1d").Str("jpeg_library", imagepipeline.JPEGLibrary()).Str("resize_backend", "libvips").Str("tensor_backend", planarBackendName()).Str("image_decoder", "libvips").Str("execution_provider", e.provider).Str("coreml_compute_units", c.CoreMLComputeUnits).Int("cpus", plan.cpus).Int64("memory_bytes", plan.memory).Int("threads", plan.threads).Int("image_concurrency", plan.concurrency).Int("max_requests", plan.requests).Int("intra_spin_us", plan.spinMicros).Int("dynamic_block_base", plan.dynamicBlockBase).Bool("cache_bypass", c.BypassCache).Int("cache_entries", c.CacheEntries).Str("model_sha256", m.SHA256).Str("onnxruntime_version", ort.GetVersion()).Msg("image filter automatic resource plan")
	return &Service{
		engine: e, http: newDownloader(c, plan), config: c, plan: plan, labels: slices.Clone(m.Labels), modelSHA256: m.SHA256,
		requests: make(chan struct{}, plan.requests), cache: newResultStore(c),
	}, nil
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
func (s *Service) Check(ctx context.Context, src Source) (result Result, returnErr error) {
	ctx, trace, owner := imagetiming.Ensure(ctx)
	if owner {
		defer func() { trace.Finish(ctx, returnErr == nil, 0) }()
	}
	trace.Input(sourceKind(src), 0)
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
	return s.checkSource(ctx, src)
}

func (s *Service) checkSource(ctx context.Context, src Source) (Result, error) {
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
	result, err := s.checkContent(ctx, key, data)
	if err != nil {
		return Result{}, err
	}
	result.URL = src.URL
	result.Labels = slices.Clone(result.Labels)
	result.Probabilities = slices.Clone(result.Probabilities)
	return result, nil
}

func (s *Service) checkContent(ctx context.Context, key string, data []byte) (Result, error) {
	trace := imagetiming.From(ctx)
	if s.config.BypassCache {
		trace.Cache("bypass")
		return s.classify(ctx, key, data)
	}
	done := imagetiming.Measure(ctx, imagetiming.CacheLookup)
	result, call, owner := s.cache.begin(key)
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
	result, err := s.classify(ctx, key, data)
	done = imagetiming.Measure(ctx, imagetiming.CacheStore)
	s.cache.finish(key, call, result, err)
	done(nil)
	return result, err
}

func (s *Service) classify(ctx context.Context, key string, data []byte) (Result, error) {
	result, err := s.infer(ctx, data)
	result.SHA256 = key
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

func (s *Service) infer(ctx context.Context, data []byte) (Result, error) {
	done := imagetiming.Measure(ctx, imagetiming.SlotWait)
	select {
	case <-ctx.Done():
		done(ctx.Err())
		return Result{}, errors.Join(ErrInference, ctx.Err())
	case slot := <-s.engine.slots:
		done(nil)
		defer func() { s.engine.slots <- slot }()
		if err := preprocess(ctx, data, s.config.MaxPixels, slot.input.GetData()); err != nil {
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
	best := 0
	sum := float64(0)
	for i, p := range probabilities {
		if math.IsNaN(float64(p)) || math.IsInf(float64(p), 0) || p < 0 || p > 1 {
			return Result{}, ErrInference
		}
		sum += float64(p)
		if p > probabilities[best] {
			best = i
		}
	}
	if math.Abs(sum-1) > 0.001 {
		return Result{}, ErrInference
	}
	return Result{
		Label: s.labels[best], ClassID: best, Confidence: probabilities[best],
		Labels: s.labels, Probabilities: slices.Clone(probabilities),
	}, nil
}
