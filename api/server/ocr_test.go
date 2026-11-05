package server

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/ocr"
)

func nativeOCR(t *testing.T) (*ocr.Service, string) {
	t.Helper()
	dir := os.Getenv("OCR_TEST_DIR")
	if dir == "" {
		t.Skip("set OCR_TEST_DIR to run authenticated native OCR HTTP integration")
	}
	c := ocr.Configuration(viper.New())
	c.DetectionModel = filepath.Join(dir, "det.onnx")
	c.RecognitionModel = filepath.Join(dir, "rec.onnx")
	c.Dictionary = filepath.Join(dir, "dict.txt")
	c.Library = os.Getenv("OCR_TEST_LIBRARY")
	service, err := ocr.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := service.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return service, dir
}

func TestOCRAuthenticatedAPIIntegration(t *testing.T) {
	service, dir := nativeOCR(t)
	token := imageFilterAPIToken(t)
	s := NewServer("", gin.TestMode)
	s.OCR = service
	router := s.newRouter()
	assertOCRPage(t, router)
	unauthorized := httptest.NewRecorder()
	router.ServeHTTP(unauthorized, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/image/ocr", nil))
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated OCR: %d", unauthorized.Code)
	}
	for _, format := range []string{"jpeg", "webp", "png", "avif"} {
		data := readOCRFixture(t, "../../services/imagefilter/testdata", "sample."+format)
		assertOCRUpload(t, router, token, data)
	}
	result := assertOCRUpload(t, router, token, readOCRFixture(t, dir, "sample.png"))
	if !strings.Contains(result.Text, "登机牌") {
		t.Fatalf("native OCR text missing: %q", result.Text)
	}
	assertOCRURL(t, router, token, readOCRFixture(t, dir, "sample.png"))
}

func readOCRFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	data, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertOCRUpload(t *testing.T, router http.Handler, token string, data []byte) ocr.Result {
	t.Helper()
	request := imageFilterUploadBytes(t, data, "image.bin")
	request.URL.Path = "/api/image/ocr"
	request.Header.Set("Authorization", "Bearer "+token)
	return assertOCRResponse(t, router, request)
}

func assertOCRResponse(t *testing.T, router http.Handler, request *http.Request) ocr.Result {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("OCR API: %d %s", response.Code, response.Body)
	}
	var body struct {
		Data struct {
			Result ocr.Result `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Data.Result.Width < 1 {
		t.Fatalf("missing image dimensions: %s", response.Body)
	}
	return body.Data.Result
}

func assertOCRURL(t *testing.T, router http.Handler, token string, data []byte) {
	t.Helper()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(data); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(source.Close)
	body, err := json.Marshal(map[string]string{"url": source.URL})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/image/ocr", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	result := assertOCRResponse(t, router, request)
	if !strings.Contains(result.Text, "登机牌") {
		t.Fatalf("URL OCR text missing: %q", result.Text)
	}
}

func assertOCRPage(t *testing.T, router http.Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ocr", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "本次 OCR 历史") {
		t.Fatalf("OCR page: %d %s", response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), `href="/image-filter"`) {
		t.Fatal("uninitialized image filter appears in OCR navigation")
	}
}

func TestOCRPageDisabledWithoutModel(t *testing.T) {
	s := NewServer("", gin.TestMode)
	router := s.newRouter()
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ocr", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled OCR page: %d", response.Code)
	}
}
