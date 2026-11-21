package cmd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// The reads of what was spent and of the drafts that are waiting, through the real use
// cases on a real PostgreSQL: the ledger of the budget tests, with a few more of what
// counts as spent, and what is the same in the budgets of the areas and in the spending
// of a month.

// spendingApp is the use cases that read what was spent and the drafts that wait, which
// the ledger app of the other tests does not hold.
type spendingApp struct {
	fx.In

	GetSpending      usecase.GetSpending
	GetSpendingTrend usecase.GetSpendingTrend
	GetDraftSummary  usecase.GetDraftSummary
}

// spendingWorld is the budget world, with the reads of spending and of drafts: two areas,
// a project of the area Work and one of no area, categories (Food with a subcategory Lunch,
// Rent, Fun, Fees and Interest), accounts, US dollars, and a clock that the test moves.
type spendingWorld struct {
	*budgetWorld
	spendingApp

	usd, move uuid.UUID
}

func newSpendingWorld(t *testing.T) *spendingWorld {
	t.Helper()
	ctx := context.Background()
	pool := testpg.New(t)
	clk := newTestClock(day) // the 14th of November 2026
	var (
		l     ledgerApp
		reads spendingApp
	)
	app := fx.New(
		fx.NopLogger,
		fx.Supply(pool),
		fx.Provide(func() financeclock.Clock { return clk }),
		cmd.CatalogModule(),
		cmd.FinanceModule(),
		fx.Populate(&l, &reads),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the modules: %v", err)
	}
	w := &budgetWorld{ledgerApp: l, t: t, clk: clk, names: map[uuid.UUID]string{}}

	area := func(name string) uuid.UUID {
		a, err := w.CreateArea.CreateArea(ctx, catalogusecase.CreateAreaInput{Name: name})
		if err != nil {
			t.Fatalf("CreateArea(%q): %v", name, err)
		}
		w.names[a.ID] = name
		return a.ID
	}
	category := func(name string, parent *uuid.UUID) uuid.UUID {
		c, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", ParentID: parent, Name: name})
		if err != nil {
			t.Fatalf("CreateCategory(%q): %v", name, err)
		}
		w.names[c.ID] = name
		return c.ID
	}
	account := func(name, kind string) uuid.UUID {
		a, err := w.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: name, Kind: kind})
		if err != nil {
			t.Fatalf("CreateAccount(%q): %v", name, err)
		}
		w.names[a.ID] = name
		return a.ID
	}

	w.home, w.work = area("Home"), area("Work")
	w.trip = w.project("Trip", &w.work) // its expenses are of Work, unless they say another area
	move := w.project("Move", nil)      // and its expenses are of no area
	w.food = category("Food", nil)
	w.lunch = category("Lunch", &w.food)
	w.rent, w.fun, w.fees, w.interest = category("Rent", nil), category("Fun", nil), category("Fees", nil), category("Interest", nil)
	w.cash, w.bank, w.wallet, w.card = account("cash", "cash"), account("bank", "bank"), account("wallet", "wallet"), account("card", "card")
	usd, err := w.CreateInstrument.CreateInstrument(ctx, usecase.CreateInstrumentInput{Code: "USD", Name: "US dollar", Kind: "fiat", IsCurrency: true, Decimals: 2})
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	w.names[usd.ID], w.names[model.BaseCurrencyID] = "USD", "TWD"

	// Money to spend: each account is opened with 100,000 on the first of October, before
	// anything below happens, and confirmed.
	for _, a := range []uuid.UUID{w.cash, w.bank, w.wallet} {
		opening := w.details(w.key())
		opening.OccurredOn = dateOn(2026, time.October, 1)
		out, err := w.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
			RecordDetails: opening, AccountID: a, InstrumentID: model.BaseCurrencyID, Amount: dec("100000"),
		})
		if err != nil {
			t.Fatalf("SetOpeningBalance: %v", err)
		}
		w.confirm(t, out.Transaction)
	}
	return &spendingWorld{budgetWorld: w, spendingApp: reads, usd: usd.ID, move: move}
}

