package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// spendingRows returns what Spending adds up from the day from to the day to.
func (e *budgetEnv) spendingRows(from, to time.Time) []model.MonthCategorySpending {
	e.t.Helper()
	got, err := e.budgets.Spending(context.Background(), from, to)
	if err != nil {
		e.t.Fatalf("Spending(%s, %s): %v", from.Format(time.DateOnly), to.Format(time.DateOnly), err)
	}
	return got
}

// spendingOfMonthLine writes what was spent on a category in a month as "2026-11 food
// 150.75", with where the category is when it is a subcategory.
func (e *budgetEnv) spendingOfMonthLine(r model.MonthCategorySpending) string {
	names := e.label()
	line := fmt.Sprintf("%s %s %s", r.Month.Format("2006-01"), names[r.CategoryID], r.Amount)
	if r.ParentID != nil {
		line += " (under " + names[*r.ParentID] + ")"
	}
	return line
}

// monthLines is what Spending adds up from the day from to the day to, one to a line, in
// the order it comes.
func (e *budgetEnv) monthLines(from, to time.Time) []string {
	e.t.Helper()
	rows := e.spendingRows(from, to)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = e.spendingOfMonthLine(r)
	}
	return lines
}

// totalOf adds up what the rows of the month say was spent in it.
func totalOf(rows []model.MonthCategorySpending, month time.Time) decimal.Decimal {
	total := decimal.Zero
	for _, r := range rows {
		if r.Month.Equal(month) {
			total = total.Add(r.Amount)
		}
	}
	return total
}

func TestSpendingAddsUpTheExpensesOfEachMonthAndCategoryWhetherTheyHaveAnAreaAndAProjectOrNot(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "120.5")
	e.spend(nov(3), &e.work, nil, e.food, "30.25")
	e.spend(nov(3), &e.work, nil, e.lunch, "80")
	e.spend(nov(4), &e.work, &e.trip, e.food, "700") // in an area and in a project
	e.spend(nov(4), &e.home, nil, e.food, "55")
	e.spend(nov(5), nil, nil, e.food, "999")     // in no area
	e.spend(nov(5), nil, &e.trip, e.food, "998") // in a project that has no area

	wantLines(t, "what was spent", e.monthLines(nov(1), nov(30)),
		"2026-11 food 2902.75",
		"2026-11 lunch 80 (under food)",
	)
}

func TestSpendingTakesTheFirstAndTheLastDayOfTheRangeAndNeitherNeighbour(t *testing.T) {
	e := newBudgetEnv(t)
	// An amount for each day, so that what is counted is what the total adds up.
	e.spend(on(2026, time.October, 31), &e.work, nil, e.food, "1")
	e.spend(nov(1), nil, nil, e.food, "10")
	e.spend(nov(30), &e.work, nil, e.food, "100")
	e.spend(on(2026, time.December, 1), nil, nil, e.food, "1000")

	tests := []struct {
		name     string
		from, to time.Time
		want     []string
	}{
		{"the month: its first and last day, and not the days around it", nov(1), nov(30), []string{"2026-11 food 110"}},
		{"to the day before the last", nov(1), nov(29), []string{"2026-11 food 10"}},
		{"from the day after the first", nov(2), nov(30), []string{"2026-11 food 100"}},
		{"the first day alone", nov(1), nov(1), []string{"2026-11 food 10"}},
		{"the last day alone", nov(30), nov(30), []string{"2026-11 food 100"}},
		{"the day before the month alone", on(2026, time.October, 31), on(2026, time.October, 31), []string{"2026-10 food 1"}},
		{"the day after the month alone", on(2026, time.December, 1), on(2026, time.December, 1), []string{"2026-12 food 1000"}},
		{"a day with nothing", nov(15), nov(15), nil},
		{"the three months", on(2026, time.October, 1), on(2026, time.December, 31), []string{"2026-10 food 1", "2026-11 food 110", "2026-12 food 1000"}},
		{"the last day of one month to the first of the next", on(2026, time.October, 31), on(2026, time.December, 1), []string{"2026-10 food 1", "2026-11 food 110", "2026-12 food 1000"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, "spending", e.monthLines(tt.from, tt.to), tt.want...)
		})
	}
}

