package db

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"

	"github.com/caitun/inferkit/services/imagefilter"
	"github.com/caitun/inferkit/xutil"
)

const (
	testImageValid   = "1_valid"
	testImageInvalid = "0_invalid"
)

func imageTestResult(name string, confidence float32) imagefilter.Result {
	hash := sha256.Sum256([]byte(name))
	return imagefilter.Result{
		URL: "https://example.invalid/" + name + ".png", SHA256: hex.EncodeToString(hash[:]),
		Label: testImageValid, Labels: []string{testImageInvalid, testImageValid}, Probabilities: []float32{1 - confidence, confidence}, ClassID: 1, Confidence: confidence,
	}
}

func imageTestDatabase(t *testing.T, prefix string, connections int) *gorm.DB {
	t.Helper()
	if os.Getenv("IMAGE_REVIEW_TEST_DATABASE") != "1" {
		t.Skip("set IMAGE_REVIEW_TEST_DATABASE=1 and DB_LOCAL_* for rollback-only MySQL integration tests")
	}
	config := mysqldriver.NewConfig()
	config.User, config.Passwd, config.DBName = os.Getenv("DB_LOCAL_USERNAME"), os.Getenv("DB_LOCAL_PASSWORD"), os.Getenv("DB_LOCAL_NAME")
	config.Net, config.Addr, config.ParseTime = "tcp", net.JoinHostPort(os.Getenv("DB_LOCAL_HOST"), os.Getenv("DB_LOCAL_PORT")), true
	config.Timeout, config.ReadTimeout, config.WriteTimeout = time.Second, imageDatabaseTimeout, imageDatabaseTimeout
	database, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: prefix}})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := database.DB()
	if err != nil {
		t.Fatal(err)
	}
	pool.SetMaxOpenConns(connections)
	t.Cleanup(func() {
		if closeErr := pool.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return database
}

func imageTestStore(t *testing.T) *ImageStore {
	t.Helper()
	database := imageTestDatabase(t, "ifk_review_test_", 1)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)
	if err := database.WithContext(ctx).Exec("CREATE TEMPORARY TABLE ifk_review_test_image_predictions LIKE ifk_image_predictions").Error; err != nil {
		t.Fatal(err)
	}
	migrateImageTestTable(t, database, "ifk_review_test_image_predictions")
	transaction := database.WithContext(ctx).Begin()
	if transaction.Error != nil {
		t.Fatal(transaction.Error)
	}
	t.Cleanup(func() { rollbackImageTest(t, transaction) })
	store := &ImageStore{database: transaction, modelSHA256: strings.Repeat("b", 64)}
	if err := store.Ready(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}

func concurrentImageTestStore(t *testing.T) *ImageStore {
	t.Helper()
	prefix := "ifk_review_" + strings.ToLower(rand.Text()[:10]) + "_"
	database := imageTestDatabase(t, prefix, 2)
	table := clause.Table{Name: prefix + "image_predictions"}
	ctx, cancel := context.WithTimeout(t.Context(), imageDatabaseTimeout)
	defer cancel()
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(t.Context()), imageDatabaseTimeout)
		defer cleanupCancel()
		if err := database.WithContext(cleanupCtx).Exec("DROP TABLE IF EXISTS ?", table).Error; err != nil {
			t.Error(err)
		}
	})
	if err := database.WithContext(ctx).Exec("CREATE TABLE ? LIKE ?", table, clause.Table{Name: "ifk_image_predictions"}).Error; err != nil {
		t.Fatal(err)
	}
	migrateImageTestTable(t, database, table.Name)
	return &ImageStore{database: database, modelSHA256: strings.Repeat("b", 64)}
}

