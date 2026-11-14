package model

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Bounds of the idempotency key of a transaction, counted in characters (runes).
// They are the bounds of the keys of the inbox, so that one key can serve both.
const (
	MinKeyRunes = 8
	MaxKeyRunes = 128
)

// Kind is what sort of money event a transaction records. The kinds are fixed.
type Kind string

// The kinds of transaction.
const (
	KindExpense  Kind = "expense"
	KindIncome   Kind = "income"
	KindTransfer Kind = "transfer"
	KindExchange Kind = "exchange"
	KindOpening  Kind = "opening"
)

// Valid reports whether k is one of the kinds above.
func (k Kind) Valid() bool {
	switch k {
	case KindExpense, KindIncome, KindTransfer, KindExchange, KindOpening:
		return true
	}
	return false
}

// Status is where a transaction stands. A draft changes no balance; only a
// confirmed transaction counts. A confirmed transaction cannot be edited, only
// voided, and a void one counts for nothing.
type Status string

// The statuses of a transaction.
const (
	StatusDraft     Status = "draft"
	StatusConfirmed Status = "confirmed"
	StatusVoid      Status = "void"
)

// Valid reports whether s is one of the statuses above.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusConfirmed, StatusVoid:
		return true
	}
	return false
}

// Book is what a posting is made to: the thing whose amount it moves.
type Book string

// The books of a posting.
const (
	// BookAccount is an account: its balance grows by the posting.
	BookAccount Book = "account"
	// BookExpense is a category of expense: the posting is money spent on it.
	BookExpense Book = "expense"
	// BookIncome is a category of income: the posting is money earned from it.
	BookIncome Book = "income"
	// BookConversion is the go-between of an exchange, so that each instrument
	// balances by itself.
	BookConversion Book = "conversion"
	// BookOpening is where the balance an account starts with comes from.
	BookOpening Book = "opening"
)

// Valid reports whether b is one of the books above.
func (b Book) Valid() bool {
	switch b {
	case BookAccount, BookExpense, BookIncome, BookConversion, BookOpening:
		return true
	}
	return false
}

// carriesCategory reports whether a posting of the book names a category and has
// a base amount.
func (b Book) carriesCategory() bool {
	return b == BookExpense || b == BookIncome
}

// Posting is one movement of one instrument within a transaction. The postings
// of a transaction add up to zero in each instrument: money leaves somewhere and
// arrives somewhere.
type Posting struct {
	// Seq is the position of the posting in its transaction, from 1.
	Seq  int
	Book Book
	// AccountID is the account of an account posting, and nil for every other book.
	AccountID *uuid.UUID
	// CategoryID is the category of an expense or an income posting, and nil for
	// every other book.
	CategoryID   *uuid.UUID
	InstrumentID uuid.UUID
	// Quantity is how much of the instrument moves, and is never zero. The sign is
	// that of the thing the posting is made to: positive when it grows (an account
	// holds more, an expense happens), negative when it shrinks (an income
	// happens).
	Quantity decimal.Decimal
	// BaseAmount is Quantity in the base currency, at the rate of the day of the
	// transaction and fixed from then on. It has the sign of Quantity. An expense
	// or an income posting has one, and every other posting has none. For an
	// instrument that is the base currency it equals Quantity.
	BaseAmount *decimal.Decimal
	Memo       string
}

