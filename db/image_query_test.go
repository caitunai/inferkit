package db

import (
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/caitun/inferkit/services/imagereview"
	"gorm.io/gorm/schema"
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

func TestMulticlassRecordUsesExistingFields(t *testing.T) {
	t.Parallel()
	store := &ImageStore{modelSHA256: strings.Repeat("b", 64)}
	result := imageTestResult(t.Name(), 0.9)
	result.Labels = imagereview.Labels()
	result.Label, result.ClassID, result.LabelID, result.ClassName = "03_childish", 2, 3, "幼稚"
	row, err := store.newRecord(result)
	if err != nil {
		t.Fatal(err)
	}
	if row.Label != result.Label || row.Confidence != float64(result.Confidence) || row.Correction != "" {
		t.Fatalf("prediction or annotation ownership lost: %+v", row)
	}
}

func TestImagePredictionDoesNotPersistClassMetadata(t *testing.T) {
	t.Parallel()
	table, err := schema.Parse(&ImagePrediction{}, new(sync.Map), schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"class_id", "label_id", "class_name"} {
		if _, exists := table.FieldsByDBName[column]; exists {
			t.Fatal("derived metadata must not become a database column", column)
		}
	}
}

func TestAllNewAndLegacyAnnotationLabels(t *testing.T) {
	t.Parallel()
	for _, label := range imagereview.FilterLabels() {
		if !validImageAnnotation(label, "reviewer") {
			t.Fatal("supported annotation rejected", label)
		}
	}
	if validImageAnnotation("02_missing", "reviewer") {
		t.Fatal("nonexistent business label accepted")
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

func TestImageRecordBackendIdentity(t *testing.T) {
	t.Parallel()
	store := &ImageStore{modelSHA256: strings.Repeat("b", 64)}
	result := imageTestResult(t.Name(), 0.9)
	result.PredictionSHA256 = strings.Repeat("c", 64)
	row, err := store.newRecord(result)
	if err != nil || row.ModelSHA256 != result.PredictionSHA256 {
		t.Fatalf("request backend identity lost: %+v %v", row, err)
	}
	result.PredictionSHA256 = "invalid"
	if _, err = store.newRecord(result); !errors.Is(err, ErrImageQuery) {
		t.Fatal("invalid request identity accepted")
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
