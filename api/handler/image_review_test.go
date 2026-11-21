package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/caitun/inferkit/api/base"
	"github.com/caitun/inferkit/db"
	"github.com/caitun/inferkit/services/imagereview"
)

const imageReviewTestUsername = "review-admin"

const imageReviewTestPassword = "test-only-review-password"

type imageReviewStub struct {
	annotationError error
	writes          int
}

func (*imageReviewStub) List(_ context.Context, q db.ImageQuery) (db.ImagePage, error) {
	if err := q.Validate(); err != nil {
		return db.ImagePage{}, errors.Join(db.ErrImageQuery, err)
	}
	return db.ImagePage{Items: []db.ImagePrediction{}}, nil
}

func (*imageReviewStub) Get(context.Context, uint) (*db.ImagePrediction, error) {
	return &db.ImagePrediction{ID: 1}, nil
}

func (s *imageReviewStub) Annotate(_ context.Context, _ uint, _ uint64, _, reviewer string) error {
	if reviewer != imageReviewTestUsername {
		return db.ErrImageQuery
	}
	s.writes++
	return s.annotationError
}

func serveImageReview(request *http.Request, store ImageReviewRepository) *httptest.ResponseRecorder {
	engine := gin.New()
	router := base.NewRouter(engine).Group("/image-review/api")
	router.RouterGroup.Use(gin.BasicAuth(gin.Accounts{imageReviewTestUsername: imageReviewTestPassword}))
	ImageReviewControl(router, store)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	return response
}

func reviewAnnotationRequest(t *testing.T) *http.Request {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/image-review/api/results/1/annotation", strings.NewReader(`{"label":"0_invalid","revision":0}`))
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Requested-With", "XMLHttpRequest")
	return request
}

func TestImageReviewRequiresIndependentAdminAuthentication(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/results", nil)
	request.Header.Set("Authorization", "Bearer third-party-client-token")
	response := serveImageReview(request, &imageReviewStub{})
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("third-party token granted review access: %d", response.Code)
	}
}

func TestImageReviewAnnotationValidationAndConflict(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusOK, http.StatusForbidden, http.StatusConflict} {
		request := reviewAnnotationRequest(t)
		store := new(imageReviewStub)
		if status == http.StatusForbidden {
			request.Header.Set("Origin", "https://untrusted.example")
		}
		if status == http.StatusConflict {
			store.annotationError = db.ErrImageConflict
		}
		response := serveImageReview(request, store)
		if response.Code != status {
			t.Fatalf("annotation status: got %d want %d", response.Code, status)
		}
		if status == http.StatusForbidden && store.writes != 0 {
			t.Fatal("cross-origin request modified an annotation")
		}
	}
}

func TestImageReviewRejectsBadFilters(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/results?min_confidence=0.95&max_confidence=0.8", nil)
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	response := serveImageReview(request, &imageReviewStub{})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("bad probability bounds accepted: %d", response.Code)
	}
}

func TestImageReviewPagesRenderEmbeddedTemplates(t *testing.T) {
	t.Parallel()
	engine := gin.New()
	engine.HTMLRender = base.NewRender()
	router := base.NewRouter(engine)
	router.GET("/image-review", ImageReviewPage(false))
	router.GET("/image-review/review", ImageReviewPage(true))
	for _, mode := range []struct{ path, view string }{{"/image-review", "list"}, {"/image-review/review", "review"}} {
		response := httptest.NewRecorder()
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, mode.path, nil)
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-view="`+mode.view+`"`) {
			t.Fatalf("review page %s: %d %s", mode.path, response.Code, response.Body)
		}
		assertReviewLabelsRendered(t, response.Body.String())
	}
}

func assertReviewLabelsRendered(t *testing.T, body string) {
	t.Helper()
	assertReviewCardLabelsRendered(t, body)
	for _, label := range imagereview.Labels() {
		if !strings.Contains(body, `data-label="`+label+`"`) {
			t.Fatalf("annotation button missing for %s", label)
		}
	}
	if strings.Contains(body, "current.prediction") {
		t.Fatal("review page must not depend on removed prediction JSON")
	}
	if strings.Contains(body, "跳过此图") {
		t.Fatal("adopting a model prediction must not be described as skipping")
	}
}

func assertReviewCardLabelsRendered(t *testing.T, body string) {
	t.Helper()
	_, actions, found := strings.Cut(body, `class="review-card-actions"`)
	if !found {
		t.Fatal("gallery annotation controls missing")
	}
	actions, _, _ = strings.Cut(actions, "</div>")
	for _, label := range imagereview.Labels() {
		if !strings.Contains(actions, `data-label="`+label+`"`) {
			t.Fatalf("gallery must directly expose classification %s", label)
		}
	}
	if !strings.Contains(actions, "采用模型结果") {
		t.Fatal("gallery must expose explicit model adoption")
	}
}

func TestImageReviewOffsetBoundaryAndCursorExclusion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		query  string
		status int
	}{
		{"offset=20000", http.StatusOK},
		{"offset=20001", http.StatusBadRequest},
		{"offset=-1", http.StatusBadRequest},
		{"offset=20000&after_id=1", http.StatusBadRequest},
		{"offset=99999999999999999999999999999", http.StatusBadRequest},
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/results?"+test.query, nil)
		request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
		response := serveImageReview(request, &imageReviewStub{})
		if response.Code != test.status {
			t.Fatalf("%s: got %d want %d", test.query, response.Code, test.status)
		}
	}
}

// Capture binding at the handler boundary; query semantics are verified by db tests.
type correctionFilterStub struct {
	imageReviewStub
	query db.ImageQuery
}

func (s *correctionFilterStub) List(ctx context.Context, q db.ImageQuery) (db.ImagePage, error) {
	s.query = q
	return s.imageReviewStub.List(ctx, q)
}

func TestImageReviewBindsIndependentHumanFilter(t *testing.T) {
	t.Parallel()
	store := new(correctionFilterStub)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/results?label=00_invalid&correction=10_photo&review=reviewed&offset=24", nil)
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	response := serveImageReview(request, store)
	if response.Code != http.StatusOK || store.query.Label != "00_invalid" || store.query.Correction != "10_photo" || store.query.Offset != 24 {
		t.Fatal("independent filters were not bound", response.Code, store.query)
	}
}

func TestImageReviewRejectsUnknownHumanFilter(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/results?correction=unknown", nil)
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	if response := serveImageReview(request, &imageReviewStub{}); response.Code != http.StatusBadRequest {
		t.Fatal("unknown human filter accepted", response.Code)
	}
}

func TestImageReviewRendersAllHumanFilterOptions(t *testing.T) {
	t.Parallel()
	engine := gin.New()
	engine.HTMLRender = base.NewRender()
	router := base.NewRouter(engine)
	router.GET("/image-review", ImageReviewPage(false))
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review", nil))
	_, selector, found := strings.Cut(response.Body.String(), `x-model="filters.correction"`)
	if !found {
		t.Fatal("human annotation selector missing")
	}
	selector, _, _ = strings.Cut(selector, "</select>")
	for _, label := range imagereview.FilterLabels() {
		if !strings.Contains(selector, `value="`+label+`"`) {
			t.Fatal("missing human filter option", label)
		}
	}
}
