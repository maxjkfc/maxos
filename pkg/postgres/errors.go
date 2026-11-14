package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// uniqueViolation is the SQLSTATE of a unique constraint or unique index that
// rejected a row.
const uniqueViolation = "23505"

// IsUniqueViolation reports whether err, or an error it wraps, is PostgreSQL
// rejecting a row because it would duplicate a value that must be unique.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
