package reclassify

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imagetiming"
)

func (s *Service) loop(ctx context.Context) {
	defer s.drain(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case writer := <-s.runs:
			s.run(ctx, writer)
		}
	}
}

func (s *Service) drain(ctx context.Context) {
	s.control.Lock()
	defer s.control.Unlock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	select {
	case writer := <-s.runs:
		s.run(ctx, writer) // Canceled owner closes queued ownership without work.
	default:
	}
}

func (s *Service) run(ctx context.Context, writer Writer) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer s.finish(ctx, writer)
	s.mu.Lock()
	s.workCancel = cancel
	if s.state.Status != Running {
		cancel()
	}
	s.mu.Unlock()
	if err := s.execute(ctx, writer); err != nil && ctx.Err() == nil {
		s.mu.Lock()
		next := s.state
		if next.Status == Running {
			next.Status, next.Reason = Paused, "database_unavailable"
		}
		commitErr := s.commit(next)
		s.mu.Unlock()
		log.Ctx(ctx).Error().Err(errors.Join(err, commitErr)).Msg("image reclassification stopped")
	}
}

func (s *Service) finish(ctx context.Context, writer Writer) {
	closeWriter(ctx, writer)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active, s.workCancel, s.currentID = false, nil, 0
	next := s.state
	switch next.Status {
	case Running, Pausing:
		next.Status, next.Reason = Paused, "worker_stopped"
	case Cancelling:
		next.Status = Cancelled
	}
	if err := s.commit(next); err != nil {
		log.Ctx(ctx).Error().Err(err).Msg("persist image reclassification stop")
	}
}

func (s *Service) execute(ctx context.Context, writer Writer) error {
	for {
		state := s.Status().State
		if state.Status != Running {
			return nil
		}
		done, err := s.next(ctx, writer, state)
		if done || err != nil {
			return err
		}
	}
}

func (s *Service) next(ctx context.Context, writer Writer, state State) (bool, error) {
	items, err := writer.Next(ctx, state.Cursor, state.Snapshot.MaxID, s.config.Batch)
	if err != nil {
		return false, errors.Join(ErrStorage, err)
	}
	if len(items) == 0 {
		return true, s.complete()
	}
	return false, s.batch(ctx, writer, items, state)
}

func (s *Service) batch(ctx context.Context, writer Writer, items []Item, state State) error {
	for _, item := range items {
		if s.Status().State.Status != Running {
			return nil
		}
		if item.ID <= state.Cursor || item.ID > state.Snapshot.MaxID {
			return ErrStorage
		}
		if err := s.process(ctx, writer, item); err != nil {
			return err
		}
		state.Cursor = item.ID
		if err := wait(ctx, time.Duration(state.IntervalMS)*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) process(ctx context.Context, writer Writer, item Item) error {
	s.mu.Lock()
	s.currentID = item.ID
	s.mu.Unlock()
	result, code, err := s.detect(ctx, writer, item)
	if err != nil {
		return err
	}
	if code == "" {
		applied, applyErr := writer.Apply(ctx, item, result)
		if applyErr != nil {
			return errors.Join(ErrStorage, applyErr)
		}
		if !applied {
			code = "record_changed"
		}
	}
	return s.recordProgress(ctx, writer, item.ID, code)
}

func (s *Service) recordProgress(ctx context.Context, writer Writer, id uint, code string) error {
	if code != "" {
		if err := writer.Verify(ctx); err != nil {
			return errors.Join(ErrStorage, err)
		}
	}
	return s.progress(id, code)
}

func (s *Service) detect(ctx context.Context, writer Writer, item Item) (imagefilter.Result, string, error) {
	for attempts := 0; ; {
		result, err := s.ownedAttempt(ctx, writer, item)
		if err == nil {
			return result, "", nil
		}
		if errors.Is(err, ErrStorage) || errors.Is(err, ErrUnavailable) {
			return imagefilter.Result{}, "", err
		}
		code, next := detectionOutcome(err, attempts)
		attempts = next
		if code != "" {
			return imagefilter.Result{}, code, nil
		}
		if err = wait(ctx, time.Second); err != nil {
			return imagefilter.Result{}, "", err
		}
	}
}

func detectionOutcome(err error, attempts int) (string, int) {
	if errors.Is(err, imagefilter.ErrContentChanged) {
		return "content_changed", attempts
	}
	if !errors.Is(err, imagefilter.ErrBusy) {
		attempts++
	}
	if attempts >= 2 {
		return "detection_failed", attempts
	}
	return "", attempts
}

func (s *Service) ownedAttempt(ctx context.Context, writer Writer, item Item) (imagefilter.Result, error) {
	if err := ctx.Err(); err != nil {
		return imagefilter.Result{}, errors.Join(ErrStorage, err)
	}
	if err := writer.Verify(ctx); err != nil {
		return imagefilter.Result{}, errors.Join(ErrStorage, err)
	}
	result, err := s.attempt(ctx, item)
	if errors.Is(err, imagefilter.ErrClosed) {
		return imagefilter.Result{}, errors.Join(ErrUnavailable, err)
	}
	return result, err
}

func (s *Service) attempt(ctx context.Context, item Item) (imagefilter.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	// Attach an unreported trace so bulk inference does not emit 300k request
	// summaries. Progress/errors contain record IDs and safe codes, never URLs.
	ctx, _ = imagetiming.New(ctx)
	result, err := s.detector.Recheck(ctx, imagefilter.Source{URL: item.URL}, item.SHA256)
	if err != nil {
		return imagefilter.Result{}, errors.Join(ErrDetection, err)
	}
	return result, nil
}

func wait(ctx context.Context, duration time.Duration) error {
	if err := ctx.Err(); err != nil {
		return errors.Join(ErrState, err)
	}
	if duration == 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return errors.Join(ErrState, ctx.Err())
	case <-timer.C:
		return nil
	}
}
