package cmd_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// dateOn is a date, as midnight UTC, the way the callers of the use cases give one.
func dateOn(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC)
}

// held is what the balances say, as "name held unbilled", one to a line, for the
// day of the clock or for the day given.
func (l ledgerApp) held(t *testing.T, names map[uuid.UUID]string, asOf *time.Time) []string {
	t.Helper()
	out, err := l.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{AsOf: asOf})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	lines := make([]string, len(out.Balances))
	for i, b := range out.Balances {
		lines[i] = names[b.AccountID] + " " + b.Quantity.String() + " unbilled " + b.UnbilledInstalment.String()
	}
	return lines
}

// plans is what the plans say, as "principal charged remaining next", one to a line.
func (l ledgerApp) plans(t *testing.T, includeFinished bool) []string {
	t.Helper()
	out, err := l.ListInstalmentPlans.ListInstalmentPlans(context.Background(), usecase.ListInstalmentPlansInput{IncludeFinished: includeFinished})
	if err != nil {
		t.Fatalf("ListInstalmentPlans: %v", err)
	}
	lines := make([]string, len(out.Plans))
	for i, p := range out.Plans {
		next := "-"
		if p.NextDueOn != nil {
			next = p.NextDueOn.Format(time.DateOnly)
		}
		lines[i] = p.Principal.String() + " " + p.Charged.String() + " " + p.Remaining.String() + " " + next
	}
	return lines
}

// instalmentsOf is 12 periods of 1,000 that fall due on the 5th of each month from
// December 2026, with 30 of interest on the first.
func instalmentsOf(interestCategory uuid.UUID) *usecase.InstalmentSchedule {
	periods := make([]usecase.InstalmentPeriod, 12)
	for i := range periods {
		periods[i] = usecase.InstalmentPeriod{DueOn: dateOn(2026, time.Month(12+i), 5), Principal: dec("1000")}
	}
	periods[0].Interest = new(dec("30"))
	return &usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &interestCategory}
}

