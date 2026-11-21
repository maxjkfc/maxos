package moze_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// An export that ends earlier than the one before it says nothing of the days after its
// last, and must not be made to: it is compared with the ledger as of that day, and what
// the ledger has of the later days, the plan of instalments that runs on past it included,
// is the ledger's and is left alone, so that nothing in what follows asks for a void.

// withMay is the fixture and a month more, May, that the plan of instalments is not in.
func withMay() []model.Row {
	const exp = model.TypeExpense
	return append(fixture(),
		row("2026-05-04 10:00", exp, "Cash", "TWD", "-10", cat("Food", "Lunch")),
		row("2026-05-20 10:00", exp, "Cash", "TWD", "-20", cat("Food", "Lunch")),
		row("2026-05-28 10:00", exp, "Cash", "TWD", "-30", cat("Food", "Lunch")),
	)
}

// confirmedThroughMay applies the fixture with its May and confirms the five months.
func confirmedThroughMay(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, withMay(), fixtureMapping()).reviewed()
	if out := w.apply(); len(out.Failed) != 0 {
		t.Fatalf("apply failed: %+v", out.Failed)
	}
	for _, m := range []string{"2026-01", "2026-02", "2026-03", "2026-04", "2026-05"} {
		w.mustConfirm(m)
	}
	return w
}

func TestTheMonthAnExportStopsInIsComparedAsOfItsLastDay(t *testing.T) {
	w := confirmedThroughMay(t)
	ctx := context.Background()
	monthEnds := []string{"2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30"}

	// The export of the whole history says May up to the 28th, its last row: that is as far
	// as the month goes, and the earlier months go to their end.
	v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	if err != nil || len(v.Months) != 5 || v.Unexplained != 0 {
		t.Fatalf("verify of the whole export = %d months, %d unexplained, %v", len(v.Months), v.Unexplained, err)
	}
	for i, end := range monthEnds {
		if m := v.Months[i]; m.Partial || !m.End.Equal(day(end)) {
			t.Errorf("%s is compared as of %s, partial %v, want the end of the month", m.Month, m.End.Format(time.DateOnly), m.Partial)
		}
	}
	if m := v.Months[4]; !m.Partial || !m.End.Equal(day("2026-05-28")) {
		t.Errorf("May is compared as of %s, partial %v, want the 28th, its last row", m.End.Format(time.DateOnly), m.Partial)
	}

	// An export that ends on the 20th: the 28th is in the ledger and not in the export, and
	// is not what the month is compared with.
	w.export(within(withMay(), "", "2026-05-20", true))
	v, err = w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	if err != nil || len(v.Months) != 5 || v.Unexplained != 0 || v.Open != "" {
		t.Fatalf("verify of the export to 20 May = %d months, %d unexplained, open %q, %v, want five that agree", len(v.Months), v.Unexplained, v.Open, err)
	}
	for i, end := range monthEnds {
		if m := v.Months[i]; m.Partial || !m.End.Equal(day(end)) {
			t.Errorf("%s is compared as of %s, partial %v, want the end of the month, as before", m.Month, m.End.Format(time.DateOnly), m.Partial)
		}
	}
	if m := v.Months[4]; !m.Partial || !m.End.Equal(day("2026-05-20")) {
		t.Errorf("May is compared as of %s, partial %v, want the 20th, the last day of the export", m.End.Format(time.DateOnly), m.Partial)
	}
	out, err := w.confirm("2026-05")
	if err != nil || !out.Check.Partial || !out.Check.End.Equal(day("2026-05-20")) || out.Check.Unexplained() != 0 {
		t.Errorf("confirm May = partial %v, as of %s, %d unexplained, %v, want it to agree as of the 20th",
			out.Check.Partial, out.Check.End.Format(time.DateOnly), out.Check.Unexplained(), err)
	}
	apply := w.apply()
	if len(apply.Orphans)+len(apply.Removed)+len(apply.Stale) != 0 || sum(apply.New)+sum(apply.Updated) != 0 || apply.Uncovered.Count != 1 {
		t.Errorf("apply = orphans %d, removed %d, stale %d, new %v, uncovered %+v, want the 28th told and nothing else",
			len(apply.Orphans), len(apply.Removed), len(apply.Stale), apply.New, apply.Uncovered)
	}
}