func TestSpendingCountsConfirmedTransactionsOnlyNotDraftsAndNotVoidOnesWhateverTheAreaAndProject(t *testing.T) {
	e := newBudgetEnv(t)
	draft := e.store(e.expenseOn(nov(3), nil, nil, e.food, "1"))
	e.spend(nov(4), nil, nil, e.food, "10")
	voided := e.spend(nov(5), &e.work, nil, e.food, "100")

	check := func(when, want string) {
		t.Helper()
		wantLines(t, "spending "+when, e.monthLines(nov(1), nov(30)), "2026-11 food "+want)
	}

	// The draft has not been confirmed, and counts nowhere.
	check("with a draft", "110")
	// Confirming it makes it count.
	e.setStatus(draft.ID, model.StatusConfirmed)
	check("once it is confirmed", "111")
	// Voiding takes it out of the figures again.
	e.setStatus(voided.ID, model.StatusVoid)
	check("with a void one", "11")
	// A month with nothing but a draft has no row at all.
	e.store(e.expenseOn(on(2026, time.December, 3), nil, nil, e.food, "7"))
	wantLines(t, "a month with only a draft", e.monthLines(on(2026, time.December, 1), on(2026, time.December, 31)))
}

func TestSpendingDatedAfterTheLastDayOfTheRangeIsNotCountedAndIsOnceTheRangeReachesIt(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(14), nil, nil, e.food, "10")
	e.spend(nov(20), nil, nil, e.food, "100") // dated in the future, confirmed already

	tests := []struct {
		name string
		to   time.Time
		want string
	}{
		{"the day before it", nov(19), "10"},
		{"its own day", nov(20), "110"},
		{"the day after", nov(21), "110"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, "spending", e.monthLines(nov(1), tt.to), "2026-11 food "+tt.want)
		})
	}
}

func TestSpendingOfEachCategoryIsItsOwnRowWithTheParentOfASubcategory(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), nil, nil, e.food, "1")
	e.spend(nov(2), &e.work, nil, e.lunch, "10")
	e.spend(nov(2), nil, nil, e.foodOther, "100")
	e.spend(nov(2), nil, &e.trip, e.rentOther, "1000")
	e.spend(nov(3), nil, nil, e.rent, "10000")

	rows := e.spendingRows(nov(1), nov(30))

	wantLines(t, "what was spent", e.monthLines(nov(1), nov(30)),
		"2026-11 food 1",
		"2026-11 lunch 10 (under food)",
		"2026-11 food/other 100 (under food)",
		"2026-11 rent/other 1000 (under rent)",
		"2026-11 rent 10000",
	)
	// A top-level category has no parent, and a subcategory keeps its own row: adding the
	// subcategories up under their parent is for the caller.
	for _, r := range rows {
		if (r.CategoryID == e.food.ID || r.CategoryID == e.rent.ID) && r.ParentID != nil {
			t.Errorf("category %s has the parent %s, want none", e.label()[r.CategoryID], e.label()[*r.ParentID])
		}
	}
}

func TestSpendingInAForeignCurrencyIsByTheAmountInTheBaseCurrency(t *testing.T) {
	e := newBudgetEnv(t)
	// 100 USD that was 3,200 TWD on the day, and a second one, on another day, of 3,100.
	for day, base := range map[int]string{3: "3200", 4: "3100"} {
		txn, err := model.NewExpense(model.ExpenseParams{
			Header: e.dated(nov(day), nil, nil), Account: &e.card, Instrument: e.usd, BaseCurrency: e.twd,
			Lines: []model.CategoryLine{{Category: e.food, Amount: dec("100"), BaseAmount: decPtr(base)}},
		})
		if err != nil {
			t.Fatalf("NewExpense: %v", err)
		}
		e.storeConfirmed(txn)
	}

	wantLines(t, "what was spent", e.monthLines(nov(1), nov(30)), "2026-11 food 6300")
}

