package model_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// subcategory returns a subcategory of the parent, of its direction.
func subcategory(t *testing.T, parent model.Category, name string) model.Category {
	t.Helper()
	c, err := model.NewCategory(model.NewCategoryParams{
		ID: uuid.Must(uuid.NewV7()), Direction: parent.Direction, Name: name, Parent: &parent,
	})
	if err != nil {
		t.Fatalf("NewCategory(%q): %v", name, err)
	}
	return c
}

// line is an allocation to make of the amount for the category.
func line(c model.Category, amount string) model.AllocationLine {
	return model.AllocationLine{Category: c, Amount: dec(amount)}
}

// november is a day of November 2026, the month the tests set budgets for.
func november(day int) time.Time {
	return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC)
}

func areaBudgetOf(t *testing.T, month time.Time, amount string, lines ...model.AllocationLine) (model.AreaBudget, error) {
	t.Helper()
	return model.NewAreaBudget(model.NewAreaBudgetParams{
		ID: uuid.Must(uuid.NewV7()), AreaID: uuid.Must(uuid.NewV7()), Month: month,
		Amount: dec(amount), Allocations: lines, BaseCurrency: newTWD(t),
	})
}

func projectBudgetOf(t *testing.T, amount string, lines ...model.AllocationLine) (model.ProjectBudget, error) {
	t.Helper()
	return model.NewProjectBudget(model.NewProjectBudgetParams{
		ID: uuid.Must(uuid.NewV7()), ProjectID: uuid.Must(uuid.NewV7()),
		Amount: dec(amount), Allocations: lines, BaseCurrency: newTWD(t),
	})
}

func TestMonthOfIsTheFirstDayOfTheMonthAsTheLocationOfTheTimeReadsIt(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want time.Time
	}{
		{"the first day", november(1), november(1)},
		{"the middle of the month", november(15), november(1)},
		{"the last day", november(30), november(1)},
		// 23:00 on the 30th in Taipei is 15:00 in UTC, the same day; half past midnight on
		// the 1st of December in Taipei is still the 30th of November in UTC.
		{"just before the next month in Taipei", time.Date(2026, 11, 30, 23, 0, 0, 0, taipei), november(1)},
		{"just after midnight on the 1st in Taipei", time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)},
		{"a day of February in a leap year", time.Date(2028, 2, 29, 10, 0, 0, 0, time.UTC), time.Date(2028, 2, 1, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.MonthOf(tt.in); !got.Equal(tt.want) || got.Location() != time.UTC {
				t.Errorf("MonthOf(%s) = %s, want %s at midnight UTC", tt.in, got, tt.want)
			}
		})
	}
}

func TestLastDayOfIsTheLastDayOfTheMonth(t *testing.T) {
	tests := []struct {
		in   time.Time
		want time.Time
	}{
		{november(1), november(30)},
		{november(17), november(30)},
		{time.Date(2026, 12, 9, 0, 0, 0, 0, time.UTC), time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)},
		{time.Date(2027, 2, 3, 0, 0, 0, 0, time.UTC), time.Date(2027, 2, 28, 0, 0, 0, 0, time.UTC)},
		{time.Date(2028, 2, 3, 0, 0, 0, 0, time.UTC), time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC)},
	}
	for _, tt := range tests {
		if got := model.LastDayOf(tt.in); !got.Equal(tt.want) {
			t.Errorf("LastDayOf(%s) = %s, want %s", tt.in.Format(time.DateOnly), got.Format(time.DateOnly), tt.want.Format(time.DateOnly))
		}
	}
}

func TestTheBudgetOfAnAreaTakesEffectInTheFirstDayOfTheMonthItIsSetFor(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")

	for _, month := range []time.Time{november(1), november(14), november(30), time.Date(2026, 11, 30, 23, 0, 0, 0, taipei)} {
		got, err := areaBudgetOf(t, month, "30000", line(food, "8000"), line(rent, "12000.50"))
		if err != nil {
			t.Fatalf("NewAreaBudget for %s: %v", month, err)
		}
		if !got.EffectiveFrom.Equal(november(1)) {
			t.Errorf("effective from %s, want the first day of the month %s", got.EffectiveFrom, month)
		}
		if !got.Amount.Equal(dec("30000")) {
			t.Errorf("amount = %s, want 30000", got.Amount)
		}
		// The allocations are kept as they were given.
		if len(got.Allocations) != 2 ||
			got.Allocations[0].CategoryID != food.ID || !got.Allocations[0].Amount.Equal(dec("8000")) ||
			got.Allocations[1].CategoryID != rent.ID || !got.Allocations[1].Amount.Equal(dec("12000.50")) {
			t.Errorf("allocations = %+v, want food 8000 then rent 12000.50", got.Allocations)
		}
	}
}

