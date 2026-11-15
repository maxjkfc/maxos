package budget_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// areaIDs lists the areas of the statuses, in order.
func areaIDs(statuses []usecase.AreaBudgetStatus) []uuid.UUID {
	ids := make([]uuid.UUID, len(statuses))
	for i, s := range statuses {
		ids[i] = s.AreaID
	}
	return ids
}

func TestTheBudgetOfAMonthIsTheOneInForceInItAndNoLaterOne(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	e.setArea(work, day(2026, 10, 1), "5000")
	e.setArea(work, day(2026, 12, 1), "9000")
	e.setArea(work, day(2027, 2, 1), "0") // no budget from February on
	e.setArea(work, day(2027, 4, 1), "4000")

	tests := []struct {
		name  string
		month time.Time
		want  string // "" is no budget, which is an area that is not listed: it has no expenses either
	}{
		{"before the first budget", day(2026, 9, 15), ""},
		{"the month of the first budget", day(2026, 10, 1), "5000"},
		{"a month the first still covers", day(2026, 11, 20), "5000"},
		{"the month of the second", day(2026, 12, 1), "9000"},
		{"a month the second still covers", day(2027, 1, 31), "9000"},
		{"the month a budget of nothing ends the budget", day(2027, 2, 1), ""},
		{"a month it still ends", day(2027, 3, 10), ""},
		{"the month another starts", day(2027, 4, 1), "4000"},
		{"long after", day(2030, 1, 1), "4000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.areas(tt.month)

			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("areas = %+v, want none: no budget is in force and nothing was spent", got)
				}
				return
			}
			if len(got) != 1 || got[0].AreaID != work || got[0].Budget == nil {
				t.Fatalf("areas = %+v, want the work area with a budget", got)
			}
			wantMoney(t, "budget", *got[0].Budget, tt.want)
		})
	}
}

func TestAnAreaThatSpentInAMonthWithNoBudgetInForceIsListedWithoutOne(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	e.setArea(work, day(2026, 12, 1), "9000") // from December on only
	e.spend(day(2026, 11, 3), &work, nil, food, "120")
	e.spend(day(2026, 11, 9), &work, nil, food, "30.50")

	got := e.areaOf(work, day(2026, 11, 1))

	if got.Budget != nil || got.Over || len(got.Allocations) != 0 {
		t.Errorf("status = %+v, want no budget, nothing over and no allocation: the budget of December does not apply to November", got)
	}
	wantMoney(t, "spent", got.Spent, "150.50")
	wantMoney(t, "spent with projects", got.SpentWithProjects, "150.50")
	wantMoney(t, "unallocated", got.Unallocated, "150.50")
}

func TestAnAreaWithABudgetAndNoExpensesIsListedWithNothingSpent(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	e.setArea(work, day(2026, 11, 1), "9000", allocation(food, "3000"))

	got := e.areaOf(work, day(2026, 11, 1))

	if got.Budget == nil || got.Over || len(got.Allocations) != 1 {
		t.Fatalf("status = %+v, want the budget, with its allocation, and nothing over", got)
	}
	wantMoney(t, "budget", *got.Budget, "9000")
	wantMoney(t, "spent", got.Spent, "0")
	wantMoney(t, "spent with projects", got.SpentWithProjects, "0")
	wantMoney(t, "unallocated", got.Unallocated, "0")
	wantMoney(t, "spent into the allocation", got.Allocations[0].Spent, "0")
	wantMoney(t, "the allocation", got.Allocations[0].Amount, "3000")
}

