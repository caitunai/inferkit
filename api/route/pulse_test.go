package route

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/pulse"
)

type eventReporterFunc func(context.Context, string, time.Time, time.Duration) error

func (f eventReporterFunc) Enqueue(ctx context.Context, name string, admittedAt time.Time, elapsed time.Duration) error {
	return f(ctx, name, admittedAt, elapsed)
}

func TestImageUsageCountsAuthenticatedAttempts(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			for _, authorized := range []bool{false, true} {
				assertImageUsage(t, status, authorized)
			}
		})
	}
}

func assertImageUsage(t *testing.T, status int, authorized bool) {
	t.Helper()
	calls := 0
	handled := false
	reporter := usageReporter(t, &calls, &handled)
	engine := gin.New()
	router := base.NewRouter(engine)
	api := router.Group("/api", func(c *base.Context) {
		if authorized {
			c.SetAPIUser(&base.APIUser{User: "api-client"})
		}
		c.Next()
	}, apiAuthorized)
	api.POST("/image/ocr", ImageUsage(reporter, pulse.ImageOCR), func(c *base.Context) {
		handled = true
		inferenceCtx, cancel := context.WithCancel(c.Request.Context())
		c.Request = c.Request.WithContext(inferenceCtx)
		cancel()
		c.AbortWithStatus(status)
	})
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/image/ocr", nil))
	if authorized {
		if calls != 1 || response.Code != status {
			t.Fatalf("authenticated request: calls=%d, status=%d", calls, response.Code)
		}
		return
	}
	if calls != 0 || handled || response.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated request reached handler/reporting: calls=%d status=%d", calls, response.Code)
	}
}

func usageReporter(t *testing.T, calls *int, handled *bool) EventReporter {
	t.Helper()
	return eventReporterFunc(func(ctx context.Context, name string, admittedAt time.Time, elapsed time.Duration) error {
		*calls++
		if !*handled || name != pulse.ImageOCR || admittedAt.IsZero() || ctx.Err() != nil || elapsed < 0 {
			t.Error("event must retain the admission time/context and run after the handler")
		}
		return nil
	})
}

func TestImageUsageFailurePreservesResponse(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	logger := zerolog.New(&output)
	ctx := logger.WithContext(t.Context())
	for _, reporter := range []EventReporter{nil, eventReporterFunc(func(context.Context, string, time.Time, time.Duration) error { return pulse.ErrReport })} {
		engine := gin.New()
		router := base.NewRouter(engine)
		router.POST("/filter", ImageUsage(reporter, pulse.ImageFilter), func(c *base.Context) {
			c.Header("X-Result", "preserved")
			c.String(http.StatusOK, "original response")
		})
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodPost, "/filter", nil))
		if response.Code != http.StatusOK || response.Body.String() != "original response" || response.Header().Get("X-Result") != "preserved" {
			t.Fatalf("reporter changed business response: %+v", response)
		}
	}
	if !strings.Contains(output.String(), "Pulse usage reporting failed") {
		t.Fatal("reporting failure must be observable")
	}
}

func TestImageUsageExcludesReportingDuration(t *testing.T) {
	t.Parallel()
	for _, name := range []string{pulse.ImageFilter, pulse.ImageOCR} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				const processingTime = 123*time.Millisecond + 456*time.Microsecond
				const reportingTime = 2 * time.Second
				started := time.Now()
				var measured time.Duration
				reporter := eventReporterFunc(func(_ context.Context, _ string, _ time.Time, elapsed time.Duration) error {
					measured = elapsed
					time.Sleep(reportingTime)
					return nil
				})
				engine := gin.New()
				base.NewRouter(engine).POST("/image", ImageUsage(reporter, name), func(c *base.Context) {
					time.Sleep(processingTime)
					c.AbortWithStatus(http.StatusOK)
				})
				engine.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image", nil))
				if measured != processingTime {
					t.Fatalf("reported duration = %s, want %s", measured, processingTime)
				}
				if time.Since(started) != processingTime+reportingTime {
					t.Fatal("reporting latency was not exercised")
				}
			})
		})
	}
}
