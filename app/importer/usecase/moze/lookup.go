package moze

import (
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// lookup is what the ledger has of a unit: its transactions in the version the plan
// says now, and the transactions it replaces.
type lookup struct {
	// attempt is the number of the latest attempt at the unit's present version: the
	// first, until the version was voided and recorded again.
	attempt int
	// current are the transactions of the unit in its present version, at that attempt,
	// by key.
	current map[string]finance.Transaction
	// others are the transactions of earlier versions of the unit, and of units that are
	// made of rows the unit has: the pairing of two rows has changed, or a row that was
	// half of a pair stands alone. None of them is a unit of the plan, whose
	// transactions are its own to replace.
	others []versioned
	// plans are the plans of instalments that the plan does not have any more and that
	// hold the unit back (see look).
	plans []versioned
}

// look finds what the ledger has of the unit.
func (st *state) look(u model.Unit) lookup {
	l := lookup{attempt: 1, current: map[string]finance.Transaction{}}
	var mine []versioned
	seen := map[uuid.UUID]bool{}
	for _, id := range st.relatedTo(u) {
		if id != u.ID && st.planIDs[id] {
			continue
		}
		for _, v := range st.units[id] {
			if id == u.ID && v.version == u.Version {
				mine = append(mine, v)
				l.attempt = max(l.attempt, v.attempt)
				continue
			}
			v.gone = st.discarded[v.tx.ID]
			seen[v.tx.ID] = true
			l.others = append(l.others, v)
		}
	}
	for _, v := range mine {
		if v.attempt == l.attempt {
			l.current[v.tx.IdempotencyKey] = v.tx
		}
	}
	// A unit that the ledger has already in its present version is not held back by a plan
	// that is gone: it is another plan, which is as it was.
	if len(l.current) == 0 {
		l.plans = st.holdingBack(u, seen)
	}
	return l
}

// relatedTo are the IDs, in order, of the unit and of the units that are made of a row
// that it is made of too.
func (st *state) relatedTo(u model.Unit) []string {
	related := map[string]bool{u.ID: true}
	for _, row := range u.Rows {
		for id := range st.rowUnits[row] {
			related[id] = true
		}
	}
	ids := make([]string, 0, len(related))
	for id := range related {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// holdingBack are the plans of instalments, gone from the plan, that hold the unit back: a
// plan that is gone still charges the interest and takes the payments that the rows of the
// instalments in its account now come to as units of their own, so those units are not
// recorded beside it, which would count the same interest twice. The transactions the unit
// replaces already are not among them: they are not plans of their own.
func (st *state) holdingBack(u model.Unit, replaced map[uuid.UUID]bool) []versioned {
	account, ok := st.instalmentUnits[u.ID]
	if !ok {
		return nil
	}
	var plans []versioned
	for _, g := range st.plansGone {
		if g.account == account && !replaced[g.tx.ID] {
			v := g.versioned
			v.gone = st.discarded[v.tx.ID]
			plans = append(plans, v)
		}
	}
	return plans
}

// stale tells whether a transaction that the unit replaces is confirmed: it is never
// changed, so the unit cannot be recorded as the plan says until it is voided.
func (l lookup) stale() bool {
	return slices.ContainsFunc(l.others, func(v versioned) bool { return v.tx.Status == "confirmed" })
}

// blocker is the key of a plan of instalments, confirmed and gone from the plan, that
// holds the unit back; it is empty when none does. It is not the unit's own earlier
// version, so the unit is not stale: what the user voids is the plan.
func (l lookup) blocker() string {
	for _, v := range l.plans {
		if v.tx.Status == "confirmed" {
			return v.tx.IdempotencyKey
		}
	}
	return ""
}

// drafts are the transactions that the unit replaces, or that a plan which is gone
// holds it back with, that are drafts, and that have not been discarded: they are
// discarded when the unit is recorded.
func (l lookup) drafts() []versioned {
	var out []versioned
	for _, v := range slices.Concat(l.others, l.plans) {
		if v.tx.Status == "draft" && !v.gone {
			out = append(out, v)
		}
	}
	return out
}

// replaces tells whether the unit takes the place of something: another version of it,
// or a plan of instalments that is gone.
func (l lookup) replaces() bool {
	return len(l.others)+len(l.plans) > 0
}

// madeRoomFor tells whether something else was recorded after the void of the
// transactions: the void was what made room for it, so that it was the plan that
// changed, and not the user who gave the unit up.
func (l lookup) madeRoomFor(voided []finance.Transaction) bool {
	var at time.Time
	for _, t := range voided {
		if t.VoidedAt != nil && t.VoidedAt.After(at) {
			at = *t.VoidedAt
		}
	}
	if at.IsZero() {
		return false
	}
	return slices.ContainsFunc(l.others, func(v versioned) bool { return !v.gone && v.tx.CreatedAt.After(at) })
}

// unitState is where a unit stands in the ledger.
type unitState int

const (
	// unitMissing is a unit the ledger does not have in its present version, or has
	// only some of.
	unitMissing unitState = iota
	unitDraft
	unitConfirmed
	// unitVoid is a unit that the user voided in the version the plan says now, and
	// gave up: it stays as it is.
	unitVoid
	// unitRevived is a unit whose present version was voided to make room for another
	// that is gone too: the plan says what it said before, and the unit is wanted.
	unitRevived
	// unitStale is a unit that the ledger has in another version that is confirmed.
	unitStale
	// unitBlocked is a unit that is not recorded because a plan of instalments that the
	// plan does not have any more is confirmed, and charges the interest the unit's
	// rows now come to.
	unitBlocked
)

// of returns where the unit stands, and the transactions of it that are drafts.
func (st *state) of(u model.Unit) (unitState, []uuid.UUID) {
	return stateOf(u, st.look(u))
}

// stateOf tells where the unit stands by what the ledger has of it.
func stateOf(u model.Unit, l lookup) (unitState, []uuid.UUID) {
	if l.stale() {
		return unitStale, nil
	}
	if l.blocker() != "" {
		return unitBlocked, nil
	}
	var drafts []uuid.UUID
	var voided []finance.Transaction
	keys := u.KeysAt(l.attempt)
	for _, key := range keys {
		t, ok := l.current[key]
		if !ok {
			return unitMissing, nil
		}
		switch t.Status {
		case "draft":
			drafts = append(drafts, t.ID)
		case "void":
			voided = append(voided, t)
		}
	}
	switch {
	case len(drafts) > 0:
		return unitDraft, drafts
	case len(voided) == 0:
		return unitConfirmed, nil
	case len(voided) == len(keys) && l.madeRoomFor(voided):
		return unitRevived, nil
	default:
		return unitVoid, nil
	}
}

// staleUnits are the units of the month that the ledger has in a version that is
// confirmed and is not the present one.
func (st *state) staleUnits(plan model.Plan, month string) []usecase.StaleUnit {
	var out []usecase.StaleUnit
	for _, u := range unitsOf(plan, month) {
		if s, _ := st.of(u); s == unitStale {
			out = append(out, usecase.StaleUnit{Unit: u, Lines: linesOf(plan, u)})
		}
	}
	return out
}

// blockedUnits are the units of the month that a plan of instalments that is gone holds
// back, each with the key of the plan.
func (st *state) blockedUnits(plan model.Plan, month string) []usecase.BlockedUnit {
	var out []usecase.BlockedUnit
	for _, u := range unitsOf(plan, month) {
		l := st.look(u)
		if s, _ := stateOf(u, l); s == unitBlocked {
			out = append(out, usecase.BlockedUnit{Unit: u, Lines: linesOf(plan, u), Plan: l.blocker()})
		}
	}
	return out
}

// adjustment returns the transaction that reconciles the adjustment, if the import
// recorded one.
func (st *state) adjustment(adj model.Adjustment) (finance.Transaction, bool) {
	t, ok := st.byKey[adj.Key]
	return t, ok
}
