package db

import (
	"errors"
	"strings"
	"testing"
)

func TestImageStatisticsDeduplicatesModelsAndInvalidatesAfterAnnotation(t *testing.T) {
	store := imageTestStore(t) // Opt-in only; isolated temporary table and rollback.
	source := imageTestResult(t.Name(), .9)
	saveTestImage(t, store, source)
	source.PredictionSHA256 = strings.Repeat("c", 64)
	saveTestImage(t, store, source)
	before, err := store.Statistics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before.Total != 0 {
		t.Fatal("unreviewed URLs counted", before)
	}
	records := imagePage(t, store, DefaultImageQuery(), 2).Items
	if err = store.Annotate(t.Context(), records[0].ID, 0, testImageValid, "test-reviewer"); err != nil {
		t.Fatal(err)
	}
	after, err := store.Statistics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Total != 1 {
		t.Fatal("model versions were counted as separate URLs", after)
	}
	if err = store.Annotate(t.Context(), records[0].ID, 0, testImageInvalid, "stale-reviewer"); !errors.Is(err, ErrImageConflict) {
		t.Fatal(err)
	}
	unchanged, err := store.Statistics(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.GeneratedAt.Equal(after.GeneratedAt) {
		t.Fatal("failed annotation invalidated statistics")
	}
}
