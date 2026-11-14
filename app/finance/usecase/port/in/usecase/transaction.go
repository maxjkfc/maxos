package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Transaction is a transaction as the use cases return it: what happened, when,
// and the postings that say where the money went.
type Transaction struct {
	ID uuid.UUID
	// Kind is "expense", "income", "transfer", "exchange", "opening", "reward",
	// "dealing", "reconciliation", "instalment" or "trade".
	Kind string
	// Status is "draft", "confirmed" or "void". Only a confirmed transaction counts
	// toward balances, and from the day it happened on: one that is dated after
	// today counts from its day.
	Status string
	// OccurredOn is the day it happened, at midnight UTC.
	OccurredOn time.Time
	// OccurredAt is the moment it happened, when that is known.
	OccurredAt  *time.Time
	Description string
	Merchant    string
	// ProjectID and AreaID are the project and the area it belongs to, when it has
	// them.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	// IdempotencyKey is the key of the request that made it.
	IdempotencyKey string
	TagIDs         []uuid.UUID
	// Postings are its movements, in order; they add up to zero in each
	// instrument.
	Postings []Posting
	// PlanID and PlanSeq say which instalment plan it is part of, and where in it;
	// both are nil for a transaction that is in none. A plan is the transactions that
	// share a PlanID. PlanSeq 0 is its origin, the expense that is paid in
	// instalments or the instalment that converts what an account owes, and PlanID is
	// its own ID; 1, 2, ... are its periods, in the order they fall due, each an
	// instalment on the day it is charged to the account. A plan is confirmed,
	// discarded and voided through its origin, as a whole.
	PlanID    *uuid.UUID
	PlanSeq   *int
	CreatedAt time.Time
	// ConfirmedAt and VoidedAt are nil until the transaction is confirmed and
	// voided.
	ConfirmedAt *time.Time
	VoidedAt    *time.Time
}

// Posting is one movement of one instrument within a transaction.
type Posting struct {
	// Seq is its position in the transaction, from 1.
	Seq int
	// Book is what it is made to: "account", "expense", "income", "conversion",
	// "opening", "dealing", "reward", "adjustment" or "instalment".
	Book string
	// AccountID is set for the books "account" and "instalment" and only there;
	// CategoryID for "expense" and "income" and only there; PartyID, the party of a
	// dealing, for "dealing" and only there.
	AccountID  *uuid.UUID
	CategoryID *uuid.UUID
	PartyID    *uuid.UUID
	// InstrumentID is the instrument that moves.
	InstrumentID uuid.UUID
	// Quantity is how much moves. It is positive when the book grows (an account
	// holds more, an expense happens, a party owes me more) and negative when it
	// shrinks (an income or a reward happens, a party owes me less). An instalment
	// posting is the principal of a plan that is not yet charged to the account: it
	// is negative when that is made owed (the origin of the plan), and positive when
	// a period charges it.
	Quantity decimal.Decimal
	// BaseAmount is Quantity in the base currency, with its sign. Expense, income
	// and reward postings have one, the others do not.
	BaseAmount *decimal.Decimal
	// Discount is what the merchant took off the price at checkout, in the
	// instrument of the posting. Only an expense posting has one, and only when it
	// was recorded with one. It is a record only: Quantity is what was paid, and no
	// balance or total counts the discount.
	Discount *decimal.Decimal
	Memo     string
}

// RecordDetails are the facts every kind of transaction carries besides what
// makes the kind: which accounts, how much, which categories. What a use case
// records from them is always a draft: it counts toward nothing until
// ConfirmTransactions confirms it.
type RecordDetails struct {
	// IdempotencyKey makes the call safe to repeat: a key that was used before
	// returns the transaction stored under it and writes nothing. It is trimmed of
	// the whitespace around it and must be 8 to 128 characters long.
	IdempotencyKey string
	// OccurredOn is the day it happened, and is required. Only the day it falls on
	// counts, as its own location reads it.
	OccurredOn time.Time
	// OccurredAt is the moment it happened, when that is known.
	OccurredAt *time.Time
	// Description and Merchant are trimmed of the whitespace around them.
	Description string
	Merchant    string
	// ProjectID, when set, must be a project that exists and is not archived. When
	// it is not set, the project active on OccurredOn is used, if there is exactly
	// one. In both cases the transaction goes to the default area of the project
	// unless AreaID is set; a project with no default area, or whose default area
	// has been archived, leaves the area empty.
	ProjectID *uuid.UUID
	// AreaID, when set, must be an area that exists and is not archived. It always
	// wins over the default area of a project.
	AreaID *uuid.UUID
	// TagIDs must be tags that exist and are not archived.
	TagIDs []uuid.UUID
}