func TestWhatAProjectSpentInAnAreaIsInWhatTheAreaSpentButNotInWhatItIsComparedWith(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	fun := e.category("娛樂", nil)
	e.setArea(work, day(2026, 11, 1), "1000", allocation(food, "600"))
	e.spend(day(2026, 11, 2), &work, nil, food, "500")
	e.spend(day(2026, 11, 3), &work, nil, fun, "100")
	e.spend(day(2026, 11, 4), &work, &trip, food, "700") // the trip's: in the area, in a project

	got := e.areaOf(work, day(2026, 11, 1))

	wantMoney(t, "spent", got.Spent, "600")
	wantMoney(t, "spent with projects", got.SpentWithProjects, "1300")
	if len(got.Allocations) != 1 {
		t.Fatalf("allocations = %+v, want the one", got.Allocations)
	}
	wantMoney(t, "spent into the allocation of food", got.Allocations[0].Spent, "500")
	wantMoney(t, "unallocated", got.Unallocated, "100")
	if got.Over {
		t.Error("over, want not: 600 of 1000 is what the budget is compared with, and the trip's 700 is the trip's")
	}

	// What is not the project's does count against it.
	e.spend(day(2026, 11, 5), &work, nil, food, "400.01")
	if got = e.areaOf(work, day(2026, 11, 1)); !got.Over {
		t.Errorf("not over with %s spent of 1000, want over", got.Spent)
	}
	wantMoney(t, "spent", got.Spent, "1000.01")
	wantMoney(t, "spent with projects", got.SpentWithProjects, "1700.01")
}

func TestAnExpenseOfAnAreaFallsIntoTheAllocationOfItsCategoryElseOfItsParentElseIsUnallocated(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	dinner := e.category("晚餐", &food)
	rent := e.category("房租", nil)
	hostel := e.category("民宿", &rent)
	transport := e.category("交通", nil)
	taxi := e.category("計程車", &transport)
	fun := e.category("娛樂", nil)
	games := e.category("遊戲", nil)
	unrecorded := e.addCategory(model.Category{ID: model.UnrecordedExpenseCategoryID, Direction: model.DirectionExpense, Name: "未記錄"})
	e.setArea(work, day(2026, 11, 1), "20000", allocation(food, "6000"), allocation(rent, "8000"), allocation(games, "1000"))
	for _, expense := range []struct {
		category model.Category
		amount   string
	}{
		{food, "100"}, {lunch, "200"}, {dinner, "400"}, // all food
		{rent, "8000.50"}, {hostel, "3000"}, // all rent, which is more than the allocation
		{taxi, "50"}, {fun, "25"}, {unrecorded, "5"}, // none: the parent of the taxi is not allocated, and a system category has none
	} {
		e.spend(day(2026, 11, 5), &work, nil, expense.category, expense.amount)
	}

	got := e.areaOf(work, day(2026, 11, 1))

	wantMoney(t, "spent", got.Spent, "11780.50")
	if len(got.Allocations) != 3 {
		t.Fatalf("allocations = %+v, want the three, in the order they were set", got.Allocations)
	}
	for i, want := range []struct {
		category      model.Category
		amount, spent string
	}{
		{food, "6000", "700"},
		{rent, "8000", "11000.50"},
		{games, "1000", "0"},
	} {
		a := got.Allocations[i]
		if a.CategoryID != want.category.ID {
			t.Errorf("allocation %d is of %s, want %s", i+1, a.CategoryID, want.category.Name)
		}
		wantMoney(t, want.category.Name+": the allocation", a.Amount, want.amount)
		wantMoney(t, want.category.Name+": spent into it", a.Spent, want.spent)
	}
	wantMoney(t, "unallocated", got.Unallocated, "80")
	if got.Over {
		t.Error("over, want not: an allocation can be spent beyond, the budget is over when all of it is")
	}
}

func TestAnExpenseCountsInTheMonthOfItsDayAndOnlyOnceThatDayHasComeByTodayInTheTimezoneOfTheClock(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	e.setArea(work, day(2026, 11, 1), "100000")
	// An amount for each day, so that the total says which of them it adds up.
	for _, expense := range []struct {
		on     time.Time
		amount string
	}{
		{day(2026, 10, 31), "1"},     // the day before the month
		{day(2026, 11, 1), "10"},     // its first day
		{day(2026, 11, 14), "100"},   // today
		{day(2026, 11, 15), "1000"},  // tomorrow
		{day(2026, 11, 30), "10000"}, // its last day
		{day(2026, 12, 1), "100000"}, // the day after the month
	} {
		e.spend(expense.on, &work, nil, food, expense.amount)
	}

	tests := []struct {
		name      string
		now       time.Time
		want      string
		wantAsked bool // whether the repo is asked at all
	}{
		{"before the month", time.Date(2026, 10, 31, 12, 0, 0, 0, taipei), "0", false},
		{"the first day of the month", time.Date(2026, 11, 1, 0, 0, 0, 0, taipei), "10", true},
		{"in the middle of it: what is dated later is not counted yet", time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), "110", true},
		// It is still the 14th in UTC then: the day is the one in the timezone of the clock.
		{"just after midnight in Taipei, the day after", time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), "1110", true},
		{"the last day of the month", time.Date(2026, 11, 30, 18, 0, 0, 0, taipei), "11110", true},
		{"the first day of the next month: its expense is not this month's", time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), "11110", true},
		{"long after", time.Date(2027, 6, 1, 9, 0, 0, 0, taipei), "11110", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.now = tt.now
			e.budgets.areaSpendingAsked = nil

			got := e.areaOf(work, day(2026, 11, 15)) // any day of the month will do

			wantMoney(t, "spent", got.Spent, tt.want)
			wantMoney(t, "spent with projects", got.SpentWithProjects, tt.want)
			if asked := len(e.budgets.areaSpendingAsked) > 0; asked != tt.wantAsked {
				t.Errorf("repo asked about the spending: %v, want %v", asked, tt.wantAsked)
			}
		})
	}
}

