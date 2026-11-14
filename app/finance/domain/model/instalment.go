package model

import (
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// InstalmentPeriod is one period of an instalment plan: the day it falls due and
// what is charged to the account on it.
type InstalmentPeriod struct {
	// ID is the id of the transaction that charges the period.
	ID uuid.UUID
	// DueOn is the day the period is charged to the account, which is the day of its
	// transaction. Only the day it falls on counts, as its own location reads it.
	DueOn time.Time
	// Principal is what the period pays off of the amount that is split: greater
	// than zero, and no finer than the instrument. It is not an expense.
	Principal decimal.Decimal
	// Interest is what the institution charges on top of the principal in this
	// period, when it charges any: greater than zero, and no finer than the
	// instrument. It is an expense, booked on the day it is charged.
	Interest *decimal.Decimal
	// InterestBaseAmount is Interest in the base currency, at the rate of the day
	// it is charged: greater than zero, and no finer than the base currency. It is
	// required when there is Interest and the instrument is not the base currency;
	// when it is, it may be left out, and must equal Interest if given. A period
	// with no Interest has none.
	InterestBaseAmount *decimal.Decimal
}

// InstalmentSchedule is when an instalment plan is charged, and how much: its
// periods, which are set when the plan is made.
type InstalmentSchedule struct {
	// Periods are the periods in the order they fall due, at least one. They fall
	// due on later and later days, the first not before the day of the origin of
	// the plan, and their principals add up exactly to the amount that is split.
	Periods []InstalmentPeriod
	// InterestCategory is the category the interest of the periods is booked to. It
	// is required when a period has interest, and must be an expense category in
	// use, and not a system category.
	InterestCategory *Category
}

// InstalmentPlan is a plan as the transactions it is made of: its origin, and the
// transactions of its periods in the order they fall due. There is no more to it:
// the schedule is the periods themselves.
type InstalmentPlan struct {
	// Origin is the purchase that is paid in instalments, or the conversion of what
	// an account owes into a plan. Its id is the id of the plan.
	Origin Transaction
	// Periods are the transactions that charge each period, the first at position 1.
	Periods []Transaction
}

// Transactions returns the origin and then each period, which is the order they
// are stored in: a period refers to the origin.
func (p InstalmentPlan) Transactions() []Transaction {
	out := make([]Transaction, 0, 1+len(p.Periods))
	out = append(out, p.Origin)
	return append(out, p.Periods...)
}

// periodKeyPrefix starts the idempotency key of every period of an instalment
// plan, and no key a caller gives (NormalizeIdempotencyKey).
const periodKeyPrefix = "instalment:"

// PeriodIdempotencyKey is the idempotency key of the transaction of a period of a
// plan: made of the id of the plan and the position of the period in it, so that
// it is the same whenever it is worked out, and cannot be that of another period.
// Callers never give it; they give a key for the origin, and
// NormalizeIdempotencyKey refuses a key in this form (ErrReservedIdempotencyKey),
// so that no request is answered with a period as a duplicate.
func PeriodIdempotencyKey(planID uuid.UUID, seq int) string {
	return periodKeyPrefix + planID.String() + ":" + strconv.Itoa(seq)
}

// check checks the schedule of a plan that splits amount, an amount of the
// instrument, from the day of its origin. base is the base currency, which the
// base amounts of interest are checked against.
func (s InstalmentSchedule) check(originDay time.Time, instrument, base Instrument, amount decimal.Decimal) error {
	if len(s.Periods) == 0 {
		return ErrNoPeriods
	}
	if c := s.InterestCategory; c != nil {
		switch {
		case c.IsArchived():
			return fmt.Errorf("interest category %q: %w", c.Name, ErrArchived)
		case c.IsSystem():
			return fmt.Errorf("interest category %q: %w", c.Name, ErrSystemCategory)
		case c.Direction != DirectionExpense:
			return fmt.Errorf("%w: category %q is for %s, and interest is an expense", ErrWrongCategoryDirection, c.Name, c.Direction)
		}
	}

	principals := decimal.Zero
	hasInterest := false
	var previous time.Time
	for i, period := range s.Periods {
		n := i + 1
		if period.DueOn.IsZero() {
			return fmt.Errorf("period %d: %w", n, ErrDateRequired)
		}
		due := DateOf(period.DueOn)
		switch {
		case i == 0 && due.Before(originDay):
			return fmt.Errorf("period 1: %w: it falls due on %s, and the origin is on %s",
				ErrFirstDueTooEarly, due.Format(time.DateOnly), originDay.Format(time.DateOnly))
		case i > 0 && !due.After(previous):
			return fmt.Errorf("period %d: %w: it falls due on %s, and the one before it on %s",
				n, ErrDueDatesNotIncreasing, due.Format(time.DateOnly), previous.Format(time.DateOnly))
		}
		previous = due

		if err := checkAmount(instrument, period.Principal); err != nil {
			return fmt.Errorf("period %d: principal: %w", n, err)
		}
		principals = principals.Add(period.Principal)

		switch {
		case period.Interest != nil:
			if err := checkAmount(instrument, *period.Interest); err != nil {
				return fmt.Errorf("period %d: interest: %w", n, err)
			}
			if _, err := baseAmountOf(instrument, base, *period.Interest, period.InterestBaseAmount); err != nil {
				return fmt.Errorf("period %d: interest: %w", n, err)
			}
			hasInterest = true
		case period.InterestBaseAmount != nil:
			return fmt.Errorf("period %d: %w", n, ErrInterestBaseAmountWithoutInterest)
		}
	}
	if hasInterest && s.InterestCategory == nil {
		return ErrInterestCategoryRequired
	}
	if !principals.Equal(amount) {
		return fmt.Errorf("%w: they add up to %s, and %s %s is split", ErrPrincipalMismatch, principals, amount, instrument.Code)
	}
	return nil
}

// periods returns the transactions of the periods of the plan whose origin has the
// header h, one for each period of the schedule, which check has accepted. Each is
// a draft of the kind instalment that falls due on its day, and carries the
// project, area, texts and tags of the origin: a plan is one purchase, so what the
// periods belong to is what it does, and not what happens to be going on the days
// they fall due. Their keys are made of the plan and the position.
//
//	instalment(account)   +principal
//	account(account)      -(principal and interest)
//	expense(category)     +interest, base amount           (only when it has interest)
func (s InstalmentSchedule) periods(h Header, accountID uuid.UUID, instrument, base Instrument) ([]Transaction, error) {
	periods := make([]Transaction, len(s.Periods))
	for i, period := range s.Periods {
		seq := i + 1
		ph := h
		ph.ID = period.ID
		ph.OccurredOn = period.DueOn
		ph.OccurredAt = nil
		ph, err := normalizeHeader(ph)
		if err != nil {
			return nil, fmt.Errorf("period %d: %w", seq, err)
		}
		// The key is made after the header is checked, as it is the ledger's own and
		// not one a caller gave, which is what the check refuses.
		ph.IdempotencyKey = PeriodIdempotencyKey(h.ID, seq)

		charged := period.Principal
		postings := make([]Posting, 2, 3)
		postings[0] = Posting{Book: BookInstalment, AccountID: new(accountID), InstrumentID: instrument.ID, Quantity: period.Principal}
		if period.Interest != nil {
			baseAmount, err := baseAmountOf(instrument, base, *period.Interest, period.InterestBaseAmount)
			if err != nil {
				return nil, fmt.Errorf("period %d: interest: %w", seq, err)
			}
			categoryID := s.InterestCategory.ID
			charged = charged.Add(*period.Interest)
			postings = append(postings, Posting{
				Book:         BookExpense,
				CategoryID:   &categoryID,
				InstrumentID: instrument.ID,
				Quantity:     *period.Interest,
				BaseAmount:   &baseAmount,
			})
		}
		postings[1] = Posting{Book: BookAccount, AccountID: new(accountID), InstrumentID: instrument.ID, Quantity: charged.Neg()}

		periods[i], err = newPlanTransaction(KindInstalment, ph, &planRef{id: h.ID, seq: seq}, postings)
		if err != nil {
			return nil, fmt.Errorf("period %d: %w", seq, err)
		}
	}
	return periods, nil
}

// newPlan returns the plan whose origin is the draft of the kind with the
// postings and the header h (which is normalized), of the account, splitting
// amount of the instrument as the schedule says.
func newPlan(kind Kind, h Header, postings []Posting, accountID uuid.UUID, instrument, base Instrument, amount decimal.Decimal, s InstalmentSchedule) (InstalmentPlan, error) {
	if err := s.check(h.OccurredOn, instrument, base, amount); err != nil {
		return InstalmentPlan{}, err
	}
	origin, err := newPlanTransaction(kind, h, &planRef{id: h.ID, seq: 0}, postings)
	if err != nil {
		return InstalmentPlan{}, err
	}
	periods, err := s.periods(h, accountID, instrument, base)
	if err != nil {
		return InstalmentPlan{}, err
	}
	return InstalmentPlan{Origin: origin, Periods: periods}, nil
}

// NewInstalmentExpense returns the drafts of an expense that is paid in
// instalments (消費分期): the whole of it is an expense on the day of the
// purchase, and the account that paid does not pay it at once but owes it, and is
// charged the principal period by period. The amount that is split is what the
// account would have paid at once, the lines and the shares together. The origin
// of the plan is the expense, dated the day of the purchase, with an instalment
// posting of the account in the place of the account posting:
//
//	instalment(account)   -(sum of the lines and of the shares)
//	expense               +line, base amount, discount     (one per line)
//	dealing               +share                           (one per party it was paid for)
//
// and each period is what InstalmentSchedule.periods says. The principal that a
// period charges is never an expense, and the interest is one on the day it is
// charged. A party cannot pay in instalments (ErrInstalmentsWithPaidBy), and the
// instrument must be a currency (ErrNotCurrency).
func NewInstalmentExpense(p ExpenseParams, s InstalmentSchedule) (InstalmentPlan, error) {
	switch {
	case p.PaidBy != nil:
		return InstalmentPlan{}, ErrInstalmentsWithPaidBy
	case p.Account == nil:
		return InstalmentPlan{}, ErrNoPayer
	case !p.Instrument.IsCurrency:
		return InstalmentPlan{}, fmt.Errorf("%w: an instalment plan is counted in a currency, and %s is not one", ErrNotCurrency, p.Instrument.Code)
	}
	h, postings, total, err := expensePostings(p, BookInstalment)
	if err != nil {
		return InstalmentPlan{}, err
	}
	return newPlan(KindExpense, h, postings, p.Account.ID, p.Instrument, p.BaseCurrency, total, s)
}

// BillInstalmentParams is the input of NewBillInstalment.
type BillInstalmentParams struct {
	// Header is the header of the origin of the plan, and its date is the day of the
	// conversion.
	Header
	// Account is the account that owes what is turned into a plan, and is charged the
	// periods. It must be in use.
	Account Account
	// Instrument is what is owed, and must be a currency in use.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts of the fees and of
	// the interest are checked against. It is only used for an instrument other
	// than the base currency.
	BaseCurrency Instrument
	// Principal is the amount of what the account owes that is turned into a plan:
	// greater than zero, and no finer than the instrument.
	Principal decimal.Decimal
	// Schedule is the periods of the plan. Their principals add up to Principal.
	Schedule InstalmentSchedule
	// Fees are charges that came with the conversion, each an expense of its own.
	Fees []Fee
}

// NewBillInstalment returns the drafts of a bill instalment (帳單分期): what an
// account already owes is split into periods, from the day of the conversion. It
// only changes how the debt is paid, and is not an expense: the account is paid
// off at once and the same amount is owed again, as principal that is not charged
// yet. The origin of the plan is an instalment dated the day of the conversion,
// whose postings are
//
//	account(account)      +principal
//	instalment(account)   -principal
//
// followed, for each fee, by the two postings of a fee (see Fee). Each period is
// what InstalmentSchedule.periods says. The instrument must be a currency
// (ErrNotCurrency).
func NewBillInstalment(p BillInstalmentParams) (InstalmentPlan, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return InstalmentPlan{}, err
	}
	if err := checkAccount(p.Account); err != nil {
		return InstalmentPlan{}, err
	}
	if !p.Instrument.IsCurrency {
		return InstalmentPlan{}, fmt.Errorf("%w: an instalment plan is counted in a currency, and %s is not one", ErrNotCurrency, p.Instrument.Code)
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return InstalmentPlan{}, err
	}
	if err := checkAmount(p.Instrument, p.Principal); err != nil {
		return InstalmentPlan{}, err
	}
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return InstalmentPlan{}, err
	}

	postings := make([]Posting, 0, 2+len(fees))
	postings = append(postings,
		Posting{Book: BookAccount, AccountID: new(p.Account.ID), InstrumentID: p.Instrument.ID, Quantity: p.Principal},
		Posting{Book: BookInstalment, AccountID: new(p.Account.ID), InstrumentID: p.Instrument.ID, Quantity: p.Principal.Neg()},
	)
	postings = append(postings, fees...)
	return newPlan(KindInstalment, h, postings, p.Account.ID, p.Instrument, p.BaseCurrency, p.Principal, p.Schedule)
}

