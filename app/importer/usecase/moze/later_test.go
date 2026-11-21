package moze_test

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// The import works on the whole export of the history. One that begins after the import
// in the ledger does is a part of it with its start lost: what it says of the ledger is
// not meant for it (the openings would be dated from other rows, the days before it would
// be nothing), and the advice that follows from it, to void what the ledger has, would
// void what is right. apply, confirm and verify refuse it before anything is made or
// changed, status says so and nothing else, and the whole export goes on as if it had
// not been tried.

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
			want := usecase.LaterStart{From: day("2026-02-05"), LedgerFrom: day("2026-01-04")}
			ctx := context.Background()

			_, applyErr := w.imp.Apply(ctx, usecase.ApplyInput{Files: w.files})
			_, confirmErr := w.confirm("2026-02")
			_, verifyErr := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
			for name, err := range map[string]error{"apply": applyErr, "confirm": confirmErr, "verify": verifyErr} {
				var later *usecase.LaterStartError
				if !errors.As(err, &later) || !errors.Is(err, usecase.ErrLaterStart) || later.LaterStart != want {
					t.Errorf("%s = %v, want a refusal for an export of %s on, by an import of %s on",
						name, err, want.From.Format(time.DateOnly), want.LedgerFrom.Format(time.DateOnly))
				}
			}

			// status says it, and says nothing of the months, the orphans or what is not covered:
			// none of that is meant for an export that is not the whole of it.
			st, err := w.imp.Status(ctx, usecase.StatusInput{Files: w.files})
			if err != nil || st.LaterStart == nil || *st.LaterStart != want || len(st.Months) != 0 || len(st.Orphans) != 0 || st.Uncovered.Count != 0 {
				t.Errorf("status = %+v, %v, want only that the export begins late, %+v", st, err, want)
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
