package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// What was spent is one statement of SQL, BudgetRepo.Spending: what it counts, which
// days and which project it is limited to, and how it groups the rows are tested here
// on the rows it returns. What is read of the rows (an area's, a project's, a month's)
// is the model's, and has its own unit tests; the tests of the last part of this file
// read it from the rows of a real database, as the use cases do, and say what the
// separate reads of it said before there was one statement.

// between is the filter of the days from the day from to the day to, both included.
func between(from, to time.Time) repo.SpendingFilter {
	return repo.SpendingFilter{From: &from, To: to}
}

// upTo is the filter of every day up to the day to, which is in it, whenever the
// first of them was.
func upTo(to time.Time) repo.SpendingFilter {
	return repo.SpendingFilter{To: to}
}

// ofProject limits the filter to the project.
func ofProject(filter repo.SpendingFilter, project uuid.UUID) repo.SpendingFilter {
	filter.ProjectID = &project
	return filter
}

// spending returns what Spending adds up for the filter.
func (e *budgetEnv) spending(filter repo.SpendingFilter) []model.SpendingRow {
	e.t.Helper()
	got, err := e.budgets.Spending(context.Background(), filter)
	if err != nil {
		e.t.Fatalf("Spending: %v", err)
	}
	return got
}

// spendingLine writes what was spent on a category as "owner category amount", with
// where the category is when it is a subcategory.
func (e *budgetEnv) spendingLine(owner uuid.UUID, s model.Spending) string {
	names := e.label()
	line := fmt.Sprintf("%s %s %s", names[owner], names[s.CategoryID], s.Amount)
	if s.ParentID != nil {
		line += " (under " + names[*s.ParentID] + ")"
	}
	return line
}

// rowLine writes a row as "2026-11 work trip food 150.75": the month, the area and the
// project, "-" for none, and the category, with where it is when it is a subcategory.
func (e *budgetEnv) rowLine(r model.SpendingRow) string {
	names := e.label()
	owner := func(id *uuid.UUID) string {
		if id == nil {
			return "-"
		}
		return names[*id]
	}
	line := fmt.Sprintf("%s %s %s %s %s", r.Month.Format("2006-01"), owner(r.AreaID), owner(r.ProjectID), names[r.CategoryID], r.Amount)
	if r.ParentID != nil {
		line += " (under " + names[*r.ParentID] + ")"
	}
	return line
}

// rowLines is what Spending adds up for the filter, one row to a line.
func (e *budgetEnv) rowLines(filter repo.SpendingFilter) []string {
	e.t.Helper()
	rows := e.spending(filter)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = e.rowLine(r)
	}
	return lines
}

// areaLines is what the areas spent in the month, as the model makes it of the rows:
// "work food 150.75", with " in a project" when it belongs to one.
func (e *budgetEnv) areaLines(month time.Time, rows []model.SpendingRow) []string {
	var lines []string
	for _, s := range model.AreaSpendingOfMonth(month, rows) {
		line := e.spendingLine(s.AreaID, s.Spending)
		if s.InProject {
			line += " in a project"
		}
		lines = append(lines, line)
	}
	return lines
}

// projectLines is what the projects spent, as the model makes it of the rows: "trip
// food 700".
func (e *budgetEnv) projectLines(rows []model.SpendingRow) []string {
	var lines []string
	for _, s := range model.ProjectSpendingOf(rows) {
		lines = append(lines, e.spendingLine(s.ProjectID, s.Spending))
	}
	return lines
}

// ---- What the statement counts ----

func TestSpendingAddsUpTheExpensesOfEachMonthAreaProjectAndCategory(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "120.5")
	e.spend(nov(3), &e.work, nil, e.food, "30.25") // the same area and category: added up
	e.spend(nov(3), &e.work, nil, e.lunch, "80")
	e.spend(nov(4), &e.work, &e.trip, e.food, "700") // in an area and in a project
	e.spend(nov(4), &e.work, &e.move, e.food, "20")  // in another project of the area
	e.spend(nov(4), &e.home, nil, e.food, "55")
	e.spend(nov(5), nil, nil, e.food, "999")                       // in no area
	e.spend(nov(5), nil, &e.trip, e.food, "998")                   // in a project that has no area
	e.spend(on(2026, time.December, 1), &e.work, nil, e.food, "1") // in another month

	wantLines(t, "what was spent", e.rowLines(between(nov(1), on(2026, time.December, 31))),
		"2026-11 work - food 150.75",
		"2026-11 work - lunch 80 (under food)",
		"2026-11 work trip food 700",
		"2026-11 work move food 20",
		"2026-11 home - food 55",
		"2026-11 - - food 999",
		"2026-11 - trip food 998",
		"2026-12 work - food 1",
	)
}