// validate checks what a posting must satisfy by itself: that it belongs to a
// book, has the fields its book needs and no others, moves something, and that
// its base amount is a consistent one.
func (p Posting) validate() error {
	if !p.Book.Valid() {
		return fmt.Errorf("%w: %q is not a book", ErrMalformedPosting, p.Book)
	}
	if p.Quantity.IsZero() {
		return fmt.Errorf("%w: the %s posting moves nothing", ErrMalformedPosting, p.Book)
	}
	fields := []struct {
		name     string
		set      bool
		required bool
	}{
		{"an account", p.AccountID != nil, p.Book == BookAccount},
		{"a category", p.CategoryID != nil, p.Book.carriesCategory()},
		{"a base amount", p.BaseAmount != nil, p.Book.carriesCategory()},
	}
	for _, f := range fields {
		switch {
		case f.required && !f.set:
			return fmt.Errorf("%w: the %s posting needs %s", ErrMalformedPosting, p.Book, f.name)
		case !f.required && f.set:
			return fmt.Errorf("%w: the %s posting must not have %s", ErrMalformedPosting, p.Book, f.name)
		}
	}
	if !p.Book.carriesCategory() {
		return nil
	}
	base := *p.BaseAmount
	if base.Sign() != p.Quantity.Sign() {
		return fmt.Errorf("%w: the base amount %s of the %s posting must have the sign of its quantity %s",
			ErrMalformedPosting, base, p.Book, p.Quantity)
	}
	if p.InstrumentID == BaseCurrencyID && !base.Equal(p.Quantity) {
		return fmt.Errorf("%w: the base amount %s of the %s posting is not its quantity %s",
			ErrBaseAmountMismatch, base, p.Book, p.Quantity)
	}
	return nil
}

// Transaction is one money event: what happened, when, and the postings that
// say where the money went. It is the unit that is confirmed, voided and
// discarded, and its postings never change after it is made.
type Transaction struct {
	ID     uuid.UUID
	Kind   Kind
	Status Status
	// OccurredOn is the day it happened, as a date at midnight UTC (see DateOf).
	OccurredOn time.Time
	// OccurredAt is the moment it happened, when it is known.
	OccurredAt  *time.Time
	Description string
	Merchant    string
	// ProjectID and AreaID are ids of catalog entries. Whether they exist and are
	// in use is for the use case to check.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	// InboxItemID is the inbox entry the transaction was made from, when there is
	// one.
	InboxItemID *uuid.UUID
	// IdempotencyKey identifies the request that made the transaction: asking
	// again with the same key returns the transaction made the first time.
	IdempotencyKey string
	// TagIDs are ids of catalog tags, without repeats and ordered by id.
	TagIDs   []uuid.UUID
	Postings []Posting
	// CreatedAt, ConfirmedAt and VoidedAt are assigned when the transaction is
	// stored, confirmed and voided; they are zero (nil) before then.
	CreatedAt   time.Time
	ConfirmedAt *time.Time
	VoidedAt    *time.Time
}

// Validate checks the invariants of the ledger on the transaction, and returns
// the first it breaks:
//
//   - every posting has the fields its book needs and no others, and a base
//     amount that has the sign of its quantity (ErrMalformedPosting), and equals
//     it for the base currency (ErrBaseAmountMismatch);
//   - the postings are the ones the kind is made of: an expense is one account
//     posting and one or more expense postings, an income the same with income
//     postings, a transfer two account postings of different accounts, an
//     exchange two account and two conversion postings of two instruments, an
//     opening balance an account posting and an opening posting (ErrWrongPostings,
//     ErrSameAccount);
//   - each posting moves money the way the kind says: an expense takes money out
//     of its account, an income puts money into it, a transfer and an exchange do
//     both (ErrWrongSign);
//   - in each instrument the postings add up to zero (ErrUnbalanced).
//
// The constructors of the kinds validate what they make, so a transaction built
// by them passes.
func (t Transaction) Validate() error {
	if !t.Kind.Valid() {
		return fmt.Errorf("%w %q", ErrInvalidKind, t.Kind)
	}
	if !t.Status.Valid() {
		return fmt.Errorf("%w %q", ErrInvalidStatus, t.Status)
	}
	if len(t.Postings) == 0 {
		return ErrNoPostings
	}
	for _, p := range t.Postings {
		if err := p.validate(); err != nil {
			return fmt.Errorf("posting %d: %w", p.Seq, err)
		}
	}
	if err := t.checkKind(); err != nil {
		return err
	}
	return t.checkBalanced()
}