func TestTheSpendingOfAMonthIsAskedForFromItsFirstDayToItsLastOrToTodayWhicheverComesFirst(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	e.setArea(work, day(2026, 1, 1), "1000")

	tests := []struct {
		name         string
		today        time.Time
		month        time.Time
		wantFrom     time.Time
		wantTo       time.Time
		wantNotAsked bool
	}{
		{"the current month, to today", day(2026, 11, 14), day(2026, 11, 3), day(2026, 11, 1), day(2026, 11, 14), false},
		{"a month that is over, to its last day", day(2026, 11, 14), day(2026, 10, 3), day(2026, 10, 1), day(2026, 10, 31), false},
		{"February of a leap year", day(2028, 6, 1), day(2028, 2, 3), day(2028, 2, 1), day(2028, 2, 29), false},
		{"the first day of the month is today", day(2026, 11, 1), day(2026, 11, 20), day(2026, 11, 1), day(2026, 11, 1), false},
		{"a month that has not begun asks for nothing", day(2026, 11, 14), day(2026, 12, 1), time.Time{}, time.Time{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.setDay(tt.today.Year(), tt.today.Month(), tt.today.Day())
			e.budgets.areaSpendingAsked = nil

			e.areas(tt.month)

			if tt.wantNotAsked {
				if len(e.budgets.areaSpendingAsked) != 0 {
					t.Errorf("asked for %v, want nothing asked for a month that has not begun", e.budgets.areaSpendingAsked)
				}
				return
			}
			if len(e.budgets.areaSpendingAsked) != 1 ||
				!e.budgets.areaSpendingAsked[0][0].Equal(tt.wantFrom) || !e.budgets.areaSpendingAsked[0][1].Equal(tt.wantTo) {
				t.Errorf("asked for %v, want one call for %s to %s", e.budgets.areaSpendingAsked, tt.wantFrom.Format(time.DateOnly), tt.wantTo.Format(time.DateOnly))
			}
		})
	}
}

func TestAreasAreListedInTheOrderOfTheCatalogForTheBudgetsInForceAndTheExpensesOfTheMonth(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	// Made in the opposite order of their names, so that the ids do not give the order.
	delta := e.area("Delta", true) // archived, and its budget still counts
	bravo := e.area("Bravo", false)
	charlie := e.area("Charlie", false)
	alpha := e.area("Alpha", false)
	e.setArea(alpha, day(2026, 11, 1), "1000") // only a budget
	e.setArea(charlie, day(2026, 10, 1), "2000")
	e.setArea(charlie, day(2026, 11, 1), "0")               // no budget now, and nothing spent
	e.spend(day(2026, 11, 2), &bravo, nil, food, "10")      // only expenses
	e.spend(day(2026, 11, 2), nil, nil, food, "999")        // no area: in none
	e.spend(day(2026, 11, 2), nil, &uuid.UUID{}, food, "1") // no area, in a project: in none either
	e.budgets.areaBudgets = append(e.budgets.areaBudgets, model.AreaBudget{
		ID: uuid.Must(uuid.NewV7()), AreaID: delta, EffectiveFrom: day(2026, 9, 1), Amount: dec("500"),
	})

	got := e.areas(day(2026, 11, 1))

	if want := []uuid.UUID{alpha, bravo, delta}; !slices.Equal(areaIDs(got), want) {
		t.Fatalf("areas = %v, want alpha, bravo and delta in the order of their names: charlie has no budget and spent nothing", areaIDs(got))
	}
	if !slices.Equal(e.catalog.includeArchived, []bool{true}) {
		t.Errorf("the catalog was asked for archived areas too: %v, want once, yes", e.catalog.includeArchived)
	}
	if got[1].Budget != nil {
		t.Errorf("bravo has a budget %s, want none: it only spent", got[1].Budget)
	}
	wantMoney(t, "what bravo spent", got[1].Spent, "10")
	if got[2].Budget == nil {
		t.Error("delta has no budget, want the one from September, which is still in force")
	}
}