func TestTheBudgetOfAnAreaNeedsAMonth(t *testing.T) {
	_, err := areaBudgetOf(t, time.Time{}, "30000")
	if !errors.Is(err, model.ErrMonthRequired) {
		t.Fatalf("err = %v, want ErrMonthRequired", err)
	}
}

func TestABudgetMayBeOfNothingButNotOfLessAndNotFinerThanTheBaseCurrency(t *testing.T) {
	tests := []struct {
		name   string
		amount string
		want   error // nil when the budget is fine
	}{
		{"nothing", "0", nil},
		{"a round amount", "30000", nil},
		{"cents", "30000.55", nil},
		{"trailing zeros are not decimal places", "30000.500", nil},
		{"less than nothing", "-1", model.ErrBudgetAmountNegative},
		{"a tenth of a cent", "30000.555", model.ErrTooManyDecimals},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := areaBudgetOf(t, november(1), tt.amount); !errors.Is(err, tt.want) {
				t.Errorf("area: err = %v, want %v", err, tt.want)
			}
			if _, err := projectBudgetOf(t, tt.amount); !errors.Is(err, tt.want) {
				t.Errorf("project: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestABudgetOfNothingHasNoRoomForAnAllocation(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")

	_, err := areaBudgetOf(t, november(1), "0", line(food, "1"))

	if !errors.Is(err, model.ErrAllocationsExceedBudget) {
		t.Fatalf("err = %v, want ErrAllocationsExceedBudget", err)
	}
}

func TestTheAllocationsAddUpToNoMoreThanTheBudget(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")

	// All of it, and less, are fine.
	for _, rentAmount := range []string{"7000", "6999.99", "1"} {
		if _, err := areaBudgetOf(t, november(1), "10000", line(food, "3000"), line(rent, rentAmount)); err != nil {
			t.Errorf("3000 and %s of 10000: err = %v, want none", rentAmount, err)
		}
		if _, err := projectBudgetOf(t, "10000", line(food, "3000"), line(rent, rentAmount)); err != nil {
			t.Errorf("project, 3000 and %s of 10000: err = %v, want none", rentAmount, err)
		}
	}

	// A cent more is not, and the error says what they add up to.
	_, err := areaBudgetOf(t, november(1), "10000", line(food, "3000"), line(rent, "7000.01"))
	if !errors.Is(err, model.ErrAllocationsExceedBudget) {
		t.Fatalf("err = %v, want ErrAllocationsExceedBudget", err)
	}
	if !strings.Contains(err.Error(), "10000.01") || !strings.Contains(err.Error(), "budget is 10000") {
		t.Errorf("err = %q, want it to say the allocations add up to 10000.01 and the budget is 10000", err)
	}
	if _, err := projectBudgetOf(t, "10000", line(food, "3000"), line(rent, "7000.01")); !errors.Is(err, model.ErrAllocationsExceedBudget) {
		t.Errorf("project: err = %v, want ErrAllocationsExceedBudget", err)
	}
}

func TestAnAllocationIsOfAnAmountGreaterThanZeroAndNoFinerThanTheBaseCurrency(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")
	tests := []struct {
		name   string
		amount string
		want   error
	}{
		{"zero", "0", model.ErrAmountNotPositive},
		{"negative", "-5", model.ErrAmountNotPositive},
		{"too fine", "1.005", model.ErrTooManyDecimals},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The second allocation is the one that is wrong, and the error says so.
			_, err := areaBudgetOf(t, november(1), "10000", line(food, "100"), line(rent, tt.amount))

			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), "allocation 2") {
				t.Errorf("err = %v, want %v of allocation 2", err, tt.want)
			}
		})
	}
}