// The plan of instalments is of the 5th of February, with periods on the 15th of February,
// March and April. An export that ends before the last of them has the rows of the
// conversion and of the periods that fell due, which do not add up to what was converted,
// so that the plan is not there: it is the ledger's, and not an orphan, and its rows are
// not units to record beside it.
func TestAPlanOfInstalmentsThatRunsPastTheEndOfTheExportIsTheLedgersAndNotAnOrphan(t *testing.T) {
	for _, tt := range []struct {
		name, end string
		confirmed bool
		months    int
	}{
		{"confirmed, the export ends on a period", "2026-03-15", true, 3},
		{"confirmed, the export ends between two periods", "2026-03-10", true, 3},
		{"confirmed, the export ends before the first period", "2026-02-10", true, 2},
		{"draft, the export ends on a period", "2026-03-15", false, 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var w *world
			if tt.confirmed {
				w = confirmedThrough(t, "2026-03")
			} else {
				w = applied(t)
				w.mustConfirm("2026-01")
			}
			origin := w.ledger.ofKind("instalment")[0].Transaction
			before := len(w.ledger.txns)
			w.export(within(fixture(), "", tt.end, true))
			ctx := context.Background()

			out := w.apply()
			if len(out.Orphans)+len(out.Removed)+len(out.Stale)+len(out.Blocked)+len(out.Failed) != 0 || sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 || len(w.ledger.txns) != before {
				t.Fatalf("apply = orphans %d, removed %d, stale %d, blocked %d, new %v, updated %v, ledger %d -> %d, want the plan left alone and its rows not recorded beside it",
					len(out.Orphans), len(out.Removed), len(out.Stale), len(out.Blocked), out.New, out.Updated, before, len(w.ledger.txns))
			}
			// It is told, with the day of its last period, as what the export has a part of.
			if u := out.Uncovered; u.Count < 1 || !u.To.Equal(day("2026-04-15")) {
				t.Errorf("uncovered = %+v, want the plan told, to the day of its last period", u)
			}
			if got := w.ledger.byID(origin.ID); got == nil || got.Status != origin.Status {
				t.Errorf("the plan is %v, want it as it was, %s", got, origin.Status)
			}

			// What the export says of the months it has agrees with the ledger.
			if !tt.confirmed {
				for _, m := range []string{"2026-02", "2026-03"} {
					if m <= tt.end[:7] {
						w.mustConfirm(m)
					}
				}
			}
			v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
			if err != nil || v.Unexplained != 0 || v.Open != "" || len(v.Months) != tt.months || len(v.Orphans)+len(v.Stale)+len(v.Blocked) != 0 {
				t.Errorf("verify = %d months, %d unexplained, open %q, orphans %d, stale %d, blocked %d, %v, want %d months that agree",
					len(v.Months), v.Unexplained, v.Open, len(v.Orphans), len(v.Stale), len(v.Blocked), err, tt.months)
			}
			last := tt.end[:7]
			if _, err := w.confirm(last); err != nil {
				t.Errorf("confirm %s = %v, want it not stopped by the plan", last, err)
			}
		})
	}
}

// What the export has of a plan whose periods have all fallen due by its last day is all of
// it: a plan that is not there is one whose rows were edited, and is told as before.
func TestAPlanOfInstalmentsWhosePeriodsAllFellDueByTheEndOfTheExportIsStillAnOrphanWhenItsRowsAreEdited(t *testing.T) {
	w := confirmedThrough(t, "2026-03")
	rows := fixture()
	for i, r := range rows {
		if r.Type == model.TypeInstalmentPayment && r.Date.Equal(day("2026-03-15")) {
			rows[i].Amount = d("-190")
		}
	}
	w.export(rows)

	out := w.apply()
	if len(out.Orphans) != 1 || len(out.Blocked) < 2 {
		t.Errorf("apply = orphans %d, blocked %d, want the plan as an orphan and the interest rows it holds back", len(out.Orphans), len(out.Blocked))
	}
}