func TestAnInstalmentPurchaseIsOwedPeriodByPeriodThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(day)
	l := newLedgerAppOn(t, testpg.New(t), clk)
	twd := model.BaseCurrencyID

	card, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "card", Kind: "card"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	food, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Food"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	interest, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Interest"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	names := map[uuid.UUID]string{card.ID: "card"}

	// A television of 12,000 on the card, paid in 12 periods.
	recorded, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details("instalment-tv-0001"), AccountID: &card.ID, InstrumentID: twd,
		Lines:       []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("12000")}},
		Instalments: instalmentsOf(interest.ID),
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	origin := recorded.Transaction
	if origin.PlanID == nil || *origin.PlanID != origin.ID || origin.PlanSeq == nil || *origin.PlanSeq != 0 {
		t.Fatalf("origin has plan %v, position %v, want to be the origin of its own plan", origin.PlanID, origin.PlanSeq)
	}

	// A draft counts nowhere, nor do the drafts of its periods.
	if got := l.held(t, names, nil); len(got) != 0 {
		t.Fatalf("balances with a draft plan = %q, want none", got)
	}
	plan, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, origin.ID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan: %v", err)
	}
	if len(plan.Periods) != 12 {
		t.Fatalf("%d periods, want 12", len(plan.Periods))
	}
	// A period is not confirmed by itself: the plan is.
	_, err = l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.Periods[0].ID}})
	if !errors.Is(err, model.ErrPartOfPlan) {
		t.Fatalf("confirm a period: err = %v, want ErrPartOfPlan", err)
	}
	if got := l.held(t, names, nil); len(got) != 0 {
		t.Fatalf("balances after the refused confirmation = %q, want none", got)
	}

	confirmed, err := l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{origin.ID}})
	if err != nil {
		t.Fatalf("confirm the origin: %v", err)
	}
	if len(confirmed.Transactions) != 1 || confirmed.Transactions[0].ID != origin.ID {
		t.Fatalf("confirmed %d transactions, want the origin alone: the periods go with it", len(confirmed.Transactions))
	}
	listed, err := l.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{PlanID: &origin.ID, Limit: 100})
	if err != nil {
		t.Fatalf("ListTransactions of the plan: %v", err)
	}
	if len(listed.Transactions) != 13 {
		t.Fatalf("%d transactions in the plan, want the origin and its 12 periods", len(listed.Transactions))
	}
	for _, txn := range listed.Transactions {
		if txn.Status != "confirmed" {
			t.Errorf("transaction %s of the plan is %q, want it confirmed with the origin", txn.ID, txn.Status)
		}
	}

	// What each day sees of it, from the clock of the app.
	tests := []struct {
		name  string
		today time.Time
		held  []string
		plans []string
	}{
		// A plan counts from the day of its origin, in the balances and in the plans.
		{"the day before the purchase", dateOn(2026, time.November, 13), nil, nil},
		{"the day of the purchase", day, []string{"card 0 unbilled -12000"}, []string{"12000 0 12000 2026-12-05"}},
		{"the day before the first period", dateOn(2026, time.December, 4), []string{"card 0 unbilled -12000"}, []string{"12000 0 12000 2026-12-05"}},
		{"the day the first period falls due", dateOn(2026, time.December, 5), []string{"card -1030 unbilled -11000"}, []string{"12000 1000 11000 2027-01-05"}},
		{"the day the last period falls due", dateOn(2027, time.November, 5), []string{"card -12030 unbilled 0"}, nil},
	}
	for _, tt := range tests {
		clk.setDay(tt.today)
		if got := l.held(t, names, nil); !slices.Equal(got, tt.held) {
			t.Errorf("%s: balances = %q, want %q", tt.name, got, tt.held)
		}
		if got := l.plans(t, false); !slices.Equal(got, tt.plans) {
			t.Errorf("%s: plans = %q, want %q", tt.name, got, tt.plans)
		}
	}
	if got, want := l.plans(t, true), []string{"12000 12000 0 -"}; !slices.Equal(got, want) {
		t.Errorf("plans with the finished ones = %q, want %q", got, want)
	}

	// The day can be asked for, whatever the day it is.
	clk.setDay(day)
	if got, want := l.held(t, names, new(dateOn(2027, time.January, 5))), []string{"card -2030 unbilled -10000"}; !slices.Equal(got, want) {
		t.Errorf("balances as of the second period = %q, want %q", got, want)
	}

	// Voiding the origin voids the plan: what was charged is not owed any more.
	clk.setDay(dateOn(2027, time.March, 1))
	voided, err := l.Void.VoidTransaction(ctx, origin.ID)
	if err != nil {
		t.Fatalf("VoidTransaction of the origin: %v", err)
	}
	if voided.ID != origin.ID {
		t.Errorf("voided %s, want the origin", voided.ID)
	}
	if got := l.held(t, names, nil); len(got) != 0 {
		t.Errorf("balances after the plan is void = %q, want none", got)
	}
	if got := l.plans(t, true); len(got) != 0 {
		t.Errorf("plans after the plan is void = %q, want none", got)
	}
}

func TestATradeMovesCashAndHoldingThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	l := newLedgerApp(t)
	twd := model.BaseCurrencyID

	btc, err := l.CreateInstrument.CreateInstrument(ctx, usecase.CreateInstrumentInput{Code: "BTC", Name: "Bitcoin", Kind: "crypto", Decimals: 8})
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	bank, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "bank", Kind: "bank"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	wallet, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "wallet", Kind: "wallet"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	opening, err := l.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
		RecordDetails: l.details("opening-bank-0001"), AccountID: bank.ID, InstrumentID: twd, Amount: dec("500000"),
	})
	if err != nil {
		t.Fatalf("SetOpeningBalance: %v", err)
	}
	l.confirm(t, opening.Transaction)

	trade := func(key, direction, quantity, amount string) usecase.Transaction {
		out, err := l.RecordTrade.RecordTrade(ctx, usecase.RecordTradeInput{
			RecordDetails: l.details(key), Direction: direction, AccountID: wallet.ID, InstrumentID: btc.ID, Quantity: dec(quantity),
			CashAccountID: bank.ID, CurrencyID: twd, Amount: dec(amount),
		})
		if err != nil {
			t.Fatalf("RecordTrade(%q): %v", key, err)
		}
		return out.Transaction
	}
	buy := trade("trade-buy-000001", "buy", "0.1", "300000")
	if got, want := l.balance(t, bank.ID, twd), dec("500000"); !got.Equal(want) {
		t.Fatalf("cash with the purchase a draft = %s, want %s", got, want)
	}
	l.confirm(t, buy)
	l.confirm(t, trade("trade-sell-00001", "sell", "0.04", "130000"))

	for _, want := range []struct {
		account    uuid.UUID
		instrument uuid.UUID
		quantity   string
	}{
		{bank.ID, twd, "330000"},
		{wallet.ID, btc.ID, "0.06"},
		{wallet.ID, twd, "0"},
	} {
		if got := l.balance(t, want.account, want.instrument); !got.Equal(dec(want.quantity)) {
			t.Errorf("balance = %s, want %s", got, want.quantity)
		}
	}
}

func TestWhatIsDatedAfterTodayCountsFromItsDayThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(day)
	l := newLedgerAppOn(t, testpg.New(t), clk)
	twd := model.BaseCurrencyID

	bank, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "bank", Kind: "bank"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	card, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "card", Kind: "card"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	food, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Food"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	interest, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Interest"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	names := map[uuid.UUID]string{bank.ID: "bank", card.ID: "card"}
	opening, err := l.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
		RecordDetails: l.details("opening-bank-0002"), AccountID: bank.ID, InstrumentID: twd, Amount: dec("5000"),
	})
	if err != nil {
		t.Fatalf("SetOpeningBalance: %v", err)
	}
	l.confirm(t, opening.Transaction)

	// An expense of the 20th, recorded and confirmed on the 14th.
	later := l.details("expense-later-001")
	later.OccurredOn = dateOn(2026, time.November, 20)
	spent, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: later, AccountID: &bank.ID, InstrumentID: twd,
		Lines: []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	l.confirm(t, spent.Transaction)

	if got, want := l.held(t, names, nil), []string{"bank 5000 unbilled 0"}; !slices.Equal(got, want) {
		t.Fatalf("balances on the 14th = %q, want %q: the expense has not happened yet", got, want)
	}
	clk.setDay(dateOn(2026, time.November, 20))
	if got, want := l.held(t, names, nil), []string{"bank 4880 unbilled 0"}; !slices.Equal(got, want) {
		t.Fatalf("balances on the 20th = %q, want %q: the expense is counted on its day", got, want)
	}

	// The periods of a plan that are drafts do not stand in the way of reconciling
	// the card as of a day before they fall due: the reconciliation is of the day it
	// is dated, so the clock does not come into it.
	clk.setDay(day)
	if _, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details("instalment-tv-0002"), AccountID: &card.ID, InstrumentID: twd,
		Lines:       []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("12000")}},
		Instalments: instalmentsOf(interest.ID),
	}); err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	reconcile := func(key string, asOf time.Time) error {
		details := l.details(key)
		details.OccurredOn = asOf
		_, err := l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
			RecordDetails: details, AccountID: card.ID, InstrumentID: twd, ActualBalance: dec("0"),
		})
		return err
	}
	if err := reconcile("reconcile-card-001", dateOn(2026, time.December, 4)); err != nil {
		t.Fatalf("Reconcile as of the day before the first period is due: %v, want it not to be held up by the drafts of periods to come", err)
	}
	err = reconcile("reconcile-card-002", dateOn(2026, time.December, 5))
	var pending *model.PendingDraftsError
	if !errors.Is(err, model.ErrPendingDrafts) || !errors.As(err, &pending) || pending.Count != 1 {
		t.Fatalf("Reconcile on the day of the first period: err = %v, want 1 draft pending: the period that fell due", err)
	}
}

