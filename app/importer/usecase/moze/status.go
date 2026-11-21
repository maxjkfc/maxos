package moze

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Status counts, for each month, the units that are drafts, confirmed, voided, stale,
// held back by a plan that is gone, and not recorded yet, the rows that are not recorded
// at all, and the adjustments that are still to make; names the transactions of the
// import that no unit of the plan accounts for, on days the export covers; and says
// what the ledger has of the import on days it does not.
func (a *Importer) Status(ctx context.Context, in usecase.StatusInput) (usecase.StatusOutput, error) {
	var out usecase.StatusOutput
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return out, err
	}
	plan := ld.plan
	out.Warnings = plan.Warnings
	if plan.Blocked() {
		return out, &usecase.BlockedError{Plan: plan}
	}
	st, err := a.readState(ctx, plan)
	if err != nil {
		return out, err
	}

	skipped := skippedByMonth(plan)
	for _, m := range plan.Months() {
		ms := monthStatus(plan, st, m)
		ms.Skipped = skipped[m]
		out.Months = append(out.Months, ms)
	}
	out.Orphans = st.orphanViews()
	out.Uncovered = st.uncovered()
	return out, nil
}

// skippedByMonth counts, for each month, the rows of it that are not recorded.
func skippedByMonth(plan model.Plan) map[string]int {
	rows := plan.RowIndex()
	skipped := map[string]int{}
	for _, s := range plan.Skipped {
		for _, key := range s.Rows {
			skipped[rows[key].Month()]++
		}
	}
	return skipped
}

// monthStatus counts the units of a month by where they stand, and its adjustments that
// are still to make.
func monthStatus(plan model.Plan, st *state, month string) usecase.MonthStatus {
	ms := usecase.MonthStatus{Month: month}
	for _, u := range unitsOf(plan, month) {
		switch s, _ := st.of(u); s {
		case unitMissing, unitRevived:
			ms.NotApplied++
		case unitDraft:
			ms.Drafts++
		case unitConfirmed:
			ms.Confirmed++
		case unitVoid:
			ms.Voided++
		case unitStale:
			ms.Stale++
		case unitBlocked:
			ms.Blocked++
		}
	}
	_, _, ms.AdjustmentsPending = open(plan, st, month)
	return ms
}
