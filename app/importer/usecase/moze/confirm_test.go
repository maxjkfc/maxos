package moze_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// applied is a world whose export is recorded, as drafts.
func applied(t *testing.T) *world {
	t.Helper()
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	if out := w.apply(); len(out.Failed) != 0 {
		t.Fatalf("apply failed: %+v", out.Failed)
	}
	return w
}

func TestConfirmRefusesAMonthWhileAnEarlierOneIsOpen(t *testing.T) {
	w := applied(t)
	before := w.ledger.drafts()

	_, err := w.confirm("2026-02")
	var order *usecase.MonthOrderError
	if !errors.Is(err, usecase.ErrMonthOrder) || !errors.As(err, &order) {
		t.Fatalf("err = %v, want the month order to be enforced", err)
	}
	if order.Open != "2026-01" || order.Drafts == 0 || order.Adjustments == 0 {
		t.Errorf("order error = %+v, want January with its drafts and its adjustments", order)
	}
	if w.ledger.drafts() != before || len(w.ledger.confirms) != 0 {
		t.Error("something was confirmed by a refusal")
	}

	w.mustConfirm("2026-01")
	w.mustConfirm("2026-02")
	if _, err := w.confirm("2026-02"); err != nil {
		t.Errorf("a month that is confirmed already is not refused: %v", err)
	}
}

func TestConfirmWantsTheMonthToBeRecordedAndKnown(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	if _, err := w.confirm("2026-01"); !errors.Is(err, usecase.ErrNotApplied) {
		t.Errorf("err = %v, want the rows that apply has not recorded to be told", err)
	}
	if _, err := w.confirm("2019-01"); !errors.Is(err, usecase.ErrUnknownMonth) {
		t.Errorf("err = %v, want the month that the export does not have", err)
	}
	if _, err := newWorld(t, fixture(), model.Mapping{}).confirm("2026-01"); !errors.Is(err, usecase.ErrBlocked) {
		t.Errorf("err = %v, want a plan with something to map to be refused", err)
	}
}

func TestConfirmConfirmsTheDraftsOfTheMonthDayByDayInOrder(t *testing.T) {
	w := applied(t)
	out := w.mustConfirm("2026-01")

	if out.Confirmed == 0 || out.AlreadyConfirmed != 0 {
		t.Fatalf("confirmed %d, already %d", out.Confirmed, out.AlreadyConfirmed)
	}
	// Only January is confirmed: the other months' drafts are still waiting, apart from
	// the periods of the plan of instalments, which are confirmed with it.
	for _, tx := range w.ledger.txns {
		january := tx.OccurredOn.Before(day("2026-02-01"))
		if january && tx.Status != "confirmed" {
			t.Errorf("%s of %s is %s", tx.IdempotencyKey, tx.OccurredOn.Format(time.DateOnly), tx.Status)
		}
		if !january && tx.Status == "confirmed" {
			t.Errorf("%s of %s is confirmed before its month", tx.IdempotencyKey, tx.OccurredOn.Format(time.DateOnly))
		}
	}

	// The batches are the days, in order: no day is confirmed before an earlier one.
	var lastDay time.Time
	for _, ids := range w.ledger.confirms {
		var batchDay time.Time
		for _, id := range ids {
			if tx := w.ledger.byID(id); batchDay.IsZero() {
				batchDay = tx.OccurredOn
			} else if !tx.OccurredOn.Equal(batchDay) {
				t.Errorf("a batch has two days: %s and %s", batchDay.Format(time.DateOnly), tx.OccurredOn.Format(time.DateOnly))
			}
		}
		if batchDay.Before(lastDay) {
			t.Errorf("a batch of %s comes after one of %s", batchDay.Format(time.DateOnly), lastDay.Format(time.DateOnly))
		}
		lastDay = batchDay
	}
}