func TestSpendingCountsFeesInterestAndWhatAReconciliationFoundMissingAndNotTheConversionOfABill(t *testing.T) {
	e := newBudgetEnv(t)

	// A fee of 15 that comes with a transfer, which is not an expense itself, and one of 11 with
	// an area. Neither has a project.
	for _, area := range []*uuid.UUID{nil, &e.work} {
		fee := "15"
		if area != nil {
			fee = "11"
		}
		transfer, err := model.NewTransfer(model.TransferParams{
			Header: e.dated(nov(3), area, nil), From: e.bank, To: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("1000"),
			Fees: []model.Fee{{Account: e.bank, Instrument: e.twd, Category: e.fees, Amount: dec(fee)}},
		})
		if err != nil {
			t.Fatalf("NewTransfer: %v", err)
		}
		e.storeConfirmed(transfer)
	}

	// An expense of 12,000 paid in 12 periods from December: all of it is spent on the day of the
	// purchase, and each period's interest on the day it falls due. The first period has 30 of it.
	plan := e.planOf("spending-plan-0001")
	e.storePlan(plan)
	e.setPlanStatus(plan, model.StatusConfirmed)

	// A conversion of a debt into a plan is not an expense: it has no expense posting.
	bill := e.billInstalmentOf("spending-bill-0001")
	e.storePlan(bill)
	e.setPlanStatus(bill, model.StatusConfirmed)

	// 120 that a reconciliation found missing.
	reconciliation, err := model.Reconcile(model.ReconcileParams{
		Header: e.dated(nov(10), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		RecordedBalance: dec("1000"), ActualBalance: dec("880"),
	})
	if err != nil || reconciliation.Transaction == nil {
		t.Fatalf("Reconcile = %+v, %v, want a draft of the 120 that is missing", reconciliation, err)
	}
	e.storeConfirmed(*reconciliation.Transaction)

	wantLines(t, "November", e.monthLines(nov(1), nov(30)), "2026-11 fees 26", "2026-11 food 12000", "2026-11 unrecorded 120")
	wantLines(t, "December, the day before the interest falls due", e.monthLines(on(2026, time.December, 1), on(2026, time.December, 4)))
	wantLines(t, "December, on the day", e.monthLines(on(2026, time.December, 1), on(2026, time.December, 5)), "2026-12 interest 30")
	wantLines(t, "the months after", e.monthLines(on(2027, time.January, 1), on(2027, time.December, 31)))
}

func TestSpendingIsNotReducedByARewardADiscountOrAnIncome(t *testing.T) {
	e := newBudgetEnv(t)
	// 450 was paid, after the merchant took 50 off at checkout: the expense is 450.
	discounted, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), nil, nil), Account: &e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("450"), Discount: decPtr("50")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(discounted)
	wantLines(t, "spending with the discount", e.monthLines(nov(1), nov(30)), "2026-11 food 450")

	// The same day: a reward of 50 and an income of 5,000, which are books of their own.
	reward, err := model.NewReward(model.RewardParams{
		Header: e.dated(nov(3), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	e.storeConfirmed(reward)
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.dated(nov(3), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("5000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.storeConfirmed(income)

	wantLines(t, "spending with the reward and the income", e.monthLines(nov(1), nov(30)), "2026-11 food 450")
}

func TestASplitExpenseSpendsOnlyMyShareAndWhatAPartyPaidForMeIsMine(t *testing.T) {
	e := newBudgetEnv(t)
	// 500 is paid from the card: 300 is mine, and A owes me 200.
	split, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), nil, nil), Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines:  []model.CategoryLine{{Category: e.food, Amount: dec("300")}},
		Shares: []model.Share{{PartyID: e.partyA, Amount: dec("200")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(split)
	// A paid 90 of mine: there is no account posting, and the expense is still mine.
	paidBy, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(4), nil, nil), PaidBy: &e.partyA, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.rent, Amount: dec("90")}},
	})
	if err != nil {
		t.Fatalf("NewExpense paid by a party: %v", err)
	}
	e.storeConfirmed(paidBy)
	// What I paid for a party is a dealing, which is not an expense.
	e.storeConfirmed(e.dealingOf("spending-deal-001", nov(5), e.partyB, e.twd, "5000", model.DealingPaidOut))

	wantLines(t, "spending", e.monthLines(nov(1), nov(30)), "2026-11 food 300", "2026-11 rent 90")
}

