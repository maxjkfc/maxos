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
	// KindReward is money or credit that a payment instrument gives back.
	KindReward Kind = "reward"
	// KindDealing is a change in what a party owes me, paid out of or into an
	// account.
	KindDealing Kind = "dealing"
	// KindReconciliation makes up for the difference a reconciliation finds
	// between what an account holds and what the ledger says.
	KindReconciliation Kind = "reconciliation"
	// KindInstalment is a change in how what an account owes is paid, which is
	// part of an instalment plan: a period of the plan being charged to the
	// account, or the origin of a bill instalment (帳單分期), which turns what the
	// account already owes into a plan.
	KindInstalment Kind = "instalment"
	// KindTrade is buying or selling an instrument that is not a currency, for a
	// currency.
	KindTrade Kind = "trade"
)

// Valid reports whether k is one of the kinds above.
func (k Kind) Valid() bool {
	switch k {
	case KindExpense, KindIncome, KindTransfer, KindExchange, KindOpening,
		KindReward, KindDealing, KindReconciliation, KindInstalment, KindTrade:
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
	// BookDealing is what a party owes me: the posting is positive when they owe
	// me more, and negative when they owe me less, or when I owe them.
	BookDealing Book = "dealing"
	// BookReward is the rewards that payment instruments give back. Like an
	// income, it grows by a negative posting.
	BookReward Book = "reward"
	// BookAdjustment is the counterpart of the reconciliation of an instrument
	// that is not a currency: the quantity that was missing or too much.
	BookAdjustment Book = "adjustment"
	// BookInstalment is the principal of an instalment plan that has not been
	// charged to its account yet, the account that the plan charges. It is
	// negative while the account owes it without having been charged: the origin of
	// the plan makes the whole principal so, and each period that falls due takes
	// its principal out of it with a positive posting.
	BookInstalment Book = "instalment"
)

// Valid reports whether b is one of the books above.
func (b Book) Valid() bool {
	switch b {
	case BookAccount, BookExpense, BookIncome, BookConversion, BookOpening,
		BookDealing, BookReward, BookAdjustment, BookInstalment:
		return true
	}
	return false
}

// carriesAccount reports whether a posting of the book names an account.
func (b Book) carriesAccount() bool {
	return b == BookAccount || b == BookInstalment
}

// carriesCategory reports whether a posting of the book names a category.
func (b Book) carriesCategory() bool {
	return b == BookExpense || b == BookIncome
}

// carriesBase reports whether a posting of the book has a base amount.
func (b Book) carriesBase() bool {
	return b == BookExpense || b == BookIncome || b == BookReward
}

// Posting is one movement of one instrument within a transaction. The postings
// of a transaction add up to zero in each instrument: money leaves somewhere and
// arrives somewhere.
type Posting struct {
	// Seq is the position of the posting in its transaction, from 1.
	Seq  int
	Book Book
	// AccountID is the account of an account posting or an instalment posting, and
	// nil for every other book.
	AccountID *uuid.UUID
	// CategoryID is the category of an expense or an income posting, and nil for
	// every other book.
	CategoryID *uuid.UUID
	// PartyID is the id of the catalog party of a dealing posting, and nil for
	// every other book. Whether the party exists and is in use is for the use case
	// to check.
	PartyID      *uuid.UUID
	InstrumentID uuid.UUID
	// Quantity is how much of the instrument moves, and is never zero. The sign is
	// that of the thing the posting is made to: positive when it grows (an account
	// holds more, an expense happens, a party owes me more), negative when it
	// shrinks (an income or a reward happens, a party owes me less). An instalment
	// posting is negative when principal becomes owed without being charged yet,
	// and positive when a period charges it.
	Quantity decimal.Decimal
	// BaseAmount is Quantity in the base currency, at the rate of the day of the
	// transaction and fixed from then on. It has the sign of Quantity. An expense,
	// an income or a reward posting has one, and every other posting has none. For
	// an instrument that is the base currency it equals Quantity.
	BaseAmount *decimal.Decimal
	// Discount is what the merchant took off the price at checkout, in the
	// instrument of the posting, and is greater than zero. Only an expense posting
	// may have one, and it is only a record of it: Quantity is what was paid, and
	// no balance or total counts the discount.
	Discount *decimal.Decimal
	Memo     string
}

// validate checks what a posting must satisfy by itself: that it belongs to a
// book, has the fields its book needs and no others, moves something, and that
// its base amount and discount are consistent ones.
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
		{"an account", p.AccountID != nil, p.Book.carriesAccount()},
		{"a category", p.CategoryID != nil, p.Book.carriesCategory()},
		{"a party", p.PartyID != nil, p.Book == BookDealing},
		{"a base amount", p.BaseAmount != nil, p.Book.carriesBase()},
	}
	for _, f := range fields {
		switch {
		case f.required && !f.set:
			return fmt.Errorf("%w: the %s posting needs %s", ErrMalformedPosting, p.Book, f.name)
		case !f.required && f.set:
			return fmt.Errorf("%w: the %s posting must not have %s", ErrMalformedPosting, p.Book, f.name)
		}
	}
	if p.Discount != nil {
		if p.Book != BookExpense {
			return fmt.Errorf("%w: the %s posting must not have a discount", ErrMalformedPosting, p.Book)
		}
		if !p.Discount.IsPositive() {
			return fmt.Errorf("%w: the discount %s of the %s posting must be greater than zero",
				ErrMalformedPosting, *p.Discount, p.Book)
		}
	}
	if !p.Book.carriesBase() {
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
	// IdempotencyKey identifies the request that made the transaction: asking
	// again with the same key returns the transaction made the first time.
	IdempotencyKey string
	// TagIDs are ids of catalog tags, without repeats and ordered by id.
	TagIDs   []uuid.UUID
	Postings []Posting
	// PlanID and PlanSeq say which instalment plan the transaction is part of and
	// where in it. A plan is the transactions that share a PlanID: PlanSeq 0 is its
	// origin (the purchase that is paid in instalments, or the conversion of what an
	// account owes), whose id is the PlanID, and 1, 2, ... are its periods, in the
	// order they fall due. A transaction that is in no plan has neither: both are
	// nil, or both are set.
	PlanID  *uuid.UUID
	PlanSeq *int
	// CreatedAt, ConfirmedAt and VoidedAt are assigned when the transaction is
	// stored, confirmed and voided; they are zero (nil) before then.
	CreatedAt   time.Time
	ConfirmedAt *time.Time
	VoidedAt    *time.Time
}