// What a caller that confirms every draft it lists does: ListTransactions lists the
// origin of a plan and each of its periods, which are drafts of their own, and the
// ids all go to ConfirmTransactions together. The plan is confirmed once, as a
// whole, and what comes back is what was sent.
func TestConfirmingEveryDraftListedConfirmsAPlanOnceThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(dateOn(2027, time.March, 1))
	l := newLedgerAppOn(t, testpg.New(t), clk)
	twd := model.BaseCurrencyID

	bank, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "bank", Kind: "bank"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	card, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "card", Kind: "card"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	food, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Food"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	interest, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Interest"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	names := map[uuid.UUID]string{bank.ID: "bank", card.ID: "card"}

	if _, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details("instalment-tv-0004"), AccountID: &card.ID, InstrumentID: twd,
		Lines:       []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("12000")}},
		Instalments: instalmentsOf(interest.ID),
	}); err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	if _, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details("lunch-0000000001"), AccountID: &bank.ID, InstrumentID: twd,
		Lines: []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("120")}},
	}); err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	drafts, err := l.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{Status: "draft", Limit: 200})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	listed := make([]uuid.UUID, len(drafts.Transactions))
	for i, txn := range drafts.Transactions {
		listed[i] = txn.ID
	}
	if len(listed) != 14 {
		t.Fatalf("%d drafts listed, want the origin of the plan, its 12 periods and the lunch", len(listed))
	}

	confirmed, err := l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: listed})
	if err != nil {
		t.Fatalf("confirm every draft listed: %v, want a period to be covered by the origin that is sent with it", err)
	}
	got := make([]uuid.UUID, len(confirmed.Transactions))
	for i, txn := range confirmed.Transactions {
		got[i] = txn.ID
		if txn.Status != "confirmed" {
			t.Errorf("transaction %s is %q in the output, want it confirmed", txn.ID, txn.Status)
		}
	}
	if !slices.Equal(got, listed) {
		t.Errorf("confirmed %v, want the transactions that were sent, in the order they were sent,\n  %v", got, listed)
	}

	rest, err := l.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{Status: "draft", Limit: 200})
	if err != nil || len(rest.Transactions) != 0 {
		t.Errorf("drafts left = %d, %v, want none", len(rest.Transactions), err)
	}
	// Counted once: three periods have fallen due by the 1st of March, 1,000 and the
	// 30 of interest of the first charged, and 9,000 is still to come.
	if got, want := l.held(t, names, nil), []string{"bank -120 unbilled 0", "card -3030 unbilled -9000"}; !slices.Equal(got, want) {
		t.Errorf("balances = %q, want %q", got, want)
	}
}

// cardPlan is a plan on a card that is recorded for the tests of rescheduling: 12,000
// in 12 periods of 1,000 that fall due on the 5th of each month from December 2026,
// 30 of interest with the first.
type cardPlan struct {
	card, food, interest uuid.UUID
	origin               usecase.Transaction
	names                map[uuid.UUID]string
}

func (l ledgerApp) cardPlan(t *testing.T, key string, confirm bool) cardPlan {
	t.Helper()
	ctx := context.Background()
	card, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "card", Kind: "card"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	food, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Food"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	interest, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Interest"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	recorded, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details(key), AccountID: &card.ID, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("12000")}},
		Instalments: instalmentsOf(interest.ID),
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	if confirm {
		l.confirm(t, recorded.Transaction)
	}
	return cardPlan{card: card.ID, food: food.ID, interest: interest.ID, origin: recorded.Transaction, names: map[uuid.UUID]string{card.ID: "card"}}
}

// owed is what the card holds and what is unbilled on it, as the balances say.
func (l ledgerApp) owed(t *testing.T, p cardPlan) (held, unbilled decimal.Decimal) {
	t.Helper()
	out, err := l.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &p.card})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	for _, b := range out.Balances {
		held, unbilled = held.Add(b.Quantity), unbilled.Add(b.UnbilledInstalment)
	}
	return held, unbilled
}

