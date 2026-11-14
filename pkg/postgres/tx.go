package postgres

import (
	"context"
	"fmt"

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
// original context runs outside the transaction. It may call WithinTx again,
// which then runs fn in a savepoint of the open transaction: when the inner fn
// fails only its own writes are undone, and the outer fn decides what to do
// with the error.
func (db *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	var (
		tx  pgx.Tx
		err error
	)
	if outer, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		tx, err = outer.Begin(ctx) // a savepoint
	} else {
		tx, err = db.pool.Begin(ctx)
	}
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
