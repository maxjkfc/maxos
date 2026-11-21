package moze_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// The import works on the whole export of the history. One that begins after the import
// in the ledger does is a part of it with its start lost, or one whose first rows the user
// deleted or changed: what it says of the ledger is not meant for it (the openings would be
// dated from other rows, the days before it would be nothing), so apply, confirm and verify
// refuse it before anything is made or changed, with the transactions that are before it,
// and the two things it can be; status says so and nothing else, and the whole export goes
// on as if it had not been tried. The opening balances are not among the transactions.

// confirmedThrough applies the fixture and confirms the months from January to the one
// that is named.
func confirmedThrough(t *testing.T, last string) *world {
	t.Helper()
	w := applied(t)
	for _, m := range []string{"2026-01", "2026-02", "2026-03", "2026-04"} {
		w.mustConfirm(m)
		if m == last {
			break
		}
	}
	return w
}

// snapshot says what the ledger holds and how often it was asked for anything: every
// transaction with its status and its day, what was made of the reference data, and how
// many times it was recorded in, confirmed, reconciled and discarded.
func (f *fakeLedger) snapshot() string {
	lines := make([]string, 0, len(f.txns))
	for _, t := range f.txns {
		lines = append(lines, fmt.Sprintf("%s %s %s", t.IdempotencyKey, t.Status, t.OccurredOn.Format(time.DateOnly)))
	}
	sort.Strings(lines)
	return fmt.Sprintf("%s\ncreated %v; recorded %d, confirmed %d, reconciled %d, discarded %d",
		strings.Join(lines, "\n"), f.created, len(f.calls), len(f.confirms), len(f.reconciled), len(f.discards))
}

// importedBefore are the keys of the transactions of the import that are not void and are
// not opening balances, dated before the day, oldest first: what the refusal of a later
// start names.
func (f *fakeLedger) importedBefore(day time.Time) []string {
	var found []*stored
	for _, t := range f.txns {
		if strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) && t.Status != "void" && t.Kind != "opening" && t.OccurredOn.Before(day) {
			found = append(found, t)
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if !found[i].OccurredOn.Equal(found[j].OccurredOn) {
			return found[i].OccurredOn.Before(found[j].OccurredOn)
		}
		return found[i].IdempotencyKey < found[j].IdempotencyKey
	})
	keys := make([]string, 0, len(found))
	for _, t := range found {
		keys = append(keys, t.IdempotencyKey)
	}
	return keys
}

// namedBy are the keys that a refusal of a later start names, in order.
func namedBy(l usecase.LaterStart) []string {
	keys := make([]string, 0, len(l.Before))
	for _, o := range l.Before {
		keys = append(keys, o.Key)
	}
	return keys
}

// refusals asks apply, confirm and verify to work on the export that is there, and says
// what each refused with.
func refusals(w *world, month string) map[string]error {
	ctx := context.Background()
	_, applyErr := w.imp.Apply(ctx, usecase.ApplyInput{Files: w.files})
	_, confirmErr := w.confirm(month)
	_, verifyErr := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	return map[string]error{"apply": applyErr, "confirm": confirmErr, "verify": verifyErr}
}

