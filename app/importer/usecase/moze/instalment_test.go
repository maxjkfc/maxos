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

// A plan of instalments is recorded under the key of its first row, so a row that is
// edited later, which another row of the plan has, makes the plan something the export
// does not have any more while the ledger still has it: charging the interest, and the
// rows of the interest that the export has now, as expenses of their own, would count
// the same interest twice.

// instalmentsBroken is the export with one payment of the plan changed, so that the
// payments no longer add up to what was converted.
func instalmentsBroken(t *testing.T) []model.Row {
	t.Helper()
	return edit(t, fixture(), "2026-03-15 23:59", "Card instalments", "-200", func(r *model.Row) { r.Amount = d("-190") })
}

func TestAConfirmedPlanThatTheExportNoLongerHasHoldsBackTheInterestItsRowsComeToAlone(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	w.mustConfirm("2026-02")
	if got := w.ledger.ofKind("instalment"); len(got) != 1 || got[0].Status != "confirmed" {
		t.Fatalf("the plan is %v, want one confirmed instalment", got)
	}
	w.export(instalmentsBroken(t))
	for _, u := range w.plan().Units {
		if u.Kind == model.KindBillInstalment {
			t.Fatal("the plan still has the plan of instalments")
		}
	}
	before := len(w.ledger.txns)

	out := w.apply()
	blocked := 0
	for _, b := range out.Blocked {
		if b.Unit.Kind == model.KindExpense && b.Unit.Main.Account == "Card" && strings.HasPrefix(b.Plan, model.KeyPrefix+model.PlanIDPrefix) {
			blocked++
		}
	}
	if blocked < 3 || sum(out.New) != 0 || sum(out.Updated) != 0 || len(w.ledger.txns) != before {
		t.Fatalf("apply = blocked %d (of %d), new %v, updated %v, ledger %d -> %d, want the interest rows held back and nothing recorded beside the plan",
			blocked, len(out.Blocked), out.New, out.Updated, before, len(w.ledger.txns))
	}
	// They are held back by the plan, which is what the user voids: they are not
	// confirmed transactions to void, so they are not stale.
	if len(out.Stale) != 0 {
		t.Errorf("stale = %d, want none: the units are blocked by the plan, and have no confirmed version of their own", len(out.Stale))
	}
	if len(out.Orphans) != 1 || out.Orphans[0].Kind != "instalment" || !out.Orphans[0].Date.Equal(day("2026-02-05")) || out.Orphans[0].Account != "Card" {
		t.Fatalf("orphans = %+v, want the confirmed plan, named", out.Orphans)
	}
	for _, b := range out.Blocked {
		if b.Plan != out.Orphans[0].Key {
			t.Errorf("a unit is blocked by %q, want the plan that is named as an orphan, %q", b.Plan, out.Orphans[0].Key)
		}
	}

	// confirm and verify say so, and so does status: the plan is what stands in the way.
	if _, err := w.confirm("2026-03"); !errors.Is(err, usecase.ErrOrphan) || errors.Is(err, usecase.ErrStale) {
		t.Errorf("confirm err = %v, want it refused for the plan that the export does not have, and not for stale units", err)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || len(v.Stale) != 0 || len(v.Blocked) < 1 || v.Open != "2026-02" || len(v.Orphans) != 1 {
		t.Errorf("verify = stale %d, blocked %d, open %q, orphans %d, %v, want the blocked units told and the plan as the orphan", len(v.Stale), len(v.Blocked), v.Open, len(v.Orphans), err)
	}
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || len(st.Orphans) != 1 {
		t.Errorf("status = %+v, %v, want the plan named", st.Orphans, err)
	}
	held := 0
	for _, m := range st.Months {
		if m.Stale != 0 {
			t.Errorf("%s has %d stale units, want none", m.Month, m.Stale)
		}
		held += m.Blocked
	}
	if held < 3 {
		t.Errorf("status counts %d blocked units, want the interest rows", held)
	}

	// The user voids the plan: apply records what the export says.
	if n := w.ledger.void("moze:plan-"); n != 1 {
		t.Fatalf("voided %d plans", n)
	}
	out = w.apply()
	if len(out.Stale) != 0 || len(out.Blocked) != 0 || len(out.Orphans) != 0 || sum(out.New)+sum(out.Updated) < 3 {
		t.Errorf("apply after the void = stale %d, blocked %d, orphans %d, new %v, updated %v, want the interest recorded", len(out.Stale), len(out.Blocked), len(out.Orphans), out.New, out.Updated)
	}
}

func TestADraftPlanThatTheExportNoLongerHasIsDiscardedAndItsInterestRecordedAsExpenses(t *testing.T) {
	w := applied(t)
	w.export(instalmentsBroken(t))

	out := w.apply()
	if len(out.Stale) != 0 || len(out.Orphans) != 0 {
		t.Fatalf("apply = stale %d, orphans %d, want nothing in the way of a draft", len(out.Stale), len(out.Orphans))
	}
	if got := w.ledger.ofKind("instalment"); len(got) != 0 {
		t.Errorf("the ledger has %d plans, want the draft discarded", len(got))
	}
	if sum(out.Updated)+sum(out.New) < 3 {
		t.Errorf("apply = new %v, updated %v, want the interest rows recorded as expenses", out.New, out.Updated)
	}
	again := w.apply()
	if sum(again.New)+sum(again.Updated) != 0 || len(again.Removed) != 0 {
		t.Errorf("a second apply = %+v, want nothing to do", again)
	}
}

func TestAnotherPlanOfTheSameAccountThatIsAsItWasIsNotHeldBack(t *testing.T) {
	// A second plan, which the export keeps as it is, is not stale because the first is
	// gone: only the units that are not recorded are held back. It is made and paid in
	// January, before the first, so that the payments of the first cannot be its own.
	rows := append(fixture(),
		row("2026-01-08 23:59", model.TypeBillInstalment, "Card", "TWD", "300", name("Second instalment"), cat("帳單分期", "帳單分期")),
		row("2026-01-08 23:59", model.TypeBillInstalment, "Card instalments", "TWD", "300", cat("帳單分期", "帳單分期")),
		row("2026-01-08 23:59", model.TypeBillInstalment, "Card instalments", "TWD", "-300", cat("帳單分期", "帳單分期")),
		row("2026-01-28 23:59", model.TypeInstalmentPayment, "Card instalments", "TWD", "-300", cat("帳單分期", "帳單分期")),
	)
	// The rows of a file are by day, those with none first.
	slices.SortStableFunc(rows, func(a, b model.Row) int {
		if c := a.Date.Compare(b.Date); c != 0 {
			return c
		}
		return strings.Compare(a.Clock, b.Clock)
	})
	w := newWorld(t, rows, fixtureMapping()).reviewed()
	w.apply()
	plans := 0
	for _, u := range w.plan().Units {
		if u.Kind == model.KindBillInstalment {
			plans++
		}
	}
	if plans != 2 {
		t.Fatalf("the plan has %d plans of instalments, want 2", plans)
	}
	for _, month := range []string{"2026-01", "2026-02", "2026-03", "2026-04"} {
		w.mustConfirm(month)
	}

	// The first plan is broken: one of its payments is edited. The second is not.
	w.export(edit(t, rows, "2026-03-15 23:59", "Card instalments", "-200", func(r *model.Row) { r.Amount = d("-190") }))
	out := w.apply()
	for _, s := range out.Stale {
		if s.Unit.Kind == model.KindBillInstalment {
			t.Errorf("the second plan, which is as it was, is stale: %+v", s)
		}
	}
	if len(out.Orphans) != 1 || !out.Orphans[0].Date.Equal(day("2026-02-05")) {
		t.Errorf("orphans = %+v, want the first plan alone", out.Orphans)
	}
}
