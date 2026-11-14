package reclassify

import (
	"errors"
	"testing"
)

func TestFailureRetentionAndCircuitBreaker(t *testing.T) {
	t.Parallel()
	service := testService(t, &testStore{}, successDetector())
	service.mu.Lock()
	service.state.Status = Running
	service.state.Snapshot = Snapshot{MaxID: 100, Total: 100}
	service.mu.Unlock()
	for id := uint(1); id <= 60; id++ {
		if err := service.progress(id, "detection_failed"); err != nil {
			t.Fatal(err)
		}
	}
	state := service.Status().State
	if state.Status != Paused || state.Reason != "consecutive_failures" || len(state.Failures) != 50 || state.Failures[0].ID != 11 {
		t.Fatal(state)
	}
	state.Failures[0].Code = "mutated-client-copy"
	if service.Status().State.Failures[0].Code == state.Failures[0].Code {
		t.Fatal("status exposed mutable state")
	}
}

func TestCorruptCheckpointFailsClosed(t *testing.T) {
	t.Parallel()
	service := testService(t, &testStore{}, successDetector())
	if err := service.root.WriteFile(stateFile, []byte(`{"version":1,"status":"running","interval_ms":500,"cursor":10,"snapshot":{"max_id":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(service.root); !errors.Is(err, ErrCheckpoint) {
		t.Fatal(err)
	}
}
