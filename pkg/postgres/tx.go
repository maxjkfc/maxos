package postgres

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB is the database handle of the repos, and what a use case asks to open a
// transaction. It splits the two jobs between the layers that own them:
//
//   - A repo runs its statements through DB (it satisfies the DBTX interface that
//     sqlc generates, so a repo builds its queries with dbgen.New(NewDB(pool)))
//     and does not know about transactions. A statement runs in the transaction
//     the context carries, or on the pool when it carries none.
//   - A use case that must make several writes atomic calls WithinTx; every repo
//     call it makes with the context it is given joins that transaction.
//
// Use cases may not import pkg/, so a use case declares the one method it needs
// as a port of its own context (WithinTx) and cmd/ injects a *DB for it.
type DB struct {
	pool *pgxpool.Pool
}

// NewDB returns a DB on pool. Every DB built on the same pool joins the same
// transactions, so each repo may build its own.
func NewDB(pool *pgxpool.Pool) *DB {
	return &DB{pool: pool}
}

// txKey is the context key under which WithinTx carries the transaction.
type txKey struct{}

// querier is what a pool and a transaction have in common.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// active returns the transaction ctx carries, or the pool.
func (db *DB) active(ctx context.Context) querier {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return db.pool
}

// Exec runs a statement that returns no rows.
func (db *DB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return db.active(ctx).Exec(ctx, sql, args...)
}

// Query runs a statement and returns its rows; the caller must close them.
func (db *DB) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return db.active(ctx).Query(ctx, sql, args...)
}

// QueryRow runs a statement that returns at most one row.
func (db *DB) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	return db.active(ctx).QueryRow(ctx, sql, args...)
}

// WithinTx runs fn in one transaction and passes it a context that carries the
// transaction. It commits when fn returns nil; when fn returns an error, or
// panics, it rolls back, and the error (or panic) goes on to the caller.
//
// fn must make its database calls with the context it is given: a call with the
// original context runs outside the transaction.
//
// fn may call WithinTx again, which then runs the inner fn in a savepoint of the
// open transaction, and the outer fn decides what to do with what the inner call
// returns. When the inner call returns an error, whatever the reason, its writes
// are undone and the outer transaction is as usable as before it:
//
//   - the inner fn returns an error, or panics;
//   - the context of the inner call is done by the time its fn returns, so that
//     it cannot be committed;
//   - the inner fn ignores a statement that failed and returns nil: the
//     transaction is then in a failed state, which nothing but a rollback to the
//     savepoint leaves, and the savepoint cannot be released from it.
//
// Only an inner call that returns nil leaves its writes to the outer
// transaction, which keeps them or undoes them with the rest. (A context that
// is done while a statement is running is another matter: pgx closes the
// connection then, which ends the whole transaction.)
func (db *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if outer, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return withinSavepoint(ctx, outer, fn)
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	// Rolling back what was committed does nothing, so this is the path out for
	// every way but a successful commit. It does not follow ctx's cancellation: a
	// caller that gave up still gets its connection back clean.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if err := fn(context.WithValue(ctx, txKey{}, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

// savepointSeq numbers the savepoints taken by withinSavepoint, so that no two of
// them that are open at once share a name.
var savepointSeq atomic.Uint64

// withinSavepoint runs fn between SAVEPOINT and RELEASE SAVEPOINT on tx, and
// undoes what fn wrote, with ROLLBACK TO SAVEPOINT, unless the release is made.
//
// It does not use pgx's own nested transaction (tx.Begin): that one is closed
// as soon as Commit is called, whether or not the release worked, and the
// Rollback that should follow a failed release is then a no-op. The savepoint
// would stay open with the writes of an inner call that returned an error, in a
// transaction that may well be in a failed state.
func withinSavepoint(ctx context.Context, tx pgx.Tx, fn func(ctx context.Context) error) (err error) {
	name := "maxos_sp_" + strconv.FormatUint(savepointSeq.Add(1), 10)
	if _, err := tx.Exec(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("begin nested transaction: %w", err)
	}

	released := false
	defer func() {
		if released {
			return
		}
		// fn failed or panicked, or the release did. Undo what it wrote, even
		// though ctx may be done and even though the transaction may be in a failed
		// state: this is the statement that leaves that state. A caller that gave
		// up must not leave its writes behind in the transaction that goes on.
		if rollbackErr := rollbackToSavepoint(context.WithoutCancel(ctx), tx, name); rollbackErr != nil {
			err = errors.Join(err, rollbackErr)
		}
	}()

	if err := fn(ctx); err != nil {
		return err
	}
	// A done context cannot send the release (pgx refuses a statement whose
	// context is done, before it is sent), and a nested call that cannot commit is
	// one that failed.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("commit nested transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("commit nested transaction: %w", err)
	}
	released = true
	return nil
}

// rollbackToSavepoint undoes everything since the savepoint and then releases
// it: rolling back to a savepoint keeps it, and an open savepoint is only noise.
func rollbackToSavepoint(ctx context.Context, tx pgx.Tx, name string) error {
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT "+name); err != nil {
		return fmt.Errorf("roll back nested transaction: %w", err)
	}
	if _, err := tx.Exec(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("release the savepoint of a nested transaction that was rolled back: %w", err)
	}
	return nil
}
