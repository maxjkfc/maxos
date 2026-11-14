package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Header is what every kind of transaction carries besides its postings.
type Header struct {
	// ID is the id of the new transaction.
	ID uuid.UUID
	// IdempotencyKey is trimmed of the whitespace around it and must be MinKeyRunes
	// to MaxKeyRunes characters long.
	IdempotencyKey string
	// OccurredOn is the day it happened, and is required. Only the day it falls
	// on counts, as its own location reads it.
	OccurredOn time.Time
	// OccurredAt is the moment it happened, when that is known.
	OccurredAt *time.Time
	// Description and Merchant are trimmed of the whitespace around them.
	Description string
	Merchant    string
	// ProjectID, AreaID and TagIDs are ids of catalog entries. Whether they exist
	// and are in use is for the use case to check. Repeats among TagIDs are
	// dropped.
	ProjectID *uuid.UUID
	AreaID    *uuid.UUID
	TagIDs    []uuid.UUID
}

// normalizeHeader checks the header and returns it as a transaction keeps it: the
// key and the texts trimmed, the date a date, the tags in order and without
// repeats.
func normalizeHeader(h Header) (Header, error) {
	key, err := NormalizeIdempotencyKey(h.IdempotencyKey)
	if err != nil {
		return Header{}, err
	}
	if h.OccurredOn.IsZero() {
		return Header{}, ErrDateRequired
	}
	h.IdempotencyKey = key
	h.OccurredOn = DateOf(h.OccurredOn)
	h.OccurredAt = clonePtr(h.OccurredAt)
	h.Description = strings.TrimSpace(h.Description)
	h.Merchant = strings.TrimSpace(h.Merchant)
	h.ProjectID = clonePtr(h.ProjectID)
	h.AreaID = clonePtr(h.AreaID)
	h.TagIDs = normalizeTags(h.TagIDs)
	return h, nil
}