// InstalmentPlanSummary is where an instalment plan stands on a day: how much it
// splits, and how much of that has been charged to its account by then. It is
// worked out when asked for and never stored.
type InstalmentPlanSummary struct {
	// PlanID is the id of the plan, which is the id of its origin.
	PlanID uuid.UUID
	// AccountID is the account the plan charges.
	AccountID uuid.UUID
	// InstrumentID is what the plan is counted in.
	InstrumentID uuid.UUID
	// Principal is the amount the plan splits, greater than zero.
	Principal decimal.Decimal
	// Charged is the principal of the periods that fell due on or before the day,
	// which is what has reached the balance of the account.
	Charged decimal.Decimal
	// NextDueOn is the first day after the day that a period falls due, as a date at
	// midnight UTC; nil when none does.
	NextDueOn *time.Time
}

// Remaining is the principal of the plan that has not fallen due on the day, which
// is still to be charged to the account (未到期分期), as a size: zero or more. The
// balance of the account has it with the sign of what is owed.
func (s InstalmentPlanSummary) Remaining() decimal.Decimal {
	return s.Principal.Sub(s.Charged)
}

// Finished reports whether nothing is left of the plan to charge on the day.
func (s InstalmentPlanSummary) Finished() bool {
	return s.Remaining().IsZero()
}
