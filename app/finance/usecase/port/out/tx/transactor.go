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
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}
