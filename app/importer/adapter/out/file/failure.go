package file

import (
	"encoding/csv"
	"errors"
	"io/fs"
	"os"
	"strconv"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// What is wrong with a file is a *model.Failure: a code, the file and the line it is
// about, and the values its text fills in. It is never a sentence: the words are the
// command line's, in the user's language, so that none of the libraries' are put in
// front of the user. The one thing told as it is, is the reason the system gives for a
// file that cannot be read or written: that is in Err.

// failAt is a failure about a line of a file, with the values its code has; the line
// is zero for a failure that is about no line. It leaves the file to inFile.
func failAt(code model.FailureCode, line int, args ...string) *model.Failure {
	return &model.Failure{Code: code, Line: line, Args: args}
}

// inFile says that a failure is about the file at path. The parsers read from a reader
// and cannot say which file it is; the stores that open the file can. An error that is
// not a failure, or that already names its file, is returned as it is.
func inFile(path string, err error) error {
	var f *model.Failure
	if !errors.As(err, &f) || f.File != "" {
		return err
	}
	named := *f
	named.File = path
	return &named
}

// unreadable is the failure for a file that cannot be opened or read.
func unreadable(path string, err error) *model.Failure {
	return &model.Failure{Code: model.FailureFileUnreadable, File: path, Err: reasonOf(err)}
}

// notWritten is the failure for a file that cannot be made or written.
func notWritten(path string, err error) *model.Failure {
	return &model.Failure{Code: model.FailureFileNotWritten, File: path, Err: reasonOf(err)}
}

// reasonOf is the reason the system gives in an error of the os package, without the
// operation and the file it begins with: the failure has the file, and its code says
// what was being done.
func reasonOf(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err
	}
	return err
}

// csvFailure is the failure that an error of encoding/csv comes to. A quote that is
// wrong is told by its code, the line it is on and, when the record began on another
// line, that line; the library's own words are left out. An error that is not about
// the text, a file that cannot be read, is told as the reason of the system.
func csvFailure(err error) *model.Failure {
	var parseErr *csv.ParseError
	if !errors.As(err, &parseErr) {
		return &model.Failure{Code: model.FailureFileUnreadable, Err: reasonOf(err)}
	}
	f := failAt(model.FailureCSVSyntax, parseErr.Line)
	switch {
	case errors.Is(parseErr.Err, csv.ErrBareQuote):
		f.Code = model.FailureCSVBareQuote
	case errors.Is(parseErr.Err, csv.ErrQuote):
		f.Code = model.FailureCSVQuote
	}
	if parseErr.StartLine != parseErr.Line {
		f.Args = []string{strconv.Itoa(parseErr.StartLine)}
	}
	return f
}