// RecordOutput is the result of the use cases that record a transaction.
type RecordOutput struct {
	Transaction Transaction
	// Duplicate is true when the idempotency key was already used: nothing was
	// written, and Transaction is the one stored under the key, which may differ
	// from what was asked this time.
	Duplicate bool
}

// CategoryLine is one part of an expense or an income: an amount that goes to one
// category. Several lines split one payment among categories.
type CategoryLine struct {
	// CategoryID must be a category that is not archived and has the direction of
	// the transaction: an expense takes expense categories, an income takes income
	// categories. It may not be a system category (the ones called 未記錄): only
	// Reconcile records to those.
	CategoryID uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument. For an expense it is what was paid, after the discount.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency (TWD), as of the day of the
	// transaction, greater than zero. It is required when the instrument is not the
	// base currency, and is not changed afterwards. For the base currency it may be
	// left out, and must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Discount is what the merchant took off the price at checkout, in the
	// instrument: greater than zero, with no more decimal places than the
	// instrument. Only a line of an expense may have one. It is only kept as a
	// record: the expense is Amount.
	Discount *decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// Share is a part of a payment that I made for someone else, who owes it to me.
type Share struct {
	// PartyID must be a party that exists and is not archived. A party can be in
	// the shares of an expense only once.
	PartyID uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument of the expense. It is in that instrument, and has no amount in the
	// base currency: it is not an expense, but what the party owes me.
	Amount decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// Fee is a charge that comes with a transaction, such as the fee of a transfer: an
// expense of its own, paid from an account, which need not be one of the
// transaction, in an instrument that need not be its either. The fee of an
// expense is not one of these: it is just another line of the expense.
type Fee struct {
	// AccountID is the account the fee is paid from.
	AccountID uuid.UUID
	// InstrumentID is what the fee is paid in.
	InstrumentID uuid.UUID
	// CategoryID must be a category that is not archived and has the direction
	// expense. It may not be a system category.
	CategoryID uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency (TWD), as of the day of the
	// transaction, greater than zero. It is required when the instrument is not the
	// base currency. For the base currency it may be left out, and must equal Amount
	// if given.
	BaseAmount *decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// RecordExpense records money spent: on one or more categories, paid from an
// account or by a party on my behalf. What I pay for others from an account is
// not my expense: it goes to what they owe me (Shares). Accounts, parties,
// instrument and categories must exist and not be archived.
//
// With Instalments the account does not pay it at once but over a schedule that is
// set now, as an instalment plan (消費分期): the whole of it is an expense on the
// day it is recorded for, and the account is charged the principal of each period
// on the day it falls due. What is recorded is the expense, which is the origin of
// the plan, and a transaction for each period, all of them drafts in one go. The
// output is the expense; the periods are read with GetInstalmentPlan, and the
// idempotency key is the expense's, the keys of the periods being made from the
// plan and not given.
type RecordExpense interface {
	RecordExpense(ctx context.Context, in RecordExpenseInput) (RecordOutput, error)
}

// RecordExpenseInput is an expense to record.
type RecordExpenseInput struct {
	RecordDetails
	// AccountID is the account the money comes out of. Exactly one of AccountID and
	// PaidByPartyID is set.
	AccountID *uuid.UUID
	// PaidByPartyID is the party that paid on my behalf, so that I owe them what the
	// lines come to; the money does not come out of any account. It must be a party
	// that exists and is not archived. Shares are not allowed with it.
	PaidByPartyID *uuid.UUID
	// InstrumentID is what was paid in. It must be a currency when a party paid or
	// there are Shares (ErrNotCurrency): what a party owes is counted in a
	// currency. It must be a currency with Instalments too.
	InstrumentID uuid.UUID
	// Lines are my own share of the payment, at least one: a payment made only for
	// others is not an expense, and is recorded with RecordDealing. When an account
	// paid, it loses the sum of the lines and of the shares.
	Lines []CategoryLine
	// Shares are the parts of the payment that I made for others, in the
	// instrument of the expense. Each party that is in them owes me their amount.
	Shares []Share
	// Instalments, when set, splits the payment into periods: the account is
	// charged the amount of the lines and of the shares, which is what it would have
	// paid, over the schedule instead of at once. It needs AccountID, and is refused
	// with PaidByPartyID (ErrInstalmentsWithPaidBy): a party paying on my behalf has
	// no account to charge.
	Instalments *InstalmentSchedule
}

// RecordIncome records money earned: into an account, from one or more
// categories. Accounts, instrument and categories must exist and not be
// archived.
type RecordIncome interface {
	RecordIncome(ctx context.Context, in RecordIncomeInput) (RecordOutput, error)
}

// RecordIncomeInput is an income to record.
type RecordIncomeInput struct {
	RecordDetails
	// AccountID is the account the money goes into.
	AccountID uuid.UUID
	// InstrumentID is what was received.
	InstrumentID uuid.UUID
	// Lines are the parts of the income, at least one. The account gains their
	// sum.
	Lines []CategoryLine
	// Fees are charges that came with the income. Each is paid from its account,
	// besides what the account gains.
	Fees []Fee
}

// RecordTransfer records money moved from one account to another in the same
// instrument, such as a payment from the bank to a credit card. To change the
// instrument, use RecordExchange.
type RecordTransfer interface {
	RecordTransfer(ctx context.Context, in RecordTransferInput) (RecordOutput, error)
}

// RecordTransferInput is a transfer to record.
type RecordTransferInput struct {
	RecordDetails
	// FromAccountID and ToAccountID are the accounts the money leaves and arrives
	// at, and must be different.
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	InstrumentID  uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument.
	Amount decimal.Decimal
	// Fees are charges that came with the transfer. Each is paid from its account,
	// besides the amount that moves.
	Fees []Fee
}

// RecordExchange records one currency changed into another, at the amounts that
// really changed hands: the rate is what they make. Both instruments must be
// currencies; buying and selling the others is not part of the ledger yet.
type RecordExchange interface {
	RecordExchange(ctx context.Context, in RecordExchangeInput) (RecordOutput, error)
}

// RecordExchangeInput is an exchange to record.
type RecordExchangeInput struct {
	RecordDetails
	// FromAccountID and ToAccountID are the accounts the currency given up leaves
	// and the currency received arrives at. They may be the same account.
	FromAccountID uuid.UUID
	ToAccountID   uuid.UUID
	// FromInstrumentID and ToInstrumentID are the currency given up and the
	// currency received, and must be different.
	FromInstrumentID uuid.UUID
	ToInstrumentID   uuid.UUID
	// FromAmount and ToAmount are what was given up and what was received. Each is
	// greater than zero, and has no more decimal places than its instrument.
	FromAmount decimal.Decimal
	ToAmount   decimal.Decimal
	// Fees are charges that came with the exchange. Each is paid from its account,
	// besides the amount given up.
	Fees []Fee
}

// RecordDealing records a change in what a party owes me, together with the money
// that moves into or out of an account: money I paid out for a party, or lent
// them, grows what they owe me, and money I received from them shrinks it. What
// they owe me is counted per party and per currency, never settled item by item
// (see GetDealings). The account, the party and the currency must exist and not
// be archived.
type RecordDealing interface {
	RecordDealing(ctx context.Context, in RecordDealingInput) (RecordOutput, error)
}

// RecordDealingInput is a dealing to record.
type RecordDealingInput struct {
	RecordDetails
	// AccountID is the account the money goes out of or comes into.
	AccountID uuid.UUID
	// PartyID is the party on the other side.
	PartyID uuid.UUID
	// InstrumentID is what moves, and must be a currency.
	InstrumentID uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument.
	Amount decimal.Decimal
	// Direction is "paid_out" when the money leaves the account and what the party
	// owes me grows (also for a company that I paid for, or a loan), and "received"
	// when it comes into the account and what the party owes me shrinks.
	Direction string
	// Fees are charges that came with the dealing. Each is paid from its account,
	// besides the amount that moves.
	Fees []Fee
}

// RecordReward records a reward: money or credit that a payment instrument gives
// back after the fact. It is a kind of its own: it does not reduce the expense it
// came from, and it is not an income. Points are not tracked: points used to pay
// at checkout are recorded as the expense at full price and a reward of what the
// points paid into the same account. The account and instrument must exist and
// not be archived.
type RecordReward interface {
	RecordReward(ctx context.Context, in RecordRewardInput) (RecordOutput, error)
}

// RecordRewardInput is a reward to record.
type RecordRewardInput struct {
	RecordDetails
	// AccountID is the account the reward is paid into.
	AccountID uuid.UUID
	// InstrumentID is what the reward is paid in.
	InstrumentID uuid.UUID
	// Amount is greater than zero, and has no more decimal places than the
	// instrument.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency (TWD), as of the day of the
	// transaction, greater than zero. It is required when the instrument is not the
	// base currency, and is not changed afterwards. For the base currency it may be
	// left out, and must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Fees are charges that came with the reward. Each is paid from its account,
	// besides the amount that comes in.
	Fees []Fee
}

// SetOpeningBalance records what an account holds of an instrument before its
// first transaction.
type SetOpeningBalance interface {
	SetOpeningBalance(ctx context.Context, in SetOpeningBalanceInput) (RecordOutput, error)
}

// SetOpeningBalanceInput is an opening balance to record.
type SetOpeningBalanceInput struct {
	RecordDetails
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Amount is what the account holds: not zero, with no more decimal places than
	// the instrument. It is negative for an account that owes.
	Amount decimal.Decimal
}

// ConfirmTransactions confirms drafts, which makes them count toward balances,
// each from the day it happened on. It is all or nothing: when any of them cannot
// be confirmed (it does not exist, is not a draft, or is a period of an
// instalment plan) none is, and the error names every one that cannot.
//
// The origin of an instalment plan stands for the whole plan: it confirms the
// origin and each period with it, in one go, and is one transaction of the call.
// The output has the origin, and the periods are read with GetInstalmentPlan. A
// period cannot be confirmed by its own id (ErrPartOfPlan), also when its origin
// is among the ids: a plan is confirmed through its origin alone.
type ConfirmTransactions interface {
	ConfirmTransactions(ctx context.Context, in ConfirmTransactionsInput) (ConfirmTransactionsOutput, error)
}

// ConfirmTransactionsInput names the drafts to confirm.
type ConfirmTransactionsInput struct {
	// IDs are the transactions to confirm, at least one. An id given twice counts
	// once.
	IDs []uuid.UUID
}

// ConfirmTransactionsOutput holds the transactions confirmed, in the order the
// ids were given.
type ConfirmTransactionsOutput struct {
	Transactions []Transaction
}

// DiscardDraft deletes a draft, with its postings. A transaction that is
// confirmed has counted and cannot be deleted: void it instead. The origin of an
// instalment plan is discarded with all its periods, or not at all; a period
// cannot be discarded by its own id (ErrPartOfPlan).
type DiscardDraft interface {
	DiscardDraft(ctx context.Context, id uuid.UUID) error
}

// VoidTransaction voids a confirmed transaction: it stops counting toward
// balances, and stays as a record. Only a confirmed transaction can be voided.
// To correct one, void it and record it again.
//
// The origin of an instalment plan voids the whole plan, the periods that have
// already fallen due too, and the output is the origin. A plan is corrected, or
// paid off early, by voiding it and recording it again: nothing reschedules a
// plan. A period cannot be voided by its own id (ErrPartOfPlan).
type VoidTransaction interface {
	VoidTransaction(ctx context.Context, id uuid.UUID) (Transaction, error)
}

// GetTransaction returns one transaction, whatever its status.
type GetTransaction interface {
	GetTransaction(ctx context.Context, id uuid.UUID) (Transaction, error)
}

// The size of a page of ListTransactions.
const (
	// DefaultListLimit is the page size when ListTransactionsInput.Limit is zero.
	DefaultListLimit = 50
	// MaxListLimit is the largest page; a larger Limit is cut down to it.
	MaxListLimit = 200
)

// ListTransactions lists transactions, newest first, a page at a time.
type ListTransactions interface {
	ListTransactions(ctx context.Context, in ListTransactionsInput) (ListTransactionsOutput, error)
}

// ListTransactionsInput filters the listing. Every filter that is set must hold
// for a transaction to be listed.
type ListTransactionsInput struct {
	// Status is "draft", "confirmed" or "void" to list only the transactions in
	// that status; empty lists all of them.
	Status string
	// From and To bound the day the transactions happened on, both ends included;
	// only the day each falls on counts. nil leaves that end open. To may not be
	// before From.
	From *time.Time
	To   *time.Time
	// AccountID lists the transactions that move that account.
	AccountID *uuid.UUID
	// PartyID lists the transactions that have a dealing with that party: money
	// paid out to them or received from them, a share of an expense that they
	// owe me, or an expense that they paid.
	PartyID *uuid.UUID
	// PlanID lists the transactions of that instalment plan: its origin, which has
	// the id of the plan, and its periods. They are ordinary transactions to the
	// list, each on its own day.
	PlanID *uuid.UUID
	// ProjectID and AreaID list the transactions of that project and of that area.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	// Limit is the most transactions to return; zero means DefaultListLimit, and
	// more than MaxListLimit is cut down to it. Offset is how many to skip. Neither
	// may be negative.
	Limit  int
	Offset int
}

// ListTransactionsOutput is a page of transactions, newest first: by the day
// they happened on, and for the same day the latest made first.
type ListTransactionsOutput struct {
	Transactions []Transaction
	// HasMore is true when more transactions follow this page: ask again with
	// Offset advanced by the number returned.
	HasMore bool
}

// GetBalances returns what accounts hold on a day, counting only the confirmed
// transactions that happened on or before it: a confirmed transaction dated
// later counts from its day on. A balance is the quantities of the confirmed
// account postings to an account, added up per instrument; nothing is stored. The
// principal of instalment plans that has not fallen due is not in it: it is
// listed next to it, as the unbilled instalment of the account (未到期分期).
type GetBalances interface {
	GetBalances(ctx context.Context, in GetBalancesInput) (GetBalancesOutput, error)
}

// GetBalancesInput says whose balances to return, and on which day.
type GetBalancesInput struct {
	// AccountID limits the balances to that account; nil returns every account's.
	AccountID *uuid.UUID
	// AsOf is the day the balances are counted up to, that day and each day before
	// it. Only the day it falls on counts, as its own location reads it. nil is
	// today, which the clock of the application says, in its timezone.
	AsOf *time.Time
}

// Balance is how much of an instrument an account holds on a day. A negative
// quantity is what the account owes.
type Balance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	Quantity     decimal.Decimal
	// UnbilledInstalment is the principal of the instalment plans of the account
	// that has not fallen due by the day (未到期分期): zero or less, as what the
	// account owes. It is not in Quantity: a period reaches the balance of the
	// account on the day it falls due, and until then it is listed here.
	UnbilledInstalment decimal.Decimal
}

// GetBalancesOutput holds one balance for each account and instrument that has
// confirmed postings by the day, by account name and then instrument code. A
// balance can be zero, when the postings cancel out; an account with no confirmed
// postings by the day has none. An account and instrument that only has
// unbilled instalment has one, with a quantity of zero.
type GetBalancesOutput struct {
	Balances []Balance
}

// GetDealings returns what parties owe me on a day, counting only the confirmed
// transactions that happened on or before it. A dealing is the quantities of the
// confirmed dealing postings made to a party, added up per instrument, so what a
// party owes me is one amount per currency, not a list of what is open; nothing
// is stored.
type GetDealings interface {
	GetDealings(ctx context.Context, in GetDealingsInput) (GetDealingsOutput, error)
}

// GetDealingsInput says whose dealings to return, and on which day.
type GetDealingsInput struct {
	// PartyID limits the dealings to that party; nil returns every party's. A party
	// with no confirmed dealing postings has none. The party must be one the
	// catalog has: for one it does not, the error is ErrNotFound (with the id of the
	// party), not an empty result. An archived party is one the catalog has: what it
	// owes still counts.
	PartyID *uuid.UUID
	// AsOf is the day the dealings are counted up to, that day and each day before
	// it. Only the day it falls on counts, as its own location reads it. nil is
	// today, which the clock of the application says, in its timezone.
	AsOf *time.Time
}

// Dealing is what a party owes me in an instrument: positive when they owe me,
// negative when I owe them.
type Dealing struct {
	PartyID      uuid.UUID
	InstrumentID uuid.UUID
	Quantity     decimal.Decimal
}

// GetDealingsOutput holds one dealing for each party and instrument that has
// confirmed dealing postings, in the order the catalog lists its parties (by
// name) and then by instrument code. A dealing can be zero, when the postings
// cancel out.
type GetDealingsOutput struct {
	Dealings []Dealing
}
