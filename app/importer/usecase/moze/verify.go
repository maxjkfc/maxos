package moze

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Verify compares the balances of every month that is confirmed so far. A month is
// confirmed when everything the plan records in it is confirmed (or voided, which is
// the user's say), and the balance adjustments of it are made; the months are
// looked at in order, up to the first that is not.
func (a *Importer) Verify(ctx context.Context, in usecase.VerifyInput) (usecase.VerifyOutput, error) {
	var out usecase.VerifyOutput
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return out, err
	}
	plan := ld.plan
	if plan.Blocked() {
		return out, &usecase.BlockedError{Plan: plan}
	}
	st, err := a.readState(ctx)
	if err != nil {
		return out, err
	}

	for _, m := range plan.Months() {
		if drafts, notApplied, adjustments := open(plan, st, m); drafts+notApplied+adjustments > 0 {
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
