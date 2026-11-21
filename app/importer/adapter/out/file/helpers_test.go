package file_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

// Everything the tests of this package read is invented: no real account, merchant,
// person or amount.

// d is a decimal.
func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ptr is a pointer to a decimal.
func ptr(s string) *decimal.Decimal {
	v := d(s)
	return &v
}

// day is a day at midnight UTC, written 2006-01-02.
func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

// writeFile writes the content to a file of the name in the folder, and returns
// its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// readFile returns the text of the file.
func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