func TestSpendingTakesTheFirstAndTheLastDayOfTheRangeAndNeitherNeighbour(t *testing.T) {
	e := newBudgetEnv(t)
	// An amount for each day, so that what is counted is what the total adds up.
	e.spend(on(2026, time.October, 31), &e.work, nil, e.food, "1")
	e.spend(nov(1), nil, nil, e.food, "10")
	e.spend(nov(30), &e.work, nil, e.food, "100")
	e.spend(on(2026, time.December, 1), nil, nil, e.food, "1000")

	all := []string{"2026-10 work - food 1", "2026-11 - - food 10", "2026-11 work - food 100", "2026-12 - - food 1000"}
	tests := []struct {
		name   string
		filter repo.SpendingFilter
		want   []string
	}{
		{"the month: its first and last day, and not the days around it", between(nov(1), nov(30)), []string{"2026-11 - - food 10", "2026-11 work - food 100"}},
		{"to the day before the last", between(nov(1), nov(29)), []string{"2026-11 - - food 10"}},
		{"from the day after the first", between(nov(2), nov(30)), []string{"2026-11 work - food 100"}},
		{"the first day alone", between(nov(1), nov(1)), []string{"2026-11 - - food 10"}},
		{"the last day alone", between(nov(30), nov(30)), []string{"2026-11 work - food 100"}},
		{"the day before the month alone", between(on(2026, time.October, 31), on(2026, time.October, 31)), []string{"2026-10 work - food 1"}},
		{"the day after the month alone", between(on(2026, time.December, 1), on(2026, time.December, 1)), []string{"2026-12 - - food 1000"}},
		{"a day with nothing", between(nov(15), nov(15)), nil},
		{"the three months", between(on(2026, time.October, 1), on(2026, time.December, 31)), all},
		{"the last day of one month to the first of the next", between(on(2026, time.October, 31), on(2026, time.December, 1)), all},
		// With no first day it is every day up to the last, whenever the first was.
		{"no first day, up to the last day of November", upTo(nov(30)), all[:3]},
		{"no first day, up to the day before the last", upTo(nov(29)), []string{"2026-10 work - food 1", "2026-11 - - food 10"}},
		{"no first day, up to the last day of October", upTo(on(2026, time.October, 31)), all[:1]},
		{"no first day, up to the day before the first", upTo(on(2026, time.October, 30)), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, "spending", e.rowLines(tt.filter), tt.want...)
		})
	}
}

func TestWhatIsDatedAfterTheLastDayOfTheRangeIsNotSpentYetAndIsOnceTheRangeReachesIt(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(14), &e.work, &e.trip, e.food, "10")
	e.spend(nov(20), &e.work, &e.trip, e.food, "100") // dated in the future, confirmed already

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
			wantLines(t, "spending", e.rowLines(between(nov(1), tt.to)), "2026-11 work trip food "+tt.want)
			wantLines(t, "spending with no first day", e.rowLines(upTo(tt.to)), "2026-11 work trip food "+tt.want)
		})
	}
}