func TestAnExportThatBeginsAfterTheImportInTheLedgerDoesIsRefusedAndNothingIsChanged(t *testing.T) {
	for _, tt := range []struct {
		name     string
		openings bool
	}{
		{"with the rows of the opening balances", true},
		{"without them", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := confirmedThrough(t, "2026-03")
			before := w.ledger.snapshot()
			w.export(within(fixture(), "2026-02-01", "", tt.openings))
			wantFrom, wantLedgerFrom := day("2026-02-05"), day("2026-01-04")
			wantNamed := w.ledger.importedBefore(wantFrom)
			if len(wantNamed) == 0 {
				t.Fatal("the ledger has no transaction before the export, which is what the test is of")
			}
			ctx := context.Background()

			for name, err := range refusals(w, "2026-02") {
				var later *usecase.LaterStartError
				if !errors.As(err, &later) || !errors.Is(err, usecase.ErrLaterStart) ||
					!later.From.Equal(wantFrom) || !later.LedgerFrom.Equal(wantLedgerFrom) || !slices.Equal(namedBy(later.LaterStart), wantNamed) {
					t.Errorf("%s = %v, want a refusal for an export of %s on, by an import of %s on, naming the %d transactions before it and no opening balance",
						name, err, wantFrom.Format(time.DateOnly), wantLedgerFrom.Format(time.DateOnly), len(wantNamed))
				}
			}

			// status says it, and says nothing of the months, the orphans or what is not covered:
			// none of that is meant for an export that is not the whole of it.
			st, err := w.imp.Status(ctx, usecase.StatusInput{Files: w.files})
			if err != nil || st.LaterStart == nil || !st.LaterStart.From.Equal(wantFrom) || !slices.Equal(namedBy(*st.LaterStart), wantNamed) ||
				len(st.Months) != 0 || len(st.Orphans) != 0 || st.Uncovered.Count != 0 {
				t.Errorf("status = %+v, %v, want only that the export begins late, naming the same transactions", st, err)
			}
			if got := w.ledger.snapshot(); got != before {
				t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, got)
			}

			// The whole export again: nothing is different, and what the ledger has is right.
			w.export(fixture())
			out := w.apply()
			if sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 || len(out.Stale)+len(out.Orphans)+len(out.Removed)+len(out.Blocked)+len(out.Failed) != 0 || out.Uncovered.Count != 0 {
				t.Errorf("apply of the whole export = new %v, updated %v, stale %d, orphans %d, removed %d, uncovered %+v, want nothing at all",
					out.New, out.Updated, len(out.Stale), len(out.Orphans), len(out.Removed), out.Uncovered)
			}
			v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
			if err != nil || v.Unexplained != 0 || len(v.Months) != 4 || len(v.Stale)+len(v.Orphans) != 0 {
				t.Errorf("verify of the whole export = %d months, %d unexplained, stale %d, orphans %d, %v, want four months that agree",
					len(v.Months), v.Unexplained, len(v.Stale), len(v.Orphans), err)
			}
		})
	}
}

// withEarlyRow is the fixture and a row of the first day, on the card, which has no opening
// balance to be dated from it: the one row of a day that nothing else is on.
func withEarlyRow() []model.Row {
	early := row("2026-01-03 09:00", model.TypeExpense, "Card", "TWD", "-5", cat("Food", "Lunch"), name("First coffee"))
	return slices.Insert(fixture(), len(undated(fixture())), early)
}