// Apply the pending column removal only to isolated test tables, never application data.
func migrateImageTestTable(t *testing.T, database *gorm.DB, table string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), imageDatabaseTimeout)
	defer cancel()
	// MySQL hides temporary tables from information_schema; inspect the clone's source.
	if !database.WithContext(ctx).Migrator().HasColumn("ifk_image_predictions", "prediction_json") {
		return
	}
	if err := database.WithContext(ctx).Exec("ALTER TABLE ? DROP COLUMN prediction_json", clause.Table{Name: table}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestImagePersistencePreservesAnnotations(t *testing.T) {
	store := imageTestStore(t)
	result := imageTestResult(t.Name(), 0.9)
	saveTestImage(t, store, result)
	page := imagePage(t, store, DefaultImageQuery(), 1)
	record := page.Items[0]
	if err := store.Annotate(t.Context(), record.ID, record.Revision, testImageInvalid, "test-reviewer"); err != nil {
		t.Fatal(err)
	}
	if err := store.Annotate(t.Context(), record.ID, record.Revision, testImageValid, "other-reviewer"); !errors.Is(err, ErrImageConflict) {
		t.Fatalf("stale annotation must conflict: %v", err)
	}
	saveTestImage(t, store, result)
	after, err := store.Get(t.Context(), record.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Label != result.Label || after.Correction != testImageInvalid || after.Revision != 1 || after.Reviewer != "test-reviewer" {
		t.Fatalf("duplicate inference changed original or annotated data: %+v", after)
	}
	query := DefaultImageQuery()
	query.Review = "pending"
	imagePage(t, store, query, 0)
}

func TestImageConfidenceCursorAndFiltering(t *testing.T) {
	store := imageTestStore(t)
	for _, name := range []string{"cursor-a", "cursor-b", "cursor-c"} {
		saveTestImage(t, store, imageTestResult(name, 0.8))
	}
	for _, order := range []string{"confidence_asc", "confidence_desc", "newest", "oldest"} {
		t.Run(order, func(t *testing.T) { assertImageCursor(t, store, order) })
	}
	query := DefaultImageQuery()
	query.MinConfidence = 0.95
	imagePage(t, store, query, 0)
	query = DefaultImageQuery()
	query.Label = testImageInvalid
	imagePage(t, store, query, 0)
}

func rollbackImageTest(t *testing.T, transaction *gorm.DB) {
	t.Helper()
	if err := transaction.Rollback().Error; err != nil && !errors.Is(err, sql.ErrTxDone) {
		t.Error(err)
	}
}

func assertImageCursor(t *testing.T, store *ImageStore, order string) {
	t.Helper()
	query := DefaultImageQuery()
	query.Sort, query.Limit, query.Label = order, 1, testImageValid
	seen := make(map[uint]bool)
	for range 3 {
		page := imagePage(t, store, query, 1)
		item := page.Items[0]
		if seen[item.ID] {
			t.Fatal("tied confidence returned the same item twice")
		}
		seen[item.ID] = true
		query.AfterID, query.AfterConfidence = item.ID, item.Confidence
	}
}

func TestImageRecordIdentityIncludesContentURLAndModel(t *testing.T) {
	store := imageTestStore(t)
	result := imageTestResult(t.Name(), 0.9)
	saveTestImage(t, store, result)
	saveTestImage(t, store, result)
	imagePage(t, store, DefaultImageQuery(), 1)
	result.URL += "?source=other"
	saveTestImage(t, store, result)
	imagePage(t, store, DefaultImageQuery(), 2)
	nextModel := *store
	nextModel.modelSHA256 = strings.Repeat("c", 64)
	saveTestImage(t, &nextModel, result)
	imagePage(t, store, DefaultImageQuery(), 3)
}

func TestConcurrentImageAnnotationsHaveOneWinner(t *testing.T) {
	store := concurrentImageTestStore(t)
	saveTestImage(t, store, imageTestResult(t.Name(), 0.9))
	record := imagePage(t, store, DefaultImageQuery(), 1).Items[0]
	results := make(chan error, 2)
	group := xutil.WaitGroup("image_annotation_test")
	for _, label := range []string{testImageValid, testImageInvalid} {
		group.Go(t.Context(), func(ctx context.Context) {
			results <- store.Annotate(ctx, record.ID, 0, label, "test-reviewer")
		})
	}
	group.Wait()
	close(results)
	winners, conflicts := 0, 0
	for err := range results {
		switch {
		case err == nil:
			winners++
		case errors.Is(err, ErrImageConflict):
			conflicts++
		default:
			t.Fatal(err)
		}
	}
	if winners != 1 || conflicts != 1 {
		t.Fatalf("concurrent annotation results: winners=%d conflicts=%d", winners, conflicts)
	}
}

func saveTestImage(t *testing.T, store *ImageStore, result imagefilter.Result) {
	t.Helper()
	if err := store.Save(t.Context(), result); err != nil {
		t.Fatal(err)
	}
}

func imagePage(t *testing.T, store *ImageStore, query ImageQuery, length int) ImagePage {
	t.Helper()
	page, err := store.List(t.Context(), query)
	if err != nil || len(page.Items) != length {
		t.Fatalf("unexpected image page, want %d items: %+v, %v", length, page, err)
	}
	return page
}
