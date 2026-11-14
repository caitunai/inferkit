package reclassify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
)

const (
	stateFile       = "state.json"
	checkpointLimit = 64 << 10
)

func load(root *os.Root) (State, error) {
	file, err := root.Open(stateFile)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: checkpointVersion, Status: Idle, IntervalMS: DefaultIntervalMS, Failures: []Failure{}}, nil
	}
	if err != nil {
		return State{}, errors.Join(ErrCheckpoint, err)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, checkpointLimit+1))
	if err = errors.Join(readErr, file.Close()); err != nil {
		return State{}, errors.Join(ErrCheckpoint, err)
	}
	var state State
	if len(data) > checkpointLimit {
		return State{}, ErrCheckpoint
	}
	if err = json.Unmarshal(data, &state); err != nil {
		return State{}, errors.Join(ErrCheckpoint, err)
	}
	if err = state.validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s State) validate() error {
	if s.Version != checkpointVersion || !validInterval(s.IntervalMS) || len(s.Failures) > maxFailures || s.Cursor > s.Snapshot.MaxID {
		return ErrCheckpoint
	}
	if !s.validCounts() {
		return ErrCheckpoint
	}
	switch s.Status {
	case Idle:
		return nil
	case Running, Pausing, Paused, Cancelling, Cancelled, Completed:
		return s.validateModel()
	default:
		return ErrCheckpoint
	}
}

func (s State) validCounts() bool {
	remaining := s.Snapshot.Total
	for _, count := range []uint64{s.Succeeded, s.Failed, s.Skipped} {
		if count > remaining {
			return false
		}
		remaining -= count
	}
	return s.ConsecutiveFailures >= 0
}

func (s State) validateModel() error {
	digest, err := hex.DecodeString(s.ModelSHA256)
	if err != nil || len(digest) != sha256.Size || s.JobID == 0 {
		return ErrCheckpoint
	}
	return nil
}

// Sync the replacement and parent directory before accepting progress. A crash
// before the rename retains the old cursor; replayed writes are idempotent.
func persist(root *os.Root, state State) error {
	data, err := json.Marshal(state)
	if err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	if len(data) > checkpointLimit {
		return ErrCheckpoint
	}
	file, err := root.OpenFile("state.tmp", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	_, writeErr := file.Write(data)
	if err = errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	if err = root.Rename("state.tmp", stateFile); err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	directory, err := root.Open(".")
	if err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	if err = errors.Join(directory.Sync(), directory.Close()); err != nil {
		return errors.Join(ErrCheckpoint, err)
	}
	return nil
}
