package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/internal/testpg"
	"github.com/maxjkfc/maxos/pkg/postgres"
)

func TestMain(m *testing.M) {
	os.Exit(testpg.Run(m))
}

// newProbe returns a DB, and the pool under it, on a database of its own with
// an empty table tx_probe. Reading through the pool shows what is committed: a
// transaction's own connection is a different one.
func newProbe(t *testing.T) (*postgres.DB, *pgxpool.Pool) {
	t.Helper()
	pool := testpg.New(t)
	if _, err := pool.Exec(context.Background(), `CREATE TABLE tx_probe (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("create probe table: %v", err)
	}
	return postgres.NewDB(pool), pool
}

func insertProbe(ctx context.Context, db *postgres.DB, id int) error {
	_, err := db.Exec(ctx, `INSERT INTO tx_probe (id) VALUES ($1)`, id)
	return err
}

// committedIDs lists the ids every connection can see.
func committedIDs(t *testing.T, pool *pgxpool.Pool) []int {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT id FROM tx_probe ORDER BY id`)
	if err != nil {
		t.Fatalf("read probe table: %v", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil {
		t.Fatalf("read probe table: %v", err)
	}
	return ids
}

func TestWithinTxCommitsWhatTheFunctionWrote(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		if err := insertProbe(ctx, db, 2); err != nil {
			return err
		}

		if got := committedIDs(t, pool); len(got) != 0 {
			t.Errorf("another connection sees %v before the commit, want nothing", got)
		}
		var inside int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM tx_probe`).Scan(&inside); err != nil {
			return err
		}
		if inside != 2 {
			t.Errorf("the transaction sees %d rows, want its own 2", inside)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("committed = %v, want [1 2]", got)
	}
}

func TestWithinTxRollsBackAndReturnsTheError(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)
	boom := errors.New("business rule failed")

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the function's error", err)
	}
	if got := committedIDs(t, pool); len(got) != 0 {
		t.Fatalf("committed = %v, want nothing", got)
	}
}

func TestWithinTxRollsBackAndRepanics(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	func() {
		defer func() {
			if r := recover(); r != "boom" {
				t.Errorf("recovered %v, want the panic to reach the caller", r)
			}
		}()
		_ = db.WithinTx(ctx, func(ctx context.Context) error {
			if err := insertProbe(ctx, db, 1); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if got := committedIDs(t, pool); len(got) != 0 {
		t.Fatalf("committed = %v, want nothing", got)
	}
	// The connection went back to the pool clean: the pool is still usable.
	if err := insertProbe(ctx, db, 2); err != nil {
		t.Fatalf("insert after the panic: %v", err)
	}
}

func TestStatementsOutsideATransactionCommitOnTheirOwn(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	if err := insertProbe(ctx, db, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if got := committedIDs(t, pool); !slices.Equal(got, []int{1}) {
		t.Fatalf("committed = %v, want [1]", got)
	}
}

func TestNestedWithinTxUndoesOnlyTheInnerWrites(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)
	boom := errors.New("inner failed")

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		inner := db.WithinTx(ctx, func(ctx context.Context) error {
			if err := insertProbe(ctx, db, 2); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(inner, boom) {
			return fmt.Errorf("inner err = %v, want the inner function's error", inner)
		}
		return insertProbe(ctx, db, 3)
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("committed = %v, want [1 3]: the inner write undone, the rest kept", got)
	}
}

func TestNestedWithinTxIsRolledBackWithTheOuterOne(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)
	boom := errors.New("outer failed")

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := db.WithinTx(ctx, func(ctx context.Context) error { return insertProbe(ctx, db, 1) }); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the outer function's error", err)
	}
	if got := committedIDs(t, pool); len(got) != 0 {
		t.Fatalf("committed = %v, want nothing: the inner write was never the inner call's to commit", got)
	}
}

func TestIsUniqueViolation(t *testing.T) {
	ctx := context.Background()
	db, _ := newProbe(t)
	if err := insertProbe(ctx, db, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}

	dup := insertProbe(ctx, db, 1)
	if !postgres.IsUniqueViolation(dup) {
		t.Errorf("IsUniqueViolation(%v) = false, want true", dup)
	}
	if wrapped := fmt.Errorf("insert probe: %w", dup); !postgres.IsUniqueViolation(wrapped) {
		t.Errorf("IsUniqueViolation(%v) = false, want true for a wrapped error", wrapped)
	}

	_, other := db.Exec(ctx, `SELECT FROM no_such_table`)
	if postgres.IsUniqueViolation(other) {
		t.Errorf("IsUniqueViolation(%v) = true, want false", other)
	}
	if postgres.IsUniqueViolation(nil) {
		t.Error("IsUniqueViolation(nil) = true, want false")
	}
}