// Validate checks the invariants of the ledger on the transaction, and returns
// the first it breaks:
//
//   - every posting has the fields its book needs and no others, a discount only
//     if it is an expense posting, and a base amount that has the sign of its
//     quantity (ErrMalformedPosting), and equals it for the base currency
//     (ErrBaseAmountMismatch);
//   - only a reconciliation uses a system category (ErrSystemCategory);
//   - the transaction is in a plan only where it can be: an instalment always is,
//     as the origin of a bill instalment or as a period, and the expense that is
//     paid in instalments is as the origin of its plan; the plan id and the
//     position are both set or both left out, and the origin, which is the position
//     0, is the transaction the plan id names (ErrMalformedPlan);
//   - the postings are the ones the kind is made of (ErrWrongPostings,
//     ErrSameAccount, ErrDuplicateParty): an expense is one account posting and
//     one or more expense postings, and a dealing posting for each party it was
//     paid for, or, when a party paid, one dealing posting in place of the account
//     posting, or, when it is paid in instalments, one instalment posting in place
//     of it; an income is one account posting and one or more income postings; a
//     transfer is two account postings of different accounts; an exchange and a
//     trade two account and two conversion postings of two instruments; a dealing
//     an account posting and a dealing posting; a reward an account posting and a
//     reward posting; an opening balance an account posting and an opening
//     posting; a reconciliation an account posting and the posting that makes up
//     for it, an expense or an income of the system category of its direction, or
//     an adjustment; the origin of a bill instalment an account posting and an
//     instalment posting of the same account; a period of a plan an instalment
//     posting and an account posting of the same account, and an expense posting
//     for its interest if it has any. An income, a transfer, an exchange, a trade,
//     a dealing, a reward and the origin of a bill instalment may also have fees,
//     each an expense posting and the account posting that pays it;
//   - each posting moves money the way the kind says: an expense takes money out
//     of its account, an income puts money into it, a transfer, an exchange and a
//     trade do both, a period charges its account and makes less principal
//     unbilled, and the origin of a bill instalment is the other way round
//     (ErrWrongSign);
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
		if p.CategoryID != nil && IsSystemCategory(*p.CategoryID) && t.Kind != KindReconciliation {
			return fmt.Errorf("posting %d: %w: category %s is for reconciliations", p.Seq, ErrSystemCategory, *p.CategoryID)
		}
	}
	if err := t.checkPlan(); err != nil {
		return err
	}
	if err := t.checkKind(); err != nil {
		return err
	}
	return t.checkBalanced()
}

