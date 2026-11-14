package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/services/reclassify"
)

type imageReclassificationWriter struct {
	database   *gorm.DB
	connection *sql.Conn
	lockName   string
}

// AcquireReclassification reserves one shared-pool session for a MySQL named
// lock. It is not a transaction and holds no row locks during downloads/inference.
func (s *ImageStore) AcquireReclassification(ctx context.Context) (reclassify.Writer, error) {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	pool, err := s.database.DB()
	if err != nil {
		return nil, errors.Join(reclassify.ErrStorage, err)
	}
	if maximum := pool.Stats().MaxOpenConnections; maximum > 0 && maximum < 2 {
		return nil, reclassify.ErrUnavailable // Leave capacity for foreground traffic.
	}
	connection, err := pool.Conn(ctx)
	if err != nil {
		return nil, errors.Join(reclassify.ErrStorage, err)
	}
	// Initialized creates a private Statement before changing ConnPool. The
	// shared application's GORM handle and request contexts are never retained.
	database := s.database.Session(&gorm.Session{NewDB: true, Initialized: true, SkipDefaultTransaction: true})
	database.Statement.ConnPool = connection
	w := &imageReclassificationWriter{database: database, connection: connection}
	if err = w.acquire(ctx); err != nil {
		return nil, errors.Join(err, w.discard())
	}
	return w, nil
}

func (w *imageReclassificationWriter) acquire(ctx context.Context) error {
	var databaseName string
	if err := w.database.WithContext(ctx).Raw("SELECT DATABASE()").Scan(&databaseName).Error; err != nil {
		return errors.Join(reclassify.ErrStorage, err)
	}
	table := w.database.NamingStrategy.TableName("ImagePrediction")
	digest := sha256.Sum256([]byte(databaseName + "/" + table))
	w.lockName = "inferkit_reclassify_" + hex.EncodeToString(digest[:16])
	var acquired sql.NullInt64
	if err := w.database.WithContext(ctx).Raw("SELECT GET_LOCK(?, 0)", w.lockName).Scan(&acquired).Error; err != nil {
		return errors.Join(reclassify.ErrStorage, err)
	}
	if !acquired.Valid || acquired.Int64 != 1 {
		return reclassify.ErrBusy
	}
	return nil
}

func (w *imageReclassificationWriter) Snapshot(ctx context.Context) (reclassify.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	var snapshot reclassify.Snapshot
	err := w.database.WithContext(ctx).Model(&ImagePrediction{}).
		Select("COALESCE(MAX(id), 0) AS max_id, COUNT(*) AS total").Scan(&snapshot).Error
	if err != nil {
		return reclassify.Snapshot{}, errors.Join(reclassify.ErrStorage, err)
	}
	return snapshot, nil
}

func (w *imageReclassificationWriter) Next(ctx context.Context, after, ceiling uint, limit int) ([]reclassify.Item, error) {
	if limit < 1 || limit > reclassify.MaxBatch || after > ceiling {
		return nil, ErrImageQuery
	}
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	items := make([]reclassify.Item, 0, limit)
	err := w.database.WithContext(ctx).Model(&ImagePrediction{}).
		Select("id", "url", "sha256", "url_sha256", "model_sha256").
		Where("id > ? AND id <= ?", after, ceiling).Order("id ASC").Limit(limit).Scan(&items).Error
	if err != nil {
		return nil, errors.Join(reclassify.ErrStorage, err)
	}
	return items, nil
}

func (w *imageReclassificationWriter) Verify(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	var owned sql.NullBool
	err := w.database.WithContext(ctx).Raw("SELECT IS_USED_LOCK(?) = CONNECTION_ID()", w.lockName).Scan(&owned).Error
	if err != nil {
		return errors.Join(reclassify.ErrStorage, err)
	}
	if !owned.Valid || !owned.Bool {
		return reclassify.ErrBusy
	}
	return nil
}

// Apply changes only model-owned result fields. Keeping the original identity
// avoids unique-key collisions between historical model versions; the job's
// durable checkpoint records which model produced these replacement labels.
func (w *imageReclassificationWriter) Apply(ctx context.Context, item reclassify.Item, result imagefilter.Result) (bool, error) {
	if result.SHA256 != item.SHA256 || result.Label == "" || len(result.Label) > 128 || !validImageConfidence(float64(result.Confidence)) {
		return false, ErrImageQuery
	}
	ctx, cancel := context.WithTimeout(ctx, imageDatabaseTimeout)
	defer cancel()
	query := w.target(ctx, item).
		Updates(map[string]any{"label": result.Label, "confidence": float64(result.Confidence), "updated_at": time.Now().UTC()})
	if query.Error != nil {
		return false, errors.Join(reclassify.ErrStorage, query.Error)
	}
	if query.RowsAffected > 0 {
		return true, nil
	}
	return w.replayed(ctx, item, result)
}

func (w *imageReclassificationWriter) replayed(ctx context.Context, item reclassify.Item, result imagefilter.Result) (bool, error) {
	if err := w.Verify(ctx); err != nil {
		return false, err
	}
	// MySQL may report zero changed rows for an already applied replay.
	var count int64
	err := w.target(ctx, item).Where("label = ? AND confidence = ?", result.Label, float64(result.Confidence)).Count(&count).Error
	if err != nil {
		return false, errors.Join(reclassify.ErrStorage, err)
	}
	return count > 0, nil
}

func (w *imageReclassificationWriter) target(ctx context.Context, item reclassify.Item) *gorm.DB {
	return w.database.WithContext(ctx).Model(&ImagePrediction{}).
		Where("id = ? AND url_sha256 = ? AND sha256 = ? AND model_sha256 = ?", item.ID, item.URLSHA256, item.SHA256, item.ModelSHA256).
		Where("IS_USED_LOCK(?) = CONNECTION_ID()", w.lockName)
}

func (w *imageReclassificationWriter) Close(ctx context.Context) error {
	var released sql.NullInt64
	err := w.database.WithContext(ctx).Raw("SELECT RELEASE_LOCK(?)", w.lockName).Scan(&released).Error
	if err != nil {
		// A failed release must not return a possibly locked session to the pool.
		return errors.Join(reclassify.ErrStorage, err, w.discard())
	}
	closeErr := w.connection.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	if err = errors.Join(err, closeErr); err != nil {
		return errors.Join(reclassify.ErrStorage, err)
	}
	return nil
}

// Discard uncertain ownership rather than returning a locked session to a pool.
func (w *imageReclassificationWriter) discard() error {
	err := w.connection.Raw(func(any) error { return driver.ErrBadConn })
	if errors.Is(err, driver.ErrBadConn) || errors.Is(err, sql.ErrConnDone) {
		err = nil
	}
	closeErr := w.connection.Close()
	if errors.Is(closeErr, sql.ErrConnDone) {
		closeErr = nil
	}
	if err = errors.Join(err, closeErr); err != nil {
		return errors.Join(reclassify.ErrStorage, err)
	}
	return nil
}
