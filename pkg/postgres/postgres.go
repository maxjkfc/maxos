// Package postgres builds pgx connection pools and applies goose migrations.
package postgres

import (
	"context"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// NewPool builds a pool for url. It does not connect: the first query, or a
// Ping, does. The caller owns the pool and must Close it.
func NewPool(ctx context.Context, url string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	return pool, nil
}

// Migrate applies the pending goose migrations found at the root of fsys under
// a Postgres advisory lock, so concurrent callers (e.g. parallel test packages)
// serialise safely.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) (err error) {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer func() {
		if cerr := sqlDB.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close migration connection: %w", cerr)
		}
	}()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("create migration locker: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("create migration provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