func TestOnlyAnExpenseCategoryInUseThatIsNotASystemCategoryCanBeAllocated(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	tests := []struct {
		name     string
		category model.Category
		want     error
	}{
		{"an archived category", archivedCategory(topLevel(t, model.DirectionExpense, "舊的分類")), model.ErrArchived},
		{"an income category", topLevel(t, model.DirectionIncome, "薪水"), model.ErrWrongCategoryDirection},
		{"the system category of what was not recorded", unrecordedCategory(model.DirectionExpense), model.ErrSystemCategory},
		{"the system category of the income that was not recorded", unrecordedCategory(model.DirectionIncome), model.ErrSystemCategory},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := areaBudgetOf(t, november(1), "10000", line(food, "100"), line(tt.category, "100"))
			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), "allocation 2") {
				t.Errorf("area: err = %v, want %v of allocation 2", err, tt.want)
			}

			_, err = projectBudgetOf(t, "10000", line(tt.category, "100"))
			if !errors.Is(err, tt.want) {
				t.Errorf("project: err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestACategoryHasOneAllocationAtMostInABudget(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")

	_, err := areaBudgetOf(t, november(1), "10000", line(food, "100"), line(rent, "100"), line(food, "200"))

	if !errors.Is(err, model.ErrDuplicateAllocation) || !strings.Contains(err.Error(), "allocation 3") || !strings.Contains(err.Error(), "allocation 1") {
		t.Fatalf("err = %v, want ErrDuplicateAllocation of allocation 3, which is allocation 1 too", err)
	}
	if _, err := projectBudgetOf(t, "10000", line(food, "100"), line(food, "100")); !errors.Is(err, model.ErrDuplicateAllocation) {
		t.Errorf("project: err = %v, want ErrDuplicateAllocation", err)
	}
}

func TestACategoryAndItsParentCannotBothBeAllocatedWhicheverComesFirst(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	dinner := subcategory(t, food, "晚餐")
	rent := topLevel(t, model.DirectionExpense, "房租")

	for _, tt := range []struct {
		name  string
		lines []model.AllocationLine
	}{
		{"the parent first", []model.AllocationLine{line(food, "3000"), line(lunch, "1000")}},
		{"the child first", []model.AllocationLine{line(lunch, "1000"), line(food, "3000")}},
		{"another category between them", []model.AllocationLine{line(lunch, "1000"), line(rent, "1000"), line(food, "3000")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := areaBudgetOf(t, november(1), "10000", tt.lines...)
			if !errors.Is(err, model.ErrParentAndChildAllocated) {
				t.Errorf("area: err = %v, want ErrParentAndChildAllocated", err)
			}
			_, err = projectBudgetOf(t, "10000", tt.lines...)
			if !errors.Is(err, model.ErrParentAndChildAllocated) {
				t.Errorf("project: err = %v, want ErrParentAndChildAllocated", err)
			}
		})
	}

	// A subcategory by itself, siblings, and a subcategory next to the allocation of
	// another parent are all fine.
	for _, lines := range [][]model.AllocationLine{
		{line(lunch, "1000")},
		{line(lunch, "1000"), line(dinner, "2000")},
		{line(lunch, "1000"), line(rent, "2000")},
	} {
		if _, err := areaBudgetOf(t, november(1), "10000", lines...); err != nil {
			t.Errorf("err = %v, want none", err)
		}
	}
}

func TestTheBudgetOfAProjectIsOneTotalWithNoMonth(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")

	got, err := projectBudgetOf(t, "50000", line(food, "20000"))
	if err != nil {
		t.Fatalf("NewProjectBudget: %v", err)
	}

	if !got.Amount.Equal(dec("50000")) || len(got.Allocations) != 1 || got.Allocations[0].CategoryID != food.ID {
		t.Errorf("budget = %+v, want 50000 with an allocation for food", got)
	}
}

// areaBudget is a budget of an area, in force from the month, as the repo stores it.
func areaBudget(area uuid.UUID, from time.Time, amount string) model.AreaBudget {
	return model.AreaBudget{ID: uuid.Must(uuid.NewV7()), AreaID: area, EffectiveFrom: from, Amount: dec(amount)}
}

