package moze_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// An export is the whole history only when the user makes it so. One that ends earlier
// than the export that was applied before says nothing of the days it does not reach:
// what the ledger has of those days is not what it says should not be there, so it is
// neither an orphan nor removed. What it does say of the days it reaches is said as
// before: a month in the middle that it has no rows of is a month whose transactions are
// orphans. One that begins later is refused (see later_test.go).

// within keeps the rows of an export that are dated from one day to another ("" for no
// limit), and those that have no day (the openings) when keepOpenings is true.
func within(rows []model.Row, from, to string, keepOpenings bool) []model.Row {
	var out []model.Row
	for _, r := range rows {
		switch {
		case r.Date.IsZero():
			if keepOpenings {
				out = append(out, r)
			}
		case from != "" && r.Date.Before(day(from)), to != "" && r.Date.After(day(to)):
		default:
			out = append(out, r)
		}
	}
	return out
}

// importedOutside says how many transactions of the import the ledger has, drafts and
// confirmed ones, of the days that the filter says, and the first and the last of them.
func (f *fakeLedger) importedOutside(outside func(time.Time) bool) (n int, from, to time.Time) {
	for _, t := range f.txns {
		if !strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) || t.Status == "void" || !outside(t.OccurredOn) {
			continue
		}
		n++
		if from.IsZero() || t.OccurredOn.Before(from) {
			from = t.OccurredOn
		}
		if to.IsZero() || t.OccurredOn.After(to) {
			to = t.OccurredOn
		}
	}
	return n, from, to
}

func TestAnExportThatEndsEarlierLeavesWhatTheLedgerHasOfLaterDaysAloneAndSaysSo(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	later := func(d time.Time) bool { return d.After(day("2026-01-31")) }
	n, from, to := w.ledger.importedOutside(later)
	if n == 0 {
		t.Fatal("the ledger has nothing of February or later")
	}
	txns := len(w.ledger.txns)
	w.export(within(fixture(), "", "2026-01-31", true))

	out := w.apply()
	if len(out.Removed) != 0 || len(out.Orphans) != 0 || len(w.ledger.discards) != 0 || len(w.ledger.txns) != txns {
		t.Fatalf("apply = removed %d, orphans %d, discards %d, ledger %d -> %d, want nothing removed: the export does not reach those days",
			len(out.Removed), len(out.Orphans), len(w.ledger.discards), txns, len(w.ledger.txns))
	}
	// The export covers 4 to 27 January: the openings are dated from the first row of their
	// account, the 4th, a day that no row has.
	u := out.Uncovered
	if u.Count != n || !u.First.Equal(day("2026-01-04")) || !u.Last.Equal(day("2026-01-27")) || !u.From.Equal(from) || !u.To.Equal(to) {
		t.Errorf("uncovered = %+v, want %d transactions from %s to %s, by an export of 4 to 27 January", u, n, from.Format(time.DateOnly), to.Format(time.DateOnly))
	}
	if again := w.apply(); len(again.Removed) != 0 || len(again.Orphans) != 0 || again.Uncovered != u {
		t.Errorf("a second apply = removed %d, orphans %d, uncovered %+v, want the same again", len(again.Removed), len(again.Orphans), again.Uncovered)
	}

	// status says it too, and what the ledger has of the months the export has is checked as
	// usual: the months it does not have are not asked for.
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || len(st.Orphans) != 0 || st.Uncovered != u {
		t.Errorf("status = orphans %d, uncovered %+v, %v, want none and the same", len(st.Orphans), st.Uncovered, err)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || v.Open != "" || len(v.Months) != 1 || len(v.Orphans) != 0 {
		t.Errorf("verify = %+v, %v, want January to agree with the export", v, err)
	}
	if _, err := w.confirm("2026-01"); err != nil {
		t.Errorf("confirm January = %v", err)
	}
	if _, err := w.confirm("2026-02"); !errors.Is(err, usecase.ErrUnknownMonth) {
		t.Errorf("confirm February = %v, want the export to have nothing in it, as usual", err)
	}
}

func TestAnExportThatHasNoRowsOfAMonthInTheMiddleHasItsTransactionsAsOrphans(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	w.mustConfirm("2026-02")
	// February's rows are gone, with the rest of the export on both sides of them.
	rows := within(fixture(), "", "2026-01-31", true)
	rows = append(rows, within(fixture(), "2026-03-01", "", false)...)
	w.export(rows)

	out := w.apply()
	if len(out.Orphans) == 0 {
		t.Fatalf("apply = orphans 0, removed %d, want the confirmed transactions of February to be orphans: the export reaches those days and has none of them", len(out.Removed))
	}
	for _, o := range out.Orphans {
		if o.Date.Before(day("2026-02-01")) || o.Date.After(day("2026-02-28")) {
			t.Errorf("orphan = %+v, want the ones of February", o)
		}
	}
	if out.Uncovered.Count != 0 {
		t.Errorf("uncovered = %+v, want nothing: every day the ledger has is inside the export's range", out.Uncovered)
	}
	if _, err := w.confirm("2026-03"); !errors.Is(err, usecase.ErrOrphan) {
		t.Errorf("confirm March = %v, want it refused for the orphans", err)
	}
}

func TestAnExportWithNoDayAtAllCoversNothingAndRemovesNothing(t *testing.T) {
	w := applied(t)
	txns := len(w.ledger.txns)
	w.export(undated(fixture()))

	out := w.apply()
	if len(out.Removed) != 0 || len(out.Orphans) != 0 || len(w.ledger.txns) != txns {
		t.Fatalf("apply = removed %d, orphans %d, ledger %d -> %d, want nothing removed: the export has no day", len(out.Removed), len(out.Orphans), txns, len(w.ledger.txns))
	}
	n, _, _ := w.ledger.importedOutside(func(time.Time) bool { return true })
	if !out.Uncovered.First.IsZero() || out.Uncovered.Count != n {
		t.Errorf("uncovered = %+v, want all %d transactions, by an export of no day", out.Uncovered, n)
	}
}

func TestAnExportWithNoRowsAtAllCoversNothingAndRemovesNothing(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	before := w.ledger.snapshot()
	w.export(nil)
	ctx := context.Background()

	out := w.apply()
	n, _, _ := w.ledger.importedOutside(func(time.Time) bool { return true })
	if len(out.Removed) != 0 || len(out.Orphans) != 0 || out.Uncovered.Count != n || !out.Uncovered.First.IsZero() {
		t.Errorf("apply = removed %d, orphans %d, uncovered %+v, want nothing removed and all %d told: the export has no day, so it does not begin after anything",
			len(out.Removed), len(out.Orphans), out.Uncovered, n)
	}
	if _, err := w.confirm("2026-01"); !errors.Is(err, usecase.ErrUnknownMonth) {
		t.Errorf("confirm = %v, want the export to have nothing in the month", err)
	}
	v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	if err != nil || len(v.Months) != 0 || v.Unexplained != 0 {
		t.Errorf("verify = %+v, %v, want nothing to compare", v, err)
	}
	st, err := w.imp.Status(ctx, usecase.StatusInput{Files: w.files})
	if err != nil || st.LaterStart != nil || len(st.Orphans) != 0 || st.Uncovered.Count != n {
		t.Errorf("status = %+v, %v, want the transactions told as what the export does not cover", st, err)
	}
	if got := w.ledger.snapshot(); got != before {
		t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, got)
	}
}

// undated keeps the rows that have no day: the openings.
func undated(rows []model.Row) []model.Row {
	var out []model.Row
	for _, r := range rows {
		if r.Date.IsZero() {
			out = append(out, r)
		}
	}
	return out
}
