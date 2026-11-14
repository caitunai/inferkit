package reclassify

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/xutil"
)

// Service owns one serial worker and one durable checkpoint.
type Service struct {
	repository Repository
	detector   Detector
	root       *os.Root
	lock       *os.File
	runs       chan Writer
	tasks      *xutil.Group
	stop       context.CancelFunc
	workCancel context.CancelFunc
	closeErr   error
	config     Config
	state      State
	control    sync.Mutex
	mu         sync.Mutex
	currentID  uint
	once       sync.Once
	active     bool
	closed     bool
}

// New restores progress without automatically resuming interrupted work.
func New(ctx context.Context, c Config, repository Repository, detector Detector) (*Service, error) {
	if err := errors.Join(ctx.Err(), c.validate()); err != nil {
		return nil, err
	}
	if repository == nil || detector == nil {
		return nil, ErrUnavailable
	}
	root, file, err := openDirectory(c.Directory)
	if err != nil {
		return nil, err
	}
	s := &Service{root: root, lock: file, repository: repository, detector: detector, config: c, runs: make(chan Writer, 1)}
	if err = s.restore(); err != nil {
		return nil, errors.Join(err, file.Close(), root.Close())
	}
	owner, stop := context.WithCancel(ctx)
	s.stop = stop
	s.tasks = xutil.WaitGroup("image_reclassification").Go(owner, s.loop)
	return s, nil
}

func openDirectory(path string) (*os.Root, *os.File, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, nil, errors.Join(ErrCheckpoint, err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, nil, errors.Join(ErrCheckpoint, err)
	}
	file, err := lock(root)
	if err != nil {
		return nil, nil, errors.Join(err, root.Close())
	}
	return root, file, nil
}

func (s *Service) restore() error {
	state, err := load(s.root)
	if err != nil {
		return err
	}
	switch state.Status {
	case Running, Pausing:
		state.Status, state.Reason = Paused, "service_restarted"
	case Cancelling:
		state.Status, state.Reason = Cancelled, "service_restarted"
	default:
		s.state = state
		return nil
	}
	state.UpdatedAt = time.Now().UTC()
	if err = persist(s.root, state); err != nil {
		return err
	}
	s.state = state
	return nil
}

// Status returns an independent snapshot without querying the database.
func (s *Service) Status() View {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	state.Failures = slices.Clone(state.Failures)
	return View{Available: !s.closed, Active: s.active, CurrentModel: s.detector.ModelSHA256(), State: state, CurrentID: s.currentID}
}

// commit requires mu. Checkpoint failure immediately stops further image work.
func (s *Service) commit(next State) error {
	next.UpdatedAt = time.Now().UTC()
	if err := persist(s.root, next); err != nil {
		s.state.Status, s.state.Reason = Paused, "checkpoint_unavailable"
		if s.workCancel != nil {
			s.workCancel()
		}
		return err
	}
	if s.state.Status != next.Status {
		log.Info().Uint64("job_id", next.JobID).Str("status", next.Status).
			Str("reason", next.Reason).Str("model_sha256", next.ModelSHA256).
			Uint("cursor", next.Cursor).Uint64("succeeded", next.Succeeded).
			Uint64("failed", next.Failed).Uint64("skipped", next.Skipped).
			Msg("image reclassification state changed")
	}
	s.state = next
	return nil
}

// Stop refuses new work and cancels the worker when process shutdown begins.
func (s *Service) Stop() {
	if s == nil {
		return
	}
	s.control.Lock()
	defer s.control.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.stop()
}

// Close waits for owned work and releases locks before the database shuts down.
func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.once.Do(func() {
		s.Stop()
		s.tasks.Wait()
		if err := errors.Join(s.lock.Close(), s.root.Close()); err != nil {
			s.closeErr = errors.Join(ErrCheckpoint, err)
		}
	})
	return s.closeErr
}

func closeWriter(ctx context.Context, writer Writer) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := writer.Close(ctx); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("release image reclassification ownership")
	}
}
