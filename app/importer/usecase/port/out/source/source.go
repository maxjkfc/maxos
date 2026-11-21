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
	// model.AssignKeys). A row that cannot be read, which is a malformed file and not
	// a row that cannot be recorded, is an error that names its line; so is a header
	// that lacks a column.
	Rows(ctx context.Context, path string) ([]model.Row, error)
}