// IsPlanOrigin reports whether the transaction is the origin of an instalment
// plan: the one the plan is named after, which comes first.
func (t Transaction) IsPlanOrigin() bool {
	return t.PlanSeq != nil && *t.PlanSeq == 0
}

// IsPlanPeriod reports whether the transaction is a period of an instalment plan,
// which falls due on its day and is not acted on without the rest of the plan.
func (t Transaction) IsPlanPeriod() bool {
	return t.PlanSeq != nil && *t.PlanSeq > 0
}

// checkPlan checks where the transaction stands in an instalment plan, which for
// most transactions is nowhere.
func (t Transaction) checkPlan() error {
	if (t.PlanID == nil) != (t.PlanSeq == nil) {
		return fmt.Errorf("%w: the plan id and the position in the plan are both set or both left out", ErrMalformedPlan)
	}
	if t.PlanID == nil {
		if t.Kind == KindInstalment {
			return fmt.Errorf("%w: an instalment is part of a plan", ErrMalformedPlan)
		}
		return nil
	}
	switch {
	case *t.PlanSeq < 0:
		return fmt.Errorf("%w: the position in the plan is %d, not 0 or more", ErrMalformedPlan, *t.PlanSeq)
	case t.IsPlanOrigin() && *t.PlanID != t.ID:
		return fmt.Errorf("%w: the origin of a plan is the transaction the plan id names", ErrMalformedPlan)
	case t.IsPlanPeriod() && *t.PlanID == t.ID:
		return fmt.Errorf("%w: a period of a plan is not the transaction the plan id names", ErrMalformedPlan)
	case t.Kind == KindExpense && t.IsPlanPeriod():
		return fmt.Errorf("%w: only the origin of a plan is an expense", ErrMalformedPlan)
	case t.Kind != KindExpense && t.Kind != KindInstalment:
		return fmt.Errorf("%w: a transaction of the kind %s is not part of a plan", ErrMalformedPlan, t.Kind)
	}
	return nil
}

