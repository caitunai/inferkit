package db

import (
	"context"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/caitun/inferkit/services/imagereview"
)

// ImageQuery supports keyset pagination and bounded direct page offsets.
type ImageQuery struct {
	Label           string  `form:"label"`
	Correction      string  `form:"correction"`
	Review          string  `form:"review"`
	Sort            string  `form:"sort"`
	MinConfidence   float64 `form:"min_confidence"`
	MaxConfidence   float64 `form:"max_confidence"`
	AfterConfidence float64 `form:"after_confidence"`
	AfterID         uint    `form:"after_id"`
	Limit           int     `form:"limit"`
	Offset          int     `form:"offset"`
}

// ImagePage returns a bounded page without a full-history COUNT query.
type ImagePage struct {
	Items   []ImagePrediction `json:"items"`
	HasMore bool              `json:"has_more"`
}

type imageOrder struct {
	column string
	desc   bool
}

const (
	imageConfidenceColumn = "confidence"
	// MaxImageOffset bounds direct page jumps; keyset browsing is not capped.
	MaxImageOffset = 20_000
)

var imageOrders = map[string]imageOrder{
	"newest": {column: "id", desc: true}, "oldest": {column: "id"},
	"confidence_desc": {column: imageConfidenceColumn, desc: true}, "confidence_asc": {column: imageConfidenceColumn},
}

// DefaultImageQuery includes all predictions, ordered newest first.
func DefaultImageQuery() ImageQuery {
	return ImageQuery{MaxConfidence: 1, Sort: "newest", Review: "all", Limit: 24}
}

// Validate rejects invalid numeric bounds and non-allowlisted sort expressions.
func (q ImageQuery) Validate() error {
	if !validImageConfidence(q.MinConfidence) || !validImageConfidence(q.MaxConfidence) || q.MinConfidence > q.MaxConfidence {
		return ErrImageQuery
	}
	if !q.validPage() || !q.validOffset() {
		return ErrImageQuery
	}
	if _, ok := imageOrders[q.Sort]; !ok {
		return ErrImageQuery
	}
	switch q.Review {
	case "all", "pending", "reviewed":
		return nil
	default:
		return ErrImageQuery
	}
}

func (q ImageQuery) validPage() bool {
	return validImageConfidence(q.AfterConfidence) && q.Limit >= 1 && q.Limit <= 100 && q.validLabels()
}

func (q ImageQuery) validLabels() bool {
	return len(q.Label) <= 128 && (q.Correction == "" || imagereview.ValidLabel(q.Correction))
}

func (q ImageQuery) validOffset() bool {
	return q.Offset >= 0 && q.Offset <= MaxImageOffset && (q.Offset == 0 || q.AfterID == 0)
}

// List uses a stable (confidence,id) or id order, including deterministic ties.
func (s *ImageStore) List(ctx context.Context, q ImageQuery) (ImagePage, error) {
	if err := q.Validate(); err != nil {
		return ImagePage{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	items := make([]ImagePrediction, 0, q.Limit+1)
	if err := s.query(ctx, q).Offset(q.Offset).Limit(q.Limit + 1).Find(&items).Error; err != nil {
		return ImagePage{}, imageReadError(err)
	}
	hasMore := len(items) > q.Limit
	if hasMore {
		items = items[:q.Limit]
	}
	return ImagePage{Items: items, HasMore: hasMore}, nil
}

func (s *ImageStore) query(ctx context.Context, q ImageQuery) *gorm.DB {
	query := s.database.WithContext(ctx).Model(&ImagePrediction{}).
		Where("confidence >= ? AND confidence <= ?", q.MinConfidence, q.MaxConfidence)
	if q.Label != "" {
		query = query.Where("label = ?", q.Label)
	}
	if q.Correction != "" {
		query = query.Where("correction = ?", q.Correction)
	}
	switch q.Review {
	case "pending":
		query = query.Where("correction = ?", "")
	case "reviewed":
		query = query.Where("correction <> ?", "")
	}
	order := imageOrders[q.Sort]
	query = query.Order(clause.OrderByColumn{Column: clause.Column{Name: order.column}, Desc: order.desc})
	if order.column != "id" {
		query = query.Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}, Desc: order.desc})
	}
	return imageCursor(query, q, order)
}

func imageCursor(query *gorm.DB, q ImageQuery, order imageOrder) *gorm.DB {
	if q.AfterID == 0 {
		return query
	}
	comparison := ">"
	if order.desc {
		comparison = "<"
	}
	if order.column == "id" {
		return query.Where("id "+comparison+" ?", q.AfterID)
	}
	condition := strings.Join([]string{imageConfidenceColumn, comparison, "? OR (confidence = ? AND id", comparison, "?)"}, " ")
	return query.Where("("+condition+")", q.AfterConfidence, q.AfterConfidence, q.AfterID)
}
