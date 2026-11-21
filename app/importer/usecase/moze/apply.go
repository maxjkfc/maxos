package moze

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Apply makes the reference data the plan uses and records its units, as drafts: what
// the ledger lacks is recorded, what it has in the version the plan says is left, a
// draft of an earlier version is discarded and recorded again, and a confirmed
// earlier version is reported and left alone.
func (a *Importer) Apply(ctx context.Context, in usecase.ApplyInput) (usecase.ApplyOutput, error) {
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return usecase.ApplyOutput{}, err
	}
	plan := ld.plan
	// What the ledger is made of, its accounts and instruments, cannot be changed
	// afterwards, so a guess that the user has not looked at is not recorded from.
	if plan.Blocked() || ld.mapping.Guessed > 0 {
		return usecase.ApplyOutput{}, &usecase.BlockedError{Plan: plan, Guessed: ld.mapping.Guessed}
	}

	r, made, err := a.ensure(ctx, plan)
	if err != nil {
		return usecase.ApplyOutput{}, fmt.Errorf("make the reference data: %w", err)
	}
	st, err := a.readState(ctx, plan)
	if err != nil {
		return usecase.ApplyOutput{}, err
	}

	out := usecase.ApplyOutput{
		Created:            made.created,
		ProjectDifferences: made.projectDifferences,
		New:                map[model.UnitKind]int{},
		Updated:            map[model.UnitKind]int{},
		Duplicate:          map[model.UnitKind]int{},
		Adjustments:        len(plan.Adjustments),
		NoOps:              len(plan.NoOps),
	}
	for _, s := range plan.Skipped {
		out.Skipped += len(s.Rows)
	}
	for _, u := range plan.Units {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		res, err := a.applyUnit(ctx, r, st, u)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, usecase.Failure{Unit: u, Lines: linesOf(plan, u), Err: err})
		case res == resultStale:
			out.Stale = append(out.Stale, usecase.StaleUnit{Unit: u, Lines: linesOf(plan, u)})
		case res == resultUpdated:
			out.Updated[u.Kind]++
		case res == resultDuplicate:
			out.Duplicate[u.Kind]++
		default:
			out.New[u.Kind]++
		}
	}
	return out, nil
}

// result is what applying a unit came to.
type result int

const (
	// resultNew is a unit the ledger did not have.
	resultNew result = iota
	// resultUpdated is a unit whose draft of an earlier version was discarded and
	// recorded again (or whose earlier version was voided, and is recorded again).
	resultUpdated
	// resultDuplicate is a unit the ledger had in the version the plan says.
	resultDuplicate
	// resultStale is a unit that the ledger has in an earlier version that is
	// confirmed, which is left alone.
	resultStale
)

// applyUnit brings the ledger up to what the plan says of the unit. The transactions of
// an earlier version of it, and of units that were made of its rows (a pair of rows
// that the plan now pairs otherwise), are what it replaces: while one of them is
// confirmed nothing is recorded, since the unit would be there twice once both were
// confirmed; otherwise the drafts are discarded, and the unit is recorded. The
// transactions it already has in its present version are not recorded again, which is
// how an apply that was cut short is taken up where it stopped.
func (a *Importer) applyUnit(ctx context.Context, r refs, st *state, u model.Unit) (result, error) {
	l := st.look(u)
	if l.stale() {
		return resultStale, nil
	}
	if len(l.others) == 0 && len(l.current) == len(u.Keys()) {
		// All of it is there, in the version the plan says: nothing to ask of the ledger.
		return resultDuplicate, nil
	}
	discarded := 0
	for _, o := range l.others {
		if o.gone || o.tx.Status != "draft" {
			continue
		}
		if err := a.ledger.DiscardDraft.DiscardDraft(ctx, o.tx.ID); err != nil {
			return resultNew, fmt.Errorf("discard the draft of an earlier version: %w", err)
		}
		st.discarded[o.tx.ID] = true
		discarded++
	}
	c, err := a.record(ctx, r, u)
	if err != nil {
		return resultNew, err
	}
	switch {
	case c.New == 0 && discarded == 0:
		return resultDuplicate, nil
	case len(l.others) > 0:
		return resultUpdated, nil
	default:
		return resultNew, nil
	}
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