// The user deletes in MOZE the one row of the first day: the export begins on a later
// day, and looks like one that was cut short. What the ledger has of that row is before it,
// and the refusal names it, with the two things it can be; the user voids it (a draft is
// discarded), and the import goes on. It is refused while the ledger has only drafts too.
func TestAnExportThatLostItsFirstRowIsRefusedWithWhatWasMadeOfItAndGoesOnOnceThatIsVoidedOrDiscarded(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		name := "a ledger of drafts"
		if confirmed {
			name = "a ledger that is confirmed"
		}
		t.Run(name, func(t *testing.T) {
			w := newWorld(t, withEarlyRow(), fixtureMapping()).reviewed()
			w.apply()
			if confirmed {
				for _, m := range []string{"2026-01", "2026-02", "2026-03"} {
					w.mustConfirm(m)
				}
			}
			stored := w.ledger.ofRow(w.rowKeyOf("First coffee"))
			if len(stored) != 1 {
				t.Fatalf("the ledger has %d transactions of the first row", len(stored))
			}
			first := stored[0]
			before := w.ledger.snapshot()
			w.export(fixture())
			ctx := context.Background()

			for name, err := range refusals(w, "2026-01") {
				var later *usecase.LaterStartError
				if !errors.As(err, &later) || !later.From.Equal(day("2026-01-05")) || !later.LedgerFrom.Equal(day("2026-01-03")) || len(later.Before) != 1 {
					t.Fatalf("%s = %v, want a refusal that names the one transaction before 5 January", name, err)
				}
				o := later.Before[0]
				wantStatus := map[bool]string{true: "confirmed", false: "draft"}[confirmed]
				if o.Key != first.IdempotencyKey || o.Status != wantStatus || !o.Date.Equal(day("2026-01-03")) || o.Account != "Card" || o.Instrument != "TWD" || !o.Amount.Equal(d("-5")) {
					t.Errorf("%s names %+v, want the first row's %s transaction of the card: -5 on 3 January", name, o, wantStatus)
				}
			}
			if got := w.ledger.snapshot(); got != before {
				t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, got)
			}

			// The user voids it, or discards it, as the refusal says: the import goes on.
			if confirmed {
				if n := w.ledger.void(first.IdempotencyKey); n != 1 {
					t.Fatalf("voided %d", n)
				}
			} else if err := w.ledger.DiscardDraft(ctx, first.ID); err != nil {
				t.Fatal(err)
			}
			out := w.apply()
			if sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 || len(out.Stale)+len(out.Orphans)+len(out.Removed)+len(out.Blocked)+len(out.Failed) != 0 {
				t.Errorf("apply after it = new %v, updated %v, stale %d, orphans %d, removed %d, want nothing: the row is not in the export", out.New, out.Updated, len(out.Stale), len(out.Orphans), len(out.Removed))
			}
			for _, m := range []string{"2026-01", "2026-02", "2026-03"} {
				if _, err := w.confirm(m); err != nil {
					t.Errorf("confirm %s after it = %v", m, err)
				}
			}
			v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
			if err != nil || v.Unexplained != 0 || len(v.Months) < 3 || len(v.Stale)+len(v.Orphans) != 0 {
				t.Errorf("verify after it = %d months, %d unexplained, stale %d, orphans %d, %v, want the months to agree", len(v.Months), v.Unexplained, len(v.Stale), len(v.Orphans), err)
			}
		})
	}
}

// An opening balance is dated from the first row of its account, so a whole export has
// them on the day before its first row, and from the first row of its file, which is
// before every other row, for an account with no row: they are not what says that an
// export begins late.
func TestTheOpeningBalancesOfTheLedgerAreNotWhatSaysThatAnExportBeginsLate(t *testing.T) {
	w := confirmedThrough(t, "2026-01")
	if n, _, _ := w.ledger.importedOutside(func(d time.Time) bool { return d.Before(day("2026-01-05")) }); n == 0 {
		t.Fatal("the ledger has no opening balance dated before the first row of the export")
	}
	if out := w.apply(); sum(out.New)+sum(out.Updated) != 0 || len(out.Stale)+len(out.Orphans) != 0 {
		t.Errorf("apply of the whole export = new %v, updated %v, stale %d, orphans %d, want nothing", out.New, out.Updated, len(out.Stale), len(out.Orphans))
	}
	if _, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files}); err != nil {
		t.Errorf("verify of the whole export = %v", err)
	}
}

// An export of one month is the whole of it only when it is the first.
func TestAnExportOfOneMonthIsRefusedUnlessItIsTheFirst(t *testing.T) {
	w := confirmedThrough(t, "2026-03")
	ctx := context.Background()
	for _, tt := range []struct {
		month, from, to string
		refused         bool
	}{
		{"January", "2026-01-01", "2026-01-31", false},
		{"February", "2026-02-01", "2026-02-28", true},
		{"March", "2026-03-01", "2026-03-31", true},
	} {
		w.export(within(fixture(), tt.from, tt.to, true))
		_, err := w.imp.Apply(ctx, usecase.ApplyInput{Files: w.files})
		if refused := errors.Is(err, usecase.ErrLaterStart); refused != tt.refused || (!tt.refused && err != nil) {
			t.Errorf("apply of the export of %s = %v, want it refused: %v", tt.month, err, tt.refused)
		}
	}
}
