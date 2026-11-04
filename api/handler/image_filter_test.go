package handler

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
)

type imageCheckerStub struct {
	check func(imagefilter.Source) (imagefilter.Result, error)
}

func (s imageCheckerStub) Limits() (int64, time.Duration, int) { return 32, time.Second, 2 }

func (s imageCheckerStub) Check(_ context.Context, source imagefilter.Source) (imagefilter.Result, error) {
	return s.check(source)
}

func serveFilterRequest(request *http.Request, checker ImageChecker) *httptest.ResponseRecorder {
	r := gin.New()
	base.NewRouter(r).POST("/image/filter", FilterImage(checker))
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	return response
}

func TestImageFilterUploadAndURL(t *testing.T) {
	t.Parallel()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "image.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("uploaded image")); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("url", "https://example.com/image.png"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image/filter", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	checker := imageCheckerStub{check: func(source imagefilter.Source) (imagefilter.Result, error) { return checkPairedUpload(t, source) }}
	response := serveFilterRequest(request, checker)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "1_valid") {
		t.Fatalf("response: %d %s", response.Code, response.Body)
	}
}

func checkPairedUpload(t *testing.T, source imagefilter.Source) (imagefilter.Result, error) {
	t.Helper()
	if source.Reader == nil || source.URL != "https://example.com/image.png" {
		t.Errorf("lost paired source: %+v", source)
		return imagefilter.Result{}, imagefilter.ErrInput
	}
	data, err := io.ReadAll(source.Reader)
	if err != nil || string(data) != "uploaded image" {
		t.Errorf("upload: %q %v", data, err)
	}
	return imagefilter.Result{URL: source.URL, Label: "1_valid"}, nil
}

func TestImageFilterURLBodies(t *testing.T) {
	t.Parallel()
	cases := []struct{ mediaType, body string }{
		{"application/json", `{"url":"https://example.com/image.png"}`},
		{"application/x-www-form-urlencoded", "url=https%3A%2F%2Fexample.com%2Fimage.png"},
	}
	for _, test := range cases {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image/filter", strings.NewReader(test.body))
		request.Header.Set("Content-Type", test.mediaType)
		checker := imageCheckerStub{check: func(source imagefilter.Source) (imagefilter.Result, error) {
			if source.Reader != nil || source.URL != "https://example.com/image.png" {
				t.Errorf("URL source: %+v", source)
			}
			return imagefilter.Result{}, nil
		}}
		if response := serveFilterRequest(request, checker); response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", test.mediaType, response.Code, response.Body)
		}
	}
}

func TestImageFilterRejectsInvalidRequest(t *testing.T) {
	t.Parallel()
	checker := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
		t.Error("invalid body reached inference")
		return imagefilter.Result{}, nil
	}}
	for _, body := range []string{`{"url":"a","url":"b"}`, `{"url":`, strings.Repeat("x", 70<<10)} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image/filter", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := serveFilterRequest(request, checker)
		if response.Code != http.StatusBadRequest && response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("unexpected status %d", response.Code)
		}
	}
}

func TestImageFilterErrorStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		status int
	}{
		{imagefilter.ErrInput, http.StatusBadRequest},
		{imagefilter.ErrURL, http.StatusBadRequest},
		{imagefilter.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{imagefilter.ErrBusy, http.StatusTooManyRequests},
		{imagefilter.ErrDownload, http.StatusBadGateway},
		{imagefilter.ErrClosed, http.StatusServiceUnavailable},
		{errors.Join(imagefilter.ErrInference, context.Canceled), http.StatusRequestTimeout},
		{imagefilter.ErrInference, http.StatusInternalServerError},
	}
	for _, test := range cases {
		status, _ := imageFilterStatus(test.err)
		if status != test.status {
			t.Errorf("%v: status %d, want %d", test.err, status, test.status)
		}
	}
}
