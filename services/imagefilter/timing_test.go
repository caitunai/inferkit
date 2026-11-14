package imagefilter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/services/imagetiming"
)

func TestCachedDetectionTimingWithoutInference(t *testing.T) {
	t.Parallel()
	data := []byte("cached image bytes")
	hash := sha256.Sum256(data)
	key := hex.EncodeToString(hash[:])
	c := Config{Timeout: time.Second, CacheTTL: time.Minute, CacheEntries: 1, MaxBytes: 1024}
	s := &Service{config: c, plan: executionPlan{concurrency: 1, requests: 2, threads: 1}, requests: make(chan struct{}, 2), cache: newResultStore(c)}
	_, pending, _ := s.cache.begin(key)
	s.cache.finish(key, pending, Result{Label: "cached-result", SHA256: key}, nil)
	var buffer bytes.Buffer
	output := zerolog.New(&buffer).With().Str("requestID", "cache-timing-test").Logger()
	result, err := s.Check(output.WithContext(t.Context()), Source{Reader: bytes.NewReader(data)})
	if err != nil || result.Label != "cached-result" {
		t.Fatalf("cache lookup changed: %+v %v", result, err)
	}
	var fields map[string]any
	if err = json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["cache_state"] != "hit" || fields["requestID"] != "cache-timing-test" || fields["image_bytes"] != float64(len(data)) || fields["success"] != true {
		t.Fatalf("cache diagnostics incomplete: %+v", fields)
	}
	for _, name := range []string{"inference_ms", "decode_ms", "slot_wait_ms"} {
		if _, exists := fields[name]; exists {
			t.Fatalf("cache hit reported unexecuted %s", name)
		}
	}
}

func TestSharedTimingPreservesCancellation(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	ctx, trace := imagetiming.New(output.WithContext(t.Context()))
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	_, err := awaitResult(ctx, &pendingCall{done: make(chan struct{})})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
	trace.Finish(ctx, false, 0)
	var fields map[string]any
	if err = json.Unmarshal(buffer.Bytes(), &fields); err != nil {
		t.Fatal(err)
	}
	if fields["failed_stage"] != "cache_wait" || fields["success"] != false {
		t.Fatalf("canceled wait not measured: %+v", fields)
	}
}

func TestRecheckGuardsHistoricalContentBeforeCachedInference(t *testing.T) {
	t.Parallel()
	data := []byte("cached historical image")
	digest := sha256.Sum256(data)
	key := hex.EncodeToString(digest[:])
	config := Config{Timeout: time.Second, CacheTTL: time.Minute, CacheEntries: 1, MaxBytes: 1024}
	service := &Service{config: config, requests: make(chan struct{}, 2), cache: newResultStore(config)}
	_, pending, _ := service.cache.begin(key)
	service.cache.finish(key, pending, Result{Label: "historical-cached-result", SHA256: key}, nil)
	other := sha256.Sum256([]byte("different historical bytes"))
	if _, err := service.Recheck(t.Context(), Source{Reader: bytes.NewReader(data)}, hex.EncodeToString(other[:])); !errors.Is(err, ErrContentChanged) {
		t.Fatal("cached content bypassed source identity", err)
	}
	service.requests <- struct{}{}
	if _, err := service.Recheck(t.Context(), Source{Reader: bytes.NewReader(data)}, key); !errors.Is(err, ErrBusy) {
		t.Fatal("background did not yield", err)
	}
	<-service.requests
}
