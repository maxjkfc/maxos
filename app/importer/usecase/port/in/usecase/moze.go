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

// Apply records the export in the ledger as drafts. Before it makes or changes anything
// it refuses an export that begins after the import in the ledger does (a
// *LaterStartError): that is a part of the history, not the whole of it. It refuses a
// plan that has anything to map or to put right, and a mapping file that still has an
// entry marked "# guessed" (a *BlockedError): what the ledger is made of, the accounts
// and the instruments, cannot be changed afterwards, so the user looks at every guess
// first.
//
// It makes the reference data the mapping names that the ledger does not have
// (instruments, institutions, accounts, categories, areas, projects, tags, parties)
// and records each unit of the plan with the use case of its kind, under an
// idempotency key made of the unit's rows and its version (model.Unit.Key), so that a
// second run records nothing, a longer export adds only the rows it has more, and what
// the plan says differently since the last run reaches the ledger:
//
//   - a unit that the ledger has in its present version is there already;
//   - a unit that the ledger has in an earlier version, as a draft, is discarded and
//     recorded again;
//   - a unit that the ledger has in an earlier version that is confirmed is left
//     alone and reported as stale, and so are the units that are made of the rows of
//     one that is: the user voids it, and the next run records the unit again;
//   - a unit whose present version the user voided is left as it is when the void is
//     theirs to keep, and recorded again, under a key of its own, when the void was
//     what made room for another version that is gone too (the plan went back to
//     what it said before);
//   - a unit that is made of the rows of an instalment is not recorded while a plan
//     of instalments that the export does not have any more is confirmed in the
//     ledger, as it charges the interest the unit's rows come to: it is reported as
//     blocked, and the plan is what the user voids;
//   - a plan of instalments of the ledger whose periods run past the last day of the
//     export is not one that the export does not have any more: the export ends before
//     its later periods, and has a part of it. It is not an orphan, and the rows of the
//     instalment that the export has are not recorded either, as the plan has them;
//   - a transaction of the import that no unit of the plan accounts for any more (its
//     row was edited, which makes it another row, or deleted, or is left out now) is
//     an orphan: a draft is discarded, a confirmed one is reported and left alone. It
//     is one only on a day that the export covers, from its first row to its last: an
//     export that ends earlier than an older one says nothing of the days it does not
//     reach, so what the ledger has of them is told (Uncovered) and left alone.
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
	// Warnings are what is odd in the mapping, which the plan tells too.
	Warnings []model.Warning
	// Created is how much reference data was made, by what it is.
	Created map[string]int
	// New, Updated, Revived and Duplicate count the units recorded now, the units whose
	// draft of an earlier version was discarded and recorded again, the units whose
	// present version had been voided and were recorded again, and the units the ledger
	// already had, by kind.
	New       map[model.UnitKind]int
	Updated   map[model.UnitKind]int
	Revived   map[model.UnitKind]int
	Duplicate map[model.UnitKind]int
	// Failed are the units the ledger refused.
	Failed []Failure
	// Stale are the units whose confirmed version is not what the plan says now.
	Stale []StaleUnit
	// Blocked are the units that a plan of instalments which the export does not have
	// any more, and which is confirmed, holds back: not stale, and not to be voided
	// themselves, since it is the plan that the user voids.
	Blocked []BlockedUnit
	// Voided are the units whose present version the user voided, which stay as they
	// are.
	Voided []VoidedUnit
	// Removed are the drafts of the import that no unit of the plan accounts for any
	// more, which were discarded; Orphans are the confirmed transactions of the import
	// that none does, which are left alone and which the user voids. Both are of days
	// that the export covers.
	Removed []Orphan
	Orphans []Orphan
	// Uncovered is what the ledger has of the import on days that the export does not
	// cover, which no unit accounts for: it was left alone.
	Uncovered Uncovered
	// ProjectDifferences are the projects the ledger already had, which are used as
	// they are, whose default area is not the one the mapping says.
	ProjectDifferences []ProjectDifference
	// Adjustments are the balance adjustments that are left for confirmation, and
	// NoOps the rows that need nothing; Skipped is how many rows are not recorded.
	Adjustments int
	NoOps       int
	Skipped     int
}

// Failure is a unit that the ledger would not take.
type Failure struct {
	Unit model.Unit
	// Lines are the lines of the export the unit is made of.
	Lines []int
	Err   error
}

// StaleUnit is a unit that the ledger has in a version that is confirmed, and that
// the plan now says in another: a confirmed transaction is never changed, so the
// import does not touch it, and does not record the new version beside it.
type StaleUnit struct {
	Unit model.Unit
	// Lines are the lines of the export the unit is made of.
	Lines []int
}

