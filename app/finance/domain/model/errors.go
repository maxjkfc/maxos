package model

import (
	"errors"
	"fmt"
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

	// ErrNoLines is returned for an expense or an income with no category line.
	ErrNoLines = errors.New("at least one category line is required")

	// ErrAmountNotPositive is returned for an amount that is zero or negative
	// where the amount of an expense, an income, a transfer or an exchange must be
	// greater than zero.
	ErrAmountNotPositive = errors.New("the amount must be greater than zero")

	// ErrAmountZero is returned for an opening balance of nothing.
	ErrAmountZero = errors.New("the amount must not be zero")

	// ErrBaseAmountRequired is returned for an expense or an income in an
	// instrument other than the base currency that comes without the amount in the
	// base currency.
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

	// ErrNotCurrency is returned for an exchange that has an instrument which is
	// not a currency. Buying and selling such an instrument is a trade.
	ErrNotCurrency = errors.New("an exchange is between two currencies")

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
)

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
