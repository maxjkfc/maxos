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
// that are of days the export covers: it has the days of them, and says nothing of
// them, so they are not what it says. What it does not cover (an export that ends
// earlier, or begins later, than what was recorded of an older and longer one) is not
// an orphan: it is what the export does not say anything of, and is left alone.
func (st *state) orphans() []finance.Transaction {
	return slices.DeleteFunc(st.unaccounted(), func(t finance.Transaction) bool { return !st.covered(t.OccurredOn) })
}

// uncovered says what the ledger has of the import on days that the export does not
// cover, which no unit of the plan accounts for.
func (st *state) uncovered() usecase.Uncovered {
	u := usecase.Uncovered{First: st.first, Last: st.last}
	for _, t := range st.unaccounted() {
		if st.covered(t.OccurredOn) {
			continue
		}
		u.Count++
		if u.Count == 1 || t.OccurredOn.Before(u.From) {
			u.From = t.OccurredOn
		}
		if u.Count == 1 || t.OccurredOn.After(u.To) {
			u.To = t.OccurredOn
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
