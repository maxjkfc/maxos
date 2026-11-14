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
	// PartyID limits the list to the transactions with a dealing posting to that
	// party.
	PartyID *uuid.UUID
	// PlanID limits the list to the transactions of that instalment plan.
	PlanID *uuid.UUID
	// ProjectID and AreaID limit the list to the transactions of that project and
	// of that area.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	// Limit is how many transactions to return at most, and is positive; Offset is
	// how many to skip.
	Limit  int
	Offset int
}

// BalanceFilter selects the balances TransactionRepo.Balances returns.
type BalanceFilter struct {
	// AccountID limits the balances to that account; nil leaves it open.
	AccountID *uuid.UUID
	// InstrumentID limits the balances to that instrument; nil leaves it open.
	InstrumentID *uuid.UUID
	// Day is the day the balances are counted up to, which is in them, as are the
	// days before it. It is a date as model.DateOf makes it.
	Day time.Time
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
	// transaction, returns its transaction. A transaction of an instalment plan
	// refers to the origin of the plan, which must be stored first: Insert the
	// origin, then the periods, in one database transaction.
	Insert(ctx context.Context, txn model.Transaction) (model.Transaction, error)
	// Get returns the transaction, whatever its status, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Transaction, error)
	// GetForUpdate is Get that also locks the transaction against being changed or
	// deleted by anyone else until the database transaction ends: they wait, and
	// then see what this one left. Whoever locks several transactions locks them in
	// the order of their ids, which keeps two such callers from waiting for each
	// other.
	GetForUpdate(ctx context.Context, id uuid.UUID) (model.Transaction, error)
	// GetByIdempotencyKey returns the transaction stored under the key, or
	// model.ErrNotFound.
	GetByIdempotencyKey(ctx context.Context, key string) (model.Transaction, error)
	// GetPlan returns the instalment plan whose origin has the id: the origin and
	// the periods in the order they fall due, whatever their status. It returns
	// model.ErrNotFound for an id that is not the id of a plan, as the id of a
	// period is not. It takes no lock.
	GetPlan(ctx context.Context, planID uuid.UUID) (model.InstalmentPlan, error)
	// SetStatus moves the transaction to the status, stamps the time it got there
	// (ConfirmedAt for confirmed, VoidedAt for void), and returns it. It returns
	// model.ErrNotFound for an id that is not stored. It does not check that the
	// move is a valid one; the model does.
	SetStatus(ctx context.Context, id uuid.UUID, status model.Status) (model.Transaction, error)
	// Delete deletes the transaction with its postings and tags, or returns
	// model.ErrNotFound. It does not check the status; the model does. The origin
	// of an instalment plan cannot be deleted while a period of the plan is stored:
	// delete the periods first.
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
	// Balances returns what each account holds of each instrument on the filter's
	// day, from the postings of the confirmed transactions that happened on or
	// before it, by account name and then instrument code. Each balance has the
	// account postings added up, and, apart from them, the instalment postings:
	// the principal of plans that has not fallen due. There is a balance for each
	// account and instrument with a posting of either, however they add up. The
	// filter's account and instrument limit it, each when set.
	Balances(ctx context.Context, filter BalanceFilter) ([]model.Balance, error)
	// PendingDrafts returns how many drafts happened on or before the day and have
	// an account posting to the account in the instrument: transactions that would
	// change what Balances returns for them on that day once confirmed. day is a
	// date as model.DateOf makes it.
	PendingDrafts(ctx context.Context, accountID, instrumentID uuid.UUID, day time.Time) (int, error)
	// Dealings returns what each party owes me of each instrument on the day, from
	// the confirmed transactions that happened on or before it: the quantities of
	// the dealing postings made to the party, added up. A nil partyID returns every
	// party's. It lists a party and instrument whose postings add up to zero, and it
	// is ordered by party id and then instrument code: the names of the parties are
	// the catalog's, so ordering by them is for the use case. day is a date as
	// model.DateOf makes it.
	Dealings(ctx context.Context, partyID *uuid.UUID, day time.Time) ([]model.Dealing, error)
	// InstalmentPlans returns where each confirmed instalment plan stands on the
	// day, newest first (by the day of the origin, then the latest made first): what
	// it splits, what of it has been charged by the day, the day it is charged
	// next. It returns the plans that charge the account when accountID is set, and
	// every plan's otherwise, and the finished ones too. A draft plan and a void one
	// are not in it. day is a date as model.DateOf makes it.
	InstalmentPlans(ctx context.Context, accountID *uuid.UUID, day time.Time) ([]model.InstalmentPlanSummary, error)
}
