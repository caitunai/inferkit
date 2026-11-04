package db

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestImageSQLLoggingRedactsSourceAndReviewer(t *testing.T) {
	t.Parallel()
	pool, err := sql.Open("mysql", "test:test@tcp(127.0.0.1:1)/test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := pool.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	var buffer bytes.Buffer
	output := zerolog.New(&buffer)
	trace := NewLogger()
	trace.LogLevel = logger.Info
	database, err := gorm.Open(mysql.New(mysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{
		DisableAutomaticPing: true, DryRun: true, SkipDefaultTransaction: true, Logger: imageQueryLogger{Interface: trace},
	})
	if err != nil {
		t.Fatal(err)
	}
	record := ImagePrediction{URL: "https://example.com/image?signature=private-value", Reviewer: "private-reviewer"}
	if err = database.WithContext(output.WithContext(t.Context())).Create(&record).Error; err != nil {
		t.Fatal(err)
	}
	logged := buffer.String()
	if !strings.Contains(logged, "INSERT") || strings.Contains(logged, "private-value") || strings.Contains(logged, record.Reviewer) {
		t.Fatalf("SQL trace should contain the statement without image data: %s", logged)
	}
}