func TestSpendingCountsConfirmedTransactionsOnlyNotDraftsAndNotVoidOnes(t *testing.T) {
	e := newBudgetEnv(t)
	draft := e.store(e.expenseOn(nov(3), &e.work, &e.trip, e.food, "1"))
	e.spend(nov(4), &e.work, &e.trip, e.food, "10")
	voided := e.spend(nov(5), &e.work, &e.trip, e.food, "100")
	// In no area and no project too.
	loose := e.store(e.expenseOn(nov(6), nil, nil, e.rent, "1000"))

	check := func(when string, want ...string) {
		t.Helper()
		wantLines(t, "spending "+when, e.rowLines(between(nov(1), nov(30))), want...)
	}

	// The drafts have not been confirmed, and count nowhere.
	check("with drafts", "2026-11 work trip food 110")
	// Confirming them makes them count.
	e.setStatus(draft.ID, model.StatusConfirmed)
	e.setStatus(loose.ID, model.StatusConfirmed)
	check("once they are confirmed", "2026-11 work trip food 111", "2026-11 - - rent 1000")
	// Voiding takes them out of the figures again.
	e.setStatus(voided.ID, model.StatusVoid)
	e.setStatus(loose.ID, model.StatusVoid)
	check("with void ones", "2026-11 work trip food 11")
	// A month with nothing but a draft has no row at all.
	e.store(e.expenseOn(on(2026, time.December, 3), nil, nil, e.food, "7"))
	wantLines(t, "a month with only a draft", e.rowLines(between(on(2026, time.December, 1), on(2026, time.December, 31))))
}

func TestSpendingOfEachCategoryIsItsOwnRowWithTheParentOfASubcategory(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), nil, nil, e.food, "1")
	e.spend(nov(2), &e.work, nil, e.lunch, "10")
	e.spend(nov(2), nil, nil, e.foodOther, "100")
	e.spend(nov(2), nil, &e.trip, e.rentOther, "1000")
	e.spend(nov(3), nil, nil, e.rent, "10000")

	rows := e.spending(between(nov(1), nov(30)))

	wantLines(t, "what was spent", e.rowLines(between(nov(1), nov(30))),
		"2026-11 - - food 1",
		"2026-11 work - lunch 10 (under food)",
		"2026-11 - - food/other 100 (under food)",
		"2026-11 - trip rent/other 1000 (under rent)",
		"2026-11 - - rent 10000",
	)
	// A top-level category has no parent, and a subcategory keeps its own row: putting the
	// subcategories under their parent is for the model.
	for _, r := range rows {
		if (r.CategoryID == e.food.ID || r.CategoryID == e.rent.ID) && r.ParentID != nil {
			t.Errorf("category %s has the parent %s, want none", e.label()[r.CategoryID], e.label()[*r.ParentID])
		}
	}
}

func TestAnExpenseOfSeveralCategoriesIsSpentOnEachOfThem(t *testing.T) {
	e := newBudgetEnv(t)
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), &e.work, nil), Account: &e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("300")}, {Category: e.rent, Amount: dec("200")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(txn)

	wantLines(t, "what was spent", e.rowLines(between(nov(1), nov(30))), "2026-11 work - food 300", "2026-11 work - rent 200")
}

func TestWhatWasSpentOnACategoryThatIsArchivedSinceIsStillSpent(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.lunch, "10")
	e.spend(nov(3), nil, nil, e.food, "100")
	e.spend(nov(4), nil, nil, e.rent, "1000")
	// Archiving a category archives its subcategories with it.
	if _, err := postgres.NewCategoryRepo(e.pool).Archive(context.Background(), e.food.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	wantLines(t, "what was spent", e.rowLines(between(nov(1), nov(30))),
		"2026-11 work - lunch 10 (under food)", "2026-11 - - food 100", "2026-11 - - rent 1000")
}