// newTransaction returns a draft of the kind with the postings, numbered from 1
// in the order given. It returns what Validate finds wrong with it, which for
// postings made by the constructors below can only be a mistake in them.
func newTransaction(kind Kind, h Header, postings []Posting) (Transaction, error) {
	for i := range postings {
		postings[i].Seq = i + 1
	}
	t := Transaction{
		ID:             h.ID,
		Kind:           kind,
		Status:         StatusDraft,
		OccurredOn:     h.OccurredOn,
		OccurredAt:     h.OccurredAt,
		Description:    h.Description,
		Merchant:       h.Merchant,
		ProjectID:      h.ProjectID,
		AreaID:         h.AreaID,
		IdempotencyKey: h.IdempotencyKey,
		TagIDs:         h.TagIDs,
		Postings:       postings,
	}
	if err := t.Validate(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// checkAccount returns ErrArchived for an archived account.
func checkAccount(a Account) error {
	if a.IsArchived() {
		return fmt.Errorf("account %q: %w", a.Name, ErrArchived)
	}
	return nil
}

// checkInstrument returns ErrArchived for an archived instrument.
func checkInstrument(i Instrument) error {
	if i.IsArchived() {
		return fmt.Errorf("instrument %s: %w", i.Code, ErrArchived)
	}
	return nil
}

// checkAmount checks an amount that must be greater than zero, and no finer than
// the instrument it is of.
func checkAmount(instrument Instrument, amount decimal.Decimal) error {
	if !amount.IsPositive() {
		return fmt.Errorf("%w: got %s", ErrAmountNotPositive, amount)
	}
	return instrument.CheckQuantity(amount)
}

// CategoryLine is one part of an expense or an income: an amount that goes to one
// category. Several lines split a payment among categories.
type CategoryLine struct {
	// Category must be in use and of the direction of the transaction.
	Category Category
	// Amount is greater than zero, and no finer than the instrument.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency, at the rate of the day: greater
	// than zero, and no finer than the base currency. It is required when the
	// instrument is not the base currency; when it is, it may be left out, and
	// must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// CategorizedParams is the input of NewExpense and NewIncome, which differ in the
// way the money goes and in the direction of the categories, and in nothing else.
type CategorizedParams struct {
	Header
	// Account is the account the money comes out of (an expense) or goes into (an
	// income). It must be in use.
	Account Account
	// Instrument is what was paid or received, and must be in use.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts are checked
	// against. It is only used for an instrument other than the base currency.
	BaseCurrency Instrument
	// Lines are the parts of the amount, at least one.
	Lines []CategoryLine
}

// NewExpense returns the draft of an expense: money out of an account, spent on
// one or more categories. Its postings are
//
//	account   -(sum of the lines)
//	expense   +line, base amount     (one per line)
func NewExpense(p CategorizedParams) (Transaction, error) {
	return newCategorized(KindExpense, p)
}

// NewIncome returns the draft of an income: money into an account, earned from
// one or more categories. Its postings are
//
//	account   +(sum of the lines)
//	income    -line, base amount     (one per line)
func NewIncome(p CategorizedParams) (Transaction, error) {
	return newCategorized(KindIncome, p)
}

func newCategorized(kind Kind, p CategorizedParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if len(p.Lines) == 0 {
		return Transaction{}, ErrNoLines
	}
	if err := checkAccount(p.Account); err != nil {
		return Transaction{}, err
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Transaction{}, err
	}

	// An expense makes its categories grow and its account shrink; an income does
	// the reverse.
	income := kind == KindIncome
	direction, book := DirectionExpense, BookExpense
	if income {
		direction, book = DirectionIncome, BookIncome
	}

	postings := make([]Posting, 1, len(p.Lines)+1) // the account's, put in once the lines are added up
	total := decimal.Zero
	for i, line := range p.Lines {
		n := i + 1
		if line.Category.IsArchived() {
			return Transaction{}, fmt.Errorf("line %d: category %q: %w", n, line.Category.Name, ErrArchived)
		}
		if line.Category.Direction != direction {
			return Transaction{}, fmt.Errorf("line %d: %w: category %q is for %s, not for %s",
				n, ErrWrongCategoryDirection, line.Category.Name, line.Category.Direction, direction)
		}
		if err := checkAmount(p.Instrument, line.Amount); err != nil {
			return Transaction{}, fmt.Errorf("line %d: %w", n, err)
		}
		base, err := baseAmountOf(p.Instrument, p.BaseCurrency, line.Amount, line.BaseAmount)
		if err != nil {
			return Transaction{}, fmt.Errorf("line %d: %w", n, err)
		}

		categoryID := line.Category.ID
		quantity, baseAmount := line.Amount, base
		if income {
			quantity, baseAmount = quantity.Neg(), baseAmount.Neg()
		}
		postings = append(postings, Posting{
			Book:         book,
			CategoryID:   &categoryID,
			InstrumentID: p.Instrument.ID,
			Quantity:     quantity,
			BaseAmount:   &baseAmount,
			Memo:         strings.TrimSpace(line.Memo),
		})
		total = total.Add(line.Amount)
	}

	accountID := p.Account.ID
	accountQuantity := total.Neg()
	if income {
		accountQuantity = total
	}
	postings[0] = Posting{
		Book:         BookAccount,
		AccountID:    &accountID,
		InstrumentID: p.Instrument.ID,
		Quantity:     accountQuantity,
	}
	return newTransaction(kind, h, postings)
}

// baseAmountOf returns the base amount of a positive amount of the instrument:
// the amount itself when the instrument is the base currency, otherwise the one
// the caller gave, which must be greater than zero and no finer than base.
func baseAmountOf(instrument, base Instrument, amount decimal.Decimal, given *decimal.Decimal) (decimal.Decimal, error) {
	if instrument.ID == BaseCurrencyID {
		if given != nil && !given.Equal(amount) {
			return decimal.Decimal{}, fmt.Errorf("%w: %s %s is in the base currency, but the base amount given is %s",
				ErrBaseAmountMismatch, amount, instrument.Code, *given)
		}
		return amount, nil
	}
	if given == nil {
		return decimal.Decimal{}, fmt.Errorf("%w: %s is not the base currency %s", ErrBaseAmountRequired, instrument.Code, base.Code)
	}
	if !given.IsPositive() {
		return decimal.Decimal{}, fmt.Errorf("%w: got %s", ErrBaseAmountNotPositive, *given)
	}
	if err := base.CheckQuantity(*given); err != nil {
		return decimal.Decimal{}, err
	}
	return *given, nil
}

// TransferParams is the input of NewTransfer.
type TransferParams struct {
	Header
	// From and To are the accounts the money leaves and arrives at. They must be
	// in use and different.
	From, To Account
	// Instrument is what moves, and must be in use. A transfer does not change
	// it: to change it, exchange.
	Instrument Instrument
	// Amount is greater than zero, and no finer than the instrument.
	Amount decimal.Decimal
}

// NewTransfer returns the draft of a transfer: an amount of an instrument moved
// from one account to another, such as a payment from the bank to a credit card.
// Its postings are
//
//	account(from)   -amount
//	account(to)     +amount
func NewTransfer(p TransferParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if p.From.ID == p.To.ID {
		return Transaction{}, fmt.Errorf("%w: %q", ErrSameAccount, p.From.Name)
	}
	for _, a := range []Account{p.From, p.To} {
		if err := checkAccount(a); err != nil {
			return Transaction{}, err
		}
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Transaction{}, err
	}
	if err := checkAmount(p.Instrument, p.Amount); err != nil {
		return Transaction{}, err
	}

	from, to := p.From.ID, p.To.ID
	return newTransaction(KindTransfer, h, []Posting{
		{Book: BookAccount, AccountID: &from, InstrumentID: p.Instrument.ID, Quantity: p.Amount.Neg()},
		{Book: BookAccount, AccountID: &to, InstrumentID: p.Instrument.ID, Quantity: p.Amount},
	})
}

// ExchangeParams is the input of NewExchange.
type ExchangeParams struct {
	Header
	// FromAccount and ToAccount are the accounts the money leaves and arrives at.
	// They must be in use, and may be the same account.
	FromAccount, ToAccount Account
	// FromInstrument and ToInstrument are what is given up and what is received.
	// They must be in use, be currencies, and be different.
	FromInstrument, ToInstrument Instrument
	// FromAmount and ToAmount are what actually changed hands; the rate is what
	// they make. Each is greater than zero, and no finer than its instrument.
	FromAmount, ToAmount decimal.Decimal
}

// NewExchange returns the draft of an exchange: an amount of one currency given
// up for an amount of another. A conversion posting for each instrument keeps
// each one balanced by itself. Its postings are
//
//	account(from)   -from amount   (from instrument)
//	conversion      +from amount   (from instrument)
//	conversion      -to amount     (to instrument)
//	account(to)     +to amount     (to instrument)
func NewExchange(p ExchangeParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	for _, i := range []Instrument{p.FromInstrument, p.ToInstrument} {
		if !i.IsCurrency {
			return Transaction{}, fmt.Errorf("%w: %s is not a currency", ErrNotCurrency, i.Code)
		}
	}
	if p.FromInstrument.ID == p.ToInstrument.ID {
		return Transaction{}, fmt.Errorf("%w: both are %s", ErrSameInstrument, p.FromInstrument.Code)
	}
	for _, a := range []Account{p.FromAccount, p.ToAccount} {
		if err := checkAccount(a); err != nil {
			return Transaction{}, err
		}
	}
	for _, i := range []Instrument{p.FromInstrument, p.ToInstrument} {
		if err := checkInstrument(i); err != nil {
			return Transaction{}, err
		}
	}
	if err := checkAmount(p.FromInstrument, p.FromAmount); err != nil {
		return Transaction{}, fmt.Errorf("amount given up: %w", err)
	}
	if err := checkAmount(p.ToInstrument, p.ToAmount); err != nil {
		return Transaction{}, fmt.Errorf("amount received: %w", err)
	}

	from, to := p.FromAccount.ID, p.ToAccount.ID
	return newTransaction(KindExchange, h, []Posting{
		{Book: BookAccount, AccountID: &from, InstrumentID: p.FromInstrument.ID, Quantity: p.FromAmount.Neg()},
		{Book: BookConversion, InstrumentID: p.FromInstrument.ID, Quantity: p.FromAmount},
		{Book: BookConversion, InstrumentID: p.ToInstrument.ID, Quantity: p.ToAmount.Neg()},
		{Book: BookAccount, AccountID: &to, InstrumentID: p.ToInstrument.ID, Quantity: p.ToAmount},
	})
}

// OpeningBalanceParams is the input of NewOpeningBalance.
type OpeningBalanceParams struct {
	Header
	// Account and Instrument must be in use.
	Account    Account
	Instrument Instrument
	// Amount is what the account holds of the instrument to begin with: not zero,
	// and no finer than the instrument. It is negative for an account that owes.
	Amount decimal.Decimal
}

// NewOpeningBalance returns the draft of an opening balance: what an account
// holds of an instrument before its first transaction. Its postings are
//
//	account   +amount
//	opening   -amount
func NewOpeningBalance(p OpeningBalanceParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if err := checkAccount(p.Account); err != nil {
		return Transaction{}, err
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Transaction{}, err
	}
	if p.Amount.IsZero() {
		return Transaction{}, ErrAmountZero
	}
	if err := p.Instrument.CheckQuantity(p.Amount); err != nil {
		return Transaction{}, err
	}

	accountID := p.Account.ID
	return newTransaction(KindOpening, h, []Posting{
		{Book: BookAccount, AccountID: &accountID, InstrumentID: p.Instrument.ID, Quantity: p.Amount},
		{Book: BookOpening, InstrumentID: p.Instrument.ID, Quantity: p.Amount.Neg()},
	})
}
