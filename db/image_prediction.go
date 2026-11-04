package db

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/imagereview"
)

var (
	// ErrImageStorage identifies prediction persistence failures.
	ErrImageStorage = errors.New("image prediction storage failed")
	// ErrImageQuery identifies invalid filters or annotation input.
	ErrImageQuery = errors.New("invalid image review input")
	// ErrImageNotFound identifies a missing prediction.
	ErrImageNotFound = errors.New("image prediction not found")
	// ErrImageConflict prevents lost annotation updates.
	ErrImageConflict = errors.New("image annotation changed concurrently")
)

const imageDatabaseTimeout = 5 * time.Second

// ImagePrediction preserves immutable inference data and a separate human label.
type ImagePrediction struct {
	CreatedAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP" json:"created_at"`
	UpdatedAt   time.Time `gorm:"type:timestamp;not null;default:CURRENT_TIMESTAMP" json:"updated_at"`
	URL         string    `gorm:"type:text;not null" json:"url"`
	URLSHA256   string    `gorm:"size:64;not null;uniqueIndex:idx_image_prediction_identity" json:"-"`
	SHA256      string    `gorm:"size:64;not null;uniqueIndex:idx_image_prediction_identity" json:"sha256"`
	ModelSHA256 string    `gorm:"size:64;not null;uniqueIndex:idx_image_prediction_identity" json:"model_sha256"`
	Label       string    `gorm:"size:128;not null;index:idx_image_prediction_label_confidence,priority:1" json:"label"`
	Correction  string    `gorm:"size:128;not null;default:'';index:idx_image_prediction_review_confidence,priority:1" json:"correction"`
	Reviewer    string    `gorm:"size:128;not null;default:''" json:"reviewer"`
	ID          uint      `gorm:"type:bigint;primaryKey;autoIncrement" json:"id"`
	Confidence  float64   `gorm:"not null;default:0;index:idx_image_prediction_confidence;index:idx_image_prediction_label_confidence,priority:2;index:idx_image_prediction_review_confidence,priority:2" json:"confidence"`
	Revision    uint64    `gorm:"type:bigint;not null;default:0" json:"revision"`
	ReviewedAt  int64     `gorm:"not null;default:0" json:"reviewed_at"`
}

// ImageStore borrows the application's shared database pool.
type ImageStore struct {
	database    *gorm.DB
	modelSHA256 string
}

// NewImageStore reuses the process-wide pool; the shutdown coordinator owns it.
func NewImageStore(modelSHA256 string) *ImageStore {
	database := Conn()
	return &ImageStore{database: database.Session(&gorm.Session{Logger: imageQueryLogger{Interface: database.Logger}}), modelSHA256: modelSHA256}
}

// Ready fails startup if migrations have not created the prediction table.
func (s *ImageStore) Ready(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	var record ImagePrediction
	if err := s.database.WithContext(ctx).Limit(1).Find(&record).Error; err != nil {
		return errors.Join(ErrImageStorage, err)
	}
	return nil
}

// Save uses one atomic insert, preserving annotations and first-seen predictions on retry.
func (s *ImageStore) Save(ctx context.Context, result imagefilter.Result) error {
	if result.URL == "" {
		return nil
	}
	record, err := s.newRecord(result)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	err = s.database.WithContext(ctx).Session(&gorm.Session{SkipDefaultTransaction: true}).
		Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error
	if err != nil {
		return errors.Join(ErrImageStorage, err)
	}
	return nil
}

func (s *ImageStore) newRecord(result imagefilter.Result) (ImagePrediction, error) {
	if !validImageDigest(s.modelSHA256) || !validImageDigest(result.SHA256) || len(result.URL) > 8192 {
		return ImagePrediction{}, ErrImageQuery
	}
	if result.Label == "" || len(result.Label) > 128 || !validImageConfidence(float64(result.Confidence)) {
		return ImagePrediction{}, ErrImageQuery
	}
	hash := sha256.Sum256([]byte(result.URL))
	return ImagePrediction{
		URL: result.URL, URLSHA256: hex.EncodeToString(hash[:]), SHA256: result.SHA256,
		ModelSHA256: s.modelSHA256, Label: result.Label, Confidence: float64(result.Confidence),
	}, nil
}

func validImageDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validImageConfidence(value float64) bool {
	return !math.IsNaN(value) && value >= 0 && value <= 1
}

// Get loads the stored prediction and its independent human annotation.
func (s *ImageStore) Get(ctx context.Context, id uint) (*ImagePrediction, error) {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	record := new(ImagePrediction)
	if err := s.database.WithContext(ctx).First(record, id).Error; err != nil {
		return nil, imageReadError(err)
	}
	return record, nil
}

func imageReadError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.Join(ErrImageNotFound, err)
	}
	return errors.Join(ErrImageStorage, err)
}

// Annotate changes only human-owned fields, with an optimistic concurrency check.
func (s *ImageStore) Annotate(ctx context.Context, id uint, revision uint64, label, reviewer string) error {
	if !validImageAnnotation(label, reviewer) || revision > math.MaxInt64 {
		return ErrImageQuery
	}
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	result := s.database.WithContext(ctx).Model(&ImagePrediction{}).Where("id = ? AND revision = ?", id, revision).
		Updates(map[string]any{"correction": label, "reviewer": reviewer, "reviewed_at": time.Now().UnixMilli(), "revision": gorm.Expr("revision + 1")})
	if result.Error != nil {
		return errors.Join(ErrImageStorage, result.Error)
	}
	if result.RowsAffected == 0 {
		return ErrImageConflict
	}
	return nil
}

func validImageAnnotation(label, reviewer string) bool {
	return slices.Contains(imagereview.Labels(), label) && reviewer != "" && utf8.ValidString(reviewer) && utf8.RuneCountInString(reviewer) <= 128
}