// BlockedUnit is a unit that is not recorded because a plan of instalments that the
// export does not have any more is confirmed in the ledger, and still charges the
// interest that the rows of the unit now come to: recorded beside it, the same interest
// would be counted twice. The unit is not stale, and is not what the user voids: the
// plan is, and then apply records the unit.
type BlockedUnit struct {
	Unit model.Unit
	// Lines are the lines of the export the unit is made of.
	Lines []int
	// Plan is the idempotency key the plan was recorded under.
	Plan string
}

// VoidedUnit is a unit whose present version the user voided: the ledger has it, and
// it counts toward nothing.
type VoidedUnit struct {
	Unit model.Unit
	// Lines are the lines of the export the unit is made of.
	Lines []int
}

// Orphan is a transaction the import recorded that no unit of the plan accounts for
// any more: the row it came of was edited (an edited row is another row, with another
// key), or deleted, or is left out now.
type Orphan struct {
	// Key is the idempotency key it was recorded under.
	Key string
	// Status is "draft" or "confirmed"; a void one is not an orphan.
	Status string
	// Kind is the kind of transaction of the ledger.
	Kind string
	// Date is the day it happened.
	Date time.Time
	// Account and Instrument are by name and by code: what it does to the first account
	// it moves, which Amount says with its sign.
	Account    string
	Instrument string
	Amount     decimal.Decimal
	// Description is what it says of itself.
	Description string
}

// Confirmed tells whether the orphan is a confirmed transaction, which counts toward
// the balances until the user voids it.
func (o Orphan) Confirmed() bool { return o.Status == "confirmed" }

// Uncovered is what the ledger has of the import on days that an export does not cover,
// which none of its units accounts for: the transactions that an older and longer export
// made, when this one ends earlier or begins later. It says nothing of them, so they are
// not orphans, and they are left alone.
type Uncovered struct {
	// First and Last are the first and the last day the export covers; both are zero
	// when it has no day at all.
	First, Last time.Time
	// Count is how many transactions of the import, drafts and confirmed ones, the ledger
	// has of days outside that, and From and To the first and the last of those days.
	Count    int
	From, To time.Time
}

// LaterStart says that an export begins after the import in the ledger does: the
// ledger has transactions of the import dated before the first day of the export, the
// opening balances apart, which are dated from the first row of their account. The
// export is then a part of the history and not the whole of it, and what it says of
// the ledger is not meant for it: the import works on the whole export only, and what
// the ledger has is correct, so it is not to be voided.
type LaterStart struct {
	// From is the first day of the export, and LedgerFrom the first day that the
	// ledger has anything of the import.
	From, LedgerFrom time.Time
}

// ProjectDifference is a project that the ledger had already, whose default area is
// not what the mapping says it is. Names are of areas, empty for none.
type ProjectDifference struct {
	Project string
	Mapping string
	Ledger  string
}

// Confirm confirms the drafts of the import that are dated in a month, in date
// order, and applies the balance adjustments of that month: each when everything of
// its account and instrument up to its day is confirmed, by reconciling the account
// to what it held plus what the adjustment adds. It refuses an export that begins
// after the import in the ledger does (a *LaterStartError), before anything else; it
// refuses when a month before it still has a draft of the import to confirm, or an
// adjustment, so that the months are confirmed in order, when a unit of that month or
// of an earlier one is stale (a *StaleError), and while the ledger has a confirmed
// transaction of the import that no unit of the plan accounts for (an *OrphanError).
// It touches nothing the import did not record.
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
	// Warnings are what is odd in the mapping, which the plan tells too.
	Warnings []model.Warning
	// Confirmed is how many transactions were confirmed, and AlreadyConfirmed how many
	// units of the month were confirmed before.
	Confirmed        int
	AlreadyConfirmed int
	// Adjustments says what became of each balance adjustment of the month.
	Adjustments []AdjustmentResult
	// Orphans are drafts of the import that no unit of the plan accounts for any more,
	// which Confirm leaves alone (apply discards them).
	Orphans []Orphan
	Check   model.MonthCheck
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
// with what the export says they hold: on the last day of the export for the month it
// stops in, when that is before the end of the month. It refuses an export that begins
// after the import in the ledger does (a *LaterStartError).
type Verify interface {
	Verify(ctx context.Context, in VerifyInput) (VerifyOutput, error)
}

// VerifyInput says what to verify.
type VerifyInput struct {
	Files
}