// transfer records a transfer of 1,000 from the bank to the wallet on the day, in the area
// when there is one, with a fee of the amount, and confirms it.
func (w *spendingWorld) transfer(on time.Time, area *uuid.UUID, fee string) {
	w.t.Helper()
	d := w.details(w.key())
	d.OccurredOn, d.AreaID = on, area
	out, err := w.RecordTransfer.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.bank, ToAccountID: w.wallet, InstrumentID: model.BaseCurrencyID, Amount: dec("1000"),
		Fees: []usecase.Fee{{AccountID: w.bank, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec(fee)}},
	})
	if err != nil {
		w.t.Fatalf("RecordTransfer: %v", err)
	}
	w.confirm(w.t, out.Transaction)
}

// plan records the purchase of 12,000 of food on the card in 12 periods, whose first has 30
// of interest, for the area, and returns its origin, a draft.
func (w *spendingWorld) plan(on time.Time, area *uuid.UUID) usecase.Transaction {
	w.t.Helper()
	d := w.details(w.key())
	d.OccurredOn, d.AreaID = on, area
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("12000")}},
		Instalments: instalmentsOf(w.interest),
	})
	if err != nil {
		w.t.Fatalf("RecordExpense in instalments: %v", err)
	}
	return out.Transaction
}

// spendingOf reads what was spent in the month, and fails the test when it cannot.
func (w *spendingWorld) spendingOf(month time.Time) usecase.GetSpendingOutput {
	w.t.Helper()
	out, err := w.GetSpending.GetSpending(context.Background(), usecase.GetSpendingInput{Month: month})
	if err != nil {
		w.t.Fatalf("GetSpending(%s): %v", month.Format("2006-01"), err)
	}
	return out
}

// categoryLines writes what was spent in the month by category, one to a line, in the
// order they come: "Food 21645", and "Lunch 200 (under Food)" for a subcategory.
func (w *spendingWorld) categoryLines(out usecase.GetSpendingOutput) []string {
	lines := make([]string, len(out.Categories))
	for i, c := range out.Categories {
		lines[i] = fmt.Sprintf("%s %s", w.names[c.CategoryID], c.Amount)
		if c.ParentID != nil {
			lines[i] += " (under " + w.names[*c.ParentID] + ")"
		}
	}
	return lines
}

// trendOf reads what was spent in each month of the range, one to a line, as "2026-11
// 22798", and fails the test when it cannot.
func (w *spendingWorld) trendOf(from, to time.Time) []string {
	w.t.Helper()
	out, err := w.GetSpendingTrend.GetSpendingTrend(context.Background(), usecase.GetSpendingTrendInput{From: from, To: to})
	if err != nil {
		w.t.Fatalf("GetSpendingTrend(%s, %s): %v", from.Format("2006-01"), to.Format("2006-01"), err)
	}
	lines := make([]string, len(out.Months))
	for i, m := range out.Months {
		lines[i] = fmt.Sprintf("%s %s", m.Month.Format("2006-01"), m.Total)
	}
	return lines
}

// spentByAreas adds up what the areas spent in the month, projects included, which is what
// the budgets of the areas say, in what they call SpentWithProjects.
func (w *spendingWorld) spentByAreas(month time.Time) decimal.Decimal {
	w.t.Helper()
	out, err := w.GetAreaBudgets.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: month})
	if err != nil {
		w.t.Fatalf("GetAreaBudgets(%s): %v", month.Format("2006-01"), err)
	}
	total := decimal.Zero
	for _, a := range out.Areas {
		total = total.Add(a.SpentWithProjects)
	}
	return total
}

