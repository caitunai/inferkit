package pulse

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/xutil"
)

var ErrTestDelivery = errors.New("test Pulse delivery failure")

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func asyncClient(t *testing.T, transport roundTripFunc) *Client {
	t.Helper()
	config := testConfig("https://pulse.example.com/events")
	config.MaxConnections = 1
	config.Timeout = 10 * time.Second
	config.ShutdownTimeout = time.Second
	client, err := NewContext(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	client.http.SetTransport(transport)
	t.Cleanup(client.Close)
	return client
}

func acceptedResponse(r *http.Request) *http.Response {
	return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: r}
}

func TestEnqueueReturnsBeforeDeliveryAndDetachesRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var delivered event
		client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
			time.Sleep(500 * time.Millisecond)
			if err := r.Context().Err(); err != nil {
				t.Error("HTTP request cancellation reached background telemetry")
			}
			delivered = readEvent(t, r)
			return acceptedResponse(r), nil
		})
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		started := time.Now()
		if err := client.Enqueue(ctx, ImageOCR, started, 123*time.Millisecond); err != nil {
			t.Fatal(err)
		}
		if time.Since(started) != 0 {
			t.Fatal("event submission waited for delivery")
		}
		client.Close()
		assertDrainedReport(t, delivered, started)
	})
}

func assertDrainedReport(t *testing.T, delivered event, started time.Time) {
	t.Helper()
	if delivered.Tokens != 123 || delivered.EventName != ImageOCR {
		t.Fatalf("background event changed: %+v", delivered)
	}
	if time.Since(started) != 500*time.Millisecond {
		t.Fatal("Close did not drain the accepted report")
	}
}

func TestEnqueueBoundsAndAdmissionStates(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
		select {
		case <-release:
			return acceptedResponse(r), nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	})
	if err := client.Enqueue(t.Context(), ImageFilter, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); !errors.Is(err, ErrBusy) {
		t.Fatalf("full reporter should shed load: %v", err)
	}
	close(release)
	client.tasks.Wait()
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); err != nil {
		t.Fatalf("completed report did not release its slot: %v", err)
	}
	client.Close()
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed reporter accepted work: %v", err)
	}
	if err := client.Enqueue(t.Context(), "unsupported", time.Now(), 0); !errors.Is(err, ErrEvent) {
		t.Fatalf("invalid event accepted: %v", err)
	}
	var disabled *Client
	if err := disabled.Enqueue(t.Context(), ImageOCR, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestEnqueuePanicRecoveryReleasesCapacity(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			panic(ErrTestDelivery)
		}
		return acceptedResponse(r), nil
	})
	panics := make(chan error, 1)
	unsubscribe := xutil.OnPanic(reportTaskName, func(_ context.Context, _ string, err error) {
		panics <- err
	})
	t.Cleanup(unsubscribe)
	if err := client.Enqueue(t.Context(), ImageFilter, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	client.tasks.Wait()
	select {
	case err := <-panics:
		if !errors.Is(err, xutil.ErrPanic) || !errors.Is(err, ErrTestDelivery) {
			t.Fatalf("panic cause was lost: %v", err)
		}
	default:
		t.Fatal("task panic was not recovered and reported")
	}
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); err != nil {
		t.Fatalf("panic leaked a concurrency slot: %v", err)
	}
	client.Close()
	if calls.Load() != 2 || len(client.slots) != 0 {
		t.Fatal("reporter did not recover after the panic")
	}
}

func TestEnqueueLogsDeliveryFailure(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := zerolog.New(&output)
	client := asyncClient(t, func(_ *http.Request) (*http.Response, error) {
		return nil, ErrTestDelivery
	})
	if err := client.Enqueue(logger.WithContext(t.Context()), ImageOCR, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	client.Close()
	if !strings.Contains(output.String(), "Pulse usage reporting failed") || !strings.Contains(output.String(), ErrTestDelivery.Error()) {
		t.Fatalf("background failure was not observable: %s", &output)
	}
}

func TestReporterShutdownCancelsSlowDelivery(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		canceled := make(chan error, 1)
		client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
			<-r.Context().Done()
			canceled <- r.Context().Err()
			return nil, r.Context().Err()
		})
		if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		started := time.Now()
		client.Close()
		if time.Since(started) != client.config.ShutdownTimeout || !errors.Is(<-canceled, context.Canceled) {
			t.Fatal("shutdown did not cancel outstanding HTTP work at its deadline")
		}
		if len(client.slots) != 0 {
			t.Fatal("shutdown leaked active reports")
		}
		client.Close()
	})
}

func TestReporterOwnerCancellation(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
		close(started)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
	<-started
	client.cancel()
	if err := client.Enqueue(t.Context(), ImageOCR, time.Now(), 0); !errors.Is(err, ErrClosed) || !errors.Is(err, context.Canceled) {
		t.Fatalf("owner cancellation was not preserved: %v", err)
	}
	client.Close()
}

func TestConcurrentReporterSubmissionAndClose(t *testing.T) {
	t.Parallel()
	client := asyncClient(t, func(r *http.Request) (*http.Response, error) {
		return acceptedResponse(r), nil
	})
	failures := make(chan error, 32)
	group := xutil.WaitGroup("pulse_lifecycle_test")
	for range cap(failures) {
		group.Go(t.Context(), func(ctx context.Context) {
			err := client.Enqueue(ctx, ImageOCR, time.Now(), 0)
			if err != nil && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrClosed) {
				failures <- err
			}
		})
	}
	group.Go(t.Context(), func(_ context.Context) { client.Close() })
	group.Go(t.Context(), func(_ context.Context) { client.Close() })
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(client.slots) != 0 || !errors.Is(client.owner.Err(), context.Canceled) {
		t.Fatal("concurrent shutdown leaked a report or its owner context")
	}
}
