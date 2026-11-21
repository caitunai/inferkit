package db

import (
	"strings"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const imageTestConfidenceAscending = "confidence_asc"

func TestImagePageJumpGeneratesOneBoundedSelect(t *testing.T) {
	t.Parallel()
	store, statements := captureImageListSQL(t)
	for index, sort := range []string{"newest", "oldest", imageTestConfidenceAscending, "confidence_desc"} {
		query := DefaultImageQuery()
		query.Sort = sort
		query.Offset = MaxImageOffset
		query.Label = "08_screenshot"
		query.Review = "pending"
		query.MinConfidence = .6
		if _, err := store.List(t.Context(), query); err != nil {
			t.Fatal(err)
		}
		if len(*statements) != index+1 {
			t.Fatal("each page must execute one SELECT without a COUNT", *statements)
		}
		assertBoundedImagePageSQL(t, (*statements)[index])
	}
}

func assertBoundedImagePageSQL(t *testing.T, statement string) {
	t.Helper()
	for _, expected := range []string{"LIMIT 25 OFFSET 20000", "ORDER BY", "label =", "correction =", "confidence >="} {
		if !strings.Contains(statement, expected) {
			t.Fatal("bounded filtered ordering missing", statement)
		}
	}
	if strings.Contains(statement, "COUNT(") {
		t.Fatal("page jumps must not count all rows", statement)
	}
}

func TestImageCursorDoesNotInheritOffsetAfterJump(t *testing.T) {
	t.Parallel()
	store, statements := captureImageListSQL(t)
	query := DefaultImageQuery()
	query.Sort = imageTestConfidenceAscending
	query.AfterID = 20001
	query.AfterConfidence = .8
	if _, err := store.List(t.Context(), query); err != nil {
		t.Fatal(err)
	}
	if len(*statements) != 1 {
		t.Fatal("expected one keyset SELECT", *statements)
	}
	statement := (*statements)[0]
	if strings.Contains(statement, "OFFSET") || !strings.Contains(statement, "confidence >") || !strings.Contains(statement, "id >") {
		t.Fatal("continued browsing must use the confidence/id cursor", statement)
	}
}

// DryRun plus disabled ping captures actual List SQL without any server access.
func captureImageListSQL(t *testing.T) (*ImageStore, *[]string) {
	t.Helper()
	database := imageDryRun(t, logger.Discard)
	statements := new([]string)
	if err := database.Callback().Query().After("gorm:query").Register("capture-image-page", func(tx *gorm.DB) {
		*statements = append(*statements, tx.Explain(tx.Statement.SQL.String(), tx.Statement.Vars...))
	}); err != nil {
		t.Fatal(err)
	}
	return &ImageStore{database: database}, statements
}
