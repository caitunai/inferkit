package reclassify

import "slices"

func (s *Service) progress(id uint, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	next.Cursor = id
	s.currentID = 0
	switch code {
	case "":
		next.Succeeded++
		next.ConsecutiveFailures = 0
	case "content_changed", "record_changed":
		next.Skipped++
		next.ConsecutiveFailures = 0
	default:
		next.Failed++
		next.ConsecutiveFailures++
	}
	if code != "" {
		next.Failures = append(slices.Clone(next.Failures), Failure{ID: id, Code: code})
		if len(next.Failures) > maxFailures {
			next.Failures = next.Failures[len(next.Failures)-maxFailures:]
		}
	}
	if next.ConsecutiveFailures >= s.config.FailureLimit && next.Status == Running {
		next.Status, next.Reason = Paused, "consecutive_failures"
	}
	return s.commit(next)
}

func (s *Service) complete() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.state
	if next.Status != Running {
		return nil
	}
	next.Status, next.Reason, next.Cursor = Completed, "", next.Snapshot.MaxID
	processed := next.Succeeded + next.Failed + next.Skipped
	if processed < next.Snapshot.Total {
		next.Skipped += next.Snapshot.Total - processed // Rows removed externally during the run.
	}
	return s.commit(next)
}
