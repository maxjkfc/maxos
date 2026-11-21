package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// SetPostingDate sets, changes or clears the posting date (入帳日) of a
// transaction: the day the institution put it on a statement, when that is not the
// day it happened. It decides which statement of a card the transaction is on, so
// that the statements can be checked against the ones the bank sends, and nothing
// else: balances, budgets and dealings keep going by the day the transaction
// happened. Nothing about a transaction but this date is touched: no posting,
// amount, date or base amount changes.
//
// A draft and a confirmed transaction can have it set, changed and cleared, as
// often as wanted: the bank's day is learnt when its statement comes, which is
// after the transaction was recorded and confirmed. Giving the date it has again
// succeeds and changes nothing. It returns the transaction as it is then.
//
// What cannot have one, set or cleared, is refused and nothing is written:
//   - a transaction that has no posting to an account that is a card
//     (ErrNoCardPosting);
//   - a transaction of an instalment plan, the origin as much as a period
//     (ErrPartOfPlan): the day of a period is the day the institution bills it
//     already, and a reschedule replaces the periods;
//   - a void transaction (ErrTransactionVoid);
//   - a date before the day the transaction happened (ErrPostedBeforeOccurred);
//   - a transaction that does not exist (ErrNotFound).
//
// It locks the transaction until it is done, as ConfirmTransactions, VoidTransaction
// and DiscardDraft do: a call that overlaps one of them is the one or the other
// first, and sees what the other left. A transaction that is part of a plan is
// refused without being locked.
type SetPostingDate interface {
	SetPostingDate(ctx context.Context, in SetPostingDateInput) (Transaction, error)
}

// SetPostingDateInput names the transaction and its posting date.
type SetPostingDateInput struct {
	TransactionID uuid.UUID
	// PostedOn is the posting date. Only the day it falls on counts, as its own
	// location reads it. nil clears the date, so that the transaction is on the
	// statement of the day it happened again.
	PostedOn *time.Time
}

// StatementFigures is what a card did in one instrument on one statement. A card
// can hold several instruments, and each has its own. Previous and Closing have
// the sign of a balance: negative is what the card owes.
type StatementFigures struct {
	InstrumentID uuid.UUID
	// Previous is what the card owed, or held, when the statement began: the
	// postings with a statement date before it, added up.
	Previous decimal.Decimal
	// Charges is the negative postings of the statement added up, as a positive
	// number: what was spent on the card.
	Charges decimal.Decimal
	// Credits is the positive postings of the statement added up: payments to the
	// card, refunds and rewards.
	Credits decimal.Decimal
	// Closing is Previous less Charges plus Credits: what the card owes, or holds,
	// at the end of the statement. It is the Previous of the next statement.
	Closing decimal.Decimal
}

// CardStatementSummary is a statement without the transactions on it.
type CardStatementSummary struct {
	// From is the first day of the statement, at midnight UTC: the day after the
	// previous closing date.
	From time.Time
	// ClosingOn is the closing date, at midnight UTC: the last day of the statement.
	ClosingOn time.Time
	// DueOn is the day the statement is to be paid by, at midnight UTC: the first day
	// after ClosingOn that is the payment due day of the card. nil when the card has
	// no payment due day.
	DueOn *time.Time
	// Closed is true when the statement has closed: ClosingOn is before today. On the
	// closing date itself it is still open.
	Closed bool
	// Figures are the figures of each instrument the card has something to say of on
	// this statement: it was posted to on it, or it carried a balance into it. They
	// are by instrument code. A statement with none has no figures. Only confirmed
	// transactions count, and only those whose statement date is today or before: one
	// that is dated after today counts from its day, as it does in a balance.
	Figures []StatementFigures
}

// StatementAmount is what a transaction moved on the card, in one instrument.
type StatementAmount struct {
	InstrumentID uuid.UUID
	// Quantity is the quantity of an account posting of the transaction to the card:
	// negative when the card was charged, positive when it was credited.
	Quantity decimal.Decimal
}

// StatementTransaction is a transaction on a statement.
type StatementTransaction struct {
	// Transaction is the transaction as GetTransaction returns it: its id, kind, the
	// day it happened on and its posting date, description and merchant, and all its
	// postings, with the categories and memos they carry.
	Transaction Transaction
	// StatementDate is the day that put it on this statement: PostedOn when it has
	// one, and otherwise OccurredOn. It is at midnight UTC.
	StatementDate time.Time
	// Amounts are what it moved on the card, an account posting to it each, in the
	// order of the postings. Usually one.
	Amounts []StatementAmount
}

