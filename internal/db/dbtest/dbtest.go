// Package dbtest provides database fixtures for integration tests.
package dbtest

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/internal/db"
)

var (
	once    sync.Once
	pool    *pgxpool.Pool
	initErr error
)

// Tx returns a transaction that is rolled back when the test ends. The pool is
// shared and migrated once per process; it is intentionally never closed.
func Tx(t *testing.T) pgx.Tx {
	t.Helper()

	url := os.Getenv("MAXOS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("MAXOS_TEST_DATABASE_URL is not set; skipping database test (see `make check`)")
	}

	once.Do(func() {
		ctx := context.Background()
		pool, initErr = db.Open(ctx, url)
		if initErr == nil {
			initErr = db.Migrate(ctx, pool)
		}
	})
	if initErr != nil {
		t.Fatalf("prepare test database: %v", initErr)
	}

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin test transaction: %v", err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Errorf("rollback test transaction: %v", err)
		}
	})
	return tx
}
