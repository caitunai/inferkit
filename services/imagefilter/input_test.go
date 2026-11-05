package imagefilter

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caitun/inferkit/services/imageinput"
	"github.com/spf13/viper"
)

const testImageBody = "abc"

func TestURLSyntax(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.com/image", "https://example.com/#fragment", "/local/path"} {
		if err := imageinput.ValidateURL(raw); !errors.Is(err, ErrURL) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"http://127.0.0.1/image.png", "http://10.1.2.3/a", "https://198.18.0.69/a", "https://example.com/image.png"} {
		if err := imageinput.ValidateURL(raw); err != nil {
			t.Errorf("valid HTTP(S) URL rejected: %s %v", raw, err)
		}
	}
}

func TestDownloadResponseLimits(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		body := testImageBody
		if r.URL.Path == "/large" {
			body = "abcd"
		}
		if _, err := io.WriteString(w, body); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	c := Configuration(viper.New())
	c.MaxBytes = 3
	service := &Service{config: c, http: newDownloader(c, executionPlan{concurrency: 1, requests: 2})}
	t.Cleanup(service.http.GetClient().CloseIdleConnections)
	data, err := service.readSource(t.Context(), Source{URL: server.URL + "/ok"})
	if err != nil || string(data) != testImageBody {
		t.Fatalf("download: %q %v", data, err)
	}
	if _, err = service.readSource(t.Context(), Source{URL: server.URL + "/large"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("download size limit: %v", err)
	}
	if _, err = service.readSource(t.Context(), Source{URL: server.URL + "/missing"}); !errors.Is(err, ErrDownload) {
		t.Fatalf("download status: %v", err)
	}
	assertDownloadStatus(t, err)
}

func assertDownloadStatus(t *testing.T, err error) {
	t.Helper()
	if status, ok := errors.AsType[*DownloadStatusError](err); !ok || status.StatusCode != http.StatusNotFound || status.Host != "127.0.0.1" {
		t.Fatalf("remote HTTP failure details lost: %v", err)
	}
}

func TestSourceReaderTakesPrecedence(t *testing.T) {
	t.Parallel()
	service := &Service{config: Config{MaxBytes: 3}}
	data, err := service.readSource(t.Context(), Source{Reader: bytes.NewBufferString(testImageBody), URL: "https://127.0.0.1/must-not-fetch"})
	if err != nil || string(data) != testImageBody {
		t.Fatalf("uploaded file must win: %q %v", data, err)
	}
	if _, err = imageinput.ReadLimited(bytes.NewBufferString("abcd"), 3, ErrInput); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size limit: %v", err)
	}
}

func TestConfigurationRejectsInvalidLimits(t *testing.T) {
	t.Parallel()
	v := viper.New()
	v.Set("image.filter.model", "/model")
	c := Configuration(v)
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
	c.CacheEntries = -1
	if err := c.validate(); !errors.Is(err, ErrConfig) {
		t.Fatalf("cache entries: %v", err)
	}
	v.Set("image.filter.maxBytes", 0)
	if err := Configuration(v).validate(); !errors.Is(err, ErrConfig) {
		t.Fatalf("explicit zero limit: %v", err)
	}
}