func TestTheBudgetInForceInAMonthIsTheLatestOneThatIsNotAfterIt(t *testing.T) {
	area := uuid.Must(uuid.NewV7())
	september := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	budgets := []model.AreaBudget{
		areaBudget(area, december, "9000"), // out of order, on purpose
		areaBudget(area, september, "5000"),
		areaBudget(area, october, "7000"),
	}

	tests := []struct {
		name  string
		month time.Time
		want  string // "" is no budget
	}{
		{"before the first", time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), ""},
		{"the month of the first", september, "5000"},
		{"a day of that month", time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC), "5000"},
		{"the month of the second", october, "7000"},
		{"a month between, which the earlier one still covers", november(20), "7000"},
		{"the month of the last", december, "9000"},
		{"long after the last", time.Date(2028, 6, 1, 0, 0, 0, 0, time.UTC), "9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := model.BudgetsInForce(budgets, tt.month)[area]

			switch {
			case tt.want == "" && found:
				t.Errorf("budget in force = %s, want none: a later budget does not apply to an earlier month", got.Amount)
			case tt.want != "" && (!found || !got.Amount.Equal(dec(tt.want))):
				t.Errorf("budget in force = %s (found %v), want %s", got.Amount, found, tt.want)
			}
		})
	}
}

func TestABudgetOfNothingEndsTheOneBeforeItAndAnotherCanStartAgain(t *testing.T) {
	area := uuid.Must(uuid.NewV7())
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	december := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	january := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	february := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	budgets := []model.AreaBudget{
		areaBudget(area, october, "7000"),
		areaBudget(area, december, "0"),
		areaBudget(area, february, "8000"),
	}

	tests := []struct {
		name  string
		month time.Time
		want  string // "" is no budget
	}{
		{"before it ends", november(1), "7000"},
		{"the month it ends", december, ""},
		{"a month after, still ended", january, ""},
		{"the month another starts", february, "8000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := model.BudgetsInForce(budgets, tt.month)[area]

			switch {
			case tt.want == "" && found:
				t.Errorf("budget in force = %s, want none", got.Amount)
			case tt.want != "" && (!found || !got.Amount.Equal(dec(tt.want))):
				t.Errorf("budget in force = %s (found %v), want %s", got.Amount, found, tt.want)
			}
		})
	}
}

func TestEachAreaHasItsOwnBudgetInForce(t *testing.T) {
	work, home := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	october := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	budgets := []model.AreaBudget{
		areaBudget(work, october, "5000"),
		areaBudget(home, november(1), "3000"),
		areaBudget(work, november(1), "6000"),
	}

	inOctober := model.BudgetsInForce(budgets, october)
	if len(inOctober) != 1 || !inOctober[work].Amount.Equal(dec("5000")) {
		t.Errorf("in October = %+v, want only work, 5000: home's budget begins in November", inOctober)
	}
	// Any day of the month names the month.
	inNovember := model.BudgetsInForce(budgets, november(30))
	if len(inNovember) != 2 || !inNovember[work].Amount.Equal(dec("6000")) || !inNovember[home].Amount.Equal(dec("3000")) {
		t.Errorf("in November = %+v, want work 6000 and home 3000", inNovember)
	}
}

// spend is spending on the category, of the amount, with the parent of the category
// when it has one.
func spend(category model.Category, amount string) model.Spending {
	return model.Spending{CategoryID: category.ID, ParentID: category.ParentID, Amount: dec(amount)}
}

func allocation(c model.Category, amount string) model.Allocation {
	return model.Allocation{CategoryID: c.ID, Amount: dec(amount)}
}

