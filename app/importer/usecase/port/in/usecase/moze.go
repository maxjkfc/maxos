// Package usecase declares what the importer context offers its callers: the use
// cases that bring a MOZE export into the ledger, by the ledger's own use cases
// and no other way, so that everything imported is a draft until the user
// confirms it, month by month.
package usecase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// Files are what an import is made from: the export, the mapping the user keeps and
// the rates of the foreign currencies.
type Files struct {
	// Export is the path of the MOZE CSV.
	Export string
	// Mapping is the path of the mapping file. It is written to, by Plan: the entries
	// the mapping lacks are added to it.
	Mapping string
	// Rates is the path of the rates file; a rates file that does not exist is no
	// rates.
	Rates string
}

// Plan works out what the export comes to in the ledger and writes nothing to the
// database. It reads the export, the mapping and the rates, adds to the mapping file
// the entries it lacks, guessed and marked as guessed, and returns the plan.
type Plan interface {
	Plan(ctx context.Context, in PlanInput) (PlanOutput, error)
}

// PlanInput says what to plan.
type PlanInput struct {
	Files
}

// PlanOutput is a plan, and what Plan did to the mapping file.
type PlanOutput struct {
	Plan model.Plan
	// MappingCreated is true when there was no mapping file and Plan made one.
	MappingCreated bool
	// MappingAdded is how many entries of the guess were added to the mapping file.
	MappingAdded int
	// RatesFound is false when there was no rates file.
	RatesFound bool
	// Guessed is how many entries of the mapping file are marked "# guessed" after
	// Plan: the ones the user has not looked at yet.
	Guessed int
}

// Apply records the export in the ledger as drafts. It refuses a plan that has
// anything to map or to put right (a *BlockedError). It makes the reference data the
// mapping names that the ledger does not have (instruments, institutions, accounts,
// categories, areas, projects, tags, parties) and records each unit of the plan with
// the use case of its kind, under an idempotency key made of the rows, so that a
// second run records nothing and a longer export adds only the rows it has more.
//
// A unit that the ledger refuses is reported and does not stop the others.
type Apply interface {
	Apply(ctx context.Context, in ApplyInput) (ApplyOutput, error)
}

// ApplyInput says what to apply.
type ApplyInput struct {
	Files
}

// ApplyOutput says what Apply did.
type ApplyOutput struct {
	// Created is how much reference data was made, by what it is.
	Created map[string]int
	// New and Duplicate count the units recorded now and the units the ledger already
	// had, by kind.
	New       map[model.UnitKind]int
	Duplicate map[model.UnitKind]int
	// Failed are the units the ledger refused.
	Failed []Failure
	// Adjustments are the balance adjustments that are left for confirmation, and
	// NoOps the rows that need nothing; Skipped is how many rows are not recorded.
	Adjustments int
	NoOps       int
	Skipped     int
	// Guessed is how many entries of the mapping are still marked "# guessed".
	Guessed int
}

// Failure is a unit that the ledger would not take.
type Failure struct {
	Unit model.Unit
	// Lines are the lines of the export the unit is made of.
	Lines []int
	Err   error
}

// Confirm confirms the drafts of the import that are dated in a month, in date
// order, and applies the balance adjustments of that month: each when everything of
// its account and instrument up to its day is confirmed, by reconciling the account
// to what it held plus what the adjustment adds. It refuses when a month before it
// still has a draft of the import to confirm, or an adjustment, so that the months
// are confirmed in order. It touches nothing the import did not record.
type Confirm interface {
	Confirm(ctx context.Context, in ConfirmInput) (ConfirmOutput, error)
}

// ConfirmInput names the month.
type ConfirmInput struct {
	Files
	// Month is "2006-01".
	Month string
}

// ConfirmOutput says what Confirm did, and what the accounts hold at the end of the
// month against what the export says.
type ConfirmOutput struct {
	Month string
	// Confirmed is how many transactions were confirmed, and AlreadyConfirmed how many
	// units of the month were confirmed before.
	Confirmed        int
	AlreadyConfirmed int
	// Adjustments says what became of each balance adjustment of the month.
	Adjustments []AdjustmentResult
	// Orphans are drafts of the import that the export does not have any more, which
	// Confirm leaves alone.
	Orphans int
	Check   MonthCheck
}

// AdjustmentState is what became of a balance adjustment.
type AdjustmentState string

// The states of an adjustment.
const (
	// AdjustmentApplied is an adjustment reconciled and confirmed now.
	AdjustmentApplied AdjustmentState = "applied"
	// AdjustmentDone is one that was confirmed before.
	AdjustmentDone AdjustmentState = "done"
	// AdjustmentBlocked is one the ledger would not reconcile yet: drafts that are not
	// the import's wait on the account.
	AdjustmentBlocked AdjustmentState = "blocked"
)

// AdjustmentResult is the state of one balance adjustment.
type AdjustmentResult struct {
	Adjustment model.Adjustment
	State      AdjustmentState
	// Err says why it is blocked.
	Err error
}

// Verify compares what the accounts hold at the end of each month that is confirmed
// with what the export says they hold.
type Verify interface {
	Verify(ctx context.Context, in VerifyInput) (VerifyOutput, error)
}

// VerifyInput says what to verify.
type VerifyInput struct {
	Files
}