// An export with no opening balance in it has nothing to say of the ones the ledger has,
// which are not orphans for it, nor left out of what it covers: the openings are part of
// the history it describes, which the accounts started from. The balances it says are
// compared with the ledger's including them, so that the months agree.
func TestAnExportWithNoOpeningBalanceDoesNotMakeOrphansOfTheOnesTheLedgerHas(t *testing.T) {
	w := confirmedThrough(t, "2026-03")
	w.export(within(fixture(), "", "2026-03-15", false))

	out := w.apply()
	if len(out.Orphans)+len(out.Removed)+len(out.Stale) != 0 {
		t.Errorf("apply = orphans %d, removed %d, stale %d, want none: it is an export with no opening balance, not one that has deleted them",
			len(out.Orphans), len(out.Removed), len(out.Stale))
	}
	if out.Uncovered.Count != 1 {
		t.Errorf("uncovered = %+v, want only the plan of instalments that the export ends in the middle of: the opening balances are part of what it describes", out.Uncovered)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Months) != 3 || len(v.Orphans)+len(v.Stale) != 0 {
		t.Errorf("verify = %d months, %d unexplained, orphans %d, stale %d, %v, want the three months to agree with the openings of the ledger counted",
			len(v.Months), v.Unexplained, len(v.Orphans), len(v.Stale), err)
	}
}

// An account that has no row in an export that ends before it is used has its opening
// balance dated from other rows, which is not the day the ledger has it on, after the end
// of the export: the ledger's is right, and is left as it is, not asked to be voided.
func TestTheOpeningBalanceOfAnAccountThatTheExportEndsBeforeIsLeftAlone(t *testing.T) {
	rows := append(fixture(),
		row("", model.TypeInitial, "Purse", "TWD", "500"),
		row("2026-03-20 10:00", model.TypeExpense, "Purse", "TWD", "-10", cat("Food", "Lunch")),
	)
	w := newWorld(t, rows, fixtureMapping()).reviewed()
	if out := w.apply(); len(out.Failed) != 0 {
		t.Fatalf("apply failed: %+v", out.Failed)
	}
	for _, m := range []string{"2026-01", "2026-02", "2026-03"} {
		w.mustConfirm(m)
	}
	before := w.ledger.snapshot()
	w.export(within(rows, "", "2026-03-15", true))

	out := w.apply()
	if len(out.Stale)+len(out.Orphans)+len(out.Removed) != 0 || sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 {
		t.Errorf("apply = stale %d, orphans %d, removed %d, new %v, updated %v, want the opening balance of the purse left as it is",
			len(out.Stale), len(out.Orphans), len(out.Removed), out.New, out.Updated)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Stale)+len(v.Orphans) != 0 {
		t.Errorf("verify = %d unexplained, stale %d, orphans %d, %v, want the months to agree", v.Unexplained, len(v.Stale), len(v.Orphans), err)
	}
	if got := w.ledger.snapshot(); got != before {
		t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, got)
	}
}

