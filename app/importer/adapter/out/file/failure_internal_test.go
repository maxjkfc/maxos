package file

import (
	"encoding/csv"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// A parse error of encoding/csv that is not a quote that is wrong cannot come out of
// the reader as it is set up, but when one does, its words are left out like the
// others' and what is left is the line.
func TestCSVFailureOfAnotherParseError(t *testing.T) {
	f := csvFailure(&csv.ParseError{StartLine: 7, Line: 7, Column: 3, Err: csv.ErrFieldCount})
	if f.Code != filefailure.CSVSyntax || f.Line != 7 || len(f.Args) != 0 {
		t.Errorf("failure = %s at line %d with %q, want %s at line 7 with none", f.Code, f.Line, f.Args, filefailure.CSVSyntax)
	}
}

// What cannot be read is the reason of the system, without the operation and the path
// that the failure has of its own.
func TestCSVFailureOfAnErrorThatIsNotAboutTheText(t *testing.T) {
	f := csvFailure(&fs.PathError{Op: "read", Path: "/tmp/export.csv", Err: syscall.EIO})
	if f.Code != filefailure.Unreadable || !errors.Is(f.Err, syscall.EIO) {
		t.Errorf("failure = %s with %v, want %s with the reason EIO", f.Code, f.Err, filefailure.Unreadable)
	}
	var pathErr *fs.PathError
	if errors.As(f.Err, &pathErr) {
		t.Errorf("Err = %v, want the reason without the operation and the path", f.Err)
	}
}

func TestReasonOfTheSystem(t *testing.T) {
	tests := map[string]struct {
		err  error
		want error
	}{
		"a file": {&fs.PathError{Op: "open", Path: "/a", Err: syscall.EACCES}, syscall.EACCES},
		"a link": {&os.LinkError{Op: "rename", Old: "/a", New: "/b", Err: syscall.EXDEV}, syscall.EXDEV},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := reasonOf(tt.err)
			var pathErr *fs.PathError
			var linkErr *os.LinkError
			if !errors.Is(got, tt.want) || errors.As(got, &pathErr) || errors.As(got, &linkErr) {
				t.Errorf("reasonOf(%v) = %v, want the reason %v alone", tt.err, got, tt.want)
			}
		})
	}
	other := errors.New("not from the os package")
	if got := reasonOf(other); !errors.Is(got, other) {
		t.Errorf("reasonOf of an error of another kind = %v, want it as it is", got)
	}
}

// inFile names the file once, on a copy: the failure it was given is not changed.
func TestInFile(t *testing.T) {
	loose := failAt(filefailure.Empty, 0)
	got := inFile("a.csv", loose)
	var named *filefailure.Failure
	if !errors.As(got, &named) || named.File != "a.csv" || named.Code != filefailure.Empty {
		t.Fatalf("inFile = %v, want the failure about a.csv", got)
	}
	if loose.File != "" {
		t.Errorf("the failure that was given was changed: File = %q", loose.File)
	}

	already := &filefailure.Failure{Code: filefailure.Empty, File: "b.csv"}
	if got := inFile("a.csv", already); !errors.As(got, &named) || named.File != "b.csv" {
		t.Errorf("inFile of a failure that has its file = %v, want it to keep b.csv", got)
	}

	plain := errors.New("not a failure")
	if got := inFile("a.csv", plain); !errors.Is(got, plain) {
		t.Errorf("inFile of an error that is not a failure = %v, want it as it is", got)
	}
}