// VerifyOutput is the comparison for every month confirmed so far.
type VerifyOutput struct {
	// Warnings are what is odd in the mapping, which the plan tells too.
	Warnings []model.Warning
	Months   []model.MonthCheck
	// Open is the first month that is not confirmed yet, or empty.
	Open string
	// Unexplained is how many differences that nothing explains there are in all.
	Unexplained int
	// Stale are the units of the months confirmed so far, and of the month that is
	// open, whose confirmed version is not what the plan says now; Blocked those that a
	// plan of instalments which the export does not have any more holds back.
	Stale   []StaleUnit
	Blocked []BlockedUnit
	// Orphans are the transactions of the import that no unit of the plan accounts for
	// any more, on days the export covers, drafts and confirmed ones: a confirmed one
	// counts toward the balances.
	Orphans []Orphan
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
	// Warnings are what is odd in the mapping, which the plan tells too.
	Warnings []model.Warning
	Months   []MonthStatus
	// Orphans are the transactions of the import that no unit of the plan accounts for
	// any more, on days the export covers, drafts and confirmed ones. Uncovered is what
	// the ledger has of the import on days it does not cover. LaterStart is set when the
	// export begins after the import in the ledger does: nothing else is said then, as
	// what the export says of the ledger is not meant for it.
	Orphans    []Orphan
	Uncovered  Uncovered
	LaterStart *LaterStart
}

// MonthStatus counts units, which are one or two transactions or a whole plan, and
// rows.
type MonthStatus struct {
	Month string
	// Drafts and Confirmed are the units the ledger has as drafts and as confirmed,
	// NotApplied those it does not have yet (or has in an earlier version, which a
	// run of apply puts right), Voided those that were voided, Stale those whose
	// confirmed version is not what the plan says now, and Blocked those that a plan
	// of instalments which the export does not have any more holds back.
	Drafts     int
	Confirmed  int
	NotApplied int
	Voided     int
	Stale      int
	Blocked    int
	// Skipped is how many rows are not recorded.
	Skipped int
	// AdjustmentsPending are the balance adjustments not confirmed yet.
	AdjustmentsPending int
}

// ErrBlocked is what a plan with things to map or to put right is refused with.
var ErrBlocked = errors.New("the plan has something to map or to put right")

// BlockedError is the refusal of a plan that is not one to record from: it has
// entries to add to the mapping, or something wrong, or the mapping file still has
// entries that are marked as guessed.
type BlockedError struct {
	Plan model.Plan
	// Guessed is how many entries of the mapping file are still marked "# guessed".
	// Only Apply refuses for them; a plan can be made, and a month confirmed, with
	// them.
	Guessed int
}

// Missing lists the entries the mapping lacks: what the plan had to guess, which is
// added to the mapping file with a mark.
func (e *BlockedError) Missing() []model.EntryRef { return e.Plan.Guessed.Items() }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("%v: %d entries of the mapping are missing, %d are still marked as guessed and %d things are wrong; run plan, review the mapping and run it again",
		ErrBlocked, e.Plan.Guessed.Entries(), e.Guessed, len(e.Plan.Problems))
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

// ErrStale is what confirming a month is refused with while a unit of it, or of an
// earlier month, has a confirmed version that is not what the plan says now.
var ErrStale = errors.New("a confirmed transaction of the import is not what the plan says")

// StaleError names the units that are stale, which the user voids, so that apply
// records them again as the plan says.
type StaleError struct {
	// Month is the month that was asked for.
	Month string
	Units []StaleUnit
}

func (e *StaleError) Error() string {
	return fmt.Sprintf("%v: %d units of %s or before; void them, run apply, and confirm again", ErrStale, len(e.Units), e.Month)
}

// Is tells ErrStale.
func (e *StaleError) Is(target error) bool { return target == ErrStale }

// ErrOrphan is what confirming a month is refused with while the ledger has a
// confirmed transaction of the import that no unit of the plan accounts for.
var ErrOrphan = errors.New("a confirmed transaction of the import has no unit in the plan")

// OrphanError names the confirmed transactions that no unit of the plan accounts for,
// which count toward the balances until the user voids them.
type OrphanError struct {
	// Month is the month that was asked for.
	Month   string
	Orphans []Orphan
}

func (e *OrphanError) Error() string {
	return fmt.Sprintf("%v: %d transactions; void them, then run apply and confirm again", ErrOrphan, len(e.Orphans))
}

// Is tells ErrOrphan.
func (e *OrphanError) Is(target error) bool { return target == ErrOrphan }

// ErrLaterStart is what apply, confirm and verify are refused with when the export
// begins after the import in the ledger does.
var ErrLaterStart = errors.New("the export begins after the import in the ledger does")

// LaterStartError is that refusal. Nothing was made or changed.
type LaterStartError struct {
	LaterStart
}

func (e *LaterStartError) Error() string {
	return fmt.Sprintf("%v: the export begins on %s, the import in the ledger on %s; the import needs the whole export, and nothing is to be voided",
		ErrLaterStart, e.From.Format(time.DateOnly), e.LedgerFrom.Format(time.DateOnly))
}

// Is tells ErrLaterStart.
func (e *LaterStartError) Is(target error) bool { return target == ErrLaterStart }

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
