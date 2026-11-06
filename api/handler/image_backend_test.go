package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/xutil"
)

func TestImageBackendHTTPAdmissionIsShared(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	entered, release := make(chan struct{}, 2), make(chan struct{})
	native := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) {
		entered <- struct{}{}
		<-release
		return imagefilter.Result{}, nil
	}}
	other := imageCheckerStub{check: func(imagefilter.Source) (imagefilter.Result, error) { return imagefilter.Result{}, nil }}
	nativeHandler, goHandler := filterImageBackends(native, other, nil)
	router := gin.New()
	routes := base.NewRouter(router)
	routes.POST("/native", nativeHandler)
	routes.POST("/alias", nativeHandler)
	routes.POST("/go", goHandler)
	group := xutil.WaitGroup("http_image_backends_admission_test")
	unblock := sync.OnceFunc(func() { close(release); group.Wait() })
	t.Cleanup(unblock)
	for _, path := range []string{"/native", "/alias"} {
		group.Go(ctx, func(ctx context.Context) {
			if status := backendRequestStatus(ctx, router, path); status != http.StatusOK {
				t.Errorf("held request failed: %d", status)
			}
		})
	}
	for range 2 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("requests did not enter the shared admission budget")
		}
	}
	if status := backendRequestStatus(ctx, router, "/go"); status != http.StatusTooManyRequests {
		t.Fatalf("backend bypassed HTTP admission: %d", status)
	}
	unblock()
	if status := backendRequestStatus(ctx, router, "/go"); status != http.StatusOK {
		t.Fatalf("admission budget was not returned: %d", status)
	}
}

func backendRequestStatus(ctx context.Context, router http.Handler, path string) int {
	request := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(`{"url":"http://x"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response.Code
}
