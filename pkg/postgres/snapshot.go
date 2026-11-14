package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// WithinSnapshot runs fn so that every statement it makes with the context it is
// given reads the same snapshot of the database: what was committed when the
// first of them began, whatever is committed meanwhile. A read that takes
// several statements, such as a list of ids and then the rows of those ids, uses
// it to get the rows of the ids it found, and not what a statement of its own
// would find a moment later.
//
// The snapshot is a read-only REPEATABLE READ transaction: fn cannot write, and
// it neither waits for a writer nor makes one wait. Rolling it back ends it, as
// there is nothing to commit; fn's error, or panic, goes on to the caller.
//
// A context that already carries a transaction (see WithinTx) has one to read
// in, which is what it must see, uncommitted writes included: fn runs in it, and
// what it sees is what that transaction sees, which is a snapshot only if the
// transaction is REPEATABLE READ or stricter.
func (db *DB) WithinSnapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return fn(ctx)
	}

	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin snapshot: %w", err)
	}
	// Not bound to ctx's cancellation: a caller that gave up still gets its
	// connection back clean.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	return fn(context.WithValue(ctx, txKey{}, tx))
}
