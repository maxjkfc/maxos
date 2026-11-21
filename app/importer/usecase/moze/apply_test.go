package moze_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

func TestPlanAddsWhatTheMappingLacksAndWritesNothingToTheLedger(t *testing.T) {
	w := newWorld(t, fixture(), model.Mapping{})
	out, err := w.planner.Plan(context.Background(), usecase.PlanInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Plan.Blocked() || out.MappingAdded == 0 || len(w.mapping.appended) != 1 {
		t.Fatalf("plan = blocked %v, added %d, appended %d times; want the guesses written to the mapping once", out.Plan.Blocked(), out.MappingAdded, len(w.mapping.appended))
	}
	if len(w.ledger.txns) != 0 || len(w.ledger.created) != 0 {
		t.Errorf("plan wrote to the ledger: %d transactions, %v created", len(w.ledger.txns), w.ledger.created)
	}

	// Once the guesses are the mapping, a second plan has nothing to add and is clear.
	again, err := w.planner.Plan(context.Background(), usecase.PlanInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if again.Plan.Blocked() || again.MappingAdded != 0 || len(w.mapping.appended) != 1 {
		t.Errorf("second plan: blocked %v (%v), added %d", again.Plan.Blocked(), again.Plan.Problems, again.MappingAdded)
	}
}

func TestApplyRefusesAPlanThatHasSomethingToMap(t *testing.T) {
	w := newWorld(t, fixture(), model.Mapping{})
	_, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files})
	var blocked *usecase.BlockedError
	if !errors.Is(err, usecase.ErrBlocked) || !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want the plan to be refused", err)
	}
	if len(w.ledger.txns) != 0 || len(w.ledger.created) != 0 || len(w.ledger.calls) != 0 {
		t.Errorf("apply wrote %d transactions and made %v before refusing", len(w.ledger.txns), w.ledger.created)
	}
}