// wantOwed fails the test unless the card holds held and has unbilled, and what it
// owes in all, the two together, is total.
func (l ledgerApp) wantOwed(t *testing.T, when string, p cardPlan, held, unbilled string) {
	t.Helper()
	gotHeld, gotUnbilled := l.owed(t, p)
	if !gotHeld.Equal(dec(held)) || !gotUnbilled.Equal(dec(unbilled)) {
		t.Errorf("%s: the card holds %s and has %s unbilled, want %s and %s", when, gotHeld, gotUnbilled, held, unbilled)
	}
}

// postingsOf are the postings of the transaction in the book.
func postingsOf(txn usecase.Transaction, book string) []usecase.Posting {
	var out []usecase.Posting
	for _, p := range txn.Postings {
		if p.Book == book {
			out = append(out, p)
		}
	}
	return out
}

// Three of the 12 periods of 1,000 have fallen due, and the card is paid off of the
// other 9,000 in one period, next month, with interest of 270. What the card owes
// today does not change, and the interest, which is an expense, is booked on the
// day it is charged. The three that fell due are sent as they were, which is what
// takes no leave: the schedule is replaced as a whole.
func TestPayingOffWhatRemainsOfAPlanInOnePeriodThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(dateOn(2027, time.March, 1))
	l := newLedgerAppOn(t, testpg.New(t), clk)
	p := l.cardPlan(t, "instalment-tv-0005", true)
	before, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, p.origin.ID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan: %v", err)
	}
	l.wantOwed(t, "before", p, "-3030", "-9000")

	got, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: usecase.InstalmentSchedule{
		InterestCategoryID: &p.interest,
		Periods: []usecase.InstalmentPeriod{
			{DueOn: dateOn(2026, time.December, 5), Principal: dec("1000"), Interest: new(dec("30"))},
			{DueOn: dateOn(2027, time.January, 5), Principal: dec("1000")},
			{DueOn: dateOn(2027, time.February, 5), Principal: dec("1000")},
			{DueOn: dateOn(2027, time.April, 5), Principal: dec("9000"), Interest: new(dec("270"))},
		},
	}})
	if err != nil {
		t.Fatalf("RescheduleInstalment: %v", err)
	}

	// The origin is as it was, and the periods are 4 new confirmed transactions.
	if !reflect.DeepEqual(got.Origin, before.Origin) {
		t.Errorf("origin = %+v, want it as it was: %+v", got.Origin, before.Origin)
	}
	if len(got.Periods) != 4 {
		t.Fatalf("%d periods, want the 4 of the new schedule", len(got.Periods))
	}
	for i, period := range got.Periods {
		if period.Status != "confirmed" || period.ConfirmedAt == nil || period.PlanSeq == nil || *period.PlanSeq != i+1 {
			t.Errorf("period %d = %s, position %v, confirmed at %v, want it confirmed, at its position", i+1, period.Status, period.PlanSeq, period.ConfirmedAt)
		}
		for _, old := range before.Periods {
			if old.ID == period.ID {
				t.Errorf("period %d has the id of an old period", i+1)
			}
		}
	}

	// What the card owes today is what it owed, and the plan says what is left.
	l.wantOwed(t, "after", p, "-3030", "-9000")
	if got, want := l.plans(t, false), []string{"12000 3000 9000 2027-04-05"}; !slices.Equal(got, want) {
		t.Errorf("plans = %q, want %q", got, want)
	}
	listed, err := l.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{PlanID: &p.origin.ID, Limit: 100})
	if err != nil || len(listed.Transactions) != 5 {
		t.Errorf("transactions of the plan = %d, %v, want the origin and the 4 new periods", len(listed.Transactions), err)
	}

	// The interest is an expense of its category, 30 on the first period and 270 on
	// the last, and the last period charges the principal and the interest together.
	last := got.Periods[3]
	if instalment, account, expense := postingsOf(last, "instalment"), postingsOf(last, "account"), postingsOf(last, "expense"); len(instalment) != 1 || len(account) != 1 || len(expense) != 1 ||
		!instalment[0].Quantity.Equal(dec("9000")) || !account[0].Quantity.Equal(dec("-9270")) || !expense[0].Quantity.Equal(dec("270")) ||
		expense[0].CategoryID == nil || *expense[0].CategoryID != p.interest {
		t.Errorf("last period postings = %+v, want instalment +9000, account -9270 and 270 of expense of the interest category", last.Postings)
	}
	interest := decimal.Zero
	for _, period := range got.Periods {
		for _, e := range postingsOf(period, "expense") {
			interest = interest.Add(e.Quantity)
		}
	}
	if !interest.Equal(dec("300")) {
		t.Errorf("interest of the plan = %s, want 300: the 30 and the 270", interest)
	}

	// On the day it falls due, the card is charged it, and nothing is unbilled.
	clk.setDay(dateOn(2027, time.April, 5))
	l.wantOwed(t, "on the day of the last period", p, "-12300", "0")
	if got := l.plans(t, false); len(got) != 0 {
		t.Errorf("plans on the day of the last period = %q, want none: it is finished", got)
	}
	if got, want := l.plans(t, true), []string{"12000 12000 0 -"}; !slices.Equal(got, want) {
		t.Errorf("plans with the finished ones = %q, want %q", got, want)
	}
}

