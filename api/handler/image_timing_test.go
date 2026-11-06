package handler

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"testing"

	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/services/imagefilter"
)

func imageLogFields(tb testing.TB, buffer *bytes.Buffer, message string) map[string]any {
	tb.Helper()
	for line := range bytes.SplitSeq(bytes.TrimSpace(buffer.Bytes()), []byte("\n")) {
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			tb.Fatal(err)
		}
		if fields["message"] == message {
			return fields
		}
	}
	tb.Fatalf("missing %s log: %s", message, buffer)
	return nil
}

func TestImageTimingIncludesPersistenceAndRequestID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		failure      error
		state, stage string
		status       int
	}{{nil, "saved", "", http.StatusOK}, {errImageRecordTestStorage, "failed", "persist", http.StatusServiceUnavailable}}
	for _, item := range cases {
		var buffer bytes.Buffer
		output := zerolog.New(&buffer).With().Str("requestID", "timing-test").Logger()
		request := recordedURLRequest(t)
		request = request.WithContext(output.WithContext(request.Context()))
		checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) { return imagefilter.Result{}, nil }}
		recorder := imageRecorderFunc(func(_ context.Context, _ imagefilter.Result) error {
			return item.failure
		})
		response := serveRecordedFilterRequest(request, checker, recorder)
		fields := imageLogFields(t, &buffer, "image filter timing")
		if fields["requestID"] != "timing-test" || fields["status"] != float64(response.Code) || fields["success"] != (item.failure == nil) || response.Code != item.status {
			t.Fatalf("missing correlation or outcome: %+v", fields)
		}
		assertImageDuration(t, fields, "http_parse_ms", "persist_ms", "total_ms")
		if fields["failed_stage"] != item.stage || fields["persist_state"] != item.state {
			t.Fatalf("incorrect persistence outcome: %+v", fields)
		}
		if strings.Contains(buffer.String(), "/record.png") {
			t.Fatal("image URL path leaked")
		}
	}
}

func TestImageTimingForInvalidInput(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	request := recordedURLRequest(t)
	request.Header.Set("Content-Type", "invalid")
	request = request.WithContext(output.WithContext(request.Context()))
	checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
		t.Error("invalid request reached inference")
		return imagefilter.Result{}, nil
	}}
	response := serveFilterRequest(request, checker)
	fields := imageLogFields(t, &buffer, "image filter timing")
	if response.Code != http.StatusBadRequest || fields["failed_stage"] != "http_parse" || fields["success"] != false {
		t.Fatalf("parse failure unmeasured: %+v", fields)
	}
	assertImageDuration(t, fields, "http_parse_ms", "total_ms")
}

func assertImageDuration(tb testing.TB, fields map[string]any, names ...string) {
	tb.Helper()
	for _, name := range names {
		imageDuration(tb, fields, name)
	}
}

func imageDuration(tb testing.TB, fields map[string]any, name string) float64 {
	tb.Helper()
	value, ok := fields[name].(float64)
	if !ok || value < 0 {
		tb.Fatalf("missing duration %s: %+v", name, fields)
	}
	return value
}