// checkKind checks that the postings are the ones the kind is made of, and move
// money the way it does.
func (t Transaction) checkKind() error {
	switch t.Kind {
	case KindExpense:
		return t.checkCategorized(BookExpense)
	case KindIncome:
		return t.checkCategorized(BookIncome)
	case KindTransfer:
		return t.checkTransfer()
	case KindExchange:
		return t.checkExchange()
	case KindOpening:
		return t.checkOpening()
	default:
		return fmt.Errorf("%w %q", ErrInvalidKind, t.Kind)
	}
}

// booksOf returns the postings made to each book.
func (t Transaction) booksOf() map[Book][]Posting {
	books := make(map[Book][]Posting, 3)
	for _, p := range t.Postings {
		books[p.Book] = append(books[p.Book], p)
	}
	return books
}

// checkCategorized checks an expense or an income: one account posting and one or
// more postings of the category book, nothing else. An expense takes money out of
// the account and makes its categories grow; an income does the reverse.
func (t Transaction) checkCategorized(categoryBook Book) error {
	books := t.booksOf()
	accounts, categories := books[BookAccount], books[categoryBook]
	if len(accounts) != 1 || len(categories) == 0 || len(accounts)+len(categories) != len(t.Postings) {
		return fmt.Errorf("%w: an %s is one account posting and one or more %s postings", ErrWrongPostings, t.Kind, categoryBook)
	}
	accountLoses := categoryBook == BookExpense
	if accounts[0].Quantity.IsNegative() != accountLoses {
		return fmt.Errorf("%w: the account posting of an %s must be %s", ErrWrongSign, t.Kind, signWord(accountLoses))
	}
	for _, p := range categories {
		if p.Quantity.IsNegative() == accountLoses {
			return fmt.Errorf("%w: the %s postings of an %s must be %s", ErrWrongSign, categoryBook, t.Kind, signWord(!accountLoses))
		}
	}
	return nil
}

// checkTransfer checks a transfer: two account postings, one taking money out of
// an account and one putting it into another.
func (t Transaction) checkTransfer() error {
	accounts := t.booksOf()[BookAccount]
	if len(accounts) != 2 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: a transfer is two account postings", ErrWrongPostings)
	}
	if accounts[0].Quantity.Sign() == accounts[1].Quantity.Sign() {
		return fmt.Errorf("%w: a transfer takes money out of one account and puts it into the other", ErrWrongSign)
	}
	if *accounts[0].AccountID == *accounts[1].AccountID {
		return fmt.Errorf("%w: both postings are of account %s", ErrSameAccount, *accounts[0].AccountID)
	}
	return nil
}

// checkExchange checks an exchange: for each of two instruments an account
// posting and a conversion posting, the account of one losing and the account of
// the other gaining.
func (t Transaction) checkExchange() error {
	books := t.booksOf()
	accounts, conversions := books[BookAccount], books[BookConversion]
	if len(accounts) != 2 || len(conversions) != 2 || len(t.Postings) != 4 {
		return fmt.Errorf("%w: an exchange is two account postings and two conversion postings", ErrWrongPostings)
	}
	if accounts[0].InstrumentID == accounts[1].InstrumentID {
		return fmt.Errorf("%w: an exchange is between two instruments", ErrWrongPostings)
	}
	if conversions[0].InstrumentID == conversions[1].InstrumentID {
		return fmt.Errorf("%w: each instrument of an exchange has one conversion posting", ErrWrongPostings)
	}
	for _, c := range conversions {
		if c.InstrumentID != accounts[0].InstrumentID && c.InstrumentID != accounts[1].InstrumentID {
			return fmt.Errorf("%w: a conversion posting must be of an instrument of the accounts", ErrWrongPostings)
		}
	}
	if accounts[0].Quantity.Sign() == accounts[1].Quantity.Sign() {
		return fmt.Errorf("%w: an exchange takes one instrument out of an account and puts the other into one", ErrWrongSign)
	}
	return nil
}

