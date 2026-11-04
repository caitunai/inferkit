package imagefilter

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/spf13/viper"
)

func testDownloader(t *testing.T, timeout time.Duration) *Service {
	t.Helper()
	c := Configuration(viper.New())
	c.Timeout = timeout
	c.MaxBytes = 3
	service := &Service{config: c, http: newDownloader(c)}
	t.Cleanup(service.http.GetClient().CloseIdleConnections)
	return service
}

func TestDownloadTimeoutIncludesHeadersAndBody(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"headers", "body"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			server := stalledDownloadServer(t, stage == "body")
			t.Cleanup(server.Close)
			service := testDownloader(t, 100*time.Millisecond)
			assertDownloadTimeout(t, service, server.URL)
		})
	}
}

func stalledDownloadServer(t *testing.T, sendHeaders bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sendHeaders {
			w.WriteHeader(http.StatusOK)
			if err := http.NewResponseController(w).Flush(); err != nil {
				t.Error(err)
				return
			}
		}
		<-r.Context().Done()
	}))
}

func assertDownloadTimeout(t *testing.T, service *Service, url string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, err := service.readSource(ctx, Source{URL: url})
	if !errors.Is(err, ErrDownload) {
		t.Fatalf("download timeout classification: %v", err)
	}
	if timeout, ok := errors.AsType[net.Error](err); !ok || !timeout.Timeout() {
		t.Fatalf("timeout cause lost: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatal("download did not stop within its configured timeout")
	}
}

func TestDownloadPreservesCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service := testDownloader(t, time.Second)
	_, err := service.readSource(ctx, Source{URL: "http://127.0.0.1/canceled"})
	if !errors.Is(err, ErrDownload) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation cause lost: %v", err)
	}
}

func TestDownloadBoundsChunkedBody(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
			return
		}
		if _, err := io.WriteString(w, "abcd"); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	service := testDownloader(t, time.Second)
	if _, err := service.readSource(t.Context(), Source{URL: server.URL}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("chunked download exceeded byte limit: %v", err)
	}
}

func TestDownloadBoundsRedirects(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	t.Cleanup(server.Close)
	service := testDownloader(t, time.Second)
	_, err := service.readSource(t.Context(), Source{URL: server.URL})
	if !errors.Is(err, ErrURL) || !errors.Is(err, ErrDownload) {
		t.Fatalf("redirect loop was not bounded: %v", err)
	}
}
