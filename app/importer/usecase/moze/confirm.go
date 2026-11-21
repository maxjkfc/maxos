package moze

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// unitsOf are the units of a month, in order; adjustmentsOf its adjustments.
func unitsOf(plan model.Plan, month string) []model.Unit {
	var out []model.Unit
	for _, u := range plan.Units {
		if u.Month() == month {
			out = append(out, u)
		}
	}
	return out
}

func adjustmentsOf(plan model.Plan, month string) []model.Adjustment {
	var out []model.Adjustment
	for _, adj := range plan.Adjustments {
		if adj.Month() == month {
			out = append(out, adj)
		}
	}
	return out
}

// open tells what is left to confirm in a month: the units that are drafts or that
// the ledger does not have in the version the plan says (a version that was voided to
// make room for another that is gone too is not there either, nor is a unit that a
// plan which is gone holds back), and the adjustments that are not confirmed.
func open(plan model.Plan, st *state, month string) (drafts, notApplied, adjustments int) {
	for _, u := range unitsOf(plan, month) {
		switch s, _ := st.of(u); s {
		case unitMissing, unitRevived, unitBlocked:
			notApplied++
		case unitDraft:
			drafts++
		}
	}
	for _, adj := range adjustmentsOf(plan, month) {
		if t, ok := st.adjustment(adj); !ok || t.Status == "draft" {
			adjustments++
		}
	}
	return drafts, notApplied, adjustments
}

// Confirm confirms the month, day by day: the drafts of the import of each day, in
// the order of the plan, and then the reconciliations of the balance adjustments of the
// day, so that an adjustment is made once everything of its account up to its day is
// confirmed.
func (a *Importer) Confirm(ctx context.Context, in usecase.ConfirmInput) (usecase.ConfirmOutput, error) {
	out := usecase.ConfirmOutput{Month: in.Month}
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return out, err
	}
	plan := ld.plan
	out.Warnings = plan.Warnings
	if err := askable(plan, in.Month); err != nil {
		return out, err
	}
	st, err := a.readState(ctx, plan)
	if err != nil {
		return out, err
	}
	if err := mayConfirm(plan, st, in.Month, &out); err != nil {
		return out, err
	}

	units, adjustments := unitsOf(plan, in.Month), adjustmentsOf(plan, in.Month)
	for _, day := range daysOf(units, adjustments) {
		if err := a.confirmDay(ctx, plan, st, units, adjustments, day, &out); err != nil {
			return out, err
		}
	}
	out.Check, err = a.check(ctx, plan, st, in.Month)
	return out, err
}

// askable refuses what there is nothing to confirm in: a plan that is not one to work
// from, and a month that the export has nothing in.
func askable(plan model.Plan, month string) error {
	if plan.Blocked() {
		return &usecase.BlockedError{Plan: plan}
	}
	if !slices.Contains(plan.Months(), month) {
		return fmt.Errorf("%w: %s", usecase.ErrUnknownMonth, month)
	}
	return nil
}

// mayConfirm refuses the month while something stands in the way of confirming it: a
// confirmed transaction that is not what the plan says, a confirmed transaction that
// no unit of the plan accounts for, or a month before it that is not done. The drafts
// that no unit accounts for are left alone, and told in out.
func mayConfirm(plan model.Plan, st *state, month string, out *usecase.ConfirmOutput) error {
	// A confirmed transaction is never changed. One that is not what the plan says
	// stands in the way until the user voids it, and apply records the unit again.
	if stale := staleUpTo(plan, st, month); len(stale) > 0 {
		return &usecase.StaleError{Month: month, Units: stale}
	}
	// A confirmed transaction that no unit of the plan accounts for counts toward the
	// balances all the same, and is the more of whatever replaces it: it stands in the
	// way until the user voids it.
	var counting []usecase.Orphan
	for _, o := range st.orphanViews() {
		if o.Confirmed() {
			counting = append(counting, o)
			continue
		}
		out.Orphans = append(out.Orphans, o)
	}
	if len(counting) > 0 {
		return &usecase.OrphanError{Month: month, Orphans: counting}
	}
	return inOrder(plan, st, month)
}

// staleUpTo are the units of the month and of the months before it that the ledger has
// in a version that is confirmed and is not the one the plan says.
func staleUpTo(plan model.Plan, st *state, month string) []usecase.StaleUnit {
	var stale []usecase.StaleUnit
	for _, m := range plan.Months() {
		if m > month {
			break
		}
		stale = append(stale, st.staleUnits(plan, m)...)
	}
	return stale
}

// inOrder refuses a month while one before it is not done, and while it has units that
// the ledger does not have: the months are confirmed in order, since what an adjustment
// adds is to what the account holds, which only the months before it settle.
func inOrder(plan model.Plan, st *state, month string) error {
	for _, m := range plan.Months() {
		if m >= month {
			break
		}
		if drafts, notApplied, adjustments := open(plan, st, m); drafts+notApplied+adjustments > 0 {
			return &usecase.MonthOrderError{Open: m, Month: month, Drafts: drafts, NotApplied: notApplied, Adjustments: adjustments}
		}
	}
	if _, notApplied, _ := open(plan, st, month); notApplied > 0 {
		return fmt.Errorf("%w: %s has %d, run apply", usecase.ErrNotApplied, month, notApplied)
	}
	return nil
}

