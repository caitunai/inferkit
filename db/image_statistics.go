package db

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/caitun/inferkit/services/imagereview"
)

// ImageClassCount counts distinct annotated URLs for one stored human label.
type ImageClassCount struct {
	Label string `json:"label"`
	Count uint64 `json:"count"`
}

// ImageStatistics is a global snapshot independent of list filters and pages.
type ImageStatistics struct {
	GeneratedAt time.Time         `json:"generated_at"`
	Classes     []ImageClassCount `json:"classes"`
	Total       uint64            `json:"total"`
}

type imageStatisticsRow struct {
	Label sql.NullString `gorm:"column:label"`
	Count uint64         `gorm:"column:count"`
}

func (s ImageStatistics) clone() ImageStatistics {
	s.Classes = slices.Clone(s.Classes)
	return s
}

// Statistics shares one process-local snapshot; no Redis clients or workers are used.
func (s *ImageStore) Statistics(ctx context.Context) (ImageStatistics, error) {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	return s.statistics.get(ctx, s.loadStatistics)
}

func (s *ImageStore) loadStatistics(ctx context.Context) (ImageStatistics, error) {
	labels := imagereview.FilterLabels()
	rows := make([]imageStatisticsRow, 0, len(labels)+1)
	// The (correction,url_sha256) index covers this entire query. ROLLUP
	// calculates a globally distinct total even if historical labels conflict.
	err := s.statisticsQuery(ctx, labels).Scan(&rows).Error
	if err != nil {
		return ImageStatistics{}, errors.Join(ErrImageStorage, err)
	}
	return buildImageStatistics(labels, rows), nil
}

func (s *ImageStore) statisticsQuery(ctx context.Context, labels []string) *gorm.DB {
	return s.database.WithContext(ctx).Model(&ImagePrediction{}).
		Select("correction AS label, COUNT(DISTINCT url_sha256) AS count").
		Where("correction IN ?", labels).Group("correction WITH ROLLUP")
}

func buildImageStatistics(labels []string, rows []imageStatisticsRow) ImageStatistics {
	result := ImageStatistics{GeneratedAt: time.Now().UTC(), Classes: make([]ImageClassCount, 0, len(labels))}
	counts := make(map[string]uint64, len(labels))
	for _, row := range rows {
		if !row.Label.Valid {
			result.Total = row.Count
			continue
		}
		counts[row.Label.String] = row.Count
	}
	for _, label := range labels {
		result.Classes = append(result.Classes, ImageClassCount{Label: label, Count: counts[label]})
	}
	return result
}
