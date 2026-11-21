package db

import (
	"errors"
	"strings"
	"testing"

	"github.com/caitun/inferkit/services/imagereview"
)

func TestImageCorrectionFilterAcceptsCurrentAndLegacyLabels(t *testing.T) {
	t.Parallel()
	for _, label := range imagereview.FilterLabels() {
		query := DefaultImageQuery()
		query.Correction = label
		if err := query.Validate(); err != nil {
			t.Fatal("supported human label rejected", label, err)
		}
	}
}

func TestImageCorrectionFilterRejectsInvalidLabelsBeforeQuery(t *testing.T) {
	t.Parallel()
	for _, label := range []string{"02_missing", strings.Repeat("a", 129), "' OR 1=1 --"} {
		query := DefaultImageQuery()
		query.Correction = label
		if _, err := new(ImageStore).List(t.Context(), query); !errors.Is(err, ErrImageQuery) {
			t.Fatal("invalid annotation filter reached storage", label, err)
		}
	}
}

func TestImageCorrectionFilterCombinesPredictionAndPagination(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		offset  int
		afterID uint
	}{
		{name: "cursor", afterID: 42},
		{name: "offset", offset: MaxImageOffset},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertImageCorrectionPage(t, test.offset, test.afterID)
		})
	}
}

func assertImageCorrectionPage(t *testing.T, offset int, afterID uint) {
	t.Helper()
	store, statements := captureImageListSQL(t)
	query := DefaultImageQuery()
	query.Label = "00_invalid"
	query.Correction = "10_photo"
	query.Review = "reviewed"
	query.MinConfidence, query.MaxConfidence = .6, .9
	query.Offset, query.AfterID = offset, afterID
	if _, err := store.List(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if len(*statements) != 1 {
		t.Fatal("annotation filtering added extra database work", *statements)
	}
	statement := (*statements)[0]
	assertImageCorrectionSQL(t, statement)
	if strings.Contains(statement, "OFFSET") != (offset > 0) {
		t.Fatal("correction filter changed pagination mode", statement)
	}
}

func assertImageCorrectionSQL(t *testing.T, statement string) {
	t.Helper()
	for _, part := range []string{"label = '00_invalid'", "correction = '10_photo'", "confidence >= 0.6", "confidence <= 0.9", "LIMIT 25"} {
		if !strings.Contains(statement, part) {
			t.Fatal("combined model/human filter missing", statement)
		}
	}
}
