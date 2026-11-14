// Package postgres stores the catalog in PostgreSQL. The sqlc-generated code in
// dbgen stays inside this package: nothing generated crosses the repo boundary.
package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// mapError turns what the database reports into the errors the repo ports
// promise: no row is model.ErrNotFound, a unique violation (the name is taken)
// is model.ErrAlreadyExists. Anything else is returned as it is.
func mapError(err error) error {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return model.ErrNotFound
	case pkgpostgres.IsUniqueViolation(err):
		return model.ErrAlreadyExists
	default:
		return err
	}
}
