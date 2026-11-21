package moze_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// An edited row is another row: its key is made of what it says. What the import
// recorded of the row as it was is then a transaction that no unit of the plan accounts
// for: an orphan, which is named, and which is discarded when it is a draft and left to
// the user to void when it is confirmed (counting toward the balances all the same).

// edit changes the row that is on the day and the time, in the account, of the amount;
// a nil change takes it out of the export.
func edit(t *testing.T, rows []model.Row, when, account, amount string, change func(*model.Row)) []model.Row {
	t.Helper()
	out := slices.Clone(rows)
	for i, r := range out {
		if r.Date.Format("2006-01-02")+" "+r.Clock == when && r.Account == account && r.Amount.Equal(d(amount)) {
			if change == nil {
				return slices.Delete(out, i, i+1)
			}
			change(&out[i])
			return out
		}
	}
	t.Fatalf("no row of %s on %s for %s", account, when, amount)
	return nil
}

// theEdits are the ways a row of the export is changed in MOZE: what it says of the
// amount, the day or the account, or the row is deleted. Each is of the expense of 40
// that Cash paid on 15 January.
var theEdits = []struct {
	name string
	// change is nil for a row that is deleted.
	change func(*model.Row)
	// replaced is whether the row is still in the export, as another.
	replaced bool
}{
	{"amount", func(r *model.Row) { r.Amount = d("-45") }, true},
	{"day", func(r *model.Row) { r.Date = day("2026-01-16") }, true},
	{"account", func(r *model.Row) { r.Account = "Wallet" }, true},
	{"deleted", nil, false},
}

func TestARowEditedAfterItsMonthWasConfirmedLeavesAnOrphanThatIsNamed(t *testing.T) {
	for _, tt := range theEdits {
		t.Run(tt.name, func(t *testing.T) {
			w := applied(t)
			w.mustConfirm("2026-01")
			rows := edit(t, fixture(), "2026-01-15 10:00", "Cash", "-40", tt.change)
			w.export(rows)

			out := w.apply()
			if len(out.Orphans) != 1 || len(out.Removed) != 0 {
				t.Fatalf("apply = orphans %+v, removed %+v, want the confirmed expense as the one orphan", out.Orphans, out.Removed)
			}
			o := out.Orphans[0]
			if !o.Confirmed() || o.Kind != "expense" || !o.Date.Equal(day("2026-01-15")) || o.Account != "Cash" || o.Instrument != "TWD" ||
				!o.Amount.Equal(d("-40")) || !strings.HasPrefix(o.Key, model.KeyPrefix) {
				t.Errorf("orphan = %+v, want the confirmed expense of 40 that Cash paid on 15 January, by its key", o)
			}
			if got, want := sum(out.New), map[bool]int{true: 1, false: 0}[tt.replaced]; got != want {
				t.Errorf("apply recorded %d units, want %d: the row as it is now", got, want)
			}
			if len(w.ledger.discards) != 0 {
				t.Errorf("discards = %v, want none: a confirmed transaction is not discarded", w.ledger.discards)
			}

			// status and verify name it, and confirm refuses until it is voided.
			st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
			if err != nil || len(st.Orphans) != 1 || st.Orphans[0].Key != o.Key {
				t.Errorf("status = %+v, %v, want the orphan named", st.Orphans, err)
			}
			v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
			if err != nil || len(v.Orphans) != 1 || v.Orphans[0].Key != o.Key {
				t.Errorf("verify = %+v, %v, want the orphan named", v.Orphans, err)
			}
			_, err = w.confirm("2026-01")
			var refusal *usecase.OrphanError
			if !errors.Is(err, usecase.ErrOrphan) || !errors.As(err, &refusal) || len(refusal.Orphans) != 1 || refusal.Orphans[0].Key != o.Key || refusal.Month != "2026-01" {
				t.Errorf("confirm err = %v, want it refused for the orphan", err)
			}

			// The user voids it: the ledger is what the export says, once the row as it is
			// now is confirmed.
			if n := w.ledger.void(o.Key); n != 1 {
				t.Fatalf("voided %d transactions", n)
			}
			again := w.apply()
			if len(again.Orphans) != 0 || len(again.Removed) != 0 || sum(again.New)+sum(again.Updated)+sum(again.Revived) != 0 {
				t.Errorf("apply after the void = %+v, want nothing to do", again)
			}
			w.mustConfirm("2026-01")
			v, err = w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
			if err != nil || v.Unexplained != 0 || len(v.Orphans) != 0 || len(v.Months) != 1 {
				t.Errorf("verify after the void = %+v, %v, want January to agree with the export", v, err)
			}
		})
	}
}