func TestSpendingFallsIntoTheAllocationOfItsOwnCategory(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")
	fun := topLevel(t, model.DirectionExpense, "娛樂")
	budget := dec("10000")

	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "4000"), allocation(rent, "5000")}, []model.Spending{
		spend(food, "120"), spend(food, "30.50"), spend(rent, "5000"),
		spend(fun, "800"), // no allocation
	})

	if !got.Spent.Equal(dec("5950.50")) {
		t.Errorf("spent = %s, want 5950.50", got.Spent)
	}
	if len(got.Allocations) != 2 ||
		got.Allocations[0].CategoryID != food.ID || !got.Allocations[0].Amount.Equal(dec("4000")) || !got.Allocations[0].Spent.Equal(dec("150.50")) ||
		got.Allocations[1].CategoryID != rent.ID || !got.Allocations[1].Amount.Equal(dec("5000")) || !got.Allocations[1].Spent.Equal(dec("5000")) {
		t.Errorf("allocations = %+v, want food 4000 with 150.50 spent and rent 5000 with 5000 spent", got.Allocations)
	}
	if !got.Unallocated.Equal(dec("800")) {
		t.Errorf("unallocated = %s, want 800: the amusement has no allocation", got.Unallocated)
	}
	if got.Over {
		t.Error("over, want it not to be: 5950.50 of 10000")
	}
}

func TestSpendingOfASubcategoryFallsIntoTheAllocationOfItsParent(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	dinner := subcategory(t, food, "晚餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	hostel := subcategory(t, rent, "民宿")
	budget := dec("10000")

	// Food is allocated, so what is spent on it and on each of its subcategories falls
	// into that allocation. The hostel is a subcategory of rent, which has none.
	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "4000")}, []model.Spending{
		spend(food, "100"), spend(lunch, "200"), spend(dinner, "400"), spend(hostel, "5000"),
	})
	if !got.Allocations[0].Spent.Equal(dec("700")) {
		t.Errorf("food spent = %s, want 700: food, lunch and dinner", got.Allocations[0].Spent)
	}
	if !got.Unallocated.Equal(dec("5000")) || !got.Spent.Equal(dec("5700")) {
		t.Errorf("unallocated = %s, spent = %s, want 5000 and 5700", got.Unallocated, got.Spent)
	}

	// An allocation of the subcategory takes what is spent on it, and nothing of its
	// parent or of its sibling.
	got = model.NewBudgetStatus(&budget, []model.Allocation{allocation(lunch, "1000")}, []model.Spending{
		spend(food, "100"), spend(lunch, "200"), spend(dinner, "400"),
	})
	if !got.Allocations[0].Spent.Equal(dec("200")) || !got.Unallocated.Equal(dec("500")) {
		t.Errorf("lunch spent = %s, unallocated = %s, want 200 and 500", got.Allocations[0].Spent, got.Unallocated)
	}
}

func TestSpendingIsEitherInAnAllocationOrUnallocatedNeverBoth(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	fun := topLevel(t, model.DirectionExpense, "娛樂")
	budget := dec("10000")

	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "4000"), allocation(fun, "1000")}, []model.Spending{
		spend(food, "10.10"), spend(lunch, "20.20"), spend(fun, "30.30"), spend(fun, "0.01"),
	})

	sum := got.Unallocated
	for _, a := range got.Allocations {
		sum = sum.Add(a.Spent)
	}
	if !sum.Equal(got.Spent) || !got.Spent.Equal(dec("60.61")) {
		t.Errorf("allocations and unallocated add up to %s, spent = %s, want both 60.61", sum, got.Spent)
	}
}

func TestAnAllocationNothingFellIntoHasNothingSpent(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	budget := dec("1000")

	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "400")}, nil)

	if len(got.Allocations) != 1 || !got.Allocations[0].Spent.IsZero() || !got.Spent.IsZero() || !got.Unallocated.IsZero() || got.Over {
		t.Errorf("status = %+v, want the allocation listed with nothing spent, and nothing over", got)
	}
	if got.Allocations[0].Over {
		t.Error("the allocation nothing fell into is over, want it not to be: nothing is more than 400")
	}
}

func TestABudgetIsOverOnlyWhenMoreThanItIsSpent(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	tests := []struct {
		name  string
		spent string
		want  bool
	}{
		{"less", "999.99", false},
		{"all of it", "1000", false},
		{"a cent more", "1000.01", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := dec("1000")

			got := model.NewBudgetStatus(&budget, nil, []model.Spending{spend(food, tt.spent)})

			if got.Over != tt.want {
				t.Errorf("over = %v, want %v for %s of 1000", got.Over, tt.want, tt.spent)
			}
		})
	}

	// A budget of nothing is a budget: anything spent is over it.
	zero := decimal.Zero
	if got := model.NewBudgetStatus(&zero, nil, []model.Spending{spend(food, "0.01")}); !got.Over {
		t.Error("not over a budget of nothing with 0.01 spent, want over")
	}
}