func TestSpendingIsByMonthThenCategoryIdAcrossAYearAndEachMonthIsItsFirstDay(t *testing.T) {
	e := newBudgetEnv(t)
	categories := []model.Category{e.food, e.rent, e.lunch, e.fees}
	// Several categories in several months, given in an order of neither.
	for i, day := range []time.Time{
		on(2027, time.January, 1), on(2026, time.December, 31), on(2026, time.November, 15), on(2027, time.January, 31), on(2026, time.October, 1), on(2026, time.December, 1),
	} {
		for j := range categories {
			c := categories[(i+j)%len(categories)]
			e.spend(day, nil, nil, c, fmt.Sprint(10*(i+1)+j))
		}
	}

	rows := e.spendingRows(on(2026, time.October, 1), on(2027, time.January, 31))

	if len(rows) != 4*4 {
		t.Fatalf("%d rows, want 16: 4 months of 4 categories", len(rows))
	}
	for i, r := range rows {
		if r.Month.Day() != 1 || r.Month.Location() != time.UTC {
			t.Errorf("row %d: month %s, want the first day of the month as a date at midnight UTC", i+1, r.Month)
		}
		if i == 0 {
			continue
		}
		prev := rows[i-1]
		if prev.Month.After(r.Month) || (prev.Month.Equal(r.Month) && bytes.Compare(prev.CategoryID[:], r.CategoryID[:]) >= 0) {
			t.Errorf("row %d (%s) comes after row %d (%s), want them by month and then category id",
				i, e.spendingOfMonthLine(prev), i+1, e.spendingOfMonthLine(r))
		}
	}
	months := make([]string, 0, 4)
	for _, r := range rows {
		if m := r.Month.Format("2006-01"); !slices.Contains(months, m) {
			months = append(months, m)
		}
	}
	if want := []string{"2026-10", "2026-11", "2026-12", "2027-01"}; !slices.Equal(months, want) {
		t.Errorf("months = %v, want %v: the year ends between the last day of December and the first of January", months, want)
	}
	// The 31st of December is December's and the 1st of January is January's: each day has
	// 40(i+1)+6 spent in all, for the i-th day of those above, counted from 0.
	for month, want := range map[time.Time]string{
		on(2026, time.October, 1):  "206", // the 1st of October
		on(2026, time.November, 1): "126", // the 15th of November
		on(2026, time.December, 1): "332", // the 31st and the 1st of December
		on(2027, time.January, 1):  "212", // the 1st and the 31st of January
	} {
		if got := totalOf(rows, month); !got.Equal(dec(want)) {
			t.Errorf("%s: %s was spent, want %s", month.Format("2006-01"), got, want)
		}
	}
}

func TestSpendingOfARangeWithNoExpenseIsNothingAndNotAnError(t *testing.T) {
	e := newBudgetEnv(t)
	if rows := e.spendingRows(nov(1), nov(30)); len(rows) != 0 {
		t.Errorf("spending = %v, want nothing in a ledger with no expense", rows)
	}
	e.spend(nov(3), nil, nil, e.food, "10")
	if rows := e.spendingRows(on(2025, time.January, 1), on(2025, time.December, 31)); len(rows) != 0 {
		t.Errorf("spending = %v, want nothing in a year with no expense", rows)
	}
}