// checkKind checks that the postings are the ones the kind is made of, and move
// money the way it does. Besides the expense, the opening balance, the
// reconciliation and a period of an instalment plan, a kind is checked on the
// postings that are left once its fees are taken out.
func (t Transaction) checkKind() error {
	switch t.Kind {
	case KindExpense:
		return t.checkExpense()
	case KindOpening:
		return t.checkOpening()
	case KindReconciliation:
		return t.checkReconciliation()
	case KindInstalment:
		if t.IsPlanPeriod() {
			return t.checkInstalmentPeriod()
		}
	}

	t.Postings = t.withoutFees()
	switch t.Kind {
	case KindIncome:
		return t.checkIncome()
	case KindTransfer:
		return t.checkTransfer()
	case KindExchange:
		return t.checkSwap("an exchange")
	case KindTrade:
		return t.checkSwap("a trade")
	case KindDealing:
		return t.checkDealing()
	case KindReward:
		return t.checkReward()
	case KindInstalment:
		return t.checkBillInstalment()
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

// withoutFees returns the postings of the transaction that are not fees. A fee is
// a positive expense posting without a discount, in a kind that has no expense of
// its own, together with the account posting that pays it: the same instrument,
// the same quantity taken out. The constructors put each fee after the postings
// the kind is made of, so when several account postings could pay one, the last is
// the one that does. An expense posting that nothing pays is not taken for a fee:
// it stays, and the kind refuses it.
func (t Transaction) withoutFees() []Posting {
	taken := make([]bool, len(t.Postings))
	for i, fee := range t.Postings {
		if fee.Book != BookExpense || !fee.Quantity.IsPositive() || fee.Discount != nil {
			continue
		}
		paid := fee.Quantity.Neg()
		for j := len(t.Postings) - 1; j >= 0; j-- {
			p := t.Postings[j]
			if !taken[j] && p.Book == BookAccount && p.InstrumentID == fee.InstrumentID && p.Quantity.Equal(paid) {
				taken[i], taken[j] = true, true
				break
			}
		}
	}
	core := make([]Posting, 0, len(t.Postings))
	for i, p := range t.Postings {
		if !taken[i] {
			core = append(core, p)
		}
	}
	return core
}

// checkExpense checks an expense: who paid and where the money went. The money
// went to one or more expense postings. Either an account paid, with one account
// posting that takes it out, or an account is paying in instalments, with one
// instalment posting in place of it, which makes the expense the origin of its
// instalment plan, or a party paid on my behalf, with one dealing posting that
// takes it out of what the party owes me and no account posting. An account that
// paid, in instalments or not, may have paid for others too: each one it paid for
// has a dealing posting, who owes me that much, and a party has at most one.
func (t Transaction) checkExpense() error {
	books := t.booksOf()
	accounts, instalments := books[BookAccount], books[BookInstalment]
	expenses, dealings := books[BookExpense], books[BookDealing]
	var payer *Posting
	switch {
	case len(accounts) == 1:
		payer = &accounts[0]
	case len(instalments) == 1:
		payer = &instalments[0]
	}
	payers := len(accounts) + len(instalments)
	if payers > 1 || len(expenses) == 0 || payers+len(expenses)+len(dealings) != len(t.Postings) {
		return fmt.Errorf("%w: an expense is one account posting, or one instalment posting when it is paid in instalments, or one dealing posting when a party paid, and one or more expense postings",
			ErrWrongPostings)
	}
	if (len(instalments) == 1) != t.IsPlanOrigin() {
		return fmt.Errorf("%w: an expense is paid in instalments, with an instalment posting, if and only if it is the origin of an instalment plan",
			ErrWrongPostings)
	}
	for _, p := range expenses {
		if p.Quantity.IsNegative() {
			return fmt.Errorf("%w: the expense postings of an expense must be positive", ErrWrongSign)
		}
	}
	if payer != nil {
		if !payer.Quantity.IsNegative() {
			return fmt.Errorf("%w: the %s posting of an expense must be negative", ErrWrongSign, payer.Book)
		}
		for _, p := range dealings {
			if p.Quantity.IsNegative() {
				return fmt.Errorf("%w: the dealing postings of an expense paid from an account must be positive", ErrWrongSign)
			}
		}
	} else {
		if len(dealings) != 1 {
			return fmt.Errorf("%w: an expense with no account posting has one dealing posting, of the party that paid", ErrWrongPostings)
		}
		if !dealings[0].Quantity.IsNegative() {
			return fmt.Errorf("%w: the dealing posting of an expense paid by a party must be negative", ErrWrongSign)
		}
	}
	seen := make(map[uuid.UUID]struct{}, len(dealings))
	for _, p := range dealings {
		if _, repeated := seen[*p.PartyID]; repeated {
			return fmt.Errorf("%w: party %s has two dealing postings", ErrDuplicateParty, *p.PartyID)
		}
		seen[*p.PartyID] = struct{}{}
	}
	return nil
}

// checkIncome checks an income: one account posting and one or more income
// postings, nothing else. An income puts money into the account and makes its
// categories shrink.
func (t Transaction) checkIncome() error {
	books := t.booksOf()
	accounts, incomes := books[BookAccount], books[BookIncome]
	if len(accounts) != 1 || len(incomes) == 0 || len(accounts)+len(incomes) != len(t.Postings) {
		return fmt.Errorf("%w: an income is one account posting and one or more income postings", ErrWrongPostings)
	}
	if accounts[0].Quantity.IsNegative() {
		return fmt.Errorf("%w: the account posting of an income must be positive", ErrWrongSign)
	}
	for _, p := range incomes {
		if !p.Quantity.IsNegative() {
			return fmt.Errorf("%w: the income postings of an income must be negative", ErrWrongSign)
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

// checkSwap checks what an exchange and a trade are made of, which is the same:
// for each of two instruments an account posting and a conversion posting, the
// account of one losing and the account of the other gaining. what says which of
// the two it is, for the message.
func (t Transaction) checkSwap(what string) error {
	books := t.booksOf()
	accounts, conversions := books[BookAccount], books[BookConversion]
	if len(accounts) != 2 || len(conversions) != 2 || len(t.Postings) != 4 {
		return fmt.Errorf("%w: %s is two account postings and two conversion postings", ErrWrongPostings, what)
	}
	if accounts[0].InstrumentID == accounts[1].InstrumentID {
		return fmt.Errorf("%w: %s is between two instruments", ErrWrongPostings, what)
	}
	if conversions[0].InstrumentID == conversions[1].InstrumentID {
		return fmt.Errorf("%w: each instrument of %s has one conversion posting", ErrWrongPostings, what)
	}
	for _, c := range conversions {
		if c.InstrumentID != accounts[0].InstrumentID && c.InstrumentID != accounts[1].InstrumentID {
			return fmt.Errorf("%w: a conversion posting must be of an instrument of the accounts", ErrWrongPostings)
		}
	}
	if accounts[0].Quantity.Sign() == accounts[1].Quantity.Sign() {
		return fmt.Errorf("%w: %s takes one instrument out of an account and puts the other into one", ErrWrongSign, what)
	}
	return nil
}

// checkBillInstalment checks the origin of a bill instalment (帳單分期): an
// account posting that pays off what the account owes, and the instalment posting
// of the same account that makes it owed again as principal that is not charged
// yet, to be charged by the periods of the plan.
func (t Transaction) checkBillInstalment() error {
	books := t.booksOf()
	accounts, instalments := books[BookAccount], books[BookInstalment]
	if len(accounts) != 1 || len(instalments) != 1 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: the origin of a bill instalment is one account posting and one instalment posting", ErrWrongPostings)
	}
	account, instalment := accounts[0], instalments[0]
	if *account.AccountID != *instalment.AccountID || account.InstrumentID != instalment.InstrumentID {
		return fmt.Errorf("%w: the account posting and the instalment posting of a bill instalment are of the same account and instrument", ErrWrongPostings)
	}
	if !account.Quantity.IsPositive() {
		return fmt.Errorf("%w: the account posting of a bill instalment must be positive: what the account owed is paid off", ErrWrongSign)
	}
	if !instalment.Quantity.IsNegative() {
		return fmt.Errorf("%w: the instalment posting of a bill instalment must be negative: the principal is owed and not yet charged", ErrWrongSign)
	}
	return nil
}

// checkInstalmentPeriod checks a period of an instalment plan, as it falls due:
// its principal stops being unbilled (an instalment posting) and is charged to
// the account (an account posting), together with the interest of the period,
// which is an expense of its own (an expense posting) when it has any. Both of
// the first two are of the account the plan charges.
func (t Transaction) checkInstalmentPeriod() error {
	books := t.booksOf()
	instalments, accounts, expenses := books[BookInstalment], books[BookAccount], books[BookExpense]
	if len(instalments) != 1 || len(accounts) != 1 || len(expenses) > 1 ||
		len(instalments)+len(accounts)+len(expenses) != len(t.Postings) {
		return fmt.Errorf("%w: a period of an instalment plan is one instalment posting, one account posting, and one expense posting when it has interest",
			ErrWrongPostings)
	}
	instalment, account := instalments[0], accounts[0]
	if *instalment.AccountID != *account.AccountID || instalment.InstrumentID != account.InstrumentID {
		return fmt.Errorf("%w: the instalment posting and the account posting of a period are of the same account and instrument", ErrWrongPostings)
	}
	if !instalment.Quantity.IsPositive() {
		return fmt.Errorf("%w: the instalment posting of a period must be positive: its principal stops being unbilled", ErrWrongSign)
	}
	if !account.Quantity.IsNegative() {
		return fmt.Errorf("%w: the account posting of a period must be negative: the principal and the interest are charged to it", ErrWrongSign)
	}
	if len(expenses) == 1 && !expenses[0].Quantity.IsPositive() {
		return fmt.Errorf("%w: the expense posting for the interest of a period must be positive", ErrWrongSign)
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

// checkDealing checks a dealing: an account posting and a dealing posting. Money
// paid out of the account makes what the party owes me grow, and money paid into
// it makes it shrink.
func (t Transaction) checkDealing() error {
	books := t.booksOf()
	accounts, dealings := books[BookAccount], books[BookDealing]
	if len(accounts) != 1 || len(dealings) != 1 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: a dealing is one account posting and one dealing posting", ErrWrongPostings)
	}
	if accounts[0].Quantity.Sign() == dealings[0].Quantity.Sign() {
		return fmt.Errorf("%w: a dealing takes money out of the account and grows the dealing, or puts it into the account and shrinks the dealing", ErrWrongSign)
	}
	return nil
}

// checkReward checks a reward: an account posting that puts money into the
// account, and the reward posting that mirrors it.
func (t Transaction) checkReward() error {
	books := t.booksOf()
	accounts, rewards := books[BookAccount], books[BookReward]
	if len(accounts) != 1 || len(rewards) != 1 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: a reward is one account posting and one reward posting", ErrWrongPostings)
	}
	if !accounts[0].Quantity.IsPositive() {
		return fmt.Errorf("%w: the account posting of a reward must be positive", ErrWrongSign)
	}
	if !rewards[0].Quantity.IsNegative() {
		return fmt.Errorf("%w: the reward posting of a reward must be negative", ErrWrongSign)
	}
	return nil
}

// checkReconciliation checks a reconciliation: an account posting and the posting
// that makes up for what it adds or takes away. A currency that is short is an
// expense of the system category of expenses, and one that is over is an income
// of the system category of incomes; any other instrument has an adjustment.
func (t Transaction) checkReconciliation() error {
	accounts := t.booksOf()[BookAccount]
	if len(accounts) != 1 || len(t.Postings) != 2 {
		return fmt.Errorf("%w: a reconciliation is one account posting and the posting that makes up for it", ErrWrongPostings)
	}
	account := accounts[0]
	counterpart := t.Postings[0]
	if counterpart.Book == BookAccount {
		counterpart = t.Postings[1]
	}
	switch counterpart.Book {
	case BookExpense:
		if *counterpart.CategoryID != UnrecordedExpenseCategoryID {
			return fmt.Errorf("%w: the expense of a reconciliation is of the system category %s, not %s",
				ErrWrongPostings, UnrecordedExpenseCategoryID, *counterpart.CategoryID)
		}
		if !counterpart.Quantity.IsPositive() {
			return fmt.Errorf("%w: the expense of a reconciliation must be positive", ErrWrongSign)
		}
	case BookIncome:
		if *counterpart.CategoryID != UnrecordedIncomeCategoryID {
			return fmt.Errorf("%w: the income of a reconciliation is of the system category %s, not %s",
				ErrWrongPostings, UnrecordedIncomeCategoryID, *counterpart.CategoryID)
		}
		if !counterpart.Quantity.IsNegative() {
			return fmt.Errorf("%w: the income of a reconciliation must be negative", ErrWrongSign)
		}
	case BookAdjustment:
	default:
		return fmt.Errorf("%w: a reconciliation is made up for by an expense, an income or an adjustment, not a %s posting",
			ErrWrongPostings, counterpart.Book)
	}
	if account.Quantity.Sign() == counterpart.Quantity.Sign() {
		return fmt.Errorf("%w: the posting that makes up for a reconciliation has the other sign than its account posting", ErrWrongSign)
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
