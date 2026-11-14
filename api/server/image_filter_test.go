package server

import (
	"bytes"
	"encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/caitun/inferkit/services/apitoken"
	"github.com/caitun/inferkit/services/imagefilter"
)

const imageFilterTestIdentity = "image-filter-test"

func TestImageFilterAuthenticatedAPIIntegration(t *testing.T) {
	dir := os.Getenv("IMAGE_FILTER_TEST_MODEL")
	if dir == "" {
		t.Skip("set IMAGE_FILTER_TEST_MODEL to run native HTTP integration")
	}
	settings := viper.New()
	settings.Set("image.filter.model", dir)
	settings.Set("image.filter.library", os.Getenv("IMAGE_FILTER_TEST_LIBRARY"))
	filter, err := imagefilter.New(imagefilter.Configuration(settings))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := filter.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	token := imageFilterAPIToken(t)
	s := NewServer("", gin.TestMode)
	s.ImageFilter = filter
	router := s.newRouter()
	assertImageFilterPage(t, router)
	assertBackendAuthorization(t, router)
	request := imageFilterUpload(t, dir)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertClassificationResponse(t, response)
	assertImageFilterFormats(t, router, token)
}

func assertClassificationResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("classification: %d %s", response.Code, response.Body)
	}
	var body struct {
		Data struct {
			Result imagefilter.Result `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	r := body.Data.Result
	if r.ClassID < 0 || r.ClassID >= len(r.Labels) || len(r.ClassMapping) != len(r.Labels) {
		t.Fatal("incomplete classification metadata")
	}
	c := r.ClassMapping[r.ClassID]
	if r.Label != r.Labels[r.ClassID] || r.LabelID != c.LabelID || r.ClassName != c.Name {
		t.Fatal("output index and business label metadata diverged")
	}
}

func assertBackendAuthorization(t *testing.T, router http.Handler) {
	t.Helper()
	for _, path := range imageBackendPaths {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil))
		if response.Code != http.StatusForbidden {
			t.Fatalf("unauthenticated backend %s: %d", path, response.Code)
		}
	}
}

var imageBackendPaths = [...]string{"/api/image/filter", "/api/image/filter/libvips", "/api/image/filter/go"}

func assertImageFilterFormats(t *testing.T, router http.Handler, token string) {
	t.Helper()
	root, err := os.OpenRoot("../../services/imagefilter/testdata")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	for _, format := range []string{"jpeg", "webp", "avif", "png"} {
		data, readErr := root.ReadFile("sample." + format)
		if readErr != nil {
			t.Fatal(readErr)
		}
		// A misleading extension and generic upload content type must not prevent
		// decoding supported formats from their actual bytes.
		for _, path := range imageBackendPaths {
			assertBackendFormat(t, router, token, path, data)
		}
	}
}

func assertBackendFormat(t *testing.T, router http.Handler, token, path string, data []byte) {
	t.Helper()
	request := imageFilterUploadBytes(t, data, "image.bin")
	request.URL.Path = path
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Errorf("%s upload: %d %s", path, response.Code, response.Body)
	}
	if strings.Contains(response.Body.String(), "prediction_sha256") || strings.Contains(response.Body.String(), "PredictionSHA256") {
		t.Fatal("internal record identity leaked into the public response")
	}
}

func assertImageFilterPage(t *testing.T, router http.Handler) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-filter", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "本次检测历史") {
		t.Fatalf("image workbench: %d %s", response.Code, response.Body)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("workbench should not be cached")
	}
}

func imageFilterAPIToken(t *testing.T) string {
	t.Helper()
	settings := map[string]any{"auth.api.secret": "image-filter-integration-test-secret", "auth.api.audience": imageFilterTestIdentity, "auth.api.issuers": []string{imageFilterTestIdentity}}
	for key, value := range settings {
		old := viper.Get(key)
		viper.Set(key, value)
		t.Cleanup(func() { viper.Set(key, old) })
	}
	token, err := apitoken.Issue(apitoken.Settings{
		Secret: []byte(viper.GetString("auth.api.secret")), Audience: imageFilterTestIdentity,
		Issuers: []string{imageFilterTestIdentity}, Lifetime: apitoken.DefaultLifetime,
	}, "test-client", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func imageFilterUpload(t *testing.T, dir string) *http.Request {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "fixtures/000_source.png")) //nolint:gosec // The test operator explicitly selects the local model fixture directory through IMAGE_FILTER_TEST_MODEL.
	if err != nil {
		t.Fatal(err)
	}
	return imageFilterUploadBytes(t, data, "fixture.png")
}

func imageFilterUploadBytes(t *testing.T, data []byte, name string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err = writer.WriteField("url", "https://example.com/original.png"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/image/filter", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}
