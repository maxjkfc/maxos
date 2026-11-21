package moze

import (
	"context"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// Apply makes the reference data the plan uses and records its units, as drafts: what
// the ledger lacks is recorded, what it has in the version the plan says is left, a
// draft of an earlier version is discarded and recorded again, and a confirmed
// earlier version is reported and left alone. A transaction of the import that no
// unit of the plan accounts for is an orphan, if it is of a day that the export
// covers: a draft is discarded, a confirmed one is reported. What is of a day the
// export does not cover is told, and left alone.
func (a *Importer) Apply(ctx context.Context, in usecase.ApplyInput) (usecase.ApplyOutput, error) {
	ld, err := a.inputs.load(ctx, in.Files)
	if err != nil {
		return usecase.ApplyOutput{}, err
	}
	plan := ld.plan
	out := usecase.ApplyOutput{
		Warnings:    plan.Warnings,
		New:         map[model.UnitKind]int{},
		Updated:     map[model.UnitKind]int{},
		Revived:     map[model.UnitKind]int{},
		Duplicate:   map[model.UnitKind]int{},
		Adjustments: len(plan.Adjustments),
		NoOps:       len(plan.NoOps),
	}
	// What the ledger is made of, its accounts and instruments, cannot be changed
	// afterwards, so a guess that the user has not looked at is not recorded from.
	if plan.Blocked() || ld.mapping.Guessed > 0 {
		return out, &usecase.BlockedError{Plan: plan, Guessed: ld.mapping.Guessed}
	}

	r, made, err := a.ensure(ctx, plan)
	if err != nil {
		return out, err
	}
	out.Created, out.ProjectDifferences = made.created, made.projectDifferences
	st, err := a.readState(ctx, plan)
	if err != nil {
		return out, err
	}

	for _, s := range plan.Skipped {
		out.Skipped += len(s.Rows)
	}
	if err := a.applyUnits(ctx, r, st, plan, &out); err != nil {
		return out, err
	}
	if err := a.removeOrphans(ctx, st, &out); err != nil {
		return out, err
	}
	out.Uncovered = st.uncovered()
	return out, nil
}

// applyUnits records every unit of the plan, and says what became of each in out. A
// unit that the ledger refuses is told and does not stop the others.
func (a *Importer) applyUnits(ctx context.Context, r refs, st *state, plan model.Plan, out *usecase.ApplyOutput) error {
	for _, u := range plan.Units {
		if err := ctx.Err(); err != nil {
			return err
		}
		res, err := a.applyUnit(ctx, r, st, u)
		switch {
		case err != nil:
			out.Failed = append(out.Failed, usecase.Failure{Unit: u, Lines: linesOf(plan, u), Err: err})
		case res == resultStale:
			out.Stale = append(out.Stale, usecase.StaleUnit{Unit: u, Lines: linesOf(plan, u)})
		case res == resultBlocked:
			out.Blocked = append(out.Blocked, usecase.BlockedUnit{Unit: u, Lines: linesOf(plan, u), Plan: st.look(u).blocker()})
		case res == resultVoided:
			out.Voided = append(out.Voided, usecase.VoidedUnit{Unit: u, Lines: linesOf(plan, u)})
		case res == resultRevived:
			out.Revived[u.Kind]++
		case res == resultUpdated:
			out.Updated[u.Kind]++
		case res == resultDuplicate:
			out.Duplicate[u.Kind]++
		default:
			out.New[u.Kind]++
		}
	}
	return nil
}

// removeOrphans deals with what no unit of the plan accounts for, on the days the
// export covers: the drafts are discarded, which is what the export says of them, and
// the confirmed ones are reported, for the user to void.
func (a *Importer) removeOrphans(ctx context.Context, st *state, out *usecase.ApplyOutput) error {
	for _, t := range st.orphans() {
		o := st.orphanOf(t)
		if t.Status == "confirmed" {
			out.Orphans = append(out.Orphans, o)
			continue
		}
		if err := a.ledger.DiscardDraft.DiscardDraft(ctx, t.ID); err != nil {
			return usecase.StepFailure(usecase.VerbDiscard, usecase.ThingTransaction, t.IdempotencyKey, err)
		}
		st.discarded[t.ID] = true
		out.Removed = append(out.Removed, o)
	}
	return nil
}

// result is what applying a unit came to.
type result int

const (
	// resultNew is a unit the ledger did not have.
	resultNew result = iota
	// resultUpdated is a unit whose draft of an earlier version was discarded and
	// recorded again (or whose earlier version was voided, and is recorded again).
	resultUpdated
	// resultRevived is a unit whose present version had been voided, to make room for
	// another version that is gone too, and which is recorded again under a key of its
	// own.
	resultRevived
	// resultDuplicate is a unit the ledger had in the version the plan says.
	resultDuplicate
	// resultStale is a unit that the ledger has in an earlier version that is
	// confirmed, which is left alone.
	resultStale
	// resultBlocked is a unit that a plan of instalments that is gone, and confirmed,
	// holds back: it is not recorded beside it.
	resultBlocked
	// resultVoided is a unit whose present version the user voided, which stays so.
	resultVoided
)

// applyUnit brings the ledger up to what the plan says of the unit. The transactions of
// an earlier version of it, and of units that were made of its rows (a pair of rows
// that the plan now pairs otherwise), are what it replaces: while one of them is
// confirmed nothing is recorded, since the unit would be there twice once both were
// confirmed; otherwise the drafts are discarded, and the unit is recorded. The
// transactions it already has in its present version are not recorded again, which is
// how an apply that was cut short is taken up where it stopped. A present version that
// was voided is left so, unless the void was what made room for a version that is gone
// too: then it is recorded again, under a key of its own, since the ledger would answer
// the key it has with the voided transaction.
func (a *Importer) applyUnit(ctx context.Context, r refs, st *state, u model.Unit) (result, error) {
	l := st.look(u)
	s, _ := stateOf(u, l)
	drafts := l.drafts()
	switch {
	case s == unitStale:
		return resultStale, nil
	case s == unitBlocked:
		return resultBlocked, nil
	case s == unitVoid:
		return resultVoided, nil
	case (s == unitDraft || s == unitConfirmed) && len(drafts) == 0:
		// All of it is there, in the version the plan says, and nothing is left of
		// another: nothing to ask of the ledger.
		return resultDuplicate, nil
	}
	discarded := 0
	for _, o := range drafts {
		if err := a.ledger.DiscardDraft.DiscardDraft(ctx, o.tx.ID); err != nil {
			return resultNew, err
		}
		st.discarded[o.tx.ID] = true
		discarded++
	}
	attempt := l.attempt
	if s == unitRevived {
		attempt++
	}
	c, err := a.record(ctx, r, u, attempt)
	if err != nil {
		return resultNew, err
	}
	switch {
	case s == unitRevived:
		return resultRevived, nil
	case c.New == 0 && discarded == 0:
		return resultDuplicate, nil
	case l.replaces():
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
