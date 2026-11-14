package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// InstalmentPeriod is one period of an instalment plan: the day it falls due and
// what is charged to the account on it.
type InstalmentPeriod struct {
	// DueOn is the day the period is charged to the account, and is required. Only
	// the day it falls on counts, as its own location reads it. The principal
	// reaches the balance of the account that day, and not before.
	DueOn time.Time
	// Principal is the part of the amount being split that the period pays off:
	// greater than zero, with no more decimal places than the instrument. It is not
	// an expense: the expense was the purchase, or, for a bill instalment, was
	// recorded when the debt was made.
	Principal decimal.Decimal
	// Interest is what the institution charges on top of the principal in this
	// period, when it charges any: greater than zero, with no more decimal places
	// than the instrument. It is an expense, on the day it is charged, of the
	// schedule's InterestCategoryID, and the account is charged the principal and
	// the interest together.
	Interest *decimal.Decimal
	// InterestBaseAmount is Interest in the base currency (TWD), as of the day it is
	// charged, greater than zero. It is required when there is Interest and the
	// instrument is not the base currency. For the base currency it may be left out,
	// and must equal Interest if given. A period without Interest has none.
	InterestBaseAmount *decimal.Decimal
}

// InstalmentSchedule is when an instalment plan is charged and how much, which is
// set when the plan is recorded and does not change: the periods become the
// transactions of the plan.
type InstalmentSchedule struct {
	// Periods are the periods in the order they fall due, at least one. They fall
	// due on later and later days, each on a day after the one before it, the first
	// not before the day the plan is recorded for (the day of the purchase, or of
	// the conversion). Their principals add up exactly to the amount being split
	// (ErrPrincipalMismatch).
	Periods []InstalmentPeriod
	// InterestCategoryID is the category the interest of the periods is booked to.
	// It is required when a period has interest (ErrInterestCategoryRequired), and
	// must be a category that is not archived and has the direction expense. It may
	// not be a system category.
	InterestCategoryID *uuid.UUID
}

// RecordBillInstalment records the conversion of what an account already owes into
// an instalment plan (帳單分期), as drafts: the origin of the plan, an instalment
// dated the day of the conversion, and a transaction for each period. It only
// changes how the debt is paid, so it is not an expense: the account is paid off
// by the amount at once and owes it again as principal that is not yet charged,
// which each period then charges on the day it falls due. The output is the
// origin, with the idempotency key of the request; the periods are read with
// GetInstalmentPlan. Like all that is recorded it is confirmed with
// ConfirmTransactions, through its origin, as a whole.
//
// The account, instrument and categories must exist and not be archived, and the
// instrument must be a currency.
type RecordBillInstalment interface {
	RecordBillInstalment(ctx context.Context, in RecordBillInstalmentInput) (RecordOutput, error)
}

// RecordBillInstalmentInput is the conversion of a debt into a plan to record.
// RecordDetails.OccurredOn is the day of the conversion.
type RecordBillInstalmentInput struct {
	RecordDetails
	// AccountID is the account that owes, and is charged the periods.
	AccountID uuid.UUID
	// InstrumentID is what is owed, and must be a currency.
	InstrumentID uuid.UUID
	// Principal is the amount of what the account owes that is turned into a plan:
	// greater than zero, with no more decimal places than the instrument.
	Principal decimal.Decimal
	// Schedule is the periods. Their principals add up to Principal.
	Schedule InstalmentSchedule
	// Fees are charges that came with the conversion. Each is paid from its account,
	// besides the principal.
	Fees []Fee
}

// InstalmentPlan is an instalment plan as the transactions it is made of.
type InstalmentPlan struct {
	// Origin is the purchase that is paid in instalments, or the conversion of a
	// debt. Its ID is the id of the plan.
	Origin Transaction
	// Periods are the transactions that charge the periods, in the order they fall
	// due: the first has PlanSeq 1.
	Periods []Transaction
}

// GetInstalmentPlan returns an instalment plan: its origin, and its periods in
// order. It is of whatever status the plan has, a draft and a void one too, and
// is ErrNotFound for an id that is not the id of a plan, the origin's.
type GetInstalmentPlan interface {
	GetInstalmentPlan(ctx context.Context, planID uuid.UUID) (InstalmentPlan, error)
}

// ListInstalmentPlans lists the confirmed instalment plans, where each stands
// today: what it splits, how much has been charged, how much has not, and when
// the next period is due. A draft plan and a void one are not in it.
type ListInstalmentPlans interface {
	ListInstalmentPlans(ctx context.Context, in ListInstalmentPlansInput) (ListInstalmentPlansOutput, error)
}

// ListInstalmentPlansInput filters the list.
type ListInstalmentPlansInput struct {
	// AccountID lists the plans that charge that account; nil lists every
	// account's.
	AccountID *uuid.UUID
	// IncludeFinished lists the plans that are finished too: those of which nothing
	// remains to charge today, because every period has fallen due. They are left
	// out otherwise.
	IncludeFinished bool
}

// InstalmentPlanSummary is where an instalment plan stands today. Its amounts are
// sizes, and so are never negative, unlike the balances they are read beside.
type InstalmentPlanSummary struct {
	// PlanID is the id of the plan, which is the id of its origin.
	PlanID uuid.UUID
	// AccountID is the account the plan charges, and InstrumentID what it is counted
	// in.
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Principal is the amount the plan splits.
	Principal decimal.Decimal
	// Charged is the principal of the periods that have fallen due, today's included,
	// which is what has reached the balance of the account.
	Charged decimal.Decimal
	// UnbilledInstalment is the principal that has not fallen due yet (未到期分期):
	// Principal less Charged, zero for a finished plan. It is the same amount as the
	// unbilled instalment of the balance of the account for this plan, without the
	// sign.
	UnbilledInstalment decimal.Decimal
	// NextDueOn is the first day after today that a period falls due, at midnight
	// UTC; nil for a finished plan.
	NextDueOn *time.Time
}

// ListInstalmentPlansOutput holds the plans, the newest first: by the day of
// their origin, and for the same day the latest recorded first.
type ListInstalmentPlansOutput struct {
	Plans []InstalmentPlanSummary
}