func TestTheCatalogIsNotAskedForTheAreasWhenThereIsNothingToList(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	e.setArea(work, day(2026, 12, 1), "9000")

	got := e.areas(day(2026, 11, 1))

	if len(got) != 0 || e.catalog.listAreasCalls != 0 {
		t.Errorf("areas = %+v and the catalog was asked %d times, want none of either", got, e.catalog.listAreasCalls)
	}
}

func TestAnAreaTheCatalogDoesNotListIsNotMadeUp(t *testing.T) {
	t.Run("a budget of it", func(t *testing.T) {
		e := newEnv(t)
		gone := e.area("不見了", false)
		e.setArea(gone, day(2026, 11, 1), "100")
		delete(e.catalog.areas, gone)

		_, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: day(2026, 11, 1)})

		if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), gone.String()) {
			t.Errorf("err = %v, want ErrNotFound naming the area", err)
		}
	})
	t.Run("expenses in it", func(t *testing.T) {
		e := newEnv(t)
		food := e.category("飲食", nil)
		unknown := uuid.Must(uuid.NewV7())
		e.spend(day(2026, 11, 2), &unknown, nil, food, "10")

		_, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: day(2026, 11, 1)})

		if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %v, want ErrNotFound naming the area", err)
		}
	})
}

func TestGetAreaBudgetsNeedsAMonthAndNamesItByTheFirstDayOfTheMonthTheRequestFallsIn(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{})
	if !errors.Is(err, model.ErrMonthRequired) {
		t.Errorf("err = %v, want ErrMonthRequired", err)
	}

	for _, month := range []time.Time{day(2026, 11, 1), day(2026, 11, 30), time.Date(2026, 11, 30, 23, 0, 0, 0, taipei)} {
		out, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: month})
		if err != nil {
			t.Fatalf("GetAreaBudgets(%s): %v", month, err)
		}
		if !out.Month.Equal(day(2026, 11, 1)) {
			t.Errorf("month = %s, want 2026-11-01 for %s", out.Month, month)
		}
	}
}

func TestWhenTheCatalogOrTheRepoFailsTheErrorGoesOn(t *testing.T) {
	boom := errors.New("the catalog is down")

	t.Run("the list of areas", func(t *testing.T) {
		e := newEnv(t)
		work := e.area("工作", false)
		e.setArea(work, day(2026, 11, 1), "100")
		e.catalog.listErr = boom

		_, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: day(2026, 11, 1)})

		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want it to be what the catalog returned", err)
		}
	})
	t.Run("the check of the project asked for", func(t *testing.T) {
		e := newEnv(t)
		trip := e.project("日本旅行", false)
		e.catalog.verifyErr = boom

		_, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: &trip})

		if !errors.Is(err, boom) || errors.Is(err, model.ErrNotFound) {
			t.Errorf("err = %v, want what the catalog returned, and not ErrNotFound: a catalog that fails does not say the project is not there", err)
		}
	})
}

