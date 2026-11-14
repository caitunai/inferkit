package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/services/reclassify"
	"github.com/gin-gonic/gin"
)

type reclassificationStub struct {
	err    error
	writes int
}

func (*reclassificationStub) Status() reclassify.View {
	return reclassify.View{Available: true, State: reclassify.State{Status: reclassify.Idle}}
}

func (s *reclassificationStub) Start(context.Context, reclassify.Start) (reclassify.View, error) {
	s.writes++
	return s.Status(), s.err
}

func (s *reclassificationStub) Control(context.Context, string) (reclassify.View, error) {
	s.writes++
	return s.Status(), s.err
}

func serveReclassification(request *http.Request, service ReclassificationController) *httptest.ResponseRecorder {
	engine := gin.New()
	router := base.NewRouter(engine).Group("/image-review/api/reclassification")
	router.RouterGroup.Use(gin.BasicAuth(gin.Accounts{imageReviewTestUsername: imageReviewTestPassword}))
	ImageReclassificationControl(router, service)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func reclassificationRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/image-review/api/reclassification/start", strings.NewReader(body))
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	return request
}

func TestReclassificationRequiresAdminConfirmationAndOrigin(t *testing.T) {
	t.Parallel()
	body := `{"confirm_overwrite":true,"model_sha256":"` + strings.Repeat("a", 64) + `","interval_ms":0}`
	for _, mode := range []string{"accepted", "anonymous", "cross-origin", "no-confirmation", "malformed", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			request, expected := reclassificationSecurityCase(t, mode, body)
			stub := new(reclassificationStub)
			response := serveReclassification(request, stub)
			if response.Code != expected {
				t.Fatalf("got %d want %d: %s", response.Code, expected, response.Body)
			}
			if expected != http.StatusAccepted && stub.writes != 0 {
				t.Fatal("rejected request started work")
			}
		})
	}
}

func TestReclassificationClassifiesUnavailableAndConflicts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err    error
		status int
	}{{reclassify.ErrBusy, http.StatusConflict}, {reclassify.ErrModel, http.StatusConflict}, {reclassify.ErrState, http.StatusConflict}, {reclassify.ErrStorage, http.StatusServiceUnavailable}}
	for _, test := range cases {
		request := reclassificationRequest(t, `{"action":"resume"}`)
		request.URL.Path = "/image-review/api/reclassification/control"
		response := serveReclassification(request, &reclassificationStub{err: test.err})
		if response.Code != test.status {
			t.Fatalf("got %d want %d", response.Code, test.status)
		}
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/reclassification", nil)
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	if response := serveReclassification(request, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatal(response.Code)
	}
}

func reclassificationSecurityCase(t *testing.T, mode, body string) (*http.Request, int) {
	t.Helper()
	request := reclassificationRequest(t, body)
	switch mode {
	case "anonymous":
		request.Header.Del("Authorization")
		return request, http.StatusUnauthorized
	case "cross-origin":
		request.Header.Set("Origin", "https://untrusted.example")
		return request, http.StatusForbidden
	case "no-confirmation":
		return reclassificationRequest(t, `{}`), http.StatusBadRequest
	case "malformed":
		return reclassificationRequest(t, `{"confirm_overwrite":"true"}`), http.StatusBadRequest
	case "oversized":
		return reclassificationRequest(t, strings.Repeat(" ", 5000)), http.StatusBadRequest
	default:
		return request, http.StatusAccepted
	}
}

func TestReclassificationPageRendersEmbeddedEntry(t *testing.T) {
	t.Parallel()
	engine := gin.New()
	engine.HTMLRender = base.NewRender()
	router := base.NewRouter(engine)
	router.GET("/image-review/reclassify", ImageReclassificationPage())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/reclassify", nil))
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body)
	}
	for _, fragment := range []string{"reclassificationApp()", "历史图片重新分类", "保留人工标注", "从断点继续"} {
		if !strings.Contains(response.Body.String(), fragment) {
			t.Fatal("missing embedded maintenance UI", fragment)
		}
	}
}
