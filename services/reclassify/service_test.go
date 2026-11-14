package reclassify

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/caitun/inferkit/services/imagefilter"
)

var errTestStorage = errors.New("simulated database outage")

const testLabel = "10_photo"

const testModel = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type testStore struct {
	applyErr  error
	verifyErr error
	rows      []Item
	written   []uint
	mu        sync.Mutex
	closed    int
	held      bool
}

func (s *testStore) AcquireReclassification(context.Context) (Writer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held {
		return nil, ErrBusy
	}
	s.held = true
	return &testWriter{store: s}, nil
}

type testWriter struct{ store *testStore }

func (w *testWriter) Snapshot(context.Context) (Snapshot, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	var maxID uint
	for _, item := range w.store.rows {
		maxID = max(maxID, item.ID)
	}
	return Snapshot{MaxID: maxID, Total: uint64(len(w.store.rows))}, nil
}

func (w *testWriter) Next(_ context.Context, after, ceiling uint, limit int) ([]Item, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	var items []Item
	for _, item := range w.store.rows {
		if item.ID > after && item.ID <= ceiling && len(items) < limit {
			items = append(items, item)
		}
	}
	return items, nil
}

func (w *testWriter) Apply(_ context.Context, item Item, _ imagefilter.Result) (bool, error) {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	if w.store.applyErr != nil {
		return false, w.store.applyErr
	}
	w.store.written = append(w.store.written, item.ID)
	return true, nil
}

func (w *testWriter) Verify(context.Context) error {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	return w.store.verifyErr
}

func (w *testWriter) Close(context.Context) error {
	w.store.mu.Lock()
	defer w.store.mu.Unlock()
	w.store.held = false
	w.store.closed++
	return nil
}

type testDetector struct {
	check func(context.Context, imagefilter.Source, string) (imagefilter.Result, error)
	model string
}

func (d *testDetector) ModelSHA256() string {
	if d.model != "" {
		return d.model
	}
	return testModel
}

func (d *testDetector) Recheck(ctx context.Context, src imagefilter.Source, sha string) (imagefilter.Result, error) {
	return d.check(ctx, src, sha)
}

func successDetector() *testDetector {
	return &testDetector{check: func(_ context.Context, _ imagefilter.Source, sha string) (imagefilter.Result, error) {
		return imagefilter.Result{Label: testLabel, SHA256: sha, Confidence: .9}, nil
	}}
}

func testService(t *testing.T, store *testStore, detector *testDetector) *Service {
	t.Helper()
	service, err := New(t.Context(), Config{Directory: t.TempDir(), Timeout: time.Second, Batch: 2, FailureLimit: 20}, store, detector)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return service
}

func startTest(t *testing.T, s *Service) {
	t.Helper()
	if _, err := s.Start(t.Context(), Start{Confirm: true, ModelSHA256: s.detector.ModelSHA256(), IntervalMS: MinIntervalMS}); err != nil {
		t.Fatal(err)
	}
}

func waitStatus(t *testing.T, s *Service, status string) State {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		view := s.Status()
		if view.State.Status == status && !view.Active {
			return view.State
		}
		select {
		case <-deadline.C:
			t.Fatalf("waiting for %s: %+v", status, view)
		case <-ticker.C:
		}
	}
}

func TestSerialSnapshotAndRequestLifetime(t *testing.T) {
	t.Parallel()
	store := &testStore{rows: []Item{{ID: 1, SHA256: testModel}, {ID: 3, SHA256: testModel}}}
	var active, maximum atomic.Int32
	detector := successDetector()
	detector.check = func(_ context.Context, _ imagefilter.Source, sha string) (imagefilter.Result, error) {
		current := active.Add(1)
		maximum.Store(max(maximum.Load(), current))
		defer active.Add(-1)
		store.mu.Lock()
		if len(store.rows) == 2 {
			store.rows = append(store.rows, Item{ID: 4, SHA256: testModel})
		}
		store.mu.Unlock()
		return imagefilter.Result{Label: testLabel, SHA256: sha}, nil
	}
	service := testService(t, store, detector)
	request, cancel := context.WithCancel(t.Context())
	if _, err := service.Start(request, Start{Confirm: true, ModelSHA256: testModel, IntervalMS: MinIntervalMS}); err != nil {
		t.Fatal(err)
	}
	cancel() // Closing the browser request must not own the accepted worker.
	state := waitStatus(t, service, Completed)
	if state.Succeeded != 2 || state.Cursor != 3 || state.Snapshot.Total != 2 || maximum.Load() != 1 {
		t.Fatalf("serial snapshot lost: %+v active=%d", state, maximum.Load())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.written) != 2 || store.written[1] != 3 || store.closed != 1 {
		t.Fatalf("out-of-range write or leaked ownership: %+v", store)
	}
}

