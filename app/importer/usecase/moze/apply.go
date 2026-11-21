package moze

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Apply makes the reference data the plan uses and records its units, as drafts.
func (a *Importer) Apply(ctx context.Context, in usecase.ApplyInput) (usecase.ApplyOutput, error) {
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return usecase.ApplyOutput{}, err
	}
	plan := ld.plan
	if plan.Blocked() {
		return usecase.ApplyOutput{}, &usecase.BlockedError{Plan: plan}
	}

	r, created, err := a.ensure(ctx, plan)
	if err != nil {
		return usecase.ApplyOutput{}, fmt.Errorf("make the reference data: %w", err)
	}

	out := usecase.ApplyOutput{
		Created:     created,
		New:         map[model.UnitKind]int{},
		Duplicate:   map[model.UnitKind]int{},
		Adjustments: len(plan.Adjustments),
		NoOps:       len(plan.NoOps),
		Guessed:     ld.mapping.Guessed,
	}
	for _, s := range plan.Skipped {
		out.Skipped += len(s.Rows)
	}
	for _, u := range plan.Units {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		c, err := a.record(ctx, r, u)
		if err != nil {
			out.Failed = append(out.Failed, usecase.Failure{Unit: u, Lines: linesOf(plan, u), Err: err})
			continue
		}
		// A unit of two transactions is new when it recorded any, and a duplicate when
		// the ledger had all of them.
		if c.New > 0 {
			out.New[u.Kind]++
		} else {
			out.Duplicate[u.Kind]++
		}
	}
	return out, nil
}

// linesOf are the lines of the export that a unit is made of.
func linesOf(plan model.Plan, u model.Unit) []int {
	rows := plan.RowIndex()
	lines := make([]int, 0, len(u.Rows))
	for _, key := range u.Rows {
		lines = append(lines, rows[key].Line)
	}
	return lines
}
