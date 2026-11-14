package model

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

var (
	// ErrNameEmpty is returned when a name has nothing left once the whitespace
	// around it is trimmed.
	ErrNameEmpty = errors.New("name is empty")

	// ErrCodeEmpty is returned when an instrument code has nothing left once the
	// whitespace around it is trimmed.
	ErrCodeEmpty = errors.New("code is empty")

	// ErrCodeHasSpace is returned when an instrument code has whitespace inside
	// it.
	ErrCodeHasSpace = errors.New("code has whitespace in it")

	// ErrInvalidInstrumentKind is returned for an instrument kind that is not one
	// of the fixed ones.
	ErrInvalidInstrumentKind = errors.New("invalid instrument kind")

	// ErrDecimalsOutOfRange is returned for an instrument whose decimals are not
	// from MinDecimals to MaxDecimals.
	ErrDecimalsOutOfRange = errors.New("decimals out of range")

	// ErrTooManyDecimals is returned for a quantity that has more decimal places
	// than its instrument allows.
	ErrTooManyDecimals = errors.New("more decimal places than the instrument allows")

	// ErrBaseCurrency is returned when the base currency is to be archived.
	ErrBaseCurrency = errors.New("the base currency cannot be archived")

	// ErrInvalidAccountKind is returned for an account kind that is not one of
	// the fixed ones.
	ErrInvalidAccountKind = errors.New("invalid account kind")

	// ErrInvalidDirection is returned for a category direction that is neither
	// expense nor income.
	ErrInvalidDirection = errors.New("invalid category direction")

	// ErrCategoryTooDeep is returned for a category that would go under a
	// subcategory: categories have two levels at most.
	ErrCategoryTooDeep = errors.New("categories have two levels at most")

	// ErrCategoryDirectionMismatch is returned for a subcategory whose direction
	// is not the one of its parent.
	ErrCategoryDirectionMismatch = errors.New("a subcategory has the direction of its parent")

	// ErrNotFound is returned when no entry has the id asked for. Repos return
	// it and use cases pass it on.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned when the name (the code, for an instrument) of
	// an entry is already taken.
	ErrAlreadyExists = errors.New("already exists")

	// ErrArchived is returned when an archived entry is used where only an entry
	// in use will do.
	ErrArchived = errors.New("archived")
)