// A reschedule leaves what has fallen due as it is, unless it says it is meant to
// change it. The same payoff, with the interest of the first period typed as 33 and
// not as the 30 that was charged, is refused with the period that differs, and the
// plan, what the card holds, and the interest that was booked are as they were. With
// ChangePast it is what is asked: the interest that was 30 is 33, and the card holds
// 3 more.
func TestReschedulingWhatFellDueTakesLeaveToChangeItThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(dateOn(2027, time.March, 1))
	l := newLedgerAppOn(t, testpg.New(t), clk)
	p := l.cardPlan(t, "instalment-tv-0009", true)
	payoff := func(firstInterest string, changePast bool) usecase.RescheduleInstalmentInput {
		return usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, ChangePast: changePast, Schedule: usecase.InstalmentSchedule{
			InterestCategoryID: &p.interest,
			Periods: []usecase.InstalmentPeriod{
				{DueOn: dateOn(2026, time.December, 5), Principal: dec("1000"), Interest: new(dec(firstInterest))},
				{DueOn: dateOn(2027, time.January, 5), Principal: dec("1000")},
				{DueOn: dateOn(2027, time.February, 5), Principal: dec("1000")},
				{DueOn: dateOn(2027, time.April, 5), Principal: dec("9000"), Interest: new(dec("270"))},
			},
		}}
	}
	before, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, p.origin.ID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan: %v", err)
	}
	if expense := postingsOf(before.Periods[0], "expense"); len(expense) != 1 || !expense[0].Quantity.Equal(dec("30")) {
		t.Fatalf("interest of the first period = %+v, want 30 of expense", expense)
	}
	l.wantOwed(t, "before", p, "-3030", "-9000")

	_, err = l.RescheduleInstalment.RescheduleInstalment(ctx, payoff("33", false))
	var changed *model.PastPeriodsChangedError
	if !errors.Is(err, model.ErrPastPeriodsChanged) || !errors.As(err, &changed) || len(changed.Differences) != 1 || changed.Differences[0].Seq != 1 {
		t.Fatalf("reschedule with the interest of a period that fell due changed: err = %v, want ErrPastPeriodsChanged with period 1 as the one that differs", err)
	}
	after, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, p.origin.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("plan after the refusal = %+v, %v, want it as it was: %+v", after, err, before)
	}
	l.wantOwed(t, "after the refusal", p, "-3030", "-9000")

	got, err := l.RescheduleInstalment.RescheduleInstalment(ctx, payoff("33", true))
	if err != nil {
		t.Fatalf("RescheduleInstalment with ChangePast: %v", err)
	}
	if expense := postingsOf(got.Periods[0], "expense"); len(expense) != 1 || !expense[0].Quantity.Equal(dec("33")) {
		t.Errorf("interest of the first period = %+v, want the 33 that was asked, as an expense", expense)
	}
	l.wantOwed(t, "with the interest that fell due changed", p, "-3033", "-9000")
}

