package moze

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Status counts, for each month, the units that are drafts, confirmed, voided, stale
// and not recorded yet, the rows that are not recorded at all, and the adjustments
// that are still to make.
func (a *Importer) Status(ctx context.Context, in usecase.StatusInput) (usecase.StatusOutput, error) {
	var out usecase.StatusOutput
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return out, err
	}
	plan := ld.plan
	if plan.Blocked() {
		return out, &usecase.BlockedError{Plan: plan}
	}
	st, err := a.readState(ctx, plan)
	if err != nil {
		return out, err
	}

	rows := plan.RowIndex()
	skipped := map[string]int{}
	for _, s := range plan.Skipped {
		for _, key := range s.Rows {
			skipped[rows[key].Month()]++
		}
	}

	for _, m := range plan.Months() {
		ms := usecase.MonthStatus{Month: m, Skipped: skipped[m]}
		for _, u := range unitsOf(plan, m) {
			switch s, _ := st.of(u); s {
			case unitMissing:
				ms.NotApplied++
			case unitDraft:
				ms.Drafts++
			case unitConfirmed:
				ms.Confirmed++
			case unitVoid:
				ms.Voided++
			case unitStale:
				ms.Stale++
			}
		}
		_, _, ms.AdjustmentsPending = open(plan, st, m)
		out.Months = append(out.Months, ms)
	}
	out.Orphans = len(st.orphans(plan))
	return out, nil
}