func TestSpendingInAForeignCurrencyIsByTheAmountInTheBaseCurrency(t *testing.T) {
	e := newBudgetEnv(t)
	// 100 USD that was 3,200 TWD on the day, and a second one, on another day, of 3,100.
	for day, base := range map[int]string{3: "3200", 4: "3100"} {
		txn, err := model.NewExpense(model.ExpenseParams{
			Header: e.dated(nov(day), &e.work, &e.trip), Account: &e.card, Instrument: e.usd, BaseCurrency: e.twd,
			Lines: []model.CategoryLine{{Category: e.food, Amount: dec("100"), BaseAmount: decPtr(base)}},
		})
		if err != nil {
			t.Fatalf("NewExpense: %v", err)
		}
		e.storeConfirmed(txn)
	}

	wantLines(t, "what was spent", e.rowLines(between(nov(1), nov(30))), "2026-11 work trip food 6300")
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

	// A conversion of a debt into a plan is not an expense: it has no expense posting. The interest
	// of its periods is one, on the day each falls due: 20 on the first, which is the 20th of December.
	periods := []model.InstalmentPeriod{
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 20), Principal: dec("1000"), Interest: decPtr("20")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 20), Principal: dec("2000")},
	}
	bill, err := model.NewBillInstalment(model.BillInstalmentParams{
		Header: e.dated(nov(16), &e.work, nil), Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd, Principal: dec("3000"),
		Schedule: model.InstalmentSchedule{Periods: periods, InterestCategory: &e.interest},
	})
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}
	e.storePlan(bill)
	e.setPlanStatus(bill, model.StatusConfirmed)

	// 120 that a reconciliation found missing, which is spent on the system category; and 80 that it
	// found too much, which is no expense.
	for _, actual := range []string{"880", "1080"} {
		reconciliation, err := model.Reconcile(model.ReconcileParams{
			Header: e.dated(nov(10), nil, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
			RecordedBalance: dec("1000"), ActualBalance: dec(actual),
		})
		if err != nil || reconciliation.Transaction == nil {
			t.Fatalf("Reconcile = %+v, %v, want a draft of the difference", reconciliation, err)
		}
		e.storeConfirmed(*reconciliation.Transaction)
	}

	wantLines(t, "November", e.rowLines(between(nov(1), nov(30))),
		"2026-11 - - fees 15", "2026-11 work - fees 11", "2026-11 - - food 12000", "2026-11 - - unrecorded 120")
	wantLines(t, "December, the day before the interest of the plan falls due", e.rowLines(between(on(2026, time.December, 1), on(2026, time.December, 4))))
	wantLines(t, "December, on the day", e.rowLines(between(on(2026, time.December, 1), on(2026, time.December, 5))), "2026-12 - - interest 30")
	wantLines(t, "December, the day before the interest of the bill falls due", e.rowLines(between(on(2026, time.December, 1), on(2026, time.December, 19))), "2026-12 - - interest 30")
	wantLines(t, "December, on the day", e.rowLines(between(on(2026, time.December, 1), on(2026, time.December, 20))),
		"2026-12 - - interest 30", "2026-12 work - interest 20")
	wantLines(t, "the months after", e.rowLines(between(on(2027, time.January, 1), on(2027, time.December, 31))))
}