// VerifyOutput is the comparison for every month confirmed so far.
type VerifyOutput struct {
	Months []MonthCheck
	// Open is the first month that is not confirmed yet, or empty.
	Open string
	// Unexplained is how many differences that nothing explains there are in all.
	Unexplained int
}

// MonthCheck is the balances of the accounts at the end of a month, by the ledger and
// by the export.
type MonthCheck struct {
	Month string
	End   time.Time
	Lines []BalanceLine
	// Decimals are the decimal places of each instrument in Lines, by code.
	Decimals map[string]int
}

// Differences are the lines whose ledger balance is not what the export says.
func (c MonthCheck) Differences() []BalanceLine {
	var out []BalanceLine
	for _, l := range c.Lines {
		if !l.Difference().IsZero() {
			out = append(out, l)
		}
	}
	return out
}

// Unexplained is how many differences are not accounted for by rows that the import
// does not record.
func (c MonthCheck) Unexplained() int {
	n := 0
	for _, l := range c.Lines {
		if !l.Unexplained().IsZero() {
			n++
		}
	}
	return n
}

// BalanceLine is what an account holds of an instrument at a day.
type BalanceLine struct {
	Account    string
	Instrument string
	// Ledger is what the ledger says, Expected what the export says, and Unrecorded
	// what the rows that are not recorded add to it: the part of the export that the
	// ledger does not have.
	Ledger     decimal.Decimal
	Expected   decimal.Decimal
	Unrecorded decimal.Decimal
}

// Difference is how much more the ledger holds than the export says.
func (l BalanceLine) Difference() decimal.Decimal { return l.Ledger.Sub(l.Expected) }

// Unexplained is the difference that the rows that are not recorded do not account
// for: it is nothing when the ledger holds what the export says without them.
func (l BalanceLine) Unexplained() decimal.Decimal {
	return l.Ledger.Sub(l.Expected.Sub(l.Unrecorded))
}

// Status counts, for each month of the export, what the import has done.
type Status interface {
	Status(ctx context.Context, in StatusInput) (StatusOutput, error)
}

// StatusInput says what to report on.
type StatusInput struct {
	Files
}

// StatusOutput is the state of the import by month.
type StatusOutput struct {
	Months []MonthStatus
	// Orphans are drafts of the import that the export does not have any more.
	Orphans int
}

// MonthStatus counts units, which are one or two transactions or a whole plan, and
// rows.
type MonthStatus struct {
	Month string
	// Drafts and Confirmed are the units the ledger has as drafts and as confirmed,
	// NotApplied those it does not have yet, Voided those that were voided.
	Drafts     int
	Confirmed  int
	NotApplied int
	Voided     int
	// Skipped is how many rows are not recorded.
	Skipped int
	// AdjustmentsPending are the balance adjustments not confirmed yet.
	AdjustmentsPending int
}

// ErrBlocked is what a plan with things to map or to put right is refused with.
var ErrBlocked = errors.New("the plan has something to map or to put right")

// BlockedError is the refusal of a plan that is not one to record from.
type BlockedError struct {
	Plan model.Plan
}

func (e *BlockedError) Error() string {
	g := e.Plan.Guessed
	unmapped := len(g.Accounts) + len(g.Instruments) + len(g.Projects) + len(g.Expense) + len(g.Income) + len(g.Parties) + len(g.Tags)
	return fmt.Sprintf("%v: %d entries of the mapping are missing and %d things are wrong; run plan, review the mapping and run it again",
		ErrBlocked, unmapped, len(e.Plan.Problems))
}

// Is tells ErrBlocked.
func (e *BlockedError) Is(target error) bool { return target == ErrBlocked }

// ErrMonthOrder is what confirming a month is refused with when an earlier one is not
// done.
var ErrMonthOrder = errors.New("an earlier month is not confirmed")

// MonthOrderError says which earlier month is not done and what is left of it.
type MonthOrderError struct {
	// Open is the earlier month, and Month the one that was asked for.
	Open  string
	Month string
	// Drafts are the units of Open that are still drafts, NotApplied those the ledger
	// does not have, and Adjustments the balance adjustments not confirmed.
	Drafts      int
	NotApplied  int
	Adjustments int
}

func (e *MonthOrderError) Error() string {
	return fmt.Sprintf("%v: %s still has %d drafts, %d rows that are not applied and %d adjustments, confirm it before %s",
		ErrMonthOrder, e.Open, e.Drafts, e.NotApplied, e.Adjustments, e.Month)
}

// Is tells ErrMonthOrder.
func (e *MonthOrderError) Is(target error) bool { return target == ErrMonthOrder }

// ErrUnknownMonth is returned for a month that the export has nothing in.
var ErrUnknownMonth = errors.New("the export has nothing in that month")

// ErrNotApplied is returned for a month with units that the ledger does not have,
// which apply has to record first.
var ErrNotApplied = errors.New("the month has rows that apply has not recorded")

// The names of the reference data Apply makes, as ApplyOutput.Created counts them.
const (
	CreatedInstruments  = "instruments"
	CreatedInstitutions = "institutions"
	CreatedAccounts     = "accounts"
	CreatedCategories   = "categories"
	CreatedAreas        = "areas"
	CreatedProjects     = "projects"
	CreatedTags         = "tags"
	CreatedParties      = "parties"
)
