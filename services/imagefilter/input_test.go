package imagefilter

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/spf13/viper"
)

const testImageBody = "abc"

func TestURLSafety(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{"file:///etc/passwd", "https://user:secret@example.com/image", "https://example.com/#fragment", "/local/path"} {
		if err := validateURL(raw); !errors.Is(err, ErrURL) {
			t.Errorf("accepted %q: %v", raw, err)
		}
	}
	for _, raw := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "64:ff9b::7f00:1", "2002:7f00:1::"} {
		if publicIP(netip.MustParseAddr(raw)) {
			t.Errorf("accepted non-public IP %s", raw)
		}
	}
	if !publicIP(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("rejected public IP")
	}
	if conn, err := publicDial(t.Context(), "tcp", "127.0.0.1:80"); conn != nil || !errors.Is(err, ErrURL) {
		t.Fatalf("loopback dial: %v %v", conn, err)
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
	service := &Service{config: c, http: newDownloader(c)}
	// Replace only the test transport so a local fixture server can stand in for
	// the public network. Production publicDial rejection is tested separately.
	service.http.SetTransport(server.Client().Transport)
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
}

func TestSourceReaderTakesPrecedence(t *testing.T) {
	t.Parallel()
	service := &Service{config: Config{MaxBytes: 3}}
	data, err := service.readSource(t.Context(), Source{Reader: bytes.NewBufferString(testImageBody), URL: "https://127.0.0.1/must-not-fetch"})
	if err != nil || string(data) != testImageBody {
		t.Fatalf("uploaded file must win: %q %v", data, err)
	}
	if _, err = readLimited(bytes.NewBufferString("abcd"), 3); !errors.Is(err, ErrTooLarge) {
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
	c.Concurrency = 0
	if err := c.validate(); !errors.Is(err, ErrConfig) {
		t.Fatalf("concurrency: %v", err)
	}
	v.Set("image.filter.maxBytes", 0)
	if err := Configuration(v).validate(); !errors.Is(err, ErrConfig) {
		t.Fatalf("explicit zero limit: %v", err)
	}
}