func TestARowEditedBeforeItsMonthIsConfirmedHasItsDraftRemoved(t *testing.T) {
	for _, tt := range theEdits {
		t.Run(tt.name, func(t *testing.T) {
			w := applied(t)
			old := w.ledger.ofKind("expense")
			var draft *stored
			for _, x := range old {
				if x.OccurredOn.Equal(day("2026-01-15")) && x.Postings[0].Quantity.Equal(d("-40")) {
					draft = x
				}
			}
			if draft == nil {
				t.Fatal("the expense of 40 is not in the ledger")
			}
			drafts := w.ledger.drafts()
			w.export(edit(t, fixture(), "2026-01-15 10:00", "Cash", "-40", tt.change))

			// Before apply, status names it as an orphan, a draft.
			st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
			if err != nil || len(st.Orphans) != 1 || st.Orphans[0].Status != "draft" || st.Orphans[0].Key != draft.IdempotencyKey {
				t.Errorf("status before apply = %+v, %v, want the draft named", st.Orphans, err)
			}

			out := w.apply()
			if len(out.Removed) != 1 || len(out.Orphans) != 0 {
				t.Fatalf("apply = removed %+v, orphans %+v, want the draft as the one removed", out.Removed, out.Orphans)
			}
			r := out.Removed[0]
			if r.Status != "draft" || r.Key != draft.IdempotencyKey || !r.Date.Equal(day("2026-01-15")) || r.Account != "Cash" || !r.Amount.Equal(d("-40")) {
				t.Errorf("removed = %+v, want the draft of the expense of 40, by its key", r)
			}
			if !slices.Contains(w.ledger.discards, draft.ID) {
				t.Errorf("discards = %v, want the draft discarded", w.ledger.discards)
			}
			if w.ledger.byID(draft.ID) != nil {
				t.Error("the draft is still in the ledger")
			}
			want := drafts
			if !tt.replaced {
				want--
			}
			if w.ledger.drafts() != want {
				t.Errorf("drafts = %d, want %d: the row as it is now, in place of the one it was", w.ledger.drafts(), want)
			}

			again := w.apply()
			if len(again.Removed) != 0 || len(again.Orphans) != 0 || sum(again.New)+sum(again.Updated) != 0 {
				t.Errorf("a second apply = %+v, want nothing to do", again)
			}
			w.mustConfirm("2026-01")
			v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
			if err != nil || v.Unexplained != 0 || len(v.Orphans) != 0 {
				t.Errorf("verify = %+v, %v", v, err)
			}
		})
	}
}

func TestAnAdjustmentEditedAfterItWasReconciledLeavesTheReconciliationAnOrphan(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	// The 餘額調整 of Cash on 15 January was made up as a reconciliation. It is edited.
	w.export(edit(t, fixture(), "2026-01-15 12:00", "Cash", "-20", func(r *model.Row) { r.Amount = d("-25") }))

	out := w.apply()
	if len(out.Orphans) != 1 || out.Orphans[0].Kind != "reconciliation" || out.Orphans[0].Account != "Cash" || !out.Orphans[0].Date.Equal(day("2026-01-15")) {
		t.Fatalf("orphans = %+v, want the reconciliation of Cash of that day", out.Orphans)
	}
	if _, err := w.confirm("2026-01"); !errors.Is(err, usecase.ErrOrphan) {
		t.Errorf("confirm err = %v, want it refused for the reconciliation that the export does not have", err)
	}
}

func TestADraftTheExportLostIsRemovedByApplyAndConfirmedOnesAreNot(t *testing.T) {
	// The export is another now: it has neither the expense of 15 January (confirmed)
	// nor the one of 2 March (a draft).
	w := applied(t)
	w.mustConfirm("2026-01")
	rows := edit(t, fixture(), "2026-01-15 10:00", "Cash", "-40", nil)
	rows = edit(t, rows, "2026-03-02 12:00", "Card", "-300", nil)
	w.export(rows)

	out := w.apply()
	if len(out.Orphans) != 1 || !out.Orphans[0].Confirmed() || len(out.Removed) != 1 || out.Removed[0].Status != "draft" {
		t.Errorf("apply = orphans %+v, removed %+v, want the confirmed one left and the draft removed", out.Orphans, out.Removed)
	}
	if !out.Orphans[0].Date.Equal(day("2026-01-15")) || !out.Removed[0].Date.Equal(day("2026-03-02")) {
		t.Errorf("apply named %v and %v", out.Orphans[0].Date, out.Removed[0].Date)
	}
}