func TestWhatWasSpentInAMonthIsWhatTheAreasSpentWithTheirProjectsPlusTheExpensesOfNoAreaThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newSpendingWorld(t)
	october, november, december := dateOn(2026, time.October, 1), dateOn(2026, time.November, 1), dateOn(2026, time.December, 1)
	noArea := map[time.Time]decimal.Decimal{} // what was made in no area, and counts, by month

	// The expenses of Work: lunch 200 (a subcategory of food), food 300, fun 150, a fee of 15 that comes
	// with a transfer, 100 US dollars that were 3,200 TWD, and the purchase of 12,000 of food on the card
	// in 12 periods, whose first has 30 of interest. Home has 90 of food. The trip is in a project of the
	// area Work, and the Move is in a project of none.
	w.spend(dateOn(2026, time.November, 2), &w.work, nil, w.cash, w.lunch, "200")
	w.spend(dateOn(2026, time.November, 3), &w.work, nil, w.cash, w.food, "300")
	w.spend(dateOn(2026, time.November, 4), &w.work, nil, w.cash, w.fun, "150")
	w.spend(dateOn(2026, time.November, 5), nil, &w.trip, w.cash, w.food, "6000") // in the project, so in the area of it, Work
	w.spend(dateOn(2026, time.November, 5), &w.home, nil, w.cash, w.food, "90")
	w.transfer(dateOn(2026, time.November, 8), &w.work, "15")
	d := w.details(w.key())
	d.OccurredOn, d.AreaID = dateOn(2026, time.November, 9), &w.work
	dollars, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.bank, InstrumentID: w.usd,
		Lines: []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("100"), BaseAmount: new(dec("3200"))}},
	})
	if err != nil {
		t.Fatalf("RecordExpense in dollars: %v", err)
	}
	w.confirm(t, dollars.Transaction)
	television := w.plan(dateOn(2026, time.November, 10), &w.work)
	w.confirm(t, television)

	// What no area has: rent 777, food of the project Move (which has no area), and a fee of 11.
	w.spend(dateOn(2026, time.November, 6), nil, nil, w.cash, w.rent, "777")
	w.spend(dateOn(2026, time.November, 6), nil, &w.move, w.cash, w.food, "55")
	w.transfer(dateOn(2026, time.November, 8), nil, "11")
	noArea[november] = dec("777").Add(dec("55")).Add(dec("11"))

	// The days around November: the last of October, and the first of December, which has not come.
	w.spend(dateOn(2026, time.October, 31), &w.work, nil, w.cash, w.fun, "11")
	w.spend(dateOn(2026, time.December, 1), &w.work, nil, w.cash, w.fun, "22")
	// A rent of 5,000 for the 20th, which is confirmed now and has not happened yet.
	w.spend(dateOn(2026, time.November, 20), &w.work, nil, w.bank, w.rent, "5000")
	// A draft is not spent, and neither is what was confirmed and then voided.
	draft := w.expense(dateOn(2026, time.November, 6), &w.work, nil, w.wallet, w.food, "5000")
	voided := w.spend(dateOn(2026, time.November, 7), &w.work, nil, w.cash, w.fun, "7000")
	if _, err := w.Void.VoidTransaction(ctx, voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	// check says what a month is spent: it is what the areas spent, projects included, and what no area
	// has, and it is what is written down.
	check := func(when string, month time.Time, want string) {
		t.Helper()
		spending := w.spendingOf(month)
		inAreas := w.spentByAreas(month)
		if got := inAreas.Add(noArea[month]); !spending.Total.Equal(got) {
			t.Errorf("%s: %s was spent, want what the areas spent, %s, and what no area has, %s: %s", when, spending.Total, inAreas, noArea[month], got)
		}
		if !spending.Total.Equal(dec(want)) {
			t.Errorf("%s: %s was spent, want %s", when, spending.Total, want)
		}
		var sum decimal.Decimal
		for _, c := range spending.Categories {
			sum = sum.Add(c.Amount)
		}
		if !sum.Equal(spending.Total) {
			t.Errorf("%s: the categories add up to %s, and %s was spent", when, sum, spending.Total)
		}
	}

	// Today is the 14th. In Work: 200 + 300 + 150 + 6,000 + 15 + 3,200 + 12,000; in Home 90; in no area
	// 843. The rent of the 20th, the draft and the void expense are not in it.
	check("November, on the 14th", november, "22798")
	wantStatus(t, "November by category, on the 14th", w.categoryLines(w.spendingOf(november)),
		"Food 21645", "Rent 777", "Lunch 200 (under Food)", "Fun 150", "Fees 26")
	check("October", october, "11")
	check("December, on the 14th of November", december, "0")
	wantStatus(t, "the months, on the 14th", w.trendOf(october, december), "2026-10 11", "2026-11 22798", "2026-12 0")
	if got := w.spendingOf(december); len(got.Categories) != 0 || !got.Total.IsZero() {
		t.Errorf("December has %+v, want nothing: it has not begun", got)
	}

	// The 20th: the rent has happened, and counts, in the area Work.
	w.clk.setDay(dateOn(2026, time.November, 20))
	check("November, on the 20th", november, "27798")
	wantStatus(t, "November by category, on the 20th", w.categoryLines(w.spendingOf(november)),
		"Food 21645", "Rent 5777", "Lunch 200 (under Food)", "Fun 150", "Fees 26")

	// Confirming the draft makes it count.
	w.confirm(t, draft)
	check("November, with the draft confirmed", november, "32798")

	// December: the 1st has come, and the interest of the first period falls due on the 5th. The day
	// before it it is not spent, and on the day it is, once, in the month it falls due in.
	w.clk.setDay(dateOn(2026, time.December, 4))
	check("December, the 4th", december, "22")
	w.clk.setDay(dateOn(2026, time.December, 5))
	check("December, the 5th", december, "52")
	wantStatus(t, "December by category, the 5th", w.categoryLines(w.spendingOf(december)), "Interest 30", "Fun 22")
	wantStatus(t, "the months, on the 5th of December", w.trendOf(october, december), "2026-10 11", "2026-11 32798", "2026-12 52")

	// Each month of a trend is what the spending of the month says, whatever the range it is asked in.
	for _, tt := range []struct{ from, to time.Time }{
		{october, november}, {november, december}, {october, dateOn(2027, time.March, 1)}, {december, december},
	} {
		months := w.trendOf(tt.from, tt.to)
		for _, line := range months {
			month, total, _ := strings.Cut(line, " ")
			first, err := time.Parse("2006-01", month)
			if err != nil {
				t.Fatalf("a month of the trend is %q: %v", month, err)
			}
			if spending := w.spendingOf(first); spending.Total.String() != total {
				t.Errorf("%s of the trend %s..%s: %s, but the spending of the month is %s", month, tt.from.Format("2006-01"), tt.to.Format("2006-01"), total, spending.Total)
			}
		}
	}

	// Voiding the purchase in instalments takes it out of November, and the interest out of December,
	// of both the areas and the spending of the month.
	if _, err := w.Void.VoidTransaction(ctx, television.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	check("November without the purchase in instalments", november, "20798")
	check("December without the interest", december, "22")
}

func TestTheSpendingReadsRefuseWhatIsNotAMonthOrARangeThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newSpendingWorld(t)

	if _, err := w.GetSpending.GetSpending(ctx, usecase.GetSpendingInput{}); err == nil || !strings.Contains(err.Error(), "month") {
		t.Errorf("GetSpending of no month: err = %v, want the month required", err)
	}
	november := dateOn(2026, time.November, 1)
	for name, in := range map[string]usecase.GetSpendingTrendInput{
		"no first month":   {To: november},
		"no last month":    {From: november},
		"a reversed range": {From: november, To: dateOn(2026, time.October, 1)},
		"too wide":         {From: dateOn(2000, time.January, 1), To: dateOn(2026, time.November, 1)},
	} {
		if _, err := w.GetSpendingTrend.GetSpendingTrend(ctx, in); err == nil {
			t.Errorf("GetSpendingTrend of %s: no error, want it refused", name)
		}
	}
}