// A period that is moved across today moves what is owed between what the card
// holds and what is unbilled on it, and what it owes in all stays the same: a
// period that has no interest is moved, so that the interest does not move with it.
func TestMovingAPeriodAcrossTodayMovesWhatIsOwedBetweenTheBalanceAndTheUnbilledInstalmentThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(dateOn(2026, time.December, 20))
	l := newLedgerAppOn(t, testpg.New(t), clk)
	p := l.cardPlan(t, "instalment-tv-0006", true)
	reschedule := func(schedule *usecase.InstalmentSchedule, changePast bool) error {
		_, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *schedule, ChangePast: changePast})
		return err
	}
	l.wantOwed(t, "to begin with, with one period fallen due", p, "-1030", "-11000")

	// A period that falls due after today is moved to before it: it is charged now,
	// and 1,000 moves from unbilled to what the card holds. It makes a period that
	// fell due that the plan did not have, which takes leave.
	earlier := instalmentsOf(p.interest)
	earlier.Periods[1].DueOn = dateOn(2026, time.December, 15)
	if err := reschedule(earlier, false); !errors.Is(err, model.ErrPastPeriodsChanged) {
		t.Fatalf("move a period to before today without leave: err = %v, want ErrPastPeriodsChanged", err)
	}
	l.wantOwed(t, "with the refused move", p, "-1030", "-11000")
	if err := reschedule(earlier, true); err != nil {
		t.Fatalf("move a period to before today with leave: %v", err)
	}
	l.wantOwed(t, "with a future period moved to before today", p, "-2030", "-10000")
	if got, want := l.plans(t, false), []string{"12000 2000 10000 2027-02-05"}; !slices.Equal(got, want) {
		t.Errorf("plans = %q, want %q", got, want)
	}

	// A period that fell due is moved to after today: it is not charged any more,
	// and the 1,000 is unbilled again. It drops a period that fell due, which takes
	// leave too.
	if err := reschedule(instalmentsOf(p.interest), false); !errors.Is(err, model.ErrPastPeriodsChanged) {
		t.Fatalf("move a period that fell due to after today without leave: err = %v, want ErrPastPeriodsChanged", err)
	}
	if err := reschedule(instalmentsOf(p.interest), true); err != nil {
		t.Fatalf("move a period that fell due to after today with leave: %v", err)
	}
	l.wantOwed(t, "with the period moved back to after today", p, "-1030", "-11000")

	// A plan that is void is not rescheduled, and counts for nothing.
	if _, err := l.Void.VoidTransaction(ctx, p.origin.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	_, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *instalmentsOf(p.interest)})
	if !errors.Is(err, model.ErrPlanVoid) {
		t.Fatalf("reschedule a void plan: err = %v, want ErrPlanVoid", err)
	}
	l.wantOwed(t, "once void", p, "0", "0")
}

// What cannot be rescheduled is refused with what is wrong, and the plan is left as
// it was.
func TestReschedulingWhatItCannotDoLeavesThePlanAsItWasThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	l := newLedgerApp(t)
	p := l.cardPlan(t, "instalment-tv-0007", true)
	before, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, p.origin.ID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan: %v", err)
	}

	// The amount is the purchase, which is not rescheduled: the error says both.
	other := instalmentsOf(p.interest)
	other.Periods[11].Principal = dec("1500")
	_, err = l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *other})
	var mismatch *model.PrincipalMismatchError
	if !errors.As(err, &mismatch) || !mismatch.Principals.Equal(dec("12500")) || !mismatch.Amount.Equal(dec("12000")) {
		t.Errorf("err = %v, want a mismatch of the 12500 the schedule adds up to and the 12000 of the plan", err)
	}
	// A period is not a plan.
	_, err = l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: before.Periods[0].ID, Schedule: *instalmentsOf(p.interest)})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("reschedule a period: err = %v, want ErrNotFound", err)
	}

	after, err := l.GetInstalmentPlan.GetInstalmentPlan(ctx, p.origin.ID)
	if err != nil || !reflect.DeepEqual(after, before) {
		t.Errorf("plan after = %+v, %v, want it as it was: %+v", after, err, before)
	}
}