// CardStatement is one statement of a card: what was posted to it between two
// closing days, and what is owed at the end. It is worked out from the transactions
// when asked for and never stored.
type CardStatement struct {
	CardStatementSummary
	// Transactions are the confirmed transactions with an account posting to the card
	// whose statement date is on the statement and not after today, oldest first, the
	// way the lines of a bank's statement run: by statement date, then the day they
	// happened, and for the same day the one made first. That is the opposite of
	// ListTransactions, which lists the newest first.
	Transactions []StatementTransaction
	// Drafts is how many draft transactions with an account posting to the card have
	// a statement date on the statement. The figures do not count them: it is how
	// many are waiting to change them. A draft dated after today is counted too, as
	// it would be on the statement from its day.
	Drafts int
}

// GetCardStatement returns one statement of a card, the one whose days include the
// day asked for, and today when none is: the statement that is open now. A card
// has a closing day, and its statements are the days between one closing date and
// the next: a statement covers the days after the previous closing date up to and
// including its own, and a day is on exactly one. The closing date of a month is
// the card's closing day of it, or the last day of the month when the month has
// fewer days, so a closing day of 31 is always the last day.
//
// A transaction is on the statement whose days include its statement date, which
// is its posting date when it has one (see SetPostingDate) and otherwise the day it
// happened. What counts is the confirmed postings to the card, those of an account
// posting only: the origin of an instalment plan that was bought on the card has
// none, so it is not on a statement, and each period is, on its due day; what is
// unbilled is not part of a statement. Drafts and void transactions count nowhere.
// Today is what the clock of the application says.
//
// The days of the card are not historised: the statements are cut from the closing
// day the card has now. Changing it cuts the past statements again.
//
// The figures are not what GetBalances says for the closing date. A balance goes by
// the day a transaction happened, and a statement by the day it was posted: where
// the two differ around a closing date, as with a purchase on the closing day that
// the bank put on the next statement, the balance of the closing date includes it
// and the statement does not. Reconcile compares the balance of the day with what
// the bank says it holds; to compare with the statement the bank sends, compare
// its closing balance with Closing here.
//
// An account that does not exist is ErrNotFound. One that is not a card, or a card
// with no closing day, has no statements: ErrNoStatements.
type GetCardStatement interface {
	GetCardStatement(ctx context.Context, in GetCardStatementInput) (CardStatement, error)
}

// GetCardStatementInput names the card and the statement.
type GetCardStatementInput struct {
	AccountID uuid.UUID
	// ClosingOn is a day of the statement asked for, usually its closing date:
	// the statement whose days include it is returned. Only the day it falls on
	// counts, as its own location reads it. nil is today, which the clock of the
	// application says: the statement that is open.
	ClosingOn *time.Time
}

// MaxStatementRangeYears is the longest range of days ListCardStatements takes, in
// years: a range of closing dates longer than that is ErrStatementRangeTooWide, so
// that a list is not as long as the days a caller can write.
const MaxStatementRangeYears = 10

// ListCardStatements lists the statements of a card whose closing date is from
// From to To, both ends included, newest first, each with its figures and without
// its transactions: read one with GetCardStatement. A range with no closing date
// in it has no statements. What a statement is and counts is as it is for
// GetCardStatement, and a statement is listed whether it has closed or not,
// including those that fall after today, which have nothing on them yet but what
// the card carries into them.
//
// The figures of all the statements are read together, so that they agree: the
// Closing of a statement is the Previous of the one after it.
//
// An account that does not exist is ErrNotFound; one that is not a card, or has no
// closing day, ErrNoStatements. A range that has no From or no To is
// ErrDateRangeRequired, one that ends before it starts ErrDateRangeReversed, and
// one longer than MaxStatementRangeYears ErrStatementRangeTooWide.
type ListCardStatements interface {
	ListCardStatements(ctx context.Context, in ListCardStatementsInput) (ListCardStatementsOutput, error)
}

// ListCardStatementsInput names the card and the range of closing dates.
type ListCardStatementsInput struct {
	AccountID uuid.UUID
	// From and To are the first and the last day a closing date may be on, both
	// required. Only the day each falls on counts, as its own location reads it.
	From time.Time
	To   time.Time
}

// ListCardStatementsOutput holds the statements, newest first.
type ListCardStatementsOutput struct {
	Statements []CardStatementSummary
}