func TestSpendingIsNotReducedByARewardADiscountOrAnIncome(t *testing.T) {
	e := newBudgetEnv(t)
	// 450 was paid, after the merchant took 50 off at checkout: the expense is 450.
	discounted, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), &e.work, nil), Account: &e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("450"), Discount: decPtr("50")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(discounted)
	wantLines(t, "spending with the discount", e.rowLines(between(nov(1), nov(30))), "2026-11 work - food 450")

	// The same day: a reward of 50 and an income of 5,000, which are books of their own.
	reward, err := model.NewReward(model.RewardParams{
		Header: e.dated(nov(3), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	e.storeConfirmed(reward)
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.dated(nov(3), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("5000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.storeConfirmed(income)

	wantLines(t, "spending with the reward and the income", e.rowLines(between(nov(1), nov(30))), "2026-11 work - food 450")
}

func TestASplitExpenseSpendsOnlyMyShareAndWhatAPartyPaidForMeIsMine(t *testing.T) {
	e := newBudgetEnv(t)
	// 500 is paid from the card: 300 is mine, and A owes me 200.
	split, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), &e.work, nil), Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
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

	wantLines(t, "spending", e.rowLines(between(nov(1), nov(30))), "2026-11 work - food 300", "2026-11 - - rent 90")
}

func TestEachMonthOfTheRowsIsItsFirstDayAsADateAtMidnightUTCAndTheYearEndsBetweenDecemberAndJanuary(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(on(2026, time.December, 31), nil, nil, e.food, "1")
	e.spend(on(2027, time.January, 1), nil, nil, e.food, "10")
	e.spend(on(2027, time.January, 31), nil, nil, e.food, "100")
	e.spend(on(2027, time.February, 1), nil, nil, e.food, "1000")

	rows := e.spending(between(on(2026, time.December, 1), on(2027, time.February, 28)))

	wantLines(t, "what was spent", e.rowLines(between(on(2026, time.December, 1), on(2027, time.February, 28))),
		"2026-12 - - food 1", "2027-01 - - food 110", "2027-02 - - food 1000")
	for _, r := range rows {
		if r.Month.Day() != 1 || r.Month.Hour() != 0 || r.Month.Location() != time.UTC {
			t.Errorf("month %s, want the first day of the month as a date at midnight UTC", r.Month)
		}
	}
}

func TestSpendingCanBeLimitedToOneProject(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(on(2025, time.March, 1), nil, &e.trip, e.food, "1") // long ago, in no area
	e.spend(nov(2), &e.work, &e.trip, e.food, "10")
	e.spend(nov(3), &e.home, &e.trip, e.lunch, "100")
	e.spend(nov(3), nil, &e.move, e.food, "1000")
	e.spend(nov(3), &e.work, nil, e.food, "9999") // in no project

	wantLines(t, "every project, and what has none", e.rowLines(upTo(nov(30))),
		"2025-03 - trip food 1",
		"2026-11 work trip food 10",
		"2026-11 home trip lunch 100 (under food)",
		"2026-11 - move food 1000",
		"2026-11 work - food 9999",
	)
	wantLines(t, "the trip", e.rowLines(ofProject(upTo(nov(30)), e.trip)),
		"2025-03 - trip food 1", "2026-11 work trip food 10", "2026-11 home trip lunch 100 (under food)")
	wantLines(t, "the move", e.rowLines(ofProject(upTo(nov(30)), e.move)), "2026-11 - move food 1000")
	wantLines(t, "the trip, in November", e.rowLines(ofProject(between(nov(1), nov(30)), e.trip)),
		"2026-11 work trip food 10", "2026-11 home trip lunch 100 (under food)")
	wantLines(t, "a project with no expense", e.rowLines(ofProject(upTo(nov(30)), uuid.Must(uuid.NewV7()))))
	wantLines(t, "the trip, up to a day before its expenses", e.rowLines(ofProject(upTo(on(2025, time.February, 28)), e.trip)))
}

func TestSpendingInProjectsLeavesOutWhatBelongsToNoProjectAndChangesNoProjectTotal(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(on(2025, time.March, 1), nil, &e.trip, e.food, "1")
	e.spend(nov(2), &e.work, &e.trip, e.food, "10")
	e.spend(nov(3), &e.home, &e.trip, e.lunch, "100")
	e.spend(nov(3), nil, &e.move, e.food, "1000")
	e.spend(nov(3), &e.work, nil, e.food, "9999") // in an area, in no project
	e.spend(nov(4), nil, nil, e.food, "99999")    // in neither

	inProjects := func(filter repo.SpendingFilter) repo.SpendingFilter {
		filter.InProject = true
		return filter
	}

	wantLines(t, "in projects", e.rowLines(inProjects(upTo(nov(30)))),
		"2025-03 - trip food 1",
		"2026-11 work trip food 10",
		"2026-11 home trip lunch 100 (under food)",
		"2026-11 - move food 1000",
	)
	wantLines(t, "not limited to projects, as before", e.rowLines(upTo(nov(30))),
		"2025-03 - trip food 1",
		"2026-11 work trip food 10",
		"2026-11 home trip lunch 100 (under food)",
		"2026-11 - move food 1000",
		"2026-11 work - food 9999",
		"2026-11 - - food 99999",
	)
	wantLines(t, "in projects, in November", e.rowLines(inProjects(between(nov(1), nov(30)))),
		"2026-11 work trip food 10",
		"2026-11 home trip lunch 100 (under food)",
		"2026-11 - move food 1000",
	)
	wantLines(t, "in projects and of the trip", e.rowLines(inProjects(ofProject(upTo(nov(30)), e.trip))),
		"2025-03 - trip food 1", "2026-11 work trip food 10", "2026-11 home trip lunch 100 (under food)")
	wantLines(t, "in projects and of a project with no expense", e.rowLines(inProjects(ofProject(upTo(nov(30)), uuid.Must(uuid.NewV7())))))

	// What the projects spent is the same whether or not the rows of the expenses of no project were read.
	wantLines(t, "the projects' totals from the rows in projects", e.projectLines(e.spending(inProjects(upTo(nov(30))))),
		"trip food 11", "trip lunch 100 (under food)", "move food 1000")
	wantLines(t, "the projects' totals from every row", e.projectLines(e.spending(upTo(nov(30)))),
		"trip food 11", "trip lunch 100 (under food)", "move food 1000")
	wantLines(t, "the trip's totals from the rows in projects", e.projectLines(e.spending(inProjects(ofProject(upTo(nov(30)), e.trip)))),
		e.projectLines(e.spending(ofProject(upTo(nov(30)), e.trip)))...)
}

func TestSpendingOfALedgerWithNoExpenseIsNothingAndNotAnError(t *testing.T) {
	e := newBudgetEnv(t)
	if rows := e.spending(between(nov(1), nov(30))); len(rows) != 0 {
		t.Errorf("spending = %v, want nothing in a ledger with no expense", rows)
	}
	e.spend(nov(3), nil, nil, e.food, "10")
	if rows := e.spending(between(on(2025, time.January, 1), on(2025, time.December, 31))); len(rows) != 0 {
		t.Errorf("spending = %v, want nothing in a year with no expense", rows)
	}
}

// ---- What the model reads from the rows of a real database ----

// The areas, the projects and the months are read from the rows of the one statement.
// They give what the separate statements of the areas and of the projects gave: the same
// ledgers, and the same lines.

func TestTheSpendingOfTheAreasInAMonthIsAddedUpFromTheRowsByAreaAndCategoryApartFromThoseOfAProject(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "120.5")
	e.spend(nov(3), &e.work, nil, e.food, "30.25")
	e.spend(nov(3), &e.work, nil, e.lunch, "80")
	e.spend(nov(4), &e.work, &e.trip, e.food, "700") // in the area and in a project
	e.spend(nov(4), &e.work, &e.move, e.food, "20")  // in another project, which is no matter
	e.spend(nov(4), &e.home, nil, e.food, "55")
	e.spend(nov(5), nil, nil, e.food, "999")     // in no area
	e.spend(nov(5), nil, &e.trip, e.food, "998") // in a project that has no area

	wantLines(t, "what the areas spent", e.areaLines(nov(1), e.spending(between(nov(1), nov(30)))),
		"home food 55",
		"work food 150.75",
		"work food 720 in a project",
		"work lunch 80 (under food)",
	)
}

func TestTheSpendingOfTheProjectsIsAddedUpFromTheRowsByProjectAndCategoryWhateverTheDayUpToTheDayGiven(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(on(2025, time.March, 1), nil, &e.trip, e.food, "1") // long ago, and in no area
	e.spend(nov(2), &e.work, &e.trip, e.food, "10")             // in an area too
	e.spend(nov(3), &e.home, &e.trip, e.lunch, "100")
	e.spend(nov(3), nil, &e.move, e.food, "1000")
	e.spend(nov(3), &e.work, nil, e.food, "9999") // in no project

	wantLines(t, "every project", e.projectLines(e.spending(upTo(nov(30)))),
		"trip food 11",
		"trip lunch 100 (under food)",
		"move food 1000",
	)
	wantLines(t, "one project", e.projectLines(e.spending(ofProject(upTo(nov(30)), e.trip))), "trip food 11", "trip lunch 100 (under food)")
	wantLines(t, "a project with no expense", e.projectLines(e.spending(ofProject(upTo(nov(30)), uuid.Must(uuid.NewV7())))))
	wantLines(t, "up to a day before them all", e.projectLines(e.spending(upTo(on(2025, time.February, 28)))))
	wantLines(t, "up to the day of the first", e.projectLines(e.spending(upTo(on(2025, time.March, 1)))), "trip food 1")
}

func TestSpendingOfASubcategoryIsListedWithItsParentAndSoFallsIntoTheAllocationOfIt(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "1")
	e.spend(nov(2), &e.work, nil, e.lunch, "10")
	e.spend(nov(2), &e.work, nil, e.foodOther, "100")
	e.spend(nov(2), &e.work, &e.trip, e.rentOther, "1000")
	rows := e.spending(between(nov(1), nov(30)))

	wantLines(t, "what the areas spent", e.areaLines(nov(1), rows),
		"work food 1",
		"work lunch 10 (under food)",
		"work food/other 100 (under food)",
		"work rent/other 1000 (under rent) in a project",
	)
	wantLines(t, "what the projects spent", e.projectLines(rows), "trip rent/other 1000 (under rent)")

	// What the rows come with is what the model needs to put them in an allocation: the rows as the
	// database returns them, of the expenses that are not a project's, fall into the allocation of food,
	// which takes lunch and the other of food with it, and leave the allocation of rent, which the
	// project's expense is not in, with nothing.
	var outsideProjects []model.Spending
	for _, s := range model.AreaSpendingOfMonth(nov(1), rows) {
		if !s.InProject {
			outsideProjects = append(outsideProjects, s.Spending)
		}
	}
	budget := dec("5000")
	area := model.NewBudgetStatus(&budget, []model.Allocation{alloc(e.food, "2000"), alloc(e.rent, "500")}, outsideProjects)
	if !area.Allocations[0].Spent.Equal(dec("111")) || !area.Allocations[1].Spent.IsZero() || !area.Unallocated.IsZero() {
		t.Errorf("area: spent into food %s, into rent %s, unallocated %s, want 111, 0 and 0", area.Allocations[0].Spent, area.Allocations[1].Spent, area.Unallocated)
	}
	// The project's own expense, of a subcategory of rent, falls into the allocation of rent: 1000 of 900 is over.
	var ofTheTrip []model.Spending
	for _, s := range model.ProjectSpendingOf(rows) {
		ofTheTrip = append(ofTheTrip, s.Spending)
	}
	project := model.NewBudgetStatus(&budget, []model.Allocation{alloc(e.rent, "900")}, ofTheTrip)
	if !project.Allocations[0].Spent.Equal(dec("1000")) || !project.Allocations[0].Over || !project.Unallocated.IsZero() {
		t.Errorf("project: spent into rent %s (over %v), unallocated %s, want 1000, over, and 0", project.Allocations[0].Spent, project.Allocations[0].Over, project.Unallocated)
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
			rows := e.spending(between(m.first, model.LastDayOf(m.first)))

			inAreas := decimal.Zero
			want := map[string]decimal.Decimal{} // what each category was spent on: by the areas, and in no area
			for _, a := range model.AreaSpendingOfMonth(m.first, rows) {
				inAreas = inAreas.Add(a.Amount)
				want[names[a.CategoryID]] = want[names[a.CategoryID]].Add(a.Amount)
			}
			notedNoArea := decimal.Zero
			for k, amount := range noArea {
				if category, found := strings.CutPrefix(k, month+" "); found {
					want[category] = want[category].Add(amount)
					notedNoArea = notedNoArea.Add(amount)
				}
			}

			got := map[string]decimal.Decimal{} // what the rows say each category was spent on
			total := decimal.Zero
			for _, r := range rows {
				got[names[r.CategoryID]] = got[names[r.CategoryID]].Add(r.Amount)
				total = total.Add(r.Amount)
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
			if !total.Equal(dec(m.altogether)) || !total.Equal(inAreas.Add(notedNoArea)) {
				t.Errorf("%s was spent, want %s, which is %s and %s of no area", total, m.altogether, inAreas, notedNoArea)
			}
			// The month, read by its top-level categories, is the same total, and so is the month in a series.
			topLevel, byCategories := model.TopLevelSpendingOfMonth(m.first, rows)
			sum := decimal.Zero
			for _, c := range topLevel {
				sum = sum.Add(c.Total)
			}
			if !byCategories.Equal(total) || !sum.Equal(total) {
				t.Errorf("the top-level categories add up to %s and the month to %s, want %s", sum, byCategories, total)
			}
			if series := model.TotalsByMonth(m.first, m.first, rows); len(series) != 1 || !series[0].Total.Equal(total) {
				t.Errorf("the total of the month in a series is %v, want %s", series, total)
			}
		})
	}
}
