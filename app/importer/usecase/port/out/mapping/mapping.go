// Package mapping declares where the mapping of an import is kept.
package mapping

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// Loaded is a mapping as it was read.
type Loaded struct {
	Mapping model.Mapping
	// Exists is false when there was no file: the mapping is then empty.
	Exists bool
	// Guessed is how many of its entries are still marked "# guessed", which the user
	// has not looked at yet.
	Guessed int
	// Warnings are what is odd in the file and stops nothing: a key that is no longer
	// used, which is ignored and left in the file.
	Warnings []model.Warning
}

// Store keeps the mapping.
type Store interface {
	// Load reads the mapping at path. A file that does not exist is an empty mapping,
	// not an error; a file that cannot be read, or read as a mapping, is a
	// *filefailure.Failure that names the file and says what is wrong with it and
	// where.
	Load(ctx context.Context, path string) (Loaded, error)
	// Append adds the entries of guessed to the file at path, each marked "# guessed",
	// and leaves everything that is in the file alone, comments and the order of its
	// entries included. It creates the file, with a comment that says what it is, when
	// there is none. An entry the file has is not added again. What it cannot do, a
	// file that cannot be read or written, or entries that cannot be added to it as it
	// is laid out, is a *filefailure.Failure, and nothing is written.
	Append(ctx context.Context, path string, guessed model.Mapping) error
}