// checkOpening checks an opening balance: an account posting and the opening
// posting that mirrors it. The balance may be negative, for an account that owes.
func (t Transaction) checkOpening() error {
	books := t.booksOf()
	if len(books[BookAccount]) != 1 || len(books[BookOpening]) != 1 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: an opening balance is one account posting and one opening posting", ErrWrongPostings)
	}
	return nil
}

// checkBalanced checks the first invariant of the ledger: in each instrument the
// quantities of the postings add up to zero.
func (t Transaction) checkBalanced() error {
	sums := make(map[uuid.UUID]decimal.Decimal, 2)
	var order []uuid.UUID // the instruments, as they first appear
	for _, p := range t.Postings {
		sum, seen := sums[p.InstrumentID]
		if !seen {
			order = append(order, p.InstrumentID)
		}
		sums[p.InstrumentID] = sum.Add(p.Quantity)
	}
	for _, id := range order {
		if sum := sums[id]; !sum.IsZero() {
			return fmt.Errorf("%w: the postings of instrument %s add up to %s, not zero", ErrUnbalanced, id, sum)
		}
	}
	return nil
}

// signWord names the sign a posting must have, for an error message.
func signWord(negative bool) string {
	if negative {
		return "negative"
	}
	return "positive"
}

// Confirm returns the transaction confirmed: from then on it counts toward the
// balances. Only a draft can be confirmed; the other statuses return ErrNotDraft.
// The time it is confirmed at is assigned when the change is stored.
func (t Transaction) Confirm() (Transaction, error) {
	if t.Status != StatusDraft {
		return Transaction{}, fmt.Errorf("%w: transaction %s is %s, only a draft can be confirmed", ErrNotDraft, t.ID, t.Status)
	}
	t.Status = StatusConfirmed
	return t, nil
}

// Void returns the transaction void: it stops counting, and stays as a record
// of what was wrong. Only a confirmed transaction can be voided, since a draft
// never counted and is discarded instead; the other statuses return
// ErrNotConfirmed. The time it is voided at is assigned when the change is
// stored.
func (t Transaction) Void() (Transaction, error) {
	if t.Status != StatusConfirmed {
		return Transaction{}, fmt.Errorf("%w: transaction %s is %s, only a confirmed transaction can be voided", ErrNotConfirmed, t.ID, t.Status)
	}
	t.Status = StatusVoid
	return t, nil
}

// CheckDiscardable returns nil when the transaction may be discarded, that is
// deleted: only a draft may, since a confirmed transaction has counted and is
// voided instead. The others return ErrNotDraft.
func (t Transaction) CheckDiscardable() error {
	if t.Status != StatusDraft {
		return fmt.Errorf("%w: transaction %s is %s, only a draft can be discarded", ErrNotDraft, t.ID, t.Status)
	}
	return nil
}

// DateOf returns the calendar day t falls on, as t's own location reads it, as
// midnight UTC. Every date in the ledger has this form, so dates compare with
// Before, After and Equal, and are exactly what the database date type holds.
func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// NormalizeIdempotencyKey trims the whitespace around key and checks its length.
// It returns an *IdempotencyKeyLengthError for a key that is too short or too
// long.
func NormalizeIdempotencyKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if n := utf8.RuneCountInString(key); n < MinKeyRunes || n > MaxKeyRunes {
		return "", &IdempotencyKeyLengthError{Runes: n, Min: MinKeyRunes, Max: MaxKeyRunes}
	}
	return key, nil
}

// normalizeTags returns the ids without the repeats, ordered by id: the order the
// store gives them back in. It returns nil for no ids.
func normalizeTags(ids []uuid.UUID) []uuid.UUID {
	if len(ids) == 0 {
		return nil
	}
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return slices.Compact(out)
}

// clonePtr returns a pointer to a copy of what p points at, nil for nil.
func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
