package repo

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// ErrDuplicateKey is returned by TransactionRepo.Insert when the idempotency key
// is already stored.
var ErrDuplicateKey = errors.New("idempotency key already exists")

// TransactionFilter selects the transactions TransactionRepo.List returns; every
// filter that is set must hold.
type TransactionFilter struct {
	// Status limits the list to that status; empty lists every status.
	Status model.Status
	// From and To bound the day the transactions happened on, both ends included.
	// They are dates as model.DateOf makes them; nil leaves that end open.
	From, To *time.Time
	// AccountID limits the list to the transactions with a posting to that account.
	AccountID *uuid.UUID
	// ProjectID and AreaID limit the list to the transactions of that project and
	// of that area.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	// Limit is how many transactions to return at most, and is positive; Offset is
	// how many to skip.
	Limit  int
	Offset int
}

// TransactionRepo stores transactions with their postings and tags, and works out
// balances from them.
//
// A repo does not open a database transaction to write in, so the use case
// decides what is written together. Two things need one: Insert, which writes a
// transaction in several statements and relies on the database to check, when
// its transaction commits, that the postings add up to zero; and GetForUpdate,
// whose lock lasts until its transaction ends. The only transaction a repo opens
// is the read-only snapshot of List.
type TransactionRepo interface {
	// Insert stores the transaction, a draft, with its postings and tags, and
	// returns it as stored, including the fields the store assigns (CreatedAt). A
	// transaction of another status is refused: it is confirmed with SetStatus
	// once stored. Insert writes nothing and returns ErrDuplicateKey when the
	// idempotency key is already stored. When a concurrent call has stored the key
	// but not yet committed, Insert waits for it to finish, so ErrDuplicateKey
	// means that call committed: GetByIdempotencyKey, called in the same database
	// transaction, returns its transaction.
	Insert(ctx context.Context, txn model.Transaction) (model.Transaction, error)
	// Get returns the transaction, whatever its status, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Transaction, error)
	// GetForUpdate is Get that also locks the transaction against being changed or
	// deleted by anyone else until the database transaction ends: they wait, and
	// then see what this one left.
	GetForUpdate(ctx context.Context, id uuid.UUID) (model.Transaction, error)
	// GetByIdempotencyKey returns the transaction stored under the key, or
	// model.ErrNotFound.
	GetByIdempotencyKey(ctx context.Context, key string) (model.Transaction, error)
	// SetStatus moves the transaction to the status, stamps the time it got there
	// (ConfirmedAt for confirmed, VoidedAt for void), and returns it. It returns
	// model.ErrNotFound for an id that is not stored. It does not check that the
	// move is a valid one; the model does.
	SetStatus(ctx context.Context, id uuid.UUID, status model.Status) (model.Transaction, error)
	// Delete deletes the transaction with its postings and tags, or returns
	// model.ErrNotFound. It does not check the status; the model does.
	Delete(ctx context.Context, id uuid.UUID) error
	// List returns the transactions that match the filter, newest first (by the
	// day they happened on, then the latest made first), with their postings and
	// tags: at most Limit of them, from Offset. It reads all of it in one
	// snapshot, so that what it returns is what the filter matched at one moment,
	// each transaction as it was then: one that is deleted or changed while List
	// runs does not make the page short, and is not in it with a status the
	// filter does not allow. Call List outside a database transaction, or inside
	// one that is REPEATABLE READ or stricter: under READ COMMITTED each
	// statement reads a snapshot of its own, a transaction deleted while List
	// runs makes it return an error, and one whose status changes can be returned
	// with a status the filter does not allow.
	List(ctx context.Context, filter TransactionFilter) ([]model.Transaction, error)
	// Balances returns what each account holds of each instrument, from the
	// postings of the confirmed transactions only, by account name and then
	// instrument code. A nil accountID returns every account's balances.
	Balances(ctx context.Context, accountID *uuid.UUID) ([]model.Balance, error)
}
