package handler

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caitun/inferkit/services/imagefilter"
)

type imageRecorderFunc func(context.Context, imagefilter.Result) error

var errImageRecordTestStorage = errors.New("test storage unavailable")

func (f imageRecorderFunc) Save(ctx context.Context, result imagefilter.Result) error {
	return f(ctx, result)
}

func TestImageRecordsOnlySuccessfulURLResults(t *testing.T) {
	t.Parallel()
	for _, sourceURL := range []string{"", "https://example.com/record.png"} {
		calls := 0
		checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
			return imagefilter.Result{URL: sourceURL}, nil
		}}
		recorder := imageRecorderFunc(func(_ context.Context, result imagefilter.Result) error {
			calls++
			if result.URL != sourceURL {
				t.Fatal("source URL changed before persistence")
			}
			return nil
		})
		response := serveRecordedFilterRequest(recordedUploadRequest(t, sourceURL), checker, recorder)
		if response.Code != http.StatusOK || (calls == 1) != (sourceURL != "") {
			t.Fatalf("URL=%q status=%d saves=%d", sourceURL, response.Code, calls)
		}
	}
}

func recordedURLRequest(t *testing.T) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image/filter", strings.NewReader(`{"url":"https://example.com/record.png"}`))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestImageRecordFailureDoesNotReturnSuccess(t *testing.T) {
	t.Parallel()
	checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
		return imagefilter.Result{URL: "https://example.com/record.png"}, nil
	}}
	recorder := imageRecorderFunc(func(context.Context, imagefilter.Result) error { return errImageRecordTestStorage })
	response := serveRecordedFilterRequest(recordedURLRequest(t), checker, recorder)
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), `"result"`) {
		t.Fatalf("unpersisted prediction reported success: %d %s", response.Code, response.Body)
	}
}

func TestImageInferenceFailureIsNotRecorded(t *testing.T) {
	t.Parallel()
	checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
		return imagefilter.Result{}, imagefilter.ErrInput
	}}
	recorder := imageRecorderFunc(func(context.Context, imagefilter.Result) error { t.Fatal("failed inference was recorded"); return nil })
	response := serveRecordedFilterRequest(recordedURLRequest(t), checker, recorder)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("failed input: %d", response.Code)
	}
}

func TestUploadedImageWithURLRecordsFileResult(t *testing.T) {
	t.Parallel()
	request := recordedUploadRequest(t, "https://example.com/original.png")
	calls := 0
	checker := imageCheckerStub{check: func(source imagefilter.Source) (imagefilter.Result, error) {
		data, err := io.ReadAll(source.Reader)
		if err != nil || string(data) != "uploaded bytes" || source.URL == "" {
			t.Fatalf("uploaded source was not reused: %q %v", data, err)
		}
		return imagefilter.Result{URL: source.URL, Label: "uploaded-file-result"}, nil
	}}
	recorder := imageRecorderFunc(func(_ context.Context, result imagefilter.Result) error {
		calls++
		if result.Label != "uploaded-file-result" || result.URL != "https://example.com/original.png" {
			t.Fatal("wrong content associated with URL")
		}
		return nil
	})
	response := serveRecordedFilterRequest(request, checker, recorder)
	if response.Code != http.StatusOK || calls != 1 {
		t.Fatalf("file+URL record: %d, saves=%d", response.Code, calls)
	}
}

func recordedUploadRequest(t *testing.T, sourceURL string) *http.Request {
	t.Helper()
	payload := "--image\r\nContent-Disposition: form-data; name=\"file\"; filename=\"sample.png\"\r\nContent-Type: image/png\r\n\r\nuploaded bytes\r\n"
	if sourceURL != "" {
		payload += "--image\r\nContent-Disposition: form-data; name=\"url\"\r\n\r\n" + sourceURL + "\r\n"
	}
	payload += "--image--\r\n"
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image/filter", strings.NewReader(payload))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=image")
	return request
}
