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
	return newPlanTransaction(kind, h, nil, postings)
}

// planRef is a place in an instalment plan: the plan, which is the id of its
// origin, and the position in it.
type planRef struct {
	id  uuid.UUID
	seq int
}

// newPlanTransaction is newTransaction for a transaction that is part of an
// instalment plan, at the place plan says; plan is nil for one that is in none.
func newPlanTransaction(kind Kind, h Header, plan *planRef, postings []Posting) (Transaction, error) {
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
	if plan != nil {
		t.PlanID, t.PlanSeq = new(plan.id), new(plan.seq)
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
	// Category must be in use, of the direction of the transaction, and not a
	// system category: only a reconciliation records to those.
	Category Category
	// Amount is greater than zero, and no finer than the instrument. For an
	// expense it is what was paid, after the discount.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency, at the rate of the day: greater
	// than zero, and no finer than the base currency. It is required when the
	// instrument is not the base currency; when it is, it may be left out, and
	// must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Discount is what the merchant took off the price at checkout, in the
	// instrument: greater than zero, and no finer than the instrument. Only a line
	// of an expense may have one (ErrDiscountNotAllowed), and it is only a record
	// of it: the expense is Amount.
	Discount *decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// Share is a part of a payment that I made for someone else, who owes it to me.
type Share struct {
	// PartyID is the party that owes the amount, an id of a catalog entry. Whether
	// it exists and is in use is for the use case to check. A party has one share
	// in an expense.
	PartyID uuid.UUID
	// Amount is greater than zero, and no finer than the instrument of the
	// expense. It is not part of any category: the expense is only the lines.
	Amount decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// ExpenseParams is the input of NewExpense.
type ExpenseParams struct {
	Header
	// Account is the account the money comes out of, and must be in use. It is nil
	// when a party paid: exactly one of Account and PaidBy is set.
	Account *Account
	// PaidBy is the party that paid on my behalf, so that I owe them what the lines
	// come to: an id of a catalog entry, which the use case checks. Exactly one of
	// Account and PaidBy is set, and a party that paid has no Shares.
	PaidBy *uuid.UUID
	// Instrument is what was paid, and must be in use. It must be a currency when
	// a party paid or there are Shares: what a party owes is counted in a currency.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts are checked
	// against. It is only used for an instrument other than the base currency.
	BaseCurrency Instrument
	// Lines are the parts of the expense that are mine, at least one: an expense
	// with none is not an expense, but a dealing.
	Lines []CategoryLine
	// Shares are the parts that I paid for others, if any, in the instrument of
	// the expense.
	Shares []Share
}

// NewExpense returns the draft of an expense: money spent on one or more
// categories, paid from an account or by a party. When an account paid, which
// may also have paid for others, its postings are
//
//	account   -(sum of the lines and of the shares)
//	expense   +line, base amount, discount     (one per line)
//	dealing   +share                           (one per party it was paid for)
//
// and when a party paid, there is no account and no share, and I owe them:
//
//	dealing   -(sum of the lines)
//	expense   +line, base amount, discount     (one per line)
func NewExpense(p ExpenseParams) (Transaction, error) {
	h, postings, _, err := expensePostings(p, BookAccount)
	if err != nil {
		return Transaction{}, err
	}
	return newTransaction(KindExpense, h, postings)
}

// expensePostings checks the expense and returns its header as a transaction
// keeps it, its postings, and what the payer pays: the sum of the lines and of
// the shares. The posting of the payer is the first. An account that pays is
// charged in chargedTo: BookAccount when it pays at once, and BookInstalment for
// the origin of an instalment plan, where the same amount is owed and not yet
// charged to it. A party that paid has a dealing posting in its place, whatever
// chargedTo is.
func expensePostings(p ExpenseParams, chargedTo Book) (Header, []Posting, decimal.Decimal, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Header{}, nil, decimal.Zero, err
	}
	switch {
	case p.Account == nil && p.PaidBy == nil:
		return Header{}, nil, decimal.Zero, ErrNoPayer
	case p.Account != nil && p.PaidBy != nil:
		return Header{}, nil, decimal.Zero, ErrTwoPayers
	case p.PaidBy != nil && len(p.Shares) > 0:
		return Header{}, nil, decimal.Zero, ErrSharesWithPaidBy
	}
	if len(p.Lines) == 0 {
		if len(p.Shares) > 0 {
			return Header{}, nil, decimal.Zero, fmt.Errorf("%w: money spent only for others is a dealing, not an expense", ErrNoLines)
		}
		return Header{}, nil, decimal.Zero, ErrNoLines
	}
	if (len(p.Shares) > 0 || p.PaidBy != nil) && !p.Instrument.IsCurrency {
		return Header{}, nil, decimal.Zero, fmt.Errorf("%w: a dealing is counted in a currency, and %s is not one", ErrNotCurrency, p.Instrument.Code)
	}
	if p.Account != nil {
		if err := checkAccount(*p.Account); err != nil {
			return Header{}, nil, decimal.Zero, err
		}
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Header{}, nil, decimal.Zero, err
	}

	lines, total, err := categoryPostings(KindExpense, p.Instrument, p.BaseCurrency, p.Lines)
	if err != nil {
		return Header{}, nil, decimal.Zero, err
	}
	postings := make([]Posting, 1, 1+len(lines)+len(p.Shares)) // the payer's, put in once the shares are added up
	postings = append(postings, lines...)

	seen := make(map[uuid.UUID]struct{}, len(p.Shares))
	for i, share := range p.Shares {
		n := i + 1
		if _, repeated := seen[share.PartyID]; repeated {
			return Header{}, nil, decimal.Zero, fmt.Errorf("share %d: %w: party %s", n, ErrDuplicateParty, share.PartyID)
		}
		seen[share.PartyID] = struct{}{}
		if err := checkAmount(p.Instrument, share.Amount); err != nil {
			return Header{}, nil, decimal.Zero, fmt.Errorf("share %d: %w", n, err)
		}
		partyID := share.PartyID
		postings = append(postings, Posting{
			Book:         BookDealing,
			PartyID:      &partyID,
			InstrumentID: p.Instrument.ID,
			Quantity:     share.Amount,
			Memo:         strings.TrimSpace(share.Memo),
		})
		total = total.Add(share.Amount)
	}

	payer := Posting{InstrumentID: p.Instrument.ID, Quantity: total.Neg()}
	if p.Account != nil {
		accountID := p.Account.ID
		payer.Book, payer.AccountID = chargedTo, &accountID
	} else {
		partyID := *p.PaidBy
		payer.Book, payer.PartyID = BookDealing, &partyID
	}
	postings[0] = payer
	return h, postings, total, nil
}

// IncomeParams is the input of NewIncome.
type IncomeParams struct {
	Header
	// Account is the account the money goes into. It must be in use.
	Account Account
	// Instrument is what was received, and must be in use.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts are checked
	// against. It is only used for an instrument other than the base currency.
	BaseCurrency Instrument
	// Lines are the parts of the amount, at least one.
	Lines []CategoryLine
	// Fees are charges that came with the income, each an expense of its own.
	Fees []Fee
}

// NewIncome returns the draft of an income: money into an account, earned from
// one or more categories. Its postings are
//
//	account   +(sum of the lines)
//	income    -line, base amount     (one per line)
//
// and, for each fee, the two postings of a fee (see Fee).
func NewIncome(p IncomeParams) (Transaction, error) {
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

	lines, total, err := categoryPostings(KindIncome, p.Instrument, p.BaseCurrency, p.Lines)
	if err != nil {
		return Transaction{}, err
	}
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	accountID := p.Account.ID
	postings := make([]Posting, 0, 1+len(lines)+len(fees))
	postings = append(postings, Posting{Book: BookAccount, AccountID: &accountID, InstrumentID: p.Instrument.ID, Quantity: total})
	postings = append(postings, lines...)
	postings = append(postings, fees...)
	return newTransaction(KindIncome, h, postings)
}

// categoryPostings checks the lines of an expense or an income and returns their
// postings, in the direction of the kind (an expense makes its categories grow,
// an income makes them shrink), and the amounts of the lines added up.
func categoryPostings(kind Kind, instrument, base Instrument, lines []CategoryLine) ([]Posting, decimal.Decimal, error) {
	income := kind == KindIncome
	direction, book := DirectionExpense, BookExpense
	if income {
		direction, book = DirectionIncome, BookIncome
	}

	postings := make([]Posting, 0, len(lines))
	total := decimal.Zero
	for i, line := range lines {
		n := i + 1
		if line.Category.IsArchived() {
			return nil, decimal.Zero, fmt.Errorf("line %d: category %q: %w", n, line.Category.Name, ErrArchived)
		}
		if line.Category.IsSystem() {
			return nil, decimal.Zero, fmt.Errorf("line %d: category %q: %w", n, line.Category.Name, ErrSystemCategory)
		}
		if line.Category.Direction != direction {
			return nil, decimal.Zero, fmt.Errorf("line %d: %w: category %q is for %s, not for %s",
				n, ErrWrongCategoryDirection, line.Category.Name, line.Category.Direction, direction)
		}
		if err := checkAmount(instrument, line.Amount); err != nil {
			return nil, decimal.Zero, fmt.Errorf("line %d: %w", n, err)
		}
		baseAmount, err := baseAmountOf(instrument, base, line.Amount, line.BaseAmount)
		if err != nil {
			return nil, decimal.Zero, fmt.Errorf("line %d: %w", n, err)
		}
		var discount *decimal.Decimal
		if line.Discount != nil {
			if income {
				return nil, decimal.Zero, fmt.Errorf("line %d: %w", n, ErrDiscountNotAllowed)
			}
			if err := checkAmount(instrument, *line.Discount); err != nil {
				return nil, decimal.Zero, fmt.Errorf("line %d: discount: %w", n, err)
			}
			discount = clonePtr(line.Discount)
		}

		categoryID := line.Category.ID
		quantity := line.Amount
		if income {
			quantity, baseAmount = quantity.Neg(), baseAmount.Neg()
		}
		postings = append(postings, Posting{
			Book:         book,
			CategoryID:   &categoryID,
			InstrumentID: instrument.ID,
			Quantity:     quantity,
			BaseAmount:   &baseAmount,
			Discount:     discount,
			Memo:         strings.TrimSpace(line.Memo),
		})
		total = total.Add(line.Amount)
	}
	return postings, total, nil
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
	// BaseCurrency is the base currency, which the base amounts of the fees are
	// checked against. It is only used for a fee in an instrument other than the
	// base currency.
	BaseCurrency Instrument
	// Fees are charges that came with the transfer, each an expense of its own.
	Fees []Fee
}

// NewTransfer returns the draft of a transfer: an amount of an instrument moved
// from one account to another, such as a payment from the bank to a credit card.
// Its postings are
//
//	account(from)   -amount
//	account(to)     +amount
//
// and, for each fee, the two postings of a fee (see Fee).
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
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	from, to := p.From.ID, p.To.ID
	postings := make([]Posting, 0, 2+len(fees))
	postings = append(postings,
		Posting{Book: BookAccount, AccountID: &from, InstrumentID: p.Instrument.ID, Quantity: p.Amount.Neg()},
		Posting{Book: BookAccount, AccountID: &to, InstrumentID: p.Instrument.ID, Quantity: p.Amount},
	)
	postings = append(postings, fees...)
	return newTransaction(KindTransfer, h, postings)
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
	// BaseCurrency is the base currency, which the base amounts of the fees are
	// checked against. It is only used for a fee in an instrument other than the
	// base currency.
	BaseCurrency Instrument
	// Fees are charges that came with the exchange, each an expense of its own.
	Fees []Fee
}

// NewExchange returns the draft of an exchange: an amount of one currency given
// up for an amount of another. A conversion posting for each instrument keeps
// each one balanced by itself. Its postings are
//
//	account(from)   -from amount   (from instrument)
//	conversion      +from amount   (from instrument)
//	conversion      -to amount     (to instrument)
//	account(to)     +to amount     (to instrument)
//
// and, for each fee, the two postings of a fee (see Fee).
func NewExchange(p ExchangeParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	for _, i := range []Instrument{p.FromInstrument, p.ToInstrument} {
		if !i.IsCurrency {
			return Transaction{}, fmt.Errorf("%w: an exchange is between two currencies, and %s is not one", ErrNotCurrency, i.Code)
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
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	from, to := p.FromAccount.ID, p.ToAccount.ID
	postings := make([]Posting, 0, 4+len(fees))
	postings = append(postings,
		Posting{Book: BookAccount, AccountID: &from, InstrumentID: p.FromInstrument.ID, Quantity: p.FromAmount.Neg()},
		Posting{Book: BookConversion, InstrumentID: p.FromInstrument.ID, Quantity: p.FromAmount},
		Posting{Book: BookConversion, InstrumentID: p.ToInstrument.ID, Quantity: p.ToAmount.Neg()},
		Posting{Book: BookAccount, AccountID: &to, InstrumentID: p.ToInstrument.ID, Quantity: p.ToAmount},
	)
	postings = append(postings, fees...)
	return newTransaction(KindExchange, h, postings)
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