// daysOf are the days of a month that have a unit or an adjustment, oldest first.
func daysOf(units []model.Unit, adjustments []model.Adjustment) []time.Time {
	var days []time.Time
	for _, u := range units {
		days = append(days, u.Date)
	}
	for _, adj := range adjustments {
		days = append(days, adj.Date)
	}
	slices.SortFunc(days, time.Time.Compare)
	return slices.CompactFunc(days, time.Time.Equal)
}

// confirmDay confirms what the month has on a day: the drafts of its units, and then
// the reconciliations of its adjustments, which need everything of the day confirmed.
func (a *Importer) confirmDay(ctx context.Context, plan model.Plan, st *state, units []model.Unit, adjustments []model.Adjustment, day time.Time, out *usecase.ConfirmOutput) error {
	if err := a.confirmDrafts(ctx, plan, st, units, day, out); err != nil {
		return err
	}
	for _, adj := range adjustments {
		if !adj.Date.Equal(day) {
			continue
		}
		res, err := a.applyAdjustment(ctx, st, adj)
		if err != nil {
			return err
		}
		out.Adjustments = append(out.Adjustments, res)
	}
	return nil
}

// confirmDrafts confirms the drafts of the units of a day, in one call to the ledger,
// which confirms all of them or none; it counts the units that were confirmed before.
func (a *Importer) confirmDrafts(ctx context.Context, plan model.Plan, st *state, units []model.Unit, day time.Time, out *usecase.ConfirmOutput) error {
	var ids []uuid.UUID
	for _, u := range units {
		if !u.Date.Equal(day) {
			continue
		}
		switch s, drafts := st.of(u); s {
		case unitDraft:
			ids = append(ids, drafts...)
		case unitConfirmed:
			out.AlreadyConfirmed++
		}
	}
	if len(ids) == 0 {
		return nil
	}
	confirmed, err := a.ledger.ConfirmTransactions.ConfirmTransactions(ctx, finance.ConfirmTransactionsInput{IDs: ids})
	if err != nil {
		return usecase.StepFailure(usecase.VerbConfirm, usecase.ThingTransaction, day.Format(time.DateOnly), err)
	}
	for _, t := range confirmed.Transactions {
		st.byKey[t.IdempotencyKey] = t
	}
	st.index(plan)
	out.Confirmed += len(confirmed.Transactions)
	return nil
}

// applyAdjustment reconciles the account of a balance adjustment to what it holds
// plus what the adjustment adds, and confirms the result. What the account holds is
// what the ledger says on the day, which is why everything up to the day has to be
// confirmed first. An adjustment that was made before is not made again: a draft of
// it is confirmed, and a confirmed one is left. The ledger refusing is not an error of
// Confirm: the adjustment is blocked, which the result says, and the others go on.
func (a *Importer) applyAdjustment(ctx context.Context, st *state, adj model.Adjustment) (usecase.AdjustmentResult, error) {
	res := usecase.AdjustmentResult{Adjustment: adj}
	if t, ok := st.adjustment(adj); ok {
		if t.Status != "draft" {
			res.State = usecase.AdjustmentDone
			return res, nil
		}
		return a.confirmAdjustment(ctx, st, res, t.ID)
	}

	accountID, instrumentID := st.accountIDs[adj.Account], st.instrumentIDs[adj.Instrument]
	balances, err := a.ledger.GetBalances.GetBalances(ctx, finance.GetBalancesInput{AccountID: &accountID, AsOf: &adj.Date})
	if err != nil {
		return res, usecase.StepFailure(usecase.VerbGet, usecase.ThingBalance, adj.Account+" "+adj.Date.Format(time.DateOnly), err)
	}
	recorded := model.Balances{}
	for _, b := range balances.Balances {
		if b.InstrumentID == instrumentID {
			recorded[model.Pair{Account: adj.Account, Instrument: adj.Instrument}] = b.Quantity
		}
	}
	actual := recorded[model.Pair{Account: adj.Account, Instrument: adj.Instrument}].Add(adj.Amount)

	rec, err := a.ledger.Reconcile.Reconcile(ctx, finance.ReconcileInput{
		RecordDetails:        a.detailsOf(st.refs, adj.Key, adj.Date, adj.Clock, adj.Description, adj.Merchant, adj.Area, adj.Project, adj.Tags),
		AccountID:            accountID,
		InstrumentID:         instrumentID,
		ActualBalance:        actual,
		DifferenceBaseAmount: adj.Base,
	})
	if err != nil {
		res.State, res.Err = usecase.AdjustmentBlocked, err
		return res, nil
	}
	if rec.Transaction == nil {
		// Nothing to make up: the account already holds what it should.
		res.State = usecase.AdjustmentDone
		return res, nil
	}
	return a.confirmAdjustment(ctx, st, res, rec.Transaction.ID)
}

func (a *Importer) confirmAdjustment(ctx context.Context, st *state, res usecase.AdjustmentResult, id uuid.UUID) (usecase.AdjustmentResult, error) {
	confirmed, err := a.ledger.ConfirmTransactions.ConfirmTransactions(ctx, finance.ConfirmTransactionsInput{IDs: []uuid.UUID{id}})
	if err != nil {
		res.State, res.Err = usecase.AdjustmentBlocked, err
		return res, nil
	}
	for _, t := range confirmed.Transactions {
		st.byKey[t.IdempotencyKey] = t
	}
	res.State = usecase.AdjustmentApplied
	return res, nil
}
