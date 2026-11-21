package moze

import (
	"slices"
	"sort"
	"time"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// The import works on the whole export of the history, and a shorter one must never
// lead the user to void what the ledger has right. An export that ends earlier says
// nothing of the days after its last, so what the ledger has of them is left alone (see
// orphans), and what it ends in the middle of, a plan of instalments or the opening
// balance of an account it ends before the use of, is the ledger's (see adopt). An export
// that begins later says nothing of the days before its first, and would have the import
// compare the ledger with a history that has lost its start: it is refused (see
// laterStart).

// openingKind is the kind the ledger gives to an opening balance.
const openingKind = "opening"

// laterStart tells whether the export begins after the import in the ledger does: the
// ledger has a transaction of the import, not void, dated before the first row of the
// export. An opening balance is not one: it is dated from the first row of its account,
// which can be the day before the first row of the export, and from the first row of
// the account of the ledger it is of, or of the file, when the account has none, so its
// day says nothing of where the export begins. It returns nil for an export with no
// day, which is not one that begins at all.
func (st *state) laterStart(plan model.Plan) *usecase.LaterStart {
	first, _ := plan.Days()
	if first.IsZero() {
		return nil
	}
	var ledgerFrom time.Time
	before := false
	for _, t := range st.byKey {
		if t.Status == "void" {
			continue
		}
		if ledgerFrom.IsZero() || t.OccurredOn.Before(ledgerFrom) {
			ledgerFrom = t.OccurredOn
		}
		if t.Kind != openingKind && t.OccurredOn.Before(first) {
			before = true
		}
	}
	if !before {
		return nil
	}
	return &usecase.LaterStart{From: first, LedgerFrom: ledgerFrom}
}

// cutPlan is a plan of instalments that the ledger has and the plan does not, because
// the export ends before its later periods: the rows of the conversion and of the
// periods that fell due by the last day of the export are there, and do not add up to
// what was converted.
type cutPlan struct {
	// tx is the origin of the plan, and account the account of the ledger it charges.
	tx      finance.Transaction
	account string
	// end is the day its last period falls due, which is after the last day of the export.
	end time.Time
}

// findCuts finds the plans of instalments of the import that the plan does not have, and
// that have a period after the last day of the export, from an origin that is on or
// before it. They are not plans that the export has lost: the export has not got to the
// end of them, and says nothing of what is left. A plan whose periods have all fallen due
// by that day is another thing: the export has all its rows, and a plan that is not
// there is a plan whose rows were edited.
func (st *state) findCuts(plan model.Plan) {
	_, last := plan.Days()
	if last.IsZero() {
		return
	}
	ids := map[string]bool{}
	for _, u := range plan.Units {
		ids[u.ID] = true
	}
	for key, t := range st.byKey {
		parsed, ok := model.ParseKey(key)
		if !ok || !parsed.IsPlan() || ids[parsed.ID] || t.Status == "void" || t.OccurredOn.After(last) {
			continue
		}
		end := t.OccurredOn
		if period := st.lastPeriod[t.ID]; period.After(end) {
			end = period
		}
		if end.After(last) {
			st.cuts[t.ID] = cutPlan{tx: t, account: st.firstAccount(t).Account, end: end}
		}
	}
}

// instalmentRow tells whether a row is one of an instalment: a conversion, a payment or
// interest.
func instalmentRow(t model.RecordType) bool {
	return t == model.TypeBillInstalment || t == model.TypeInstalmentPayment || t == model.TypeInterest
}

// adopt returns the plan as the ledger has it where the export ends before something that
// the ledger has: a plan of instalments in the middle of its periods, and the opening
// balance of an account that the export ends before the use of; and where it has no opening
// balance at all, which it then leaves to the ledger.
func (st *state) adopt(plan model.Plan) model.Plan {
	return st.adoptLedgerOpenings(st.adoptOpenings(st.adoptCuts(plan)))
}

// adoptLedgerOpenings returns the plan with the opening balances that the ledger has, and
// counts, among what the export says the accounts hold, when the export has rows with a day
// and none without one: it has no opening balance to say, and the balances that it is
// compared with are made of the ones the accounts started from. An export that has such
// rows says them itself, and one with no day at all has nothing to compare.
func (st *state) adoptLedgerOpenings(plan model.Plan) model.Plan {
	if first, _ := plan.Days(); first.IsZero() || slices.ContainsFunc(plan.Rows, func(r model.Row) bool { return !r.HasDate() }) {
		return plan
	}
	mine := map[string]bool{}
	for _, name := range plan.Accounts() {
		mine[name] = true
	}
	out := plan
	out.Effects = slices.Clone(plan.Effects)
	for _, t := range st.byKey {
		if t.Kind != openingKind || t.Status != "confirmed" {
			continue
		}
		if o := st.firstAccount(t); mine[o.Account] {
			out.Effects = append(out.Effects, model.Effect{Account: o.Account, Instrument: o.Instrument, Date: t.OccurredOn, Quantity: o.Amount, Row: t.IdempotencyKey})
		}
	}
	return out
}

// adoptCuts returns the plan as the ledger has it where the export ends in the middle of a
// plan of instalments: the rows of the instalment that the export has are the plan's, which
// the ledger has. So the interest among them is not a unit to record beside it, which would
// count it twice, and what the plan does not record of them is not unrecorded when the plan
// is confirmed, as the ledger counts it. A plan that is still a draft counts for nothing, so
// what the rows come to is unrecorded, as it is without the plan.
func (st *state) adoptCuts(plan model.Plan) model.Plan {
	if len(st.cuts) == 0 {
		return plan
	}
	counted := st.rowsOfCuts(plan)
	out := plan
	out.Units = nil
	var dropped []model.Unit
	for _, u := range plan.Units {
		if len(u.Rows) > 0 && !slices.ContainsFunc(u.Rows, func(row string) bool { _, ok := counted[row]; return !ok }) {
			dropped = append(dropped, u)
			continue
		}
		out.Units = append(out.Units, u)
	}
	out.Unrecorded = nil
	for _, e := range plan.Unrecorded {
		if !counted[e.Row] {
			out.Unrecorded = append(out.Unrecorded, e)
		}
	}
	for _, u := range dropped {
		if !counted[u.Rows[0]] {
			out.Unrecorded = append(out.Unrecorded, effectsOf(plan, u)...)
		}
	}
	return out
}

// rowsOfCuts are the rows of an instalment that the export has and a cut plan is of: the
// rows of a conversion, a payment or interest, on the account the plan charges and from the
// day of its origin on, that no plan the export has claims. The value is whether the ledger
// counts the row, which it does when the plan is confirmed.
func (st *state) rowsOfCuts(plan model.Plan) map[string]bool {
	claimed := map[string]bool{}
	for _, u := range plan.Units {
		if u.Kind == model.KindBillInstalment {
			for _, row := range u.Rows {
				claimed[row] = true
			}
		}
	}
	counted := map[string]bool{}
	for _, c := range st.cutsInOrder() {
		for _, r := range plan.Rows {
			if r.HasDate() && instalmentRow(r.Type) && !claimed[r.Key] && !r.Date.Before(c.tx.OccurredOn) &&
				plan.Mapping.LedgerAccount(r.Account) == c.account {
				counted[r.Key] = c.tx.Status == "confirmed"
			}
		}
	}
	return counted
}

// effectsOf are the effects of the rows a unit is made of.
func effectsOf(plan model.Plan, u model.Unit) []model.Effect {
	var out []model.Effect
	for _, e := range plan.Effects {
		if slices.Contains(u.Rows, e.Row) {
			out = append(out, e)
		}
	}
	return out
}

// adoptOpenings returns the plan without the opening balances that the ledger has on a day
// after the last day of the export, for an account that the export has no row of: such an
// opening balance is dated from the rows of another account, or of the file, which is not
// the day the ledger has it on, and the ledger's is the right one, the export ending before
// the account is used. What the opening balance comes to is not in the balances of the days
// of the export either.
func (st *state) adoptOpenings(plan model.Plan) model.Plan {
	later := st.openingsAfterTheEnd(plan)
	if len(later) == 0 {
		return plan
	}
	out := plan
	out.Units, out.Effects, out.Unrecorded = nil, nil, nil
	for _, u := range plan.Units {
		if u.Kind != model.KindOpening || !later[u.ID] {
			out.Units = append(out.Units, u)
		}
	}
	for _, e := range plan.Effects {
		if !later[e.Row] {
			out.Effects = append(out.Effects, e)
		}
	}
	for _, e := range plan.Unrecorded {
		if !later[e.Row] {
			out.Unrecorded = append(out.Unrecorded, e)
		}
	}
	return out
}

// openingsAfterTheEnd are the IDs of the opening balances of the plan that are dated from
// no row of their account, and that the ledger has, not void, on a day after the last day of
// the export.
func (st *state) openingsAfterTheEnd(plan model.Plan) map[string]bool {
	_, last := plan.Days()
	if last.IsZero() {
		return nil
	}
	fallback := map[string]bool{}
	for _, c := range plan.Caveats {
		if c.Kind == model.CaveatOpeningDate {
			fallback[c.Row] = true
		}
	}
	later := map[string]bool{}
	for key, t := range st.byKey {
		if parsed, ok := model.ParseKey(key); ok && fallback[parsed.ID] && t.Status != "void" && t.OccurredOn.After(last) {
			later[parsed.ID] = true
		}
	}
	return later
}

// cutsInOrder are the cut plans, the oldest first.
func (st *state) cutsInOrder() []cutPlan {
	out := make([]cutPlan, 0, len(st.cuts))
	for _, c := range st.cuts {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].tx.OccurredOn.Equal(out[j].tx.OccurredOn) {
			return out[i].tx.OccurredOn.Before(out[j].tx.OccurredOn)
		}
		return out[i].tx.IdempotencyKey < out[j].tx.IdempotencyKey
	})
	return out
}

// outside tells whether a transaction of the import that no unit of the plan accounts for
// is of something that the export says nothing of, and is left alone: a day it does not
// cover; a plan of instalments that it has a part of; an opening balance, when it has no
// opening balance at all (no row without a day), which is not saying that the ledger's are
// not there.
func (st *state) outside(t finance.Transaction) bool {
	if _, cut := st.cuts[t.ID]; cut {
		return true
	}
	if t.Kind == openingKind && !st.dateless {
		return true
	}
	return !st.covered(t.OccurredOn)
}
