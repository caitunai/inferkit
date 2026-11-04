package db

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestImageQueryValidation(t *testing.T) {
	t.Parallel()
	valid := DefaultImageQuery()
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	cases := []ImageQuery{valid, valid, valid, valid, valid, valid, valid}
	cases[0].MinConfidence = math.NaN()
	cases[1].MaxConfidence = math.Inf(1)
	cases[2].MinConfidence, cases[2].MaxConfidence = 0.9, 0.8
	cases[3].Sort = "id; DROP TABLE users"
	cases[4].Limit = 101
	cases[5].Review = "unknown"
	cases[6].AfterConfidence = -1
	for _, query := range cases {
		if err := query.Validate(); !errors.Is(err, ErrImageQuery) {
			t.Fatalf("invalid query accepted: %+v, error=%v", query, err)
		}
	}
}

func TestImageRecordValidation(t *testing.T) {
	t.Parallel()
	store := &ImageStore{modelSHA256: strings.Repeat("b", 64)}
	result := imageTestResult("unit", 0.9)
	row, err := store.newRecord(result)
	if err != nil {
		t.Fatal(err)
	}
	if row.Correction != "" || row.URL != result.URL || row.Label != result.Label || row.Confidence != float64(result.Confidence) {
		t.Fatal("original prediction and annotation fields must remain separate")
	}
	result.SHA256 = "invalid"
	if _, err = store.newRecord(result); !errors.Is(err, ErrImageQuery) {
		t.Fatalf("invalid digest accepted: %v", err)
	}
}

func TestImageFileOnlyNeverTouchesDatabase(t *testing.T) {
	t.Parallel()
	store := new(ImageStore)
	result := imageTestResult(t.Name(), 0.9)
	result.URL = ""
	if err := store.Save(t.Context(), result); err != nil {
		t.Fatal(err)
	}
}

func TestImageAnnotationRejectsUnknownLabelWithoutDatabase(t *testing.T) {
	t.Parallel()
	store := new(ImageStore)
	if err := store.Annotate(t.Context(), 1, 0, "unknown", "reviewer"); !errors.Is(err, ErrImageQuery) {
		t.Fatalf("unsupported annotation accepted: %v", err)
	}
}
