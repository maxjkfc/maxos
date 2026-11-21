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
// the ledger does not have, and the adjustments that are not confirmed.
func open(plan model.Plan, st *state, month string) (drafts, notApplied, adjustments int) {
	for _, u := range unitsOf(plan, month) {
		switch s, _ := st.of(u); s {
		case unitMissing:
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
// the order of the plan, and then the balance adjustments of the day, so that an
// adjustment is made once everything of its account up to its day is confirmed.
func (a *Importer) Confirm(ctx context.Context, in usecase.ConfirmInput) (usecase.ConfirmOutput, error) {
	out := usecase.ConfirmOutput{Month: in.Month}
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return out, err
	}
	plan := ld.plan
	if plan.Blocked() {
		return out, &usecase.BlockedError{Plan: plan}
	}
	if !slices.Contains(plan.Months(), in.Month) {
		return out, fmt.Errorf("%w: %s", usecase.ErrUnknownMonth, in.Month)
	}
	st, err := a.readState(ctx)
	if err != nil {
		return out, err
	}

	// The months are confirmed in order: what an adjustment adds is to what the account
	// holds, which only the months before it settle.
	for _, m := range plan.Months() {
		if m >= in.Month {
			break
		}
		if drafts, notApplied, adjustments := open(plan, st, m); drafts+notApplied+adjustments > 0 {
			return out, &usecase.MonthOrderError{Open: m, Month: in.Month, Drafts: drafts, NotApplied: notApplied, Adjustments: adjustments}
		}
	}
	if _, notApplied, _ := open(plan, st, in.Month); notApplied > 0 {
		return out, fmt.Errorf("%w: %s has %d, run apply", usecase.ErrNotApplied, in.Month, notApplied)
	}
	out.Orphans = len(st.orphans(plan))

	units := unitsOf(plan, in.Month)
	adjustments := adjustmentsOf(plan, in.Month)
	var days []time.Time
	for _, u := range units {
		days = append(days, u.Date)
	}
	for _, adj := range adjustments {
		days = append(days, adj.Date)
	}
	slices.SortFunc(days, time.Time.Compare)
	days = slices.CompactFunc(days, time.Time.Equal)

	for _, day := range days {
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
		if len(ids) > 0 {
			confirmed, err := a.ledger.ConfirmTransactions.ConfirmTransactions(ctx, finance.ConfirmTransactionsInput{IDs: ids})
			if err != nil {
				return out, fmt.Errorf("confirm the drafts of %s: %w", day.Format(time.DateOnly), err)
			}
			for _, t := range confirmed.Transactions {
				st.byKey[t.IdempotencyKey] = t
			}
			out.Confirmed += len(confirmed.Transactions)
		}
		for _, adj := range adjustments {
			if !adj.Date.Equal(day) {
				continue
			}
			res, err := a.applyAdjustment(ctx, st, adj)
			if err != nil {
				return out, err
			}
			out.Adjustments = append(out.Adjustments, res)
		}
	}

	out.Check, err = a.check(ctx, plan, st, in.Month)
	return out, err
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
		return res, fmt.Errorf("get the balance of %s on %s: %w", adj.Account, adj.Date.Format(time.DateOnly), err)
	}
	recorded := model.Balances{}
	for _, b := range balances.Balances {
		if b.InstrumentID == instrumentID {
			recorded[model.Pair{Account: adj.Account, Instrument: adj.Instrument}] = b.Quantity
		}
	}
	actual := recorded[model.Pair{Account: adj.Account, Instrument: adj.Instrument}].Add(adj.Amount)

	details := finance.RecordDetails{
		IdempotencyKey: adj.Key,
		OccurredOn:     adj.Date,
		OccurredAt:     a.occurredAt(model.Unit{Date: adj.Date, Clock: adj.Clock}),
		Description:    adj.Description,
	}
	rec, err := a.ledger.Reconcile.Reconcile(ctx, finance.ReconcileInput{
		RecordDetails:        details,
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