func TestPauseResumeAndAnnotationSafeFailure(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 1)
	var calls atomic.Int32
	detector := successDetector()
	detector.check = func(ctx context.Context, _ imagefilter.Source, sha string) (imagefilter.Result, error) {
		if calls.Add(1) == 1 {
			entered <- struct{}{}
			<-ctx.Done()
			return imagefilter.Result{}, errors.Join(imagefilter.ErrInput, ctx.Err())
		}
		return imagefilter.Result{Label: testLabel, SHA256: sha}, nil
	}
	service := testService(t, &testStore{rows: []Item{{ID: 2, SHA256: testModel}}}, detector)
	startTest(t, service)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := service.Control(t.Context(), "pause"); err != nil {
		t.Fatal(err)
	}
	state := waitStatus(t, service, Paused)
	if state.Cursor != 0 || state.Failed != 0 {
		t.Fatal("cancellation advanced checkpoint", state)
	}
	if _, err := service.Control(t.Context(), "resume"); err != nil {
		t.Fatal(err)
	}
	if state = waitStatus(t, service, Completed); state.Succeeded != 1 {
		t.Fatal(state)
	}
}

func TestDatabaseFailureStopsWithoutAdvancing(t *testing.T) {
	t.Parallel()
	store := &testStore{rows: []Item{{ID: 1}, {ID: 2}}, applyErr: errTestStorage}
	service := testService(t, store, successDetector())
	startTest(t, service)
	state := waitStatus(t, service, Paused)
	if state.Cursor != 0 || state.Succeeded != 0 || state.Reason != "database_unavailable" {
		t.Fatal("database failure skipped a row", state)
	}
}

func TestContentChangesAreSkippedBeforeWrite(t *testing.T) {
	t.Parallel()
	detector := &testDetector{check: func(context.Context, imagefilter.Source, string) (imagefilter.Result, error) {
		return imagefilter.Result{}, imagefilter.ErrContentChanged
	}}
	store := &testStore{rows: []Item{{ID: 1}}}
	service := testService(t, store, detector)
	startTest(t, service)
	state := waitStatus(t, service, Completed)
	if state.Skipped != 1 || state.Failed != 0 || len(state.Failures) != 1 {
		t.Fatal(state)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.written) != 0 {
		t.Fatal("changed source overwritten")
	}
}

func TestExclusiveOwnershipAndExplicitConfirmation(t *testing.T) {
	t.Parallel()
	service := testService(t, &testStore{}, successDetector())
	if _, err := New(t.Context(), service.config, &testStore{}, successDetector()); !errors.Is(err, ErrBusy) {
		t.Fatal("shared volume was not exclusive", err)
	}
	if _, err := service.Start(t.Context(), Start{ModelSHA256: testModel, IntervalMS: MinIntervalMS}); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	if _, err := service.Start(t.Context(), Start{Confirm: true, ModelSHA256: "old", IntervalMS: MinIntervalMS}); !errors.Is(err, ErrModel) {
		t.Fatal(err)
	}
}