func TestAProjectStandsAgainstItsBudgetWithAllItsExpensesWhenevertheyWereMade(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	other := e.project("搬家", false)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	stay := e.category("住宿", nil)
	transport := e.category("交通", nil)
	e.setProject(trip, "80000", allocation(food, "20000"), allocation(stay, "30000"))
	e.spend(day(2026, 11, 2), nil, &trip, food, "12000")
	e.spend(day(2026, 11, 3), &work, &trip, lunch, "3000") // in an area too: still the trip's
	e.spend(day(2026, 3, 1), nil, &trip, stay, "31000")    // months ago: a project's budget is for all of it
	e.spend(day(2026, 11, 4), nil, &trip, transport, "9000")
	e.spend(day(2026, 11, 4), nil, nil, food, "77")     // no project: nobody's
	e.spend(day(2026, 11, 4), &work, &other, food, "5") // another project's

	got := e.projectOf(trip)

	if got.Budget == nil {
		t.Fatal("no budget, want 80000")
	}
	wantMoney(t, "budget", *got.Budget, "80000")
	wantMoney(t, "spent", got.Spent, "55000")
	if len(got.Allocations) != 2 {
		t.Fatalf("allocations = %+v, want two", got.Allocations)
	}
	wantMoney(t, "spent into food, which takes its subcategory lunch too", got.Allocations[0].Spent, "15000")
	wantMoney(t, "spent into the stay, which is more than its allocation", got.Allocations[1].Spent, "31000")
	wantMoney(t, "unallocated", got.Unallocated, "9000")
	if got.Over {
		t.Error("over, want not: 55000 of 80000, whatever one allocation did")
	}

	e.spend(day(2026, 11, 5), nil, &trip, transport, "25000.01")
	if got = e.projectOf(trip); !got.Over {
		t.Errorf("not over with %s spent of 80000, want over", got.Spent)
	}
}

func TestAProjectExpenseCountsOnceItsDayHasCome(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	e.setProject(trip, "1000")
	e.spend(day(2026, 11, 14), nil, &trip, food, "10") // today
	e.spend(day(2026, 11, 15), nil, &trip, food, "100")
	e.spend(day(2026, 12, 25), nil, &trip, food, "1000")

	tests := []struct {
		now  time.Time
		want string
	}{
		{time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), "10"},
		{time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), "110"}, // the 14th in UTC still
		{time.Date(2026, 12, 24, 12, 0, 0, 0, taipei), "110"},
		{time.Date(2026, 12, 25, 12, 0, 0, 0, taipei), "1110"},
	}
	for _, tt := range tests {
		e.clock.now = tt.now
		e.budgets.projectSpendingAsked = nil

		got := e.projectOf(trip)

		wantMoney(t, "spent on "+tt.now.Format(time.DateTime), got.Spent, tt.want)
		if want := model.DateOf(tt.now); len(e.budgets.projectSpendingAsked) != 1 || !e.budgets.projectSpendingAsked[0].Equal(want) {
			t.Errorf("on %s the repo was asked up to %v, want today as the clock reads it, %s", tt.now, e.budgets.projectSpendingAsked, want.Format(time.DateOnly))
		}
	}
}

func TestAProjectBudgetOfNothingIsABudgetAndIsOverOnceAnythingIsSpent(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	e.setProject(trip, "0")

	got := e.projectOf(trip)
	if got.Budget == nil || got.Over {
		t.Fatalf("status = %+v, want a budget of nothing, and nothing over while nothing is spent", got)
	}

	e.spend(day(2026, 11, 2), nil, &trip, food, "0.01")
	if got = e.projectOf(trip); got.Budget == nil || !got.Over {
		t.Errorf("status = %+v, want it over its budget of nothing", got)
	}
}

func TestAProjectIsListedWithItsBudgetOrItsExpensesAndNeitherListsWhatItLacks(t *testing.T) {
	e := newEnv(t)
	onlyBudget := e.project("A 只有預算", false)
	onlySpending := e.project("B 只有支出", false)
	neither := e.project("C 都沒有", false)
	food := e.category("飲食", nil)
	e.setProject(onlyBudget, "5000", allocation(food, "1000"))
	e.spend(day(2026, 11, 2), nil, &onlySpending, food, "120")

	got := e.projects()

	if len(got) != 2 || got[0].ProjectID != onlyBudget || got[1].ProjectID != onlySpending {
		t.Fatalf("projects = %+v, want the one with a budget and the one with expenses, and not %s", got, neither)
	}
	if got[0].Budget == nil || len(got[0].Allocations) != 1 || !got[0].Spent.IsZero() || got[0].Over {
		t.Errorf("project with a budget only = %+v, want its budget and allocation, with nothing spent", got[0])
	}
	if got[1].Budget != nil || got[1].Over || len(got[1].Allocations) != 0 {
		t.Errorf("project with expenses only = %+v, want no budget, nothing over and no allocation", got[1])
	}
	wantMoney(t, "spent", got[1].Spent, "120")
	wantMoney(t, "unallocated", got[1].Unallocated, "120")
}

