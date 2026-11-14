package reclassify

import (
	"context"
	"errors"
	"slices"
	"time"
)

// Start freezes the range and hands accepted work to the process worker.
func (s *Service) Start(ctx context.Context, request Start) (View, error) {
	if !request.Confirm || !validInterval(request.IntervalMS) {
		return View{}, ErrState
	}
	if request.ModelSHA256 != s.detector.ModelSHA256() {
		return View{}, ErrModel
	}
	s.control.Lock()
	defer s.control.Unlock()
	state := s.Status().State
	if err := s.canStart(state); err != nil {
		return View{}, err
	}
	writer, err := s.repository.AcquireReclassification(ctx)
	if err != nil {
		return View{}, errors.Join(ErrStorage, err)
	}
	snapshot, err := writer.Snapshot(ctx)
	if err != nil {
		closeWriter(ctx, writer)
		return View{}, errors.Join(ErrStorage, err)
	}
	next := State{
		Version: checkpointVersion, JobID: state.JobID + 1, Status: Running,
		StartedAt: time.Now().UTC(), ModelSHA256: request.ModelSHA256, Snapshot: snapshot,
		IntervalMS: request.IntervalMS, Failures: []Failure{},
	}
	if err = s.enqueue(next, writer); err != nil {
		closeWriter(ctx, writer)
		return View{}, err
	}
	return s.Status(), nil
}

func (s *Service) canStart(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	if s.active {
		return ErrBusy
	}
	switch state.Status {
	case Idle, Completed, Cancelled:
		return nil
	default:
		return ErrState
	}
}

// Control never starts a goroutine from an HTTP request. The owned process
// worker receives accepted work and cancellation; requests only change state.
func (s *Service) Control(ctx context.Context, action string) (View, error) {
	s.control.Lock()
	defer s.control.Unlock()
	if action == "resume" {
		return s.resume(ctx)
	}
	s.mu.Lock()
	err := s.stopWork(action)
	s.mu.Unlock()
	if err != nil {
		return View{}, err
	}
	return s.Status(), nil
}

func (s *Service) stopWork(action string) error {
	if s.closed {
		return ErrUnavailable
	}
	next := s.state
	status, err := stopStatus(next.Status, action, s.active)
	if err != nil {
		return err
	}
	next.Status = status
	if err = s.commit(next); err != nil {
		return err
	}
	if s.workCancel != nil {
		s.workCancel()
	}
	return nil
}

func stopStatus(current, action string, active bool) (string, error) {
	switch action {
	case "pause":
		if current != Running {
			return "", ErrState
		}
		return Pausing, nil
	case "cancel":
		if !slices.Contains([]string{Running, Paused, Pausing}, current) {
			return "", ErrState
		}
		if active {
			return Cancelling, nil
		}
		return Cancelled, nil
	default:
		return "", ErrState
	}
}

func (s *Service) resume(ctx context.Context) (View, error) {
	s.mu.Lock()
	next, active, closed := s.state, s.active, s.closed
	s.mu.Unlock()
	if closed || active || next.Status != Paused {
		return View{}, ErrState
	}
	if next.ModelSHA256 != s.detector.ModelSHA256() {
		return View{}, ErrModel
	}
	writer, err := s.repository.AcquireReclassification(ctx)
	if err != nil {
		return View{}, errors.Join(ErrStorage, err)
	}
	next.Status, next.Reason, next.ConsecutiveFailures = Running, "", 0
	if err = s.enqueue(next, writer); err != nil {
		closeWriter(ctx, writer)
		return View{}, err
	}
	return s.Status(), nil
}

// control serializes enqueue with loop shutdown, and mu guards admission/status.
func (s *Service) enqueue(state State, writer Writer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrUnavailable
	}
	if err := s.commit(state); err != nil {
		return err
	}
	s.active = true
	s.runs <- writer // One buffered slot; no other job may be active.
	return nil
}