func TestAnAllocationIsOverOnlyWhenMoreThanItsAmountFellIntoIt(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	tests := []struct {
		name  string
		spent string
		want  bool
	}{
		{"less", "399.99", false},
		{"all of it", "400", false},
		{"a cent more", "400.01", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			budget := dec("1000")

			got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "400")}, []model.Spending{spend(food, tt.spent)})

			if got.Allocations[0].Over != tt.want {
				t.Errorf("over = %v, want %v for %s of 400", got.Allocations[0].Over, tt.want, tt.spent)
			}
		})
	}
}

func TestWhatFellIntoAnAllocationFromItsSubcategoriesCountsTowardsItBeingOver(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	dinner := subcategory(t, food, "晚餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	hostel := subcategory(t, rent, "民宿")
	budget := dec("10000")

	// Food has 300: its own 100 and the 150 of lunch are within it, and the 100 of dinner, which falls into
	// it as well, makes 350. Rent has 500 and nothing is spent on rent itself: the 600 of its subcategory,
	// the hostel, is all that makes it over.
	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "300"), allocation(rent, "500")}, []model.Spending{
		spend(food, "100"), spend(lunch, "150"), spend(dinner, "100"), spend(hostel, "600"),
	})

	if !got.Allocations[0].Over || !got.Allocations[0].Spent.Equal(dec("350")) {
		t.Errorf("food: over = %v, spent = %s, want over with 350: the subcategories are in it", got.Allocations[0].Over, got.Allocations[0].Spent)
	}
	if !got.Allocations[1].Over || !got.Allocations[1].Spent.Equal(dec("600")) {
		t.Errorf("rent: over = %v, spent = %s, want over with 600: the hostel is a subcategory of it", got.Allocations[1].Over, got.Allocations[1].Spent)
	}

	// The allocation of a subcategory takes only what is spent on it: what is spent on its parent and on its
	// sibling is in none.
	got = model.NewBudgetStatus(&budget, []model.Allocation{allocation(lunch, "200")}, []model.Spending{
		spend(food, "500"), spend(lunch, "150"), spend(dinner, "500"),
	})
	if got.Allocations[0].Over {
		t.Errorf("lunch: over with %s of 200, want not over", got.Allocations[0].Spent)
	}
}

func TestAnAllocationBeingOverDoesNotMakeTheBudgetOverNorTheOtherWayRound(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	fun := topLevel(t, model.DirectionExpense, "娛樂")
	budget := dec("1000")

	// An allocation can be spent beyond while the budget has room, and the budget can be spent beyond by
	// what is in no allocation while the allocation has room.
	got := model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "100")}, []model.Spending{spend(food, "150")})
	if !got.Allocations[0].Over || got.Over {
		t.Errorf("allocation over = %v, budget over = %v, want the allocation over and the budget not: 150 of 1000", got.Allocations[0].Over, got.Over)
	}
	got = model.NewBudgetStatus(&budget, []model.Allocation{allocation(food, "100")}, []model.Spending{spend(food, "50"), spend(fun, "2000")})
	if got.Allocations[0].Over || !got.Over {
		t.Errorf("allocation over = %v, budget over = %v, want the budget over and the allocation not: 50 of 100", got.Allocations[0].Over, got.Over)
	}
}

func TestWithNoBudgetNothingIsOverAndAllSpendingIsUnallocated(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")

	got := model.NewBudgetStatus(nil, nil, []model.Spending{spend(food, "120"), spend(food, "5")})

	if got.Budget != nil || got.Over || len(got.Allocations) != 0 {
		t.Errorf("status = %+v, want no budget, nothing over and no allocation", got)
	}
	if !got.Spent.Equal(dec("125")) || !got.Unallocated.Equal(dec("125")) {
		t.Errorf("spent = %s, unallocated = %s, want 125 for both", got.Spent, got.Unallocated)
	}
}
