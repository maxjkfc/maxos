// Package rates declares where the daily rates of the foreign currencies are kept.
package rates

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// Store keeps the rates the user has built from a public source.
type Store interface {
	// Load reads the rates at path. A file that does not exist is no rates, not an
	// error: an export of the base currency alone has no need of it, and what needs a
	// rate that is not there is reported by the plan. exists is false for it. A file
	// that cannot be read, or read as rates, is a *filefailure.Failure that names the
	// file and the line.
	Load(ctx context.Context, path string) (r model.Rates, exists bool, err error)
}