// The rows that a plan the export ends in the middle of is made of are those of its
// conversion, of its payments on the days its periods fall due, and the interest of those
// days. An interest of the same account on another day is not its own: it is a unit, as it
// is without the plan, and the ledger has it, so that it is not an orphan.
func TestAnInterestRowThatIsNotThePlansIsNotClaimedByAPlanTheExportEndsInTheMiddleOf(t *testing.T) {
	rows := fixture()
	at := slices.IndexFunc(rows, func(r model.Row) bool { return r.Date.Equal(day("2026-04-15")) })
	rows = slices.Insert(rows, at, row("2026-03-20 10:00", model.TypeInterest, "Card instalments", "TWD", "-3", cat("利息支出", "利息支出")))
	w := newWorld(t, rows, fixtureMapping()).reviewed()
	if out := w.apply(); len(out.Failed) != 0 {
		t.Fatalf("apply failed: %+v", out.Failed)
	}
	for _, m := range []string{"2026-01", "2026-02", "2026-03"} {
		w.mustConfirm(m)
	}
	before := w.ledger.snapshot()
	w.export(within(rows, "", "2026-03-25", true))

	out := w.apply()
	if len(out.Orphans)+len(out.Removed)+len(out.Stale)+len(out.Blocked)+len(out.Failed) != 0 || sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 {
		t.Errorf("apply = orphans %d, removed %d, stale %d, blocked %d, new %v, updated %v, want the interest of the 20th left as the expense it is",
			len(out.Orphans), len(out.Removed), len(out.Stale), len(out.Blocked), out.New, out.Updated)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Orphans)+len(v.Stale)+len(v.Blocked) != 0 {
		t.Errorf("verify = %d unexplained, orphans %d, stale %d, blocked %d, %v, want the months to agree", v.Unexplained, len(v.Orphans), len(v.Stale), len(v.Blocked), err)
	}
	if got := w.ledger.snapshot(); got != before {
		t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, got)
	}
}

// A plan whose conversion the export does not have is not one that the export has a part of:
// the payments of it are there and the first row of it is not, so the rows that were its
// conversion were deleted or edited, and the plan is an orphan like any other.
func TestAPlanWhoseConversionTheExportHasNotIsAnOrphanAndNotOneItHasAPartOf(t *testing.T) {
	w := confirmedThrough(t, "2026-03")
	rows := slices.DeleteFunc(within(fixture(), "", "2026-03-15", true), func(r model.Row) bool { return r.Type == model.TypeBillInstalment })
	w.export(rows)

	if out := w.apply(); len(out.Orphans) < 1 {
		t.Errorf("apply = orphans %d, want the plan, whose conversion the export has not, told as one", len(out.Orphans))
	}
}

// An export that has no rows of the opening balances says nothing of them, and they are
// part of the history it describes all the same: the accounts started from them. The ones
// the ledger has as drafts are confirmed with the month they are dated in, like any other
// unit, and not left as they are, which would hold the reconciliations of the month up.
func TestAnExportWithNoOpeningRowsConfirmsTheOpeningDraftsTheLedgerHasWithTheirMonth(t *testing.T) {
	w := applied(t)
	openings := w.ledger.ofKind("opening")
	if len(openings) == 0 {
		t.Fatal("the ledger has no opening balance")
	}
	w.export(within(fixture(), "", "", false))

	for _, m := range []string{"2026-01", "2026-02", "2026-03", "2026-04"} {
		out, err := w.confirm(m)
		if err != nil {
			t.Fatalf("confirm %s = %v", m, err)
		}
		for _, a := range out.Adjustments {
			if a.State == usecase.AdjustmentBlocked {
				t.Errorf("confirm %s: the adjustment of %s on %s is blocked, want it reconciled", m, a.Adjustment.Account, a.Adjustment.Date.Format(time.DateOnly))
			}
		}
		for _, o := range openings {
			want := "draft"
			if model.MonthOf(o.OccurredOn) <= m {
				want = "confirmed"
			}
			if o.Status != want {
				t.Errorf("after confirm %s the opening balance of %s is %s, want %s", m, o.OccurredOn.Format(time.DateOnly), o.Status, want)
			}
		}
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Months) != 4 || len(v.Orphans)+len(v.Stale)+len(v.Blocked) != 0 {
		t.Errorf("verify = %d months, %d unexplained, orphans %d, stale %d, blocked %d, %v, want the four months to agree",
			len(v.Months), v.Unexplained, len(v.Orphans), len(v.Stale), len(v.Blocked), err)
	}
}