func TestApplyRecordsEveryUnitWithTheUseCaseOfItsKind(t *testing.T) {
	w := newWorld(t, fixtureWith(), fixtureMapping()).reviewed()
	plan := w.plan()
	out := w.apply()

	if len(out.Failed) != 0 {
		t.Fatalf("failed = %+v", out.Failed)
	}
	wantNew := map[model.UnitKind]int{}
	transactions := 0
	for _, u := range plan.Units {
		wantNew[u.Kind]++
		transactions += len(u.Keys())
	}
	if !reflect.DeepEqual(out.New, wantNew) || len(out.Duplicate) != 0 {
		t.Errorf("new = %v, duplicates = %v, want %v and none", out.New, out.Duplicate, wantNew)
	}
	if len(w.ledger.txns) != transactions || w.ledger.drafts() != transactions {
		t.Errorf("the ledger has %d transactions, %d of them drafts, want %d drafts: everything imported is a draft", len(w.ledger.txns), w.ledger.drafts(), transactions)
	}
	if out.Adjustments != len(plan.Adjustments) || out.Adjustments != 5 {
		t.Errorf("adjustments = %d, want the 5 non-zero ones left for confirmation", out.Adjustments)
	}

	// One call for each kind, with what the unit says.
	calls := map[string]int{}
	for _, c := range w.ledger.calls {
		calls[reflect.TypeOf(c).Name()]++
	}
	want := map[string]int{
		"RecordExpenseInput": 10, "RecordIncomeInput": 5, "RecordRewardInput": 3, "RecordDealingInput": 3,
		"RecordTransferInput": 4, "RecordExchangeInput": 1, "RecordTradeInput": 1, "SetOpeningBalanceInput": 5,
		"RecordBillInstalmentInput": 1,
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
}

// fixtureWith is the fixture; a test that needs more rows adds them here.
func fixtureWith(extra ...model.Row) []model.Row {
	return append(fixture(), extra...)
}

func TestApplyBuildsTheInputsTheLedgerTakes(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.apply()

	var expense finance.RecordExpenseInput
	var transfer finance.RecordTransferInput
	var instalment finance.RecordBillInstalmentInput
	var trade finance.RecordTradeInput
	var coffee finance.RecordExpenseInput
	var reward finance.RecordRewardInput
	for _, c := range w.ledger.calls {
		switch in := c.(type) {
		case finance.RecordExpenseInput:
			switch in.Description {
			case "Noodles":
				expense = in
			case "Coffee":
				coffee = in
			}
		case finance.RecordRewardInput:
			if in.Description == "Coffee （折抵）" {
				reward = in
			}
		case finance.RecordTransferInput:
			if len(in.Fees) == 1 {
				transfer = in
			}
		case finance.RecordBillInstalmentInput:
			instalment = in
		case finance.RecordTradeInput:
			trade = in
		}
	}

	// An expense: its lines, the fee as a line of the fee category, text, area, tag.
	if len(expense.Lines) != 2 || !expense.Lines[0].Amount.Equal(d("120")) || expense.Lines[0].Memo != "◎ noodles ($100) ✕ 1" ||
		!expense.Lines[1].Amount.Equal(d("2")) {
		t.Errorf("expense lines = %+v", expense.Lines)
	}
	if expense.Merchant != "Fake Shop" || expense.AreaID == nil || len(expense.TagIDs) != 1 || expense.ProjectID != nil {
		t.Errorf("expense details = %+v", expense.RecordDetails)
	}
	if got := expense.OccurredAt; got == nil || got.Hour() != 10 || got.Location().String() != "UTC+8" {
		t.Errorf("occurred at = %v, want 10:00 in the zone of the user: the time of day is kept", got)
	}
	if !expense.OccurredOn.Equal(day("2026-01-05")) || !strings.HasPrefix(expense.IdempotencyKey, model.KeyPrefix) {
		t.Errorf("occurred on %v, key %q", expense.OccurredOn, expense.IdempotencyKey)
	}

	// A discount that is the whole price is the expense at the price and a reward.
	if len(coffee.Lines) != 1 || !coffee.Lines[0].Amount.Equal(d("100")) || coffee.Lines[0].Discount != nil {
		t.Errorf("coffee lines = %+v", coffee.Lines)
	}
	if !reward.Amount.Equal(d("100")) || reward.IdempotencyKey != coffee.IdempotencyKey+model.RewardKeySuffix {
		t.Errorf("reward = %+v", reward)
	}

	// A transfer: both accounts, the amount, and the fee paid from the account that sent.
	if !transfer.Amount.Equal(d("1000")) || transfer.FromAccountID == transfer.ToAccountID || !transfer.Fees[0].Amount.Equal(d("15")) ||
		transfer.Fees[0].AccountID != transfer.FromAccountID {
		t.Errorf("transfer = %+v", transfer)
	}

	// A buy of a coin: the coin's account and quantity, and what paid for it.
	if trade.Direction != "buy" || !trade.Quantity.Equal(d("0.002")) || !trade.Amount.Equal(d("100")) || trade.AccountID != trade.CashAccountID {
		t.Errorf("trade = %+v", trade)
	}

	// The plan of instalments: 600 in three periods with the interest of each.
	if !instalment.Principal.Equal(d("600")) || len(instalment.Schedule.Periods) != 3 || instalment.Schedule.InterestCategoryID == nil ||
		!instalment.Schedule.Periods[1].DueOn.Equal(day("2026-03-15")) || !instalment.Schedule.Periods[2].Interest.Equal(d("2")) {
		t.Errorf("instalment = %+v", instalment)
	}
}

func TestApplyMakesTheReferenceDataOnce(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	first := w.apply()

	for kind, want := range map[string]int{
		usecase.CreatedInstruments: 4, // USD, JPY, USDT, BTC: TWD is the ledger's own
		usecase.CreatedAccounts:    6, // Cash, Bank, Card, Exchange, Wallet, Broker
		usecase.CreatedAreas:       1,
		usecase.CreatedParties:     2, // Amy and the company
		usecase.CreatedTags:        2, // family and 待確認
	} {
		if got := first.Created[kind]; got != want {
			t.Errorf("created %s = %d, want %d", kind, got, want)
		}
	}
	if first.Created[usecase.CreatedCategories] == 0 || first.Created[usecase.CreatedInstitutions] == 0 {
		t.Errorf("created = %v, want categories and institutions", first.Created)
	}
	// The card the mapping gives days to has them.
	for _, a := range w.ledger.accounts {
		if a.Name == "Card" && (a.ClosingDay == nil || *a.ClosingDay != 5 || a.PaymentDueDay == nil || *a.PaymentDueDay != 20) {
			t.Errorf("card = %+v, want the closing day and the payment due day of the mapping", a)
		}
	}

	second := w.apply()
	if len(second.Created) != 0 || sum(second.New) != 0 || len(second.Failed) != 0 {
		t.Errorf("second apply made %v and %v and %d failures, want 0 new", second.Created, second.New, len(second.Failed))
	}
	if sum(second.Duplicate) != sum(first.New) {
		t.Errorf("second apply found %d units already there, want %d", sum(second.Duplicate), sum(first.New))
	}
}

func sum(m map[model.UnitKind]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func TestApplyTwiceGivesTheSameLedger(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.apply()
	keys := func() []string {
		var out []string
		for _, tx := range w.ledger.txns {
			out = append(out, tx.IdempotencyKey+" "+tx.ID.String())
		}
		return out
	}
	before := keys()
	w.apply()
	if after := keys(); !reflect.DeepEqual(before, after) {
		t.Errorf("a second apply changed the ledger: %d transactions, then %d", len(before), len(after))
	}
}

func TestALongerExportAddsOnlyTheRowsItHasMore(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	first := w.apply()

	more := []model.Row{
		row("2026-03-20 12:00", model.TypeExpense, "Card", "TWD", "-60", cat("Food", "Lunch")),
		row("2026-03-21 12:00", model.TypeExpense, "Card", "TWD", "-60", cat("Food", "Lunch")), // same content, another day
	}
	inputs := moze.Inputs{Source: fakeSource{rows: fixtureWith(more...)}, Mapping: w.mapping, Rates: fakeRates{fixtureRates()}}
	longer := moze.NewImporter(inputs, w.ledger.ledger(), time.UTC)
	out, err := longer.Apply(context.Background(), usecase.ApplyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if sum(out.New) != 2 || sum(out.Duplicate) != sum(first.New) {
		t.Errorf("new = %v, duplicates = %v, want the 2 new rows only", out.New, out.Duplicate)
	}

	// A row that is the same as one of the file appears again: it is a row of its own.
	again := []model.Row{
		row("2026-03-20 12:00", model.TypeExpense, "Card", "TWD", "-60", cat("Food", "Lunch")),
		row("2026-03-20 12:00", model.TypeExpense, "Card", "TWD", "-60", cat("Food", "Lunch")),
	}
	inputs.Source = fakeSource{rows: fixtureWith(again...)}
	out, err = moze.NewImporter(inputs, w.ledger.ledger(), time.UTC).Apply(context.Background(), usecase.ApplyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	if sum(out.New) != 1 {
		t.Errorf("new = %v, want the second identical row to be the one new unit", out.New)
	}
}

func TestApplyReportsWhatTheLedgerRefusesAndGoesOn(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	victim := w.plan().Units[10]
	w.ledger.failOn = victim.Key()
	out := w.apply()

	if len(out.Failed) != 1 || out.Failed[0].Unit.Key() != victim.Key() || len(out.Failed[0].Lines) == 0 {
		t.Fatalf("failed = %+v, want the one the ledger refused, with its lines", out.Failed)
	}
	if got, want := sum(out.New), len(w.plan().Units)-1; got != want {
		t.Errorf("new = %d, want %d: the others are recorded", got, want)
	}
}

func TestApplyDoesNotTakeAnAccountThatIsAnotherKindThanTheMappingSays(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.ledger.accounts = append(w.ledger.accounts, finance.Account{ID: w.ledger.id(), Name: "Cash", Kind: "bank"})
	_, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files})
	var conflict *model.Failure
	if !errors.As(err, &conflict) || conflict.Code != model.FailureAccountKindDiffers ||
		!reflect.DeepEqual(conflict.Args, []string{"Cash", "bank", "cash"}) {
		t.Fatalf("err = %v, want the conflict of the account Cash: a bank in the ledger, a cash in the mapping", err)
	}
	if len(w.ledger.txns) != 0 {
		t.Errorf("transactions = %d, want none: nothing is recorded on a conflict", len(w.ledger.txns))
	}
}

func TestApplyUsesTheReferenceDataTheLedgerHasAlready(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.ledger.accounts = append(w.ledger.accounts, finance.Account{ID: w.ledger.id(), Name: "Cash", Kind: "cash"})
	out := w.apply()
	if out.Created[usecase.CreatedAccounts] != 5 {
		t.Errorf("accounts created = %d, want 5: Cash was there", out.Created[usecase.CreatedAccounts])
	}
	cashes := 0
	for _, a := range w.ledger.accounts {
		if a.Name == "Cash" {
			cashes++
		}
	}
	if cashes != 1 {
		t.Errorf("accounts called Cash = %d", cashes)
	}
}
