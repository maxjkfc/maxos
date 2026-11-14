package model

import (
	"fmt"
	"strconv"
	"strings"
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
// base amounts of interest are checked against. inUse is the category the interest
// of the periods a plan has already is booked to, for a plan that is rescheduled
// and not nil: it is allowed though it has been archived since, which no other
// category is; nil for a plan that is not made yet.
func (s InstalmentSchedule) check(originDay time.Time, instrument, base Instrument, amount decimal.Decimal, inUse *uuid.UUID) error {
	if len(s.Periods) == 0 {
		return ErrNoPeriods
	}
	if c := s.InterestCategory; c != nil {
		switch {
		case c.IsArchived() && (inUse == nil || *inUse != c.ID):
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
		return &PrincipalMismatchError{Principals: principals, Amount: amount, InstrumentCode: instrument.Code}
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
	if err := s.check(h.OccurredOn, instrument, base, amount, nil); err != nil {
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

// InstrumentID returns the instrument the plan is counted in, which the instalment
// posting of its origin is of, and false for an origin that has none.
func (p InstalmentPlan) InstrumentID() (uuid.UUID, bool) {
	posting, ok := p.Origin.instalmentPosting()
	return posting.InstrumentID, ok
}

// instalmentPosting returns the instalment posting of the transaction: that of the
// origin of a plan holds what is split, with its sign, and that of a period what it
// charges.
func (t Transaction) instalmentPosting() (Posting, bool) {
	for _, p := range t.Postings {
		if p.Book == BookInstalment {
			return p, true
		}
	}
	return Posting{}, false
}

// scheduled is a period of a plan as its transaction says it: the day it falls due,
// its principal, and, when it has interest, the interest, the interest in the base
// currency and the category it is booked to.
type scheduled struct {
	dueOn                  time.Time
	principal              decimal.Decimal
	interest, interestBase *decimal.Decimal
	category               *uuid.UUID
}

func scheduledOf(t Transaction) scheduled {
	out := scheduled{dueOn: DateOf(t.OccurredOn)}
	for _, p := range t.Postings {
		switch p.Book {
		case BookInstalment:
			out.principal = p.Quantity
		case BookExpense:
			interest := p.Quantity
			out.interest, out.interestBase, out.category = &interest, clonePtr(p.BaseAmount), clonePtr(p.CategoryID)
		}
	}
	return out
}

// interestCategory returns the category the interest of the periods of the plan is
// booked to, and nil for a plan whose periods have none.
func (p InstalmentPlan) interestCategory() *uuid.UUID {
	for _, period := range p.Periods {
		if s := scheduledOf(period); s.category != nil {
			return s.category
		}
	}
	return nil
}

// RescheduleParams is what InstalmentPlan.Reschedule takes besides the plan.
type RescheduleParams struct {
	// Instrument is what the plan is counted in, and BaseCurrency the base currency.
	Instrument, BaseCurrency Instrument
	// Schedule is the new schedule, which replaces the old as a whole.
	Schedule InstalmentSchedule
	// Today is the day it is. The periods of the plan that fall due before it are
	// its past; one that falls due today is not.
	Today time.Time
	// ChangePast lets the new schedule be other than the old in the past: without
	// it, the periods of the new schedule that fall due before today must be the
	// periods of the plan that fell due before it, one for one, each with the day,
	// principal, interest and interest in the base currency it had, and the interest
	// must be booked to the category it was; or it is a *PastPeriodsChangedError.
	ChangePast bool
}

// checkPast returns a *PastPeriodsChangedError unless the new schedule keeps what
// fell due before today: the periods of the schedule that fall due before it are
// those of the plan that fell due before it, in the same order, the same in day,
// principal, interest and interest in the base currency, and, when any of them has
// interest, the category of the interest is the same.
func (p InstalmentPlan) checkPast(r RescheduleParams) error {
	today := DateOf(r.Today)
	var old []scheduled
	for _, period := range p.Periods {
		if s := scheduledOf(period); s.dueOn.Before(today) {
			old = append(old, s)
		}
	}
	var next []InstalmentPeriod
	for _, period := range r.Schedule.Periods {
		if DateOf(period.DueOn).Before(today) {
			next = append(next, period)
		}
	}

	err := &PastPeriodsChangedError{Today: today}
	for i := range max(len(old), len(next)) {
		switch {
		case i >= len(next):
			err.Differences = append(err.Differences, PastPeriodDifference{Seq: i + 1, DueOn: old[i].dueOn, Change: "dropped"})
		case i >= len(old):
			err.Differences = append(err.Differences, PastPeriodDifference{Seq: i + 1, DueOn: DateOf(next[i].DueOn), Change: "added"})
		default:
			if changes := changesOf(old[i], next[i], r.Instrument, r.BaseCurrency); len(changes) > 0 {
				err.Differences = append(err.Differences,
					PastPeriodDifference{Seq: i + 1, DueOn: old[i].dueOn, Change: "changed: " + strings.Join(changes, ", ")})
			}
		}
	}
	for _, s := range old {
		if s.interest != nil {
			err.InterestCategoryChanged = r.Schedule.InterestCategory == nil || r.Schedule.InterestCategory.ID != *s.category
			break
		}
	}
	if len(err.Differences) == 0 && !err.InterestCategoryChanged {
		return nil
	}
	return err
}

// changesOf says what of a period the new schedule has otherwise than the plan: its
// day, principal, interest, and the interest in the base currency.
func changesOf(old scheduled, next InstalmentPeriod, instrument, base Instrument) []string {
	var changes []string
	if !DateOf(next.DueOn).Equal(old.dueOn) {
		changes = append(changes, "day")
	}
	if !next.Principal.Equal(old.principal) {
		changes = append(changes, "principal")
	}
	switch {
	case (old.interest == nil) != (next.Interest == nil), old.interest != nil && !old.interest.Equal(*next.Interest):
		changes = append(changes, "interest")
	case old.interest != nil:
		// The schedule was checked, so its base amount is there to be worked out.
		if got, err := baseAmountOf(instrument, base, *next.Interest, next.InterestBaseAmount); err != nil || old.interestBase == nil || !got.Equal(*old.interestBase) {
			changes = append(changes, "interest in the base currency")
		}
	}
	return changes
}

// Reschedule returns the plan with a new schedule: the origin as it is, and a
// transaction for each period of the schedule in the place of those the plan had,
// which are not in what is returned. Each of the new periods is a draft made as the
// periods of a plan are when it is recorded (InstalmentSchedule.periods): it has
// the day, the principal and the interest the schedule says, the project, area,
// texts and tags of the origin, the id the schedule gives it, and the key made of
// the plan and its position. Giving them the status of the plan, and storing them
// in the place of the old ones, is for the caller.
//
// What cannot change is the origin and the amount that is split: the principals of
// the schedule must add up to the principal of the plan, which the instalment
// posting of the origin holds, or it is a *PrincipalMismatchError. To change it is
// to change the purchase, which is voided and recorded again. The schedule is
// checked as it is when a plan is recorded, from the day of the origin, except
// that the category of the interest may be one that is archived since, when it is
// the one the periods the plan has are booked to. The schedule replaces the old as
// a whole, so the periods that fell due are in it too, as they were, unless
// ChangePast gives leave to change them (see RescheduleParams). A plan that is void
// is ErrPlanVoid.
func (p InstalmentPlan) Reschedule(r RescheduleParams) (InstalmentPlan, error) {
	origin := p.Origin
	if origin.Status == StatusVoid {
		return InstalmentPlan{}, ErrPlanVoid
	}
	for _, period := range p.Periods {
		if period.Status != origin.Status {
			return InstalmentPlan{}, fmt.Errorf("%w: period %s is %s, and the origin is %s", ErrMalformedPlan, period.ID, period.Status, origin.Status)
		}
	}
	posting, ok := origin.instalmentPosting()
	if !ok || posting.AccountID == nil {
		return InstalmentPlan{}, fmt.Errorf("%w: the origin has no instalment posting of an account", ErrMalformedPlan)
	}
	if posting.InstrumentID != r.Instrument.ID {
		return InstalmentPlan{}, fmt.Errorf("%w: the plan is counted in instrument %s, not %s", ErrMalformedPlan, posting.InstrumentID, r.Instrument.ID)
	}

	h := Header{
		ID: origin.ID, IdempotencyKey: origin.IdempotencyKey, OccurredOn: origin.OccurredOn, OccurredAt: origin.OccurredAt,
		Description: origin.Description, Merchant: origin.Merchant, ProjectID: origin.ProjectID, AreaID: origin.AreaID, TagIDs: origin.TagIDs,
	}
	if err := r.Schedule.check(DateOf(origin.OccurredOn), r.Instrument, r.BaseCurrency, posting.Quantity.Neg(), p.interestCategory()); err != nil {
		return InstalmentPlan{}, err
	}
	if !r.ChangePast {
		if err := p.checkPast(r); err != nil {
			return InstalmentPlan{}, err
		}
	}
	periods, err := r.Schedule.periods(h, *posting.AccountID, r.Instrument, r.BaseCurrency)
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