func TestAdjustmentIsMadeOnceEverythingOfItsDayIsConfirmed(t *testing.T) {
	w := applied(t)
	out := w.mustConfirm("2026-01")

	// Cash on 15 January: an expense of 40 at ten, an adjustment of -20 at noon, an
	// expense of 10 at six. The account is reconciled to what it holds with both
	// expenses, 664, plus the -20: the adjustment is made after the day's drafts.
	// 1000 opening, -81 (80 and its fee), -305 and +100 (what Amy was lent and paid back
	// with the fee), -40, -10.
	var cash *finance.ReconcileInput
	for i, in := range w.ledger.reconciled {
		if w.ledger.account(in.AccountID) == "Cash" && in.OccurredOn.Equal(day("2026-01-15")) {
			cash = &w.ledger.reconciled[i]
		}
	}
	if cash == nil {
		t.Fatalf("Cash was not reconciled; reconciled = %+v", w.ledger.reconciled)
	}
	if !cash.ActualBalance.Equal(d("644")) {
		t.Errorf("actual balance = %s, want 644: what the ledger held plus the adjustment", cash.ActualBalance)
	}

	reconcile := slices.IndexFunc(w.ledger.events, func(e string) bool { return e == "reconcile Cash 2026-01-15" })
	if reconcile < 1 || !strings.HasPrefix(w.ledger.events[reconcile-1], "confirm") {
		t.Errorf("events = %v, want the drafts of the day to be confirmed right before the reconciliation", w.ledger.events)
	}
	// Nothing of Cash is a draft when it is reconciled: the fake refuses otherwise.
	var states []usecase.AdjustmentState
	for _, a := range out.Adjustments {
		states = append(states, a.State)
	}
	if len(states) != 3 || states[0] != usecase.AdjustmentApplied || states[1] != usecase.AdjustmentApplied || states[2] != usecase.AdjustmentApplied {
		t.Errorf("adjustments = %v, want the three of January applied", states)
	}
	// The adjustment of a currency that is not the base currency carries its base amount;
	// that of a coin does not.
	for _, in := range w.ledger.reconciled {
		switch w.ledger.instrument(in.InstrumentID) {
		case "USD":
			if in.DifferenceBaseAmount == nil || !in.DifferenceBaseAmount.Equal(d("75")) {
				t.Errorf("USD adjustment base = %v, want 75", in.DifferenceBaseAmount)
			}
		case "BTC", "TWD":
			if in.DifferenceBaseAmount != nil {
				t.Errorf("%s adjustment has a base amount %v", w.ledger.instrument(in.InstrumentID), in.DifferenceBaseAmount)
			}
		}
	}
}

func TestBalancesAtEveryMonthEndAreTheExportsOnceItsMonthIsConfirmed(t *testing.T) {
	w := applied(t)
	w.ledger.pageSize = 7 // the transactions are read a page at a time
	plan := w.plan()

	for _, month := range plan.Months() {
		out := w.mustConfirm(month)
		end, _ := model.MonthEnd(month)

		want := plan.ExpectedAt(end)
		for pair, q := range plan.UnrecordedAt(end) {
			want[pair] = want[pair].Sub(q)
		}
		got := w.ledger.balances(end)
		for _, pair := range want.Pairs() {
			if !got[pair].Equal(want[pair]) {
				t.Errorf("%s: %s %s = %s, want %s", month, pair.Account, pair.Instrument, got[pair], want[pair])
			}
		}
		if out.Check.Unexplained() != 0 {
			t.Errorf("%s: %d differences nothing explains: %+v", month, out.Check.Unexplained(), out.Check.Differences())
		}
	}

	// The rows that are left out are what makes the ledger differ from the export, and
	// they are told: -77 of Cash (a 轉出 with no 轉入) and the 50 USDT of the purchase
	// of a coin that has no quantity.
	last, _ := w.mustConfirmCheck(plan.Months()[len(plan.Months())-1])
	var explained []string
	for _, l := range last.Differences() {
		explained = append(explained, l.Account+" "+l.Instrument+" "+l.Difference().String())
	}
	slices.Sort(explained)
	if want := []string{"Cash TWD 77", "Exchange USDT 50"}; !slices.Equal(explained, want) {
		t.Errorf("differences = %v, want %v: the ledger lacks the money that left in rows that are not recorded", explained, want)
	}

	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if v.Unexplained != 0 || v.Open != "" || len(v.Months) != len(plan.Months()) {
		t.Errorf("verify = %d months, open %q, %d unexplained; want every month, none open, none unexplained", len(v.Months), v.Open, v.Unexplained)
	}
}