func TestProjectsAreListedInTheOrderOfTheCatalogTheArchivedOnesToo(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	// Made in the opposite order of their names, so that the ids do not give the order.
	delta := e.project("Delta", false)
	charlie := e.project("Charlie", false)
	bravo := e.project("Bravo", false)
	alpha := e.project("Alpha", false)
	e.setProject(delta, "100")
	e.setProject(alpha, "100")
	e.spend(day(2026, 11, 2), nil, &charlie, food, "10")
	e.spend(day(2026, 11, 2), nil, &bravo, food, "10")
	e.archiveProject(bravo) // what an archived project spent still counts

	got := e.projects()

	var order []uuid.UUID
	for _, p := range got {
		order = append(order, p.ProjectID)
	}
	if want := []uuid.UUID{alpha, bravo, charlie, delta}; !slices.Equal(order, want) {
		t.Errorf("projects = %v, want alpha, bravo, charlie and delta, which is the order of their names", order)
	}
	if !slices.Equal(e.catalog.includeArchived, []bool{true}) {
		t.Errorf("the catalog was asked for archived projects too: %v, want once, yes", e.catalog.includeArchived)
	}
}

func TestAProjectTheCatalogDoesNotListIsNotMadeUp(t *testing.T) {
	e := newEnv(t)
	gone := e.project("不見了", false)
	e.setProject(gone, "100")
	delete(e.catalog.projects, gone)

	_, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{})

	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), gone.String()) {
		t.Errorf("err = %v, want ErrNotFound naming the project", err)
	}
}

func TestOneProjectIsAskedForWithoutListingAllTheOthers(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	other := e.project("搬家", false)
	food := e.category("飲食", nil)
	e.setProject(trip, "5000")
	e.setProject(other, "300")
	e.spend(day(2026, 11, 2), nil, &trip, food, "120")
	e.spend(day(2026, 11, 2), nil, &other, food, "99")

	out, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: &trip})
	if err != nil {
		t.Fatalf("GetProjectBudgets: %v", err)
	}

	if len(out.Projects) != 1 || out.Projects[0].ProjectID != trip {
		t.Fatalf("projects = %+v, want the trip only", out.Projects)
	}
	wantMoney(t, "spent", out.Projects[0].Spent, "120")
	if e.catalog.listProjectsCalls != 0 {
		t.Error("the catalog was asked for the list of projects, want the one project checked and nothing to put in order")
	}
	if len(e.budgets.projectFilters) != 1 || e.budgets.projectFilters[0] == nil || *e.budgets.projectFilters[0] != trip {
		t.Errorf("the repo was asked for %v, want it limited to the trip", e.budgets.projectFilters)
	}
}

func TestAProjectThatHasNothingIsNotListedAndOneTheCatalogDoesNotKnowIsNotFound(t *testing.T) {
	t.Run("a project with neither a budget nor an expense", func(t *testing.T) {
		e := newEnv(t)
		trip := e.project("日本旅行", false)

		out, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: &trip})

		if err != nil || len(out.Projects) != 0 {
			t.Errorf("projects = %+v, err = %v, want an empty result: it is a project, with nothing in it", out.Projects, err)
		}
	})
	t.Run("a project the catalog does not have", func(t *testing.T) {
		e := newEnv(t)
		unknown := uuid.Must(uuid.NewV7())

		_, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: &unknown})

		if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %v, want ErrNotFound naming the project", err)
		}
		if e.budgets.projectBudgetReads != 0 || len(e.budgets.projectSpendingAsked) != 0 {
			t.Error("the repo was asked, want a project that does not exist to be an error and not an empty result")
		}
	})
	t.Run("an archived project", func(t *testing.T) {
		e := newEnv(t)
		trip := e.project("日本旅行", false)
		food := e.category("飲食", nil)
		e.setProject(trip, "5000")
		e.spend(day(2026, 11, 2), nil, &trip, food, "120")
		e.archiveProject(trip)

		out, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: &trip})

		if err != nil || len(out.Projects) != 1 {
			t.Fatalf("projects = %+v, err = %v, want what the archived project spent: it still counts", out.Projects, err)
		}
		wantMoney(t, "spent", out.Projects[0].Spent, "120")
	})
}
