//go:build !sqlite

package sqlstore

import (
	"errors"
	"path/filepath"
	"testing"
)

// Tanpa -tags sqlite, Open harus gagal dengan ErrNoDriver supaya dashboard kembali memakai file.
func TestOpenWithoutDriver(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if !errors.Is(err, ErrNoDriver) {
		t.Fatalf("diharapkan ErrNoDriver, dapat %v", err)
	}
}
