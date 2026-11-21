package moze

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Verify compares the balances of every month that is confirmed so far. A month is
// confirmed when everything the plan records in it is confirmed (or voided, which is
// the user's say), and the balance adjustments of it are made; the months are
// looked at in order, up to the first that is not: one that has a draft, a unit not
// recorded yet (one that a plan of instalments which is gone holds back included), or a
// unit whose confirmed version is not what the plan says now, which Verify reports. It
// names the transactions of the import that no unit of the plan accounts for as well,
// on days the export covers: a confirmed one counts toward the balances, and is what the
// differences of a month can come of.
func (a *Importer) Verify(ctx context.Context, in usecase.VerifyInput) (usecase.VerifyOutput, error) {
	var out usecase.VerifyOutput
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
	out.Orphans = st.orphanViews()

	for _, m := range plan.Months() {
		stale := st.staleUnits(plan, m)
		out.Stale = append(out.Stale, stale...)
		out.Blocked = append(out.Blocked, st.blockedUnits(plan, m)...)
		if drafts, notApplied, adjustments := open(plan, st, m); drafts+notApplied+adjustments > 0 || len(stale) > 0 {
			out.Open = m
			break
		}
		check, err := a.check(ctx, plan, st, m)
		if err != nil {
			return out, err
		}
		out.Unexplained += check.Unexplained()
		out.Months = append(out.Months, check)
	}
	return out, nil
}
