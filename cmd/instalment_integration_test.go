package cmd_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

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
