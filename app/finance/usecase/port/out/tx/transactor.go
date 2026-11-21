// Package tx declares the transaction boundary the finance use cases ask for. A
// use case decides which writes belong together; the repos it calls know nothing
// about transactions, and join the one the context it passes them carries.
package tx

import "context"

// Transactor opens a database transaction around fn.
type Transactor interface {
	// WithinTx runs fn in one database transaction, and gives it a context that
	// carries the transaction: fn must make its repo calls with that context, so
	// that they all join it. The transaction commits when fn returns nil, and rolls
	// back when it returns an error or panics; the error goes on to the caller.
	//
	// Nothing that fn calls may open a transaction of its own (fn may call other use
	// cases, as long as they do not): a call inside fn that reaches WithinTx fails with
	// the adapter's ErrNestedTx before it runs anything. That error is a mistake in the
	// code, not a case to handle: a use case only passes it up, which rolls fn's
	// transaction back.
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
