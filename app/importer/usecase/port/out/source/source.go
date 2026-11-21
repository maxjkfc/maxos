// Package source declares where the rows of an export come from.
package source

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// Source reads a MOZE export.
type Source interface {
	// Rows reads the export at path: every data row, in the order of the file, each
	// with its position and line, and with the keys that identify it (see
	// model.AssignKeys). A file that is not there or cannot be read, a row that cannot
	// be read, which is a malformed file and not a row that cannot be recorded, and a
	// header that lacks a column are each a *filefailure.Failure: it names the file and,
	// when it is about one, the line.
	Rows(ctx context.Context, path string) ([]model.Row, error)
}
