package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imageinput"
	"github.com/caitun/inferkit/services/ocr"
)

type recognizerStub struct{ err error }

func (recognizerStub) Limits() (int64, time.Duration, int) { return 32, time.Second, 2 }
func (s recognizerStub) Recognize(_ context.Context, _ imageinput.Source) (ocr.Result, error) {
	return ocr.Result{Text: "识别文本", Lines: []ocr.Line{}}, s.err
}

func TestOCRHTTPResponses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		status int
	}{
		{nil, http.StatusOK},
		{ocr.ErrBusy, http.StatusTooManyRequests},
		{ocr.ErrClosed, http.StatusServiceUnavailable},
		{imageinput.ErrInput, http.StatusBadRequest},
		{imageinput.ErrTooLarge, http.StatusRequestEntityTooLarge},
		{errors.Join(ocr.ErrLineRecognition, imageinput.ErrTooLarge), http.StatusRequestEntityTooLarge},
		{imageinput.ErrDownload, http.StatusBadGateway},
		{ocr.ErrInference, http.StatusInternalServerError},
		{errors.Join(ocr.ErrInference, context.Canceled), http.StatusRequestTimeout},
	}
	for _, test := range cases {
		router := gin.New()
		base.NewRouter(router).POST("/ocr", RecognizeImage(recognizerStub{err: test.err}))
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/ocr", strings.NewReader(`{"url":"https://example.com/a.png"}`))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != test.status {
			t.Errorf("%v: %d %s", test.err, response.Code, response.Body)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Error("OCR responses may not be cached")
		}
	}
}

func TestOCRPageTemplateAndNavigation(t *testing.T) {
	t.Parallel()
	router := gin.New()
	router.HTMLRender = base.NewRender()
	r := base.NewRouter(router)
	r.Use(func(c *base.Context) { c.Set("navigation.ocr", true); c.Set("navigation.image_filter", true); c.Next() })
	r.GET("/ocr", OCRPage(recognizerStub{}))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ocr", nil))
	for _, fragment := range []string{"本次 OCR 历史", "刷新或关闭后清空", "ocrApp()", `href="/ocr" aria-current="page"`, `href="/image-filter"`, `data-max-bytes="32"`} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Errorf("OCR page missing %q", fragment)
		}
	}
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("OCR page response: %d", response.Code)
	}
	if strings.Contains(response.Body.String(), `href="/config-center"`) {
		t.Fatal("disabled feature is present in navigation")
	}
}
