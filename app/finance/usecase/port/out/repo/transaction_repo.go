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
	// Kind limits the list to that kind of transaction; empty lists every kind.
	Kind model.Kind
	// From and To bound the day the transactions happened on, both ends included.
	// They are dates as model.DateOf makes them; nil leaves that end open.
	From, To *time.Time
	// AccountID limits the list to the transactions with a posting to that account,
	// of any book: the instalment posting of the origin of a purchase paid in
	// instalments is one.
	AccountID *uuid.UUID
	// WithoutAccount limits the list to the transactions with a posting to no
	// account, of any book. It does not go with AccountID, which the use case
	// refuses.
	WithoutAccount bool
	// WithoutPlanPeriods leaves the periods of instalment plans out of the list: the
	// origin of a plan, and the transactions that are in none, stay.
	WithoutPlanPeriods bool
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

// StatementFilter selects the statement TransactionRepo.Statement reads.
type StatementFilter struct {
	// AccountID is the card the statement is of.
	AccountID uuid.UUID
	// Period is the days of the statement, both ends included: the days after the
	// previous closing date up to its closing date. It is the model's to work out
	// from the closing day of the card.
	Period model.StatementPeriod
	// Today is the day the statement is counted up to, which is in it: what has a
	// statement date after it counts from that day, and is not on the statement yet.
	// It is a date as model.DateOf makes it.
	Today time.Time
}

// StatementContent is what one statement holds, as TransactionRepo.Statement reads
// it.
type StatementContent struct {
	// Figures are the figures of each instrument that was posted to on the
	// statement or carried a balance into it, by instrument code.
	Figures []model.StatementFigures
	// Transactions are the confirmed transactions on the statement, with their
	// postings and tags: those with an account posting to the card whose statement
	// date is on the statement and not after today. By statement date, oldest first,
	// then the day they happened, oldest first, then the one made first.
	Transactions []model.Transaction
	// Drafts is how many drafts with an account posting to the card have a statement
	// date on the statement. They are not in Figures nor in Transactions, and today
	// does not limit them.
	Drafts int
}

// TransactionRepo stores transactions with their postings and tags, and works out
// balances from them.
//
// A repo does not open a database transaction to write in, so the use case
// decides what is written together. Two things need one: Insert, which writes a
// transaction in several statements and relies on the database to check, when
// its transaction commits, that the postings add up to zero; and GetForUpdate,
// whose lock lasts until its transaction ends. The only transaction a repo opens
// is the read-only snapshot of List and of Statement.
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
	// one order, the same for every caller, which keeps two such callers from
	// waiting for each other: first those that are not periods of a plan, the
	// origins of plans and the ordinary transactions, in the order of their ids; then
	// the periods of each plan whose origin is locked, in the order of their ids.
	// The lock of the origin is the lock of the plan: nobody else holds a period of
	// it, and the periods of a plan, which a reschedule replaces, are read only once
	// the origin is locked.
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
	// PreviewBalances returns what each account would hold of each instrument on the
	// day if every draft were confirmed, and how many drafts wait. For each account
	// and instrument it has, in each of the two figures that Balances has, what
	// Balances says, and, apart from it, what the drafts that happened on or before
	// the day add to it, by account name and then instrument code: the account
	// postings of the drafts to what the account holds, and their instalment
	// postings to the principal that has not fallen due. The drafts are the
	// transactions in the status draft, the periods of draft plans among them, which
	// are drafts of their own in the store; the postings of the account book are what
	// SummarizeDrafts adds up into the amounts of an account. So a transfer between
	// two of my accounts is in both, a period of a draft plan is in from the day it
	// falls due, and a draft dated after the day, and a void transaction, are not in
	// it. Once every draft is confirmed, Balances says of each account and instrument
	// what the two figures of its balance add up to here. There is a balance for each
	// account and instrument with a posting of either book of a confirmed
	// transaction or of a draft, however they add up. Drafts is how many drafts wait,
	// as SummarizeDrafts counts them: whatever day each happened on, a plan once.
	// It reads all of it in one snapshot, so that the confirmed part, the draft part
	// and the count are of one moment: a draft that is confirmed while it runs is in
	// one of the parts, and never in both or in neither. Call it outside a database
	// transaction, or inside one that is REPEATABLE READ or stricter, as for List.
	// day is a date as model.DateOf makes it.
	PreviewBalances(ctx context.Context, day time.Time) (model.BalancePreview, error)
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
	// SetPostedOn sets the posting date of the transaction, or clears it with nil,
	// and returns the transaction, or returns model.ErrNotFound. It checks nothing
	// that the model checks: whether the transaction may have one is for the model.
	// The table refuses a date before the day the transaction happened, and any date
	// on a transaction of an instalment plan. The use case calls it with the
	// transaction locked by GetForUpdate, in the database transaction it holds the
	// lock in.
	SetPostedOn(ctx context.Context, id uuid.UUID, postedOn *time.Time) (model.Transaction, error)
	// StatementFigures returns what each instrument did on each of the statements of
	// the account, one list for each of the periods, in the order the periods are
	// given: the figures of the instruments, by instrument code. A transaction counts
	// on the statement whose days include its statement date, the posting date when
	// it has one and otherwise the day it happened, when it is a confirmed one with an
	// account posting to the account; Previous is what the postings with a statement
	// date before the statement add up to. What has a statement date after today is
	// not counted, nor is what is after the day a statement closes. An instrument is
	// listed for a statement when it was posted to on it or has a Previous that is not
	// zero. It is one statement of SQL, so every figure of every statement comes from
	// one snapshot: the Closing of one statement is the Previous of the next. today is
	// a date as model.DateOf makes it.
	StatementFigures(ctx context.Context, accountID uuid.UUID, periods []model.StatementPeriod, today time.Time) ([][]model.StatementFigures, error)
	// Statement returns what one statement holds: its figures, as StatementFigures
	// reads them, the transactions on it and how many drafts are waiting for it. It
	// reads all of it in one snapshot, so that the transactions are those that the
	// figures add up. Call it outside a database transaction, or inside one that is
	// REPEATABLE READ or stricter, as for List.
	Statement(ctx context.Context, f StatementFilter) (StatementContent, error)
	// DraftTransactions returns every transaction whose status is draft, whatever day
	// it happened on: the ordinary ones, the origins of instalment plans, and their
	// periods, which are drafts of their own in the store. Each has the postings that
	// have an account, of the account book or of the instalment book, in the order of
	// their positions, with the name of the account and the code of the instrument; a
	// draft that has none has no postings. It is what model.SummarizeDrafts works the
	// summary of the drafts out from, and they come in no particular order. It is one
	// statement, so what it returns is of one moment: a draft that is confirmed or
	// discarded while it runs is in it with all its postings, or not at all.
	DraftTransactions(ctx context.Context) ([]model.DraftTransaction, error)
}
