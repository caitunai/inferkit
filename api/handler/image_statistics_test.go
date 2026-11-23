package handler

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/caitun/inferkit/db"
)

type statisticsReviewStub struct {
	statisticsErr error
	imageReviewStub
	value db.ImageStatistics
}

func (s *statisticsReviewStub) Statistics(context.Context) (db.ImageStatistics, error) {
	return s.value, s.statisticsErr
}

func TestImageStatisticsRequiresReviewAdmin(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/statistics", nil)
	request.Header.Set("Authorization", "Bearer third-party-api-token")
	if response := serveImageReview(request, &imageReviewStub{}); response.Code != http.StatusUnauthorized {
		t.Fatal("statistics bypassed administrator authentication", response.Code)
	}
}

func TestImageStatisticsResponsePreservesDistinctCounts(t *testing.T) {
	t.Parallel()
	store := &statisticsReviewStub{value: db.ImageStatistics{Total: 2, Classes: []db.ImageClassCount{{Label: "10_photo", Count: 2}}}}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/image-review/api/statistics", nil)
	request.SetBasicAuth(imageReviewTestUsername, imageReviewTestPassword)
	response := serveImageReview(request, store)
	if response.Code != http.StatusOK {
		t.Fatal(response.Code, response.Body)
	}
	var payload struct {
		Data struct {
			Statistics db.ImageStatistics `json:"statistics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Data.Statistics.Total != 2 || len(payload.Data.Statistics.Classes) != 1 {
		t.Fatal(payload.Data)
	}
	store.statisticsErr = db.ErrImageStorage
	if response = serveImageReview(request, store); response.Code != http.StatusServiceUnavailable {
		t.Fatal("aggregate failure must be isolated", response.Code)
	}
}
