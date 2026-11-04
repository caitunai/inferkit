package db

import (
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestClassifyRecordNotFound(t *testing.T) {
	err := classifyRecordNotFound(gorm.ErrRecordNotFound)
	if !errors.Is(err, ErrRecordNotFound) {
		t.Fatalf("classifyRecordNotFound() error = %v, want ErrRecordNotFound", err)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("classifyRecordNotFound() error = %v, want gorm.ErrRecordNotFound", err)
	}
}
