package db

import (
	"strings"
	"testing"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/reclassify"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DryRun plus disabled ping performs no dial or query against any server.
func TestReclassificationOnlyUpdatesPredictionsUnderOwnership(t *testing.T) {
	t.Parallel()
	database := imageDryRun(t, logger.Discard)
	var statement string
	if err := database.Callback().Update().After("gorm:update").Register("capture-reclassification", func(tx *gorm.DB) { statement = tx.Statement.SQL.String(); tx.RowsAffected = 1 }); err != nil {
		t.Fatal(err)
	}
	writer := &imageReclassificationWriter{database: database, lockName: "test-lock"}
	item := reclassify.Item{ID: 42, SHA256: strings.Repeat("a", 64), URLSHA256: strings.Repeat("b", 64), ModelSHA256: strings.Repeat("c", 64)}
	applied, err := writer.Apply(t.Context(), item, imagefilter.Result{SHA256: item.SHA256, Label: "10_photo", Confidence: .9})
	if err != nil || !applied {
		t.Fatal(applied, err)
	}
	assertReclassificationSQL(t, statement)
}

func assertReclassificationSQL(t *testing.T, statement string) {
	t.Helper()
	update, where, ok := strings.Cut(statement, " WHERE ")
	if !ok || !strings.Contains(where, "IS_USED_LOCK") || !strings.Contains(where, "CONNECTION_ID()") {
		t.Fatal("unprotected update", statement)
	}
	assertPredictionColumns(t, update)
	for _, column := range []string{"id", "url_sha256", "sha256", "model_sha256"} {
		if !strings.Contains(where, column) {
			t.Fatal("missing identity guard", statement)
		}
	}
}

func assertPredictionColumns(t *testing.T, update string) {
	t.Helper()
	for _, column := range []string{"label", "confidence", "updated_at"} {
		if !strings.Contains(update, "`"+column+"`") {
			t.Fatal("missing prediction field", update)
		}
	}
	for _, column := range []string{"correction", "reviewer", "reviewed_at", "revision", "model_sha256", "sha256", "url"} {
		if strings.Contains(update, "`"+column+"`") {
			t.Fatal("changed human annotation or identity", update)
		}
	}
}
