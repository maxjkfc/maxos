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

// The caller of the inner call gives up after the work of the function is done
// and before the call commits. A call that cannot commit has failed, so its
// writes must go, and the outer transaction, whose own context is as it was,
// carries on without them.
func TestNestedWithinTxUndoesItsWritesWhenItsContextIsDoneBeforeTheCommit(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}

		innerCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		inner := db.WithinTx(innerCtx, func(ctx context.Context) error {
			if err := insertProbe(ctx, db, 2); err != nil {
				return err
			}
			cancel()
			return nil
		})
		if !errors.Is(inner, context.Canceled) {
			return fmt.Errorf("inner err = %v, want the cancellation: a nested call that cannot commit has failed", inner)
		}
		return insertProbe(ctx, db, 3)
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("committed = %v, want [1 3]: the write of the call that returned an error undone", got)
	}
}

// The inner function ignores a statement that failed and returns nil. The
// transaction is then in a failed state (SQLSTATE 25P02), in which the savepoint
// cannot even be released. The inner call must report that it failed, with its
// writes undone, and leave the outer transaction in a state it can go on from.
func TestNestedWithinTxUndoesItsWritesWhenTheFunctionIgnoresAFailedStatement(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}

		inner := db.WithinTx(ctx, func(ctx context.Context) error {
			if err := insertProbe(ctx, db, 2); err != nil {
				return err
			}
			if dup := insertProbe(ctx, db, 2); !postgres.IsUniqueViolation(dup) {
				return fmt.Errorf("the second insert of 2: err = %v, want a unique violation", dup)
			}
			return nil // as if nothing had happened
		})
		if inner == nil {
			return errors.New("inner err = nil, want an error: a nested call in a failed transaction cannot commit")
		}
		return insertProbe(ctx, db, 3)
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("committed = %v, want [1 3]: the write of the call that returned an error undone", got)
	}
}

func TestNestedWithinTxIsUndoneWhenItsFunctionPanicsAndTheOuterOneRecovers(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		func() {
			defer func() { _ = recover() }()
			_ = db.WithinTx(ctx, func(ctx context.Context) error {
				if err := insertProbe(ctx, db, 2); err != nil {
					return err
				}
				panic("boom")
			})
		}()
		return insertProbe(ctx, db, 3)
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 3}) {
		t.Fatalf("committed = %v, want [1 3]: the write of the call that panicked undone", got)
	}
}

func TestNestedWithinTxNestsMoreThanOneLevel(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)
	boom := errors.New("innermost failed")

	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		// A call that fails inside one that goes on: only the failed one is undone.
		middle := db.WithinTx(ctx, func(ctx context.Context) error {
			if err := insertProbe(ctx, db, 2); err != nil {
				return err
			}
			innermost := db.WithinTx(ctx, func(ctx context.Context) error {
				if err := insertProbe(ctx, db, 3); err != nil {
					return err
				}
				return boom
			})
			if !errors.Is(innermost, boom) {
				return fmt.Errorf("innermost err = %v, want its function's error", innermost)
			}
			return insertProbe(ctx, db, 4)
		})
		if middle != nil {
			return fmt.Errorf("middle err = %v, want it to commit", middle)
		}
		// A call that succeeds inside one that fails: it goes with it.
		failing := db.WithinTx(ctx, func(ctx context.Context) error {
			if err := db.WithinTx(ctx, func(ctx context.Context) error { return insertProbe(ctx, db, 5) }); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(failing, boom) {
			return fmt.Errorf("failing err = %v, want its function's error", failing)
		}
		return insertProbe(ctx, db, 6)
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 2, 4, 6}) {
		t.Fatalf("committed = %v, want [1 2 4 6]: 3 undone with its call, 5 with the call around it", got)
	}
}

func TestWithinSnapshotReadsWhatWasCommittedWhenItBegan(t *testing.T) {
	ctx := context.Background()
	db, pool := newProbe(t)
	if err := insertProbe(ctx, db, 1); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var before, after int
	err := db.WithinSnapshot(ctx, func(ctx context.Context) error {
		if err := db.QueryRow(ctx, `SELECT count(*) FROM tx_probe`).Scan(&before); err != nil {
			return err
		}
		// Another connection commits between two statements of the snapshot.
		if _, err := pool.Exec(context.Background(), `INSERT INTO tx_probe (id) VALUES (2)`); err != nil {
			return err
		}
		return db.QueryRow(ctx, `SELECT count(*) FROM tx_probe`).Scan(&after)
	})
	if err != nil {
		t.Fatalf("WithinSnapshot: %v", err)
	}

	if before != 1 || after != 1 {
		t.Fatalf("the snapshot counted %d rows, then %d, want 1 and 1: the row committed meanwhile is not in it", before, after)
	}
	if got := committedIDs(t, pool); !slices.Equal(got, []int{1, 2}) {
		t.Fatalf("committed = %v, want [1 2]", got)
	}
}

// A repo asks for a snapshot without knowing whether a use case has a database
// transaction open. When one has, the read is made in it, where the writes the
// use case has made so far are.
func TestWithinSnapshotInsideATransactionReadsInIt(t *testing.T) {
	ctx := context.Background()
	db, _ := newProbe(t)

	var seen int
	err := db.WithinTx(ctx, func(ctx context.Context) error {
		if err := insertProbe(ctx, db, 1); err != nil {
			return err
		}
		return db.WithinSnapshot(ctx, func(ctx context.Context) error {
			return db.QueryRow(ctx, `SELECT count(*) FROM tx_probe`).Scan(&seen)
		})
	})
	if err != nil {
		t.Fatalf("WithinTx: %v", err)
	}

	if seen != 1 {
		t.Fatalf("the snapshot counted %d rows, want the 1 the transaction wrote and has not committed", seen)
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
