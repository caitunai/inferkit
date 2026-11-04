package storage

import (
	"errors"
	"io/fs"
	"testing"
)

func TestLoadSubFSReturnsClassifiedUnavailableFS(t *testing.T) {
	filesystem := loadSubFS(FS, "../invalid")
	_, err := fs.ReadFile(filesystem, "asset.txt")
	if !errors.Is(err, ErrSubFS) {
		t.Fatalf("ReadFile() error = %v, want ErrSubFS", err)
	}
}