// The errors of the ledger: what a transaction, its postings and its changes of
// status can be refused for. Each names one thing the caller can put right.
var (
	// ErrDateRequired is returned for a transaction with no date.
	ErrDateRequired = errors.New("the date of the transaction is required")

	// ErrNoLines is returned for an expense or an income with no category line. An
	// expense that was only paid for others has none either: that is a dealing.
	ErrNoLines = errors.New("at least one category line is required")

	// ErrAmountNotPositive is returned for an amount that is zero or negative
	// where it must be greater than zero: the amount of an expense, an income, a
	// transfer, an exchange, a dealing, a reward, a share or a fee, and a discount.
	ErrAmountNotPositive = errors.New("the amount must be greater than zero")

	// ErrAmountZero is returned for an opening balance of nothing, and for a
	// difference of nothing to reconcile.
	ErrAmountZero = errors.New("the amount must not be zero")

	// ErrBaseAmountRequired is returned for an expense, an income, a reward or a
	// fee in an instrument other than the base currency that comes without the
	// amount in the base currency, and for the difference of a reconciliation of
	// such a currency (see DifferenceBaseAmountRequiredError).
	ErrBaseAmountRequired = errors.New("the amount in the base currency is required")

	// ErrBaseAmountNotPositive is returned for an amount in the base currency that
	// is zero or negative.
	ErrBaseAmountNotPositive = errors.New("the amount in the base currency must be greater than zero")

	// ErrBaseAmountMismatch is returned when the amount in the base currency of an
	// amount that is in the base currency is not that amount.
	ErrBaseAmountMismatch = errors.New("the amount in the base currency must equal the amount")

	// ErrWrongCategoryDirection is returned for a category of the other direction
	// than the transaction: an expense takes expense categories, an income takes
	// income categories.
	ErrWrongCategoryDirection = errors.New("the category has the wrong direction for this kind of transaction")

	// ErrSameAccount is returned for a transfer from an account to itself.
	ErrSameAccount = errors.New("a transfer needs two different accounts")

	// ErrNotCurrency is returned for an exchange, a dealing, or an expense that has
	// shares or was paid by a party, whose instrument is not a currency: what a
	// party owes is counted in a currency. Buying and selling such an instrument is
	// a trade.
	ErrNotCurrency = errors.New("the instrument must be a currency")

	// ErrSameInstrument is returned for an exchange between an instrument and
	// itself. Moving an instrument between accounts is a transfer.
	ErrSameInstrument = errors.New("an exchange needs two different currencies")

	// ErrInvalidKind is returned for a transaction kind that is not one of the
	// fixed ones.
	ErrInvalidKind = errors.New("invalid transaction kind")

	// ErrInvalidStatus is returned for a transaction status that is not draft,
	// confirmed or void.
	ErrInvalidStatus = errors.New("invalid transaction status")

	// ErrNoPostings is returned for a transaction that has no postings.
	ErrNoPostings = errors.New("a transaction needs postings")

	// ErrMalformedPosting is returned for a posting that does not fit its book:
	// without what the book needs, with what it must not have, or moving nothing.
	ErrMalformedPosting = errors.New("malformed posting")

	// ErrWrongPostings is returned for postings that are not the ones the kind of
	// the transaction is made of.
	ErrWrongPostings = errors.New("the postings do not fit the kind of the transaction")

	// ErrWrongSign is returned for a posting that moves money the wrong way for
	// the kind of the transaction.
	ErrWrongSign = errors.New("a posting has the wrong sign")

	// ErrUnbalanced is returned for a transaction whose postings do not add up to
	// zero in some instrument.
	ErrUnbalanced = errors.New("the postings do not add up to zero")

	// ErrNotDraft is returned for confirming or discarding a transaction that is
	// not a draft.
	ErrNotDraft = errors.New("the transaction is not a draft")

	// ErrNotConfirmed is returned for voiding a transaction that is not confirmed.
	ErrNotConfirmed = errors.New("the transaction is not confirmed")

	// ErrNoTransactions is returned for asking to confirm no transaction at all.
	ErrNoTransactions = errors.New("no transactions were given")

	// ErrDateRangeReversed is returned for a date range that ends before it
	// starts.
	ErrDateRangeReversed = errors.New("the end of the date range is before its start")

	// ErrInvalidPage is returned for a page of a list that has a negative size or
	// offset.
	ErrInvalidPage = errors.New("the page size and offset must not be negative")

	// ErrNoPayer is returned for an expense that does not say who paid: neither
	// the account the money came out of nor the party that paid.
	ErrNoPayer = errors.New("an expense needs the account it is paid from, or the party that paid it")

	// ErrTwoPayers is returned for an expense that says both the account the money
	// came out of and the party that paid.
	ErrTwoPayers = errors.New("an expense is paid from an account or by a party, not both")

	// ErrSharesWithPaidBy is returned for an expense that a party paid and that has
	// shares: only what I pay from an account can be paid for others.
	ErrSharesWithPaidBy = errors.New("an expense paid by a party has no shares")

	// ErrDuplicateParty is returned for an expense that has the same party twice.
	ErrDuplicateParty = errors.New("a party can be in an expense only once")

	// ErrInvalidDealingDirection is returned for a dealing whose direction is
	// neither paid out nor received.
	ErrInvalidDealingDirection = errors.New("invalid dealing direction")

	// ErrDiscountNotAllowed is returned for a discount on a line of an income:
	// only an expense has one.
	ErrDiscountNotAllowed = errors.New("only an expense can have a discount")

	// ErrSystemCategory is returned for a system category that is used in a line
	// or a fee, that is to be archived, or that is to be the parent of a new
	// category: the ledger keeps them for reconciliations.
	ErrSystemCategory = errors.New("a system category is for reconciliations only, and is never archived or divided into subcategories")

	// ErrBaseAmountNotAllowed is returned for the difference of the reconciliation
	// of an instrument that is not a currency that comes with an amount in the base
	// currency: only a currency has one.
	ErrBaseAmountNotAllowed = errors.New("the amount in the base currency is only for a currency")

	// ErrPendingDrafts is returned, as a *PendingDraftsError, for reconciling an
	// account that has drafts waiting: what it holds is not known until they are
	// confirmed or discarded.
	ErrPendingDrafts = errors.New("drafts are waiting to be confirmed or discarded")
)

// PendingDraftsError is what reconciling an account returns while drafts that
// touch it, in the instrument and up to the day, are still drafts. It is an
// ErrPendingDrafts.
type PendingDraftsError struct {
	// Count is how many drafts there are.
	Count int
}

func (e *PendingDraftsError) Error() string {
	return fmt.Sprintf("%d draft(s) are waiting to be confirmed or discarded", e.Count)
}

func (e *PendingDraftsError) Unwrap() error { return ErrPendingDrafts }

// DifferenceBaseAmountRequiredError is what the reconciliation of an account in a
// currency other than the base currency returns when it finds a difference and
// comes without that difference in the base currency. It carries the difference,
// so that the caller can work out its amount in the base currency and ask again.
// It is an ErrBaseAmountRequired.
type DifferenceBaseAmountRequiredError struct {
	// InstrumentCode is the code of the currency.
	InstrumentCode string
	// Difference is what the account holds minus what the ledger says it holds: it
	// is negative when the account holds less.
	Difference decimal.Decimal
}

func (e *DifferenceBaseAmountRequiredError) Error() string {
	return fmt.Sprintf("%v: the difference is %s %s, and its amount in the base currency is needed to record it",
		ErrBaseAmountRequired, e.Difference, e.InstrumentCode)
}

func (e *DifferenceBaseAmountRequiredError) Unwrap() error { return ErrBaseAmountRequired }

// IdempotencyKeyLengthError is returned when the idempotency key of a
// transaction is shorter than MinKeyRunes or longer than MaxKeyRunes.
type IdempotencyKeyLengthError struct {
	// Runes is the length of the rejected key, in characters.
	Runes int
	// Min and Max are the shortest and longest keys allowed, in characters.
	Min, Max int
}

func (e *IdempotencyKeyLengthError) Error() string {
	return fmt.Sprintf("idempotency key must be %d to %d characters (got %d)", e.Min, e.Max, e.Runes)
}
