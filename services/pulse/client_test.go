package pulse

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/viper"
)

const testSecret = "pulse-test-signing-key-at-least-32-bytes"

func testConfig(endpoint string) Config {
	v := viper.New()
	v.Set("pulse.server.url", endpoint)
	v.Set("pulse.server.secret", testSecret)
	v.Set("pulse.server.audience", "pulse")
	return Configuration(v)
}

func testClient(t *testing.T, server *httptest.Server, config Config) *Client {
	t.Helper()
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	client.http.SetTLSClientConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})
	t.Cleanup(client.Close)
	return client
}

func TestReportPayloadAndJWT(t *testing.T) {
	t.Parallel()
	delivered := make(chan event, 2)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assertAuthorization(t, r)
		delivered <- readEvent(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client := testClient(t, server, testConfig(server.URL))
	admitted := time.Date(2026, time.November, 10, 23, 59, 59, 0, time.UTC)
	for _, name := range []string{ImageFilter, ImageOCR} {
		if err := client.Report(t.Context(), name, admitted, 123*time.Millisecond+456*time.Microsecond); err != nil {
			t.Fatal(err)
		}
	}
	if len(delivered) != 2 {
		t.Fatalf("received %d events", len(delivered))
	}
	events := []event{<-delivered, <-delivered}
	for index, body := range events {
		assertEvent(t, body, []string{ImageFilter, ImageOCR}[index])
	}
	if events[0].RequestID == events[1].RequestID {
		t.Error("request IDs must be unique")
	}
}

func readEvent(t *testing.T, r *http.Request) event {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		t.Error(err)
	}
	var body event
	if err = json.Unmarshal(raw, &body); err != nil {
		t.Error(err)
	}
	if !strings.Contains(string(raw), `"tokens":`) {
		t.Error("tokens must be included in the payload")
	}
	return body
}

func assertEvent(t *testing.T, body event, name string) {
	t.Helper()
	want := event{
		TenantID: 1, AppID: 3, PlatformID: 1, RequestID: body.RequestID,
		EventName: name, ExternalUserID: externalUserID, ClientSessionID: "sid-20261111070000", Tokens: 123,
	}
	if body != want {
		t.Errorf("incorrect event: %+v", body)
	}
	if _, err := uuid.Parse(body.RequestID); err != nil {
		t.Error(err)
	}
}

func assertAuthorization(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("incorrect method/content type: %s %s", r.Method, r.Header.Get("Content-Type"))
	}
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), claims,
		func(_ *jwt.Token) (any, error) { return []byte(testSecret), nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithAudience("pulse"), jwt.WithIssuer("inferkit-server"), jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		t.Errorf("invalid JWT: %v", err)
		return
	}
	if claims.IssuedAt == nil || claims.ExpiresAt == nil || claims.ExpiresAt.Sub(claims.IssuedAt.Time) != 5*time.Minute {
		t.Error("JWT must include iat and the configured lifetime")
	}
}

func TestReportStatusAndRedirect(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusTemporaryRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var calls atomic.Int64
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Location", r.URL.String()+"/redirect")
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			client := testClient(t, server, testConfig(server.URL))
			err := client.Report(t.Context(), ImageFilter, time.Now(), 0)
			if !errors.Is(err, ErrReport) || calls.Load() != 1 {
				t.Fatalf("expected one failed delivery, got %d: %v", calls.Load(), err)
			}
			if status != http.StatusTemporaryRedirect {
				assertStatusError(t, err, status)
			}
		})
	}
}

func assertStatusError(t *testing.T, err error, status int) {
	t.Helper()
	statusErr, ok := errors.AsType[*HTTPStatusError](err)
	if !ok || statusErr.StatusCode != status {
		t.Fatalf("status error not preserved: %v", err)
	}
}

func TestReportCancellationAndTimeout(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	config := testConfig(server.URL)
	config.Timeout = 50 * time.Millisecond
	client := testClient(t, server, config)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := client.Report(ctx, ImageOCR, time.Now(), 0); !errors.Is(err, context.Canceled) || !errors.Is(err, ErrReport) {
		t.Fatalf("cancellation lost: %v", err)
	}
	started := time.Now()
	if err := client.Report(t.Context(), ImageOCR, time.Now(), 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout lost: %v", err)
	}
	if time.Since(started) > time.Second {
		t.Error("reporting exceeded its timeout budget")
	}
}

func TestReportBoundsAndDisabledClient(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, strings.Repeat("x", 256)); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	config := testConfig(server.URL)
	config.MaxResponseBytes = 128
	client := testClient(t, server, config)
	if err := client.Report(t.Context(), ImageFilter, time.Now(), 0); !errors.Is(err, ErrReport) {
		t.Fatalf("oversized response accepted: %v", err)
	}
	if err := client.Report(t.Context(), "unsupported", time.Now(), 0); !errors.Is(err, ErrEvent) {
		t.Fatalf("unsupported event accepted: %v", err)
	}
	var disabled *Client
	disabled.Close()
	if err := disabled.Report(t.Context(), ImageOCR, time.Now(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestReportIntegerMilliseconds(t *testing.T) {
	t.Parallel()
	delivered := make(chan event, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		delivered <- readEvent(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	client := testClient(t, server, testConfig(server.URL))
	tests := []struct {
		elapsed time.Duration
		want    int64
	}{
		{0, 0},
		{time.Nanosecond, 0},
		{time.Millisecond + 999*time.Microsecond, 1},
		{1234 * time.Millisecond, 1234},
		{-time.Millisecond, 0},
	}
	for _, test := range tests {
		if err := client.Report(t.Context(), ImageOCR, time.Now(), test.elapsed); err != nil {
			t.Fatal(err)
		}
		if body := <-delivered; body.Tokens != test.want {
			t.Errorf("elapsed %s: tokens=%d, want %d", test.elapsed, body.Tokens, test.want)
		}
	}
}