func TestCheckpointFailureStopsProcessing(t *testing.T) {
	t.Parallel()
	service := testService(t, &testStore{}, successDetector())
	// Force atomic rename to fail without permissions assumptions under root.
	if err := service.root.Mkdir(stateFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Start(t.Context(), Start{Confirm: true, ModelSHA256: testModel, IntervalMS: MinIntervalMS}); !errors.Is(err, ErrCheckpoint) {
		t.Fatal(err)
	}
	if view := service.Status(); view.Active || view.State.Reason != "checkpoint_unavailable" {
		t.Fatal(view)
	}
}

func TestRestartRequiresResumeAndMatchingModel(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	seedRunningCheckpoint(t, directory)
	service, err := New(t.Context(), Config{Directory: directory, Timeout: time.Second, Batch: 2, FailureLimit: 20}, &testStore{}, &testDetector{model: strings.Repeat("b", 64)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	if view := service.Status(); view.Active || view.State.Status != Paused || view.State.Cursor != 1 {
		t.Fatal(view)
	}
	if _, err = service.Control(t.Context(), "resume"); !errors.Is(err, ErrModel) {
		t.Fatal(err)
	}
	if _, err = service.Control(t.Context(), "cancel"); err != nil {
		t.Fatal(err)
	}
	if state := service.Status().State; state.Status != Cancelled || state.Succeeded != 1 {
		t.Fatal(state)
	}
}

func seedRunningCheckpoint(t *testing.T, directory string) {
	t.Helper()
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatal(err)
	}
	state := State{Version: checkpointVersion, Status: Running, JobID: 1, ModelSHA256: testModel, Snapshot: Snapshot{MaxID: 2, Total: 2}, Cursor: 1, Succeeded: 1, IntervalMS: MinIntervalMS}
	if err = persist(root, state); err != nil {
		t.Fatal(err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLostDatabaseOwnershipPreventsInference(t *testing.T) {
	t.Parallel()
	detector := &testDetector{check: func(context.Context, imagefilter.Source, string) (imagefilter.Result, error) {
		t.Error("inference without ownership")
		return imagefilter.Result{}, ErrUnavailable
	}}
	service := testService(t, &testStore{rows: []Item{{ID: 1}}, verifyErr: ErrBusy}, detector)
	startTest(t, service)
	if state := waitStatus(t, service, Paused); state.Cursor != 0 || state.Failed != 0 {
		t.Fatal(state)
	}
}

func TestConsecutiveFailuresPauseBeforeNextImage(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	detector := &testDetector{check: func(context.Context, imagefilter.Source, string) (imagefilter.Result, error) {
		calls.Add(1)
		return imagefilter.Result{}, imagefilter.ErrInput
	}}
	service := testService(t, &testStore{rows: []Item{{ID: 1}, {ID: 2}}}, detector)
	service.config.FailureLimit = 1
	startTest(t, service)
	state := waitStatus(t, service, Paused)
	if state.Cursor != 1 || state.Failed != 1 || state.Reason != "consecutive_failures" || calls.Load() != 2 {
		t.Fatal("retry bound or circuit breaker lost", state, calls.Load())
	}
}

func TestShutdownCancelsInFlightAndReleasesOwnership(t *testing.T) {
	t.Parallel()
	entered := make(chan struct{}, 1)
	detector := &testDetector{check: func(ctx context.Context, _ imagefilter.Source, _ string) (imagefilter.Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return imagefilter.Result{}, errors.Join(ErrDetection, ctx.Err())
	}}
	store := &testStore{rows: []Item{{ID: 1}}}
	service := testService(t, store, detector)
	startTest(t, service)
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not start")
	}
	if _, err := service.Start(t.Context(), Start{Confirm: true, ModelSHA256: testModel, IntervalMS: MinIntervalMS}); !errors.Is(err, ErrBusy) {
		t.Fatal("duplicate task accepted", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if view := service.Status(); view.Active || view.Available || view.State.Cursor != 0 {
		t.Fatal(view)
	}
	assertShutdownStore(t, store)
}

func assertShutdownStore(t *testing.T, store *testStore) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.held || store.closed != 1 || len(store.written) != 0 {
		t.Fatal("shutdown leaked ownership or committed cancelled row")
	}
}

func TestWorkerPanicReleasesOwnershipAndStopsAdmission(t *testing.T) {
	t.Parallel()
	detector := &testDetector{check: func(context.Context, imagefilter.Source, string) (imagefilter.Result, error) {
		panic("simulated native adapter panic")
	}}
	store := &testStore{rows: []Item{{ID: 1}}}
	service := testService(t, store, detector)
	startTest(t, service)
	state := waitStatus(t, service, Paused)
	if state.Cursor != 0 || state.Succeeded != 0 {
		t.Fatal("panic advanced durable progress", state)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	assertShutdownStore(t, store)
}
