package db

import (
	"database/sql"
	"strings"
	"sync"
	"testing"

	"github.com/caitun/inferkit/services/imagereview"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

const imageStatisticsPhotoLabel = "10_photo"

func TestImageStatisticsCountsDistinctURLsInOneCoveredQuery(t *testing.T) {
	t.Parallel()
	database := imageDryRun(t, logger.Discard)
	statement := database.ToSQL(func(tx *gorm.DB) *gorm.DB {
		store := &ImageStore{database: tx}
		return store.statisticsQuery(t.Context(), imagereview.FilterLabels()).Find(&[]imageStatisticsRow{})
	})
	for _, part := range []string{"COUNT(DISTINCT url_sha256)", "GROUP BY correction WITH ROLLUP", "correction IN"} {
		if !strings.Contains(statement, part) {
			t.Fatal("distinct aggregate missing", statement)
		}
	}
	for _, forbidden := range []string{"SELECT *", "url,", "reviewer", "confidence", "ORDER BY", "FOR UPDATE"} {
		if strings.Contains(statement, forbidden) {
			t.Fatal("statistics must read only covering-index fields", statement)
		}
	}
}

func TestImageStatisticsUsesGlobalDistinctTotalAndIncludesZeroClasses(t *testing.T) {
	t.Parallel()
	rows := []imageStatisticsRow{
		{Label: sql.NullString{String: "00_invalid", Valid: true}, Count: 2},
		{Label: sql.NullString{String: imageStatisticsPhotoLabel, Valid: true}, Count: 3},
		{Count: 4}, // One URL appears in both historical groups; total must not sum them.
	}
	result := buildImageStatistics(imagereview.FilterLabels(), rows)
	if result.Total != 4 || len(result.Classes) != 12 || result.GeneratedAt.IsZero() {
		t.Fatal(result)
	}
	counts := make(map[string]uint64, len(result.Classes))
	for _, group := range result.Classes {
		counts[group.Label] = group.Count
	}
	if counts["00_invalid"] != 2 || counts[imageStatisticsPhotoLabel] != 3 || counts["04_paper"] != 0 {
		t.Fatal(counts)
	}
}

func TestImageStatisticsCoveringIndexMatchesGORM(t *testing.T) {
	t.Parallel()
	table, err := schema.Parse(&ImagePrediction{}, new(sync.Map), schema.NamingStrategy{})
	if err != nil {
		t.Fatal(err)
	}
	index := table.LookIndex("idx_image_prediction_correction_url")
	if index == nil || len(index.Fields) != 2 {
		t.Fatal("covering index missing", index)
	}
	if index.Fields[0].DBName != "correction" || index.Fields[1].DBName != "url_sha256" {
		t.Fatal("index order must cover grouping and URL deduplication", index)
	}
}
