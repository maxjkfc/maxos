package moze

import (
	"slices"
	"sort"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// unaccounted are the transactions of the import, drafts and confirmed ones, that no
// unit of the plan accounts for: the row was edited (an edited row is another row,
// which has another key), or deleted, or is left out now, or it is of a day that the
// export does not reach at all. A transaction of an earlier version of a unit, or of a
// pairing of rows that the plan now has otherwise, is not one: it is what the unit
// replaces. Void ones are not: they count toward nothing. The drafts that apply has
// discarded since the state was read are not. They are oldest first.
func (st *state) unaccounted() []finance.Transaction {
	var out []finance.Transaction
	for key, t := range st.byKey {
		if t.Status == "void" || st.adjustments[key] || st.discarded[t.ID] {
			continue
		}
		parsed, ok := model.ParseKey(key)
		if !ok || st.planIDs[parsed.ID] || slices.ContainsFunc(parsed.Rows(), func(row string) bool { return st.planRows[row] }) {
			continue
		}
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].OccurredOn.Equal(out[j].OccurredOn) {
			return out[i].OccurredOn.Before(out[j].OccurredOn)
		}
		return out[i].IdempotencyKey < out[j].IdempotencyKey
	})
	return out
}

// orphans are the transactions of the import that no unit of the plan accounts for and
// that the export says something of: it has the days of them, and not them, so they are
// not what it says. What it says nothing of is not an orphan, and is left alone: the days
// that an export that ends earlier than an older and longer one does not reach, and what
// is told by outside.
func (st *state) orphans() []finance.Transaction {
	return slices.DeleteFunc(st.unaccounted(), st.outside)
}

// uncovered says what the ledger has of the import that the export says nothing of, which
// no unit of the plan accounts for, with the first and the last day of it: the day of the
// last period for a plan of instalments the export ends before the end of.
func (st *state) uncovered() usecase.Uncovered {
	u := usecase.Uncovered{First: st.first, Last: st.last}
	for _, t := range st.unaccounted() {
		if !st.outside(t) {
			continue
		}
		from, to := t.OccurredOn, t.OccurredOn
		if c, cut := st.cuts[t.ID]; cut {
			to = c.end
		}
		u.Count++
		if u.Count == 1 || from.Before(u.From) {
			u.From = from
		}
		if u.Count == 1 || to.After(u.To) {
			u.To = to
		}
	}
	return u
}

// orphanViews are the orphans as the use cases report them.
func (st *state) orphanViews() []usecase.Orphan {
	var out []usecase.Orphan
	for _, t := range st.orphans() {
		out = append(out, st.orphanOf(t))
	}
	return out
}

// orphanOf says what a transaction of the import is: its day, the first account it
// moves with how much, and what it says of itself.
func (st *state) orphanOf(t finance.Transaction) usecase.Orphan {
	o := st.firstAccount(t)
	o.Key, o.Status, o.Kind, o.Date, o.Description = t.IdempotencyKey, t.Status, t.Kind, t.OccurredOn, t.Description
	return o
}

// firstAccount is what the transaction does to the first account it moves: the
// account and the instrument by name and by code, and the quantity with its sign.
func (st *state) firstAccount(t finance.Transaction) usecase.Orphan {
	for _, p := range t.Postings {
		if p.AccountID != nil {
			return usecase.Orphan{Account: st.accountNames[*p.AccountID], Instrument: st.instrumentCodes[p.InstrumentID], Amount: p.Quantity}
		}
	}
	return usecase.Orphan{}
}
