package file_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
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

// failureOf returns the failure that an error is, and stops the test when it is not
// one: what is wrong with a file is told as a code and its values, so that the words
// are the command line's alone.
func failureOf(t *testing.T, err error) *model.Failure {
	t.Helper()
	var f *model.Failure
	if !errors.As(err, &f) {
		t.Fatalf("the error %q is not a *model.Failure", err)
	}
	return f
}

// wantFailure checks what a failure says: its code, the line it is about, which is
// zero for none, and its arguments.
func wantFailure(t *testing.T, f *model.Failure, code model.FailureCode, line int, args ...string) {
	t.Helper()
	if f.Code != code || f.Line != line || !slices.Equal(f.Args, args) {
		t.Errorf("failure %s at line %d with %q, want %s at line %d with %q", f.Code, f.Line, f.Args, code, line, args)
	}
}

// wantFile checks the file a failure is about.
func wantFile(t *testing.T, f *model.Failure, path string) {
	t.Helper()
	if f.File != path {
		t.Errorf("failure %s is about the file %q, want %q", f.Code, f.File, path)
	}
}