// mustConfirmCheck confirms again (which changes nothing) to read the check of a month.
func (w *world) mustConfirmCheck(month string) (model.MonthCheck, usecase.ConfirmOutput) {
	w.t.Helper()
	out := w.mustConfirm(month)
	return out.Check, out
}

func TestConfirmAgainChangesNothing(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	confirmed, reconciled := len(w.ledger.confirms), len(w.ledger.txns)

	out := w.mustConfirm("2026-01")
	if out.Confirmed != 0 || out.AlreadyConfirmed == 0 {
		t.Errorf("confirmed %d, already %d", out.Confirmed, out.AlreadyConfirmed)
	}
	for _, a := range out.Adjustments {
		if a.State != usecase.AdjustmentDone {
			t.Errorf("adjustment %+v is %s, want done", a.Adjustment.Key, a.State)
		}
	}
	if len(w.ledger.confirms) != confirmed || len(w.ledger.txns) != reconciled {
		t.Error("a second confirm of the month wrote or confirmed something")
	}
}

func TestConfirmLeavesTheDraftsOfOthersAlone(t *testing.T) {
	w := applied(t)
	ctx := context.Background()
	var cash, exchange finance.Account
	for _, a := range w.ledger.accounts {
		switch a.Name {
		case "Cash":
			cash = a
		case "Exchange":
			exchange = a
		}
	}
	twd := w.ledger.instruments[0].ID
	mine, err := w.ledger.RecordExpense(ctx, finance.RecordExpenseInput{
		RecordDetails: finance.RecordDetails{IdempotencyKey: "someone-else-1", OccurredOn: day("2026-01-02")},
		AccountID:     &cash.ID, InstrumentID: twd, Lines: []finance.CategoryLine{{Amount: d("7")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	unrelated, err := w.ledger.RecordExpense(ctx, finance.RecordExpenseInput{
		RecordDetails: finance.RecordDetails{IdempotencyKey: "someone-else-2", OccurredOn: day("2026-01-03")},
		AccountID:     &exchange.ID, InstrumentID: twd, Lines: []finance.CategoryLine{{Amount: d("9")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	out := w.mustConfirm("2026-01")
	for _, tx := range []finance.Transaction{mine.Transaction, unrelated.Transaction} {
		if got := w.ledger.byID(tx.ID).Status; got != "draft" {
			t.Errorf("the draft %s of someone else is %s, want it left alone", tx.IdempotencyKey, got)
		}
	}
	for _, ids := range w.ledger.confirms {
		if slices.Contains(ids, mine.Transaction.ID) || slices.Contains(ids, unrelated.Transaction.ID) {
			t.Error("confirm asked for the draft of someone else")
		}
	}

	// A draft of someone else on Cash, dated before the adjustment, keeps Cash from being
	// reconciled: the adjustment is blocked, and said to be, and the others are made.
	blocked := 0
	for _, a := range out.Adjustments {
		if a.State == usecase.AdjustmentBlocked {
			blocked++
			if !errors.Is(a.Err, errPending) || a.Adjustment.Account != "Cash" {
				t.Errorf("blocked = %+v, err %v", a.Adjustment, a.Err)
			}
		}
	}
	if blocked != 1 || len(out.Adjustments) != 3 {
		t.Fatalf("blocked %d of %d adjustments, want only the one of Cash", blocked, len(out.Adjustments))
	}
	if out.Check.Unexplained() == 0 {
		t.Error("the balance of Cash lacks the adjustment, and nothing says it is explained")
	}

	// And February is not confirmed while the adjustment of January is open.
	if _, err := w.confirm("2026-02"); !errors.Is(err, usecase.ErrMonthOrder) {
		t.Errorf("err = %v, want January to be open", err)
	}
}

func TestConfirmTellsTheDraftsTheExportDoesNotHaveAnymore(t *testing.T) {
	w := applied(t)
	rows := fixture()
	// The export is another now: the 03-02 expense is not in it.
	rows = slices.DeleteFunc(rows, func(r model.Row) bool { return r.Date.Equal(day("2026-03-02")) })
	inputs := moze.Inputs{Source: fakeSource{rows: rows}, Mapping: w.mapping, Rates: fakeRates{fixtureRates()}}
	other := moze.NewImporter(inputs, w.ledger.ledger(), time.UTC)

	out, err := other.Confirm(context.Background(), usecase.ConfirmInput{Files: w.files, Month: "2026-01"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Orphans) != 1 || out.Orphans[0].Status != "draft" || !out.Orphans[0].Date.Equal(day("2026-03-02")) {
		t.Errorf("orphans = %+v, want the one draft the export lost, by its day", out.Orphans)
	}
	status, err := other.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || len(status.Orphans) != 1 {
		t.Errorf("status orphans = %+v, %v", status.Orphans, err)
	}
}

func TestStatusCountsTheMonths(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if st.Months[0].NotApplied == 0 || st.Months[0].Drafts != 0 {
		t.Errorf("before apply: %+v, want everything not applied", st.Months[0])
	}

	w.apply()
	w.mustConfirm("2026-01")
	st, err = w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	byMonth := map[string]usecase.MonthStatus{}
	for _, m := range st.Months {
		byMonth[m.Month] = m
	}
	jan, feb := byMonth["2026-01"], byMonth["2026-02"]
	if jan.Drafts != 0 || jan.Confirmed == 0 || jan.NotApplied != 0 || jan.AdjustmentsPending != 0 {
		t.Errorf("january = %+v, want it confirmed, with no adjustment pending", jan)
	}
	// January leaves out the two rows of the purchase of a coin whose quantity is zero;
	// February the 轉出 that has no 轉入.
	if jan.Skipped != 2 || feb.Skipped != 1 {
		t.Errorf("skipped = %d in January and %d in February, want 2 and 1", jan.Skipped, feb.Skipped)
	}
	if feb.Drafts == 0 || feb.Confirmed != 0 || feb.AdjustmentsPending != 1 {
		t.Errorf("february = %+v, want drafts and the adjustment of Broker pending", feb)
	}
}

func TestVerifyBeforeAnythingIsConfirmedHasNothingToCompare(t *testing.T) {
	w := applied(t)
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Months) != 0 || v.Open != "2026-01" || v.Unexplained != 0 {
		t.Errorf("verify = %+v", v)
	}
}

func TestVerifyFindsADifferenceNothingExplains(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	// Someone confirms a transaction of their own in a month the import confirmed.
	ctx := context.Background()
	var cash finance.Account
	for _, a := range w.ledger.accounts {
		if a.Name == "Cash" {
			cash = a
		}
	}
	out, err := w.ledger.RecordExpense(ctx, finance.RecordExpenseInput{
		RecordDetails: finance.RecordDetails{IdempotencyKey: "someone-else-3", OccurredOn: day("2026-01-30")},
		AccountID:     &cash.ID, InstrumentID: w.ledger.instruments[0].ID, Lines: []finance.CategoryLine{{Amount: d("13")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.ledger.ConfirmTransactions(ctx, finance.ConfirmTransactionsInput{IDs: []uuid.UUID{out.Transaction.ID}}); err != nil {
		t.Fatal(err)
	}

	v, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if v.Unexplained != 1 || len(v.Months) != 1 {
		t.Fatalf("verify = %d unexplained in %d months, want the 13 that left Cash found once, in January", v.Unexplained, len(v.Months))
	}
	var found bool
	for _, l := range v.Months[0].Differences() {
		if l.Account == "Cash" && l.Difference().Equal(d("-13")) && !l.Unexplained().IsZero() {
			found = true
		}
	}
	if !found {
		t.Errorf("differences = %+v, want Cash to be 13 below the export, unexplained", v.Months[0].Differences())
	}
}