// draftLines writes the summary of the drafts, one line to the drafts of all, to each month
// and to each account of a month, with how many drafts there are and what they would move:
// "2026-11: 3 [TWD out 1135 in 1000 net -135]", the newest month first.
func (w *spendingWorld) draftLines(out usecase.GetDraftSummaryOutput) []string {
	amounts := func(amounts []usecase.DraftAmount) string {
		parts := make([]string, len(amounts))
		for i, a := range amounts {
			parts[i] = fmt.Sprintf("%s out %s in %s net %s", w.names[a.InstrumentID], a.Outflow, a.Inflow, a.Net)
		}
		return strings.Join(parts, ", ")
	}
	lines := []string{fmt.Sprintf("all: %d [%s]", out.Count, amounts(out.Amounts))}
	for _, m := range out.Months {
		lines = append(lines, fmt.Sprintf("%s: %d [%s]", m.Month.Format("2006-01"), m.Count, amounts(m.Amounts)))
		for _, a := range m.Accounts {
			name := "(none)"
			if a.AccountID != nil {
				name = w.names[*a.AccountID]
			}
			lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, amounts(a.Amounts)))
		}
	}
	return lines
}

// draftSummary reads the summary of the drafts, and fails the test when it cannot.
func (w *spendingWorld) draftSummary() usecase.GetDraftSummaryOutput {
	w.t.Helper()
	out, err := w.GetDraftSummary.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil {
		w.t.Fatalf("GetDraftSummary: %v", err)
	}
	return out
}