// The category of the interest of a plan is archived after the plan was recorded: the
// plan can still be rescheduled with it, as it can with no other archived category,
// and with one that is in use. The category that is allowed is the one the periods of
// the plan have now, so once the plan has moved to another, the one that is archived
// is refused like any other.
func TestReschedulingAPlanWhoseInterestCategoryIsArchivedKeepsThatCategoryThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	l := newLedgerApp(t)
	p := l.cardPlan(t, "instalment-tv-0010", true)
	category := func(name string, archive bool) uuid.UUID {
		t.Helper()
		c, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: name})
		if err != nil {
			t.Fatalf("CreateCategory %s: %v", name, err)
		}
		if archive {
			if _, err := l.ArchiveCat.ArchiveCategory(ctx, c.ID); err != nil {
				t.Fatalf("ArchiveCategory %s: %v", name, err)
			}
		}
		return c.ID
	}
	if _, err := l.ArchiveCat.ArchiveCategory(ctx, p.interest); err != nil {
		t.Fatalf("ArchiveCategory of the interest: %v", err)
	}
	retired, fees := category("Retired", true), category("Fees", false)
	reschedule := func(interest uuid.UUID) error {
		_, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *instalmentsOf(interest)})
		return err
	}

	if err := reschedule(p.interest); err != nil {
		t.Errorf("with the archived category the periods have: %v, want it allowed", err)
	}
	if err := reschedule(retired); !errors.Is(err, model.ErrArchived) {
		t.Errorf("with another archived category: err = %v, want ErrArchived", err)
	}
	if err := reschedule(fees); err != nil {
		t.Errorf("with a category in use: %v, want it allowed", err)
	}
	if err := reschedule(p.interest); !errors.Is(err, model.ErrArchived) {
		t.Errorf("with the archived category the periods no longer have: err = %v, want ErrArchived", err)
	}
}

// A draft period moved onto or before the day of a reconciliation is pending for it,
// and is not once it is moved back: nothing is kept from one call to the next.
func TestMovingADraftPeriodOntoTheDayOfAReconciliationMakesItPendingThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	l := newLedgerApp(t)
	p := l.cardPlan(t, "instalment-tv-0008", false)
	reconcile := func(key string) error {
		details := l.details(key)
		details.OccurredOn = dateOn(2026, time.December, 4)
		_, err := l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{RecordDetails: details, AccountID: p.card, InstrumentID: model.BaseCurrencyID, ActualBalance: dec("0")})
		return err
	}
	if err := reconcile("reconcile-card-r01"); err != nil {
		t.Fatalf("Reconcile with the first period of the plan after the day: %v, want it not held up", err)
	}

	earlier := instalmentsOf(p.interest)
	earlier.Periods[0].DueOn = dateOn(2026, time.December, 1)
	if _, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *earlier}); err != nil {
		t.Fatalf("RescheduleInstalment: %v", err)
	}
	var pending *model.PendingDraftsError
	if err := reconcile("reconcile-card-r02"); !errors.Is(err, model.ErrPendingDrafts) || !errors.As(err, &pending) || pending.Count != 1 {
		t.Fatalf("Reconcile with the first period moved to before the day: err = %v, want 1 draft pending", err)
	}

	if _, err := l.RescheduleInstalment.RescheduleInstalment(ctx, usecase.RescheduleInstalmentInput{PlanID: p.origin.ID, Schedule: *instalmentsOf(p.interest)}); err != nil {
		t.Fatalf("RescheduleInstalment back: %v", err)
	}
	if err := reconcile("reconcile-card-r03"); err != nil {
		t.Fatalf("Reconcile with the first period moved back to after the day: %v, want it not held up", err)
	}
}