// What is spent in a month is what the areas spent in it, projects included, and the
// expenses that have no area, which no area counts: one definition of spent, read two
// ways. The ledger has expenses in areas, in projects and in neither, and of every kind
// that counts, and things that do not count; what was made in no area is added up as it
// is made, and not read back, so that the two sides are not one statement of SQL
// compared with itself.
func TestSpendingOfAMonthIsWhatTheAreasSpentPlusTheExpensesOfNoArea(t *testing.T) {
	e := newBudgetEnv(t)
	names := e.label()
	noArea := map[string]decimal.Decimal{} // what was made in no area, and counts, by month and category
	madeInNoArea := func(on time.Time, c model.Category, amount string) {
		k := on.Format("2006-01") + " " + names[c.ID]
		noArea[k] = noArea[k].Add(dec(amount))
	}
	// spend stores a confirmed expense, and notes it when it has no area.
	spend := func(on time.Time, area, project *uuid.UUID, c model.Category, amount string) model.Transaction {
		txn := e.spend(on, area, project, c, amount)
		if area == nil {
			madeInNoArea(on, c, amount)
		}
		return txn
	}

	// Plain expenses: in an area, in an area and a project, in a project of no area, and in neither.
	spend(nov(2), &e.work, nil, e.food, "100")
	spend(nov(3), &e.work, &e.trip, e.food, "700")
	spend(nov(4), &e.home, nil, e.rent, "55")
	spend(nov(5), nil, nil, e.food, "999")
	spend(nov(6), nil, &e.trip, e.rent, "998")
	spend(nov(7), nil, &e.move, e.lunch, "7")

	// A fee with a transfer, in an area and in none.
	for _, area := range []*uuid.UUID{&e.work, nil} {
		transfer, err := model.NewTransfer(model.TransferParams{
			Header: e.dated(nov(8), area, nil), From: e.bank, To: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("1000"),
			Fees: []model.Fee{{Account: e.bank, Instrument: e.twd, Category: e.fees, Amount: dec("15")}},
		})
		if err != nil {
			t.Fatalf("NewTransfer: %v", err)
		}
		e.storeConfirmed(transfer)
		if area == nil {
			madeInNoArea(nov(8), e.fees, "15")
		}
	}

	// A purchase of 12,000 in instalments in the area Home: the whole of it in November, and the
	// interest of its first period, 30, in December, in the area of the purchase.
	header := e.header("equivalence-plan-1", ledgerDay)
	header.AreaID = &e.home
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: header, Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("12000")}},
	}, model.InstalmentSchedule{
		Periods: []model.InstalmentPeriod{
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 5), Principal: dec("6000"), Interest: decPtr("30")},
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 5), Principal: dec("6000")},
		},
		InterestCategory: &e.interest,
	})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	if got := plan.Periods[0].AreaID; got == nil || *got != e.home {
		t.Fatalf("the first period is of the area %v, want the area of the purchase, Home", got)
	}
	e.storePlan(plan)
	e.setPlanStatus(plan, model.StatusConfirmed)

	// 100 USD that were 3,200 TWD, in the area Work.
	dollars, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(9), &e.work, nil), Account: &e.card, Instrument: e.usd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("100"), BaseAmount: decPtr("3200")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(dollars)

	// 120 that a reconciliation found missing, in no area; and one that found too much, which is no
	// expense.
	for _, actual := range []string{"880", "1080"} {
		reconciliation, err := model.Reconcile(model.ReconcileParams{
			Header: e.dated(nov(10), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
			RecordedBalance: dec("1000"), ActualBalance: dec(actual),
		})
		if err != nil || reconciliation.Transaction == nil {
			t.Fatalf("Reconcile = %+v, %v", reconciliation, err)
		}
		e.storeConfirmed(*reconciliation.Transaction)
		if actual == "880" {
			txn := *reconciliation.Transaction
			madeInNoArea(txn.OccurredOn, model.Category{ID: model.UnrecordedExpenseCategoryID}, "120")
		}
	}

	// What does not count, in an area and in none: a draft, a void one, the days around the month, a
	// reward and an income.
	e.store(e.expenseOn(nov(11), &e.work, nil, e.food, "5"))
	e.store(e.expenseOn(nov(11), nil, nil, e.food, "6"))
	voided := e.spend(nov(12), nil, nil, e.food, "9")
	e.setStatus(voided.ID, model.StatusVoid)
	e.spend(on(2026, time.October, 31), &e.work, nil, e.food, "3")
	spend(on(2026, time.October, 31), nil, nil, e.food, "4")
	e.spend(on(2026, time.December, 1), &e.work, nil, e.food, "1000")
	spend(on(2026, time.December, 31), nil, nil, e.food, "2000")
	reward, err := model.NewReward(model.RewardParams{Header: e.dated(nov(13), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("50")})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	e.storeConfirmed(reward)
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.dated(nov(13), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("5000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.storeConfirmed(income)

	months := []struct {
		first      time.Time
		inAreas    string // what the areas spent in it, written down
		altogether string // what was spent in it, written down
	}{
		{on(2026, time.October, 1), "3", "7"},
		{nov(1), "16070", "18209"},
		{on(2026, time.December, 1), "1030", "3030"},
	}
	for _, m := range months {
		month := m.first.Format("2006-01")
		t.Run(month, func(t *testing.T) {
			last := model.LastDayOf(m.first)
			spending := e.spendingRows(m.first, last)
			inAreas := decimal.Zero
			want := map[string]decimal.Decimal{} // what each category was spent on: by the areas, and in no area
			for _, a := range e.areaSpending(m.first, last) {
				inAreas = inAreas.Add(a.Amount)
				want[names[a.CategoryID]] = want[names[a.CategoryID]].Add(a.Amount)
			}
			for k, amount := range noArea {
				if category, found := strings.CutPrefix(k, month+" "); found {
					want[category] = want[category].Add(amount)
				}
			}

			got := map[string]decimal.Decimal{}
			for _, r := range spending {
				got[names[r.CategoryID]] = r.Amount
			}
			if len(got) != len(want) {
				t.Errorf("%d categories spent on, want the %d the areas and no area spent on", len(got), len(want))
			}
			for category, amount := range want {
				if !got[category].Equal(amount) {
					t.Errorf("%s: %s was spent on %s, want %s: what the areas spent and what no area has", month, got[category], category, amount)
				}
			}

			if !inAreas.Equal(dec(m.inAreas)) {
				t.Errorf("the areas spent %s, want %s", inAreas, m.inAreas)
			}
			if total := totalOf(spending, m.first); !total.Equal(dec(m.altogether)) {
				t.Errorf("%s was spent, want %s", total, m.altogether)
			}
		})
	}
}