func TestTheDraftsThatAreWaitingAreSummarizedByMonthAndAccountAndConfirmingTakesThemOutThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newSpendingWorld(t)

	// Nothing waits yet: the openings were confirmed.
	if got := w.draftSummary(); got.Count != 0 || len(got.Months) != 0 {
		t.Fatalf("summary = %+v, want nothing waiting: the opening balances are confirmed", got)
	}

	// A confirmed expense is not a draft. The drafts are: an expense from cash, a transfer with a fee
	// from the bank into the wallet, and a purchase of 12,000 on the card in 12 periods, whose first has
	// 30 of interest and which fall due on the 5th of each month from December.
	w.spend(dateOn(2026, time.November, 5), nil, nil, w.cash, w.food, "9999")
	lunch := w.expense(dateOn(2026, time.November, 14), nil, nil, w.cash, w.food, "120")
	d := w.details(w.key())
	d.OccurredOn = dateOn(2026, time.November, 15)
	transfer, err := w.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.bank, ToAccountID: w.wallet, InstrumentID: model.BaseCurrencyID, Amount: dec("1000"),
		Fees: []usecase.Fee{{AccountID: w.bank, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	origin := w.plan(dateOn(2026, time.November, 14), nil)

	got := w.draftSummary()

	if got.Count != 15 || len(got.Months) != 13 {
		t.Fatalf("%d drafts in %d months, want 15 in 13: the expense, the transfer, and the purchase with its 12 periods", got.Count, len(got.Months))
	}
	lines := w.draftLines(got)
	// The first month of what the periods charge, the last, and then November with all that is
	// waiting in it: the accounts by name, and the drafts of no account, which are the origin of
	// the purchase, last.
	wantStatus(t, "all the drafts and the newest month", lines[:3],
		"all: 15 [TWD out 13165 in 1000 net -12165]",
		"2027-11: 1 [TWD out 1000 in 0 net -1000]",
		"  card: 1 [TWD out 1000 in 0 net -1000]",
	)
	wantStatus(t, "December and November", lines[len(lines)-7:],
		"2026-12: 1 [TWD out 1030 in 0 net -1030]",
		"  card: 1 [TWD out 1030 in 0 net -1030]",
		"2026-11: 3 [TWD out 1135 in 1000 net -135]",
		"  bank: 1 [TWD out 1015 in 0 net -1015]",
		"  cash: 1 [TWD out 120 in 0 net -120]",
		"  wallet: 1 [TWD out 0 in 1000 net 1000]",
		"  (none): 1 []",
	)

	// Confirming the expense and the transfer takes them out; the origin of the purchase, which moves
	// no account, stays with the periods.
	w.confirm(t, lunch, transfer.Transaction)
	got = w.draftSummary()
	if got.Count != 13 || len(got.Months) != 13 {
		t.Fatalf("%d drafts in %d months, want 13 in 13: the purchase with its 12 periods", got.Count, len(got.Months))
	}
	lines = w.draftLines(got)
	wantStatus(t, "November with the expense and the transfer confirmed", lines[len(lines)-2:], "2026-11: 1 []", "  (none): 1 []")

	// Confirming the purchase through its origin confirms its periods: nothing waits.
	w.confirm(t, origin)
	if got = w.draftSummary(); got.Count != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("summary = %+v, want nothing waiting once every draft is confirmed", got)
	}
}
