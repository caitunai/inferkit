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
	mu          sync.Mutex
	active      sync.WaitGroup
	closeOnce   sync.Once
	closed      bool
}

// New verifies the package and loads one CPU session; no inference-time initialization occurs.
func New(c Config) (*Service, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	m, data, err := loadModel(c.Model)
	if err != nil {
		return nil, err
	}
	e, err := newEngine(c, m, data)
	if err != nil {
		return nil, err
	}
	return &Service{
		engine: e, http: newDownloader(c), config: c, labels: slices.Clone(m.Labels), modelSHA256: m.SHA256,
		requests: make(chan struct{}, c.MaxRequests), cache: newResultStore(c),
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

// Limits exposes the configured body and operation bounds to HTTP adapters.
func (s *Service) Limits() (int64, time.Duration, int) {
	return s.config.MaxBytes, s.config.Timeout, s.config.MaxRequests
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
	if err := s.admit(); err != nil {
		return Result{}, err
	}
	defer func() { <-s.requests; s.active.Done() }()
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	data, err := s.readSource(ctx, src)
	if err != nil {
		return Result{}, err
	}
	if err = ctx.Err(); err != nil {
		return Result{}, errors.Join(ErrInput, err)
	}
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
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
	result, call, owner := s.cache.begin(key)
	if call == nil {
		return result, nil
	}
	if !owner {
		select {
		case <-ctx.Done():
			return Result{}, errors.Join(ErrInference, ctx.Err())
		case <-call.done:
			return call.result, call.err
		}
	}
	result, err := s.infer(ctx, data)
	result.SHA256 = key
	s.cache.finish(key, call, result, err)
	return result, err
}

func (s *Service) infer(ctx context.Context, data []byte) (Result, error) {
	select {
	case <-ctx.Done():
		return Result{}, errors.Join(ErrInference, ctx.Err())
	case slot := <-s.engine.slots:
		defer func() { s.engine.slots <- slot }()
		if err := preprocess(ctx, data, s.config.MaxPixels, slot.input.GetData()); err != nil {
			return Result{}, errors.Join(ErrPreprocess, err)
		}
		probabilities, err := s.engine.run(ctx, slot)
		if err != nil {
			return Result{}, err
		}
		return s.result(probabilities)
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
