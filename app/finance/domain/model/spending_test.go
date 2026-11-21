package model_test

import (
	"bytes"
	"slices"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// months writes the months as "2026-11", one each.
func months(ms []time.Time) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Format("2006-01")
	}
	return out
}

func TestSpendingRangeIsFromTheFirstDayOfTheFirstMonthThroughTheLastDayOfTheLastMonthOrTodayWhicheverComesFirst(t *testing.T) {
	tests := []struct {
		name        string
		first, last time.Time
		today       time.Time
		wantOK      bool // false when the first month has not begun: nothing is to be asked for
		wantFrom    time.Time
		wantThrough time.Time
	}{
		{"a month that is over, through its last day", on(2026, 10, 15), on(2026, 10, 15), on(2026, 11, 14), true, on(2026, 10, 1), on(2026, 10, 31)},
		{"the current month, through today", on(2026, 11, 3), on(2026, 11, 3), on(2026, 11, 14), true, on(2026, 11, 1), on(2026, 11, 14)},
		{"the current month on its first day", on(2026, 11, 20), on(2026, 11, 20), on(2026, 11, 1), true, on(2026, 11, 1), on(2026, 11, 1)},
		{"the current month on its last day", on(2026, 11, 1), on(2026, 11, 1), on(2026, 11, 30), true, on(2026, 11, 1), on(2026, 11, 30)},
		{"the day before a month begins", on(2026, 12, 1), on(2026, 12, 1), on(2026, 11, 30), false, time.Time{}, time.Time{}},
		{"a month long to come", on(2027, 6, 1), on(2027, 6, 1), on(2026, 11, 14), false, time.Time{}, time.Time{}},
		{"February of a leap year", on(2028, 2, 3), on(2028, 2, 3), on(2028, 6, 1), true, on(2028, 2, 1), on(2028, 2, 29)},
		{"several months that are over", on(2026, 9, 12), on(2026, 10, 3), on(2026, 11, 14), true, on(2026, 9, 1), on(2026, 10, 31)},
		{"several months up to the current one", on(2026, 9, 12), on(2026, 11, 3), on(2026, 11, 14), true, on(2026, 9, 1), on(2026, 11, 14)},
		{"several months that go on past today", on(2026, 10, 1), on(2027, 1, 1), on(2026, 11, 14), true, on(2026, 10, 1), on(2026, 11, 14)},
		{"several months across a year that are over", on(2026, 12, 1), on(2027, 1, 20), on(2027, 2, 10), true, on(2026, 12, 1), on(2027, 1, 31)},
		{"several months that have not begun", on(2026, 12, 1), on(2027, 2, 1), on(2026, 11, 14), false, time.Time{}, time.Time{}},
		// 00:30 on the 1st of November in Taipei is the 31st of October in UTC: the month is
		// the one the time falls on as its own location reads it.
		{
			"a time just after midnight on the 1st in Taipei",
			time.Date(2026, 11, 1, 0, 30, 0, 0, taipei), time.Date(2026, 11, 1, 0, 30, 0, 0, taipei), on(2026, 12, 1),
			true, on(2026, 11, 1), on(2026, 11, 30),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, through, ok := model.SpendingRange(tt.first, tt.last, tt.today)

			if ok != tt.wantOK {
				t.Fatalf("SpendingRange = %s .. %s, ok %v, want ok %v", from.Format(time.DateOnly), through.Format(time.DateOnly), ok, tt.wantOK)
			}
			if ok && (!from.Equal(tt.wantFrom) || !through.Equal(tt.wantThrough)) {
				t.Errorf("SpendingRange = %s .. %s, want %s .. %s", from.Format(time.DateOnly), through.Format(time.DateOnly),
					tt.wantFrom.Format(time.DateOnly), tt.wantThrough.Format(time.DateOnly))
			}
		})
	}
}

func TestCountMonthsCountsBothEndsAndNoMonthWhenTheEndIsBeforeTheStart(t *testing.T) {
	tests := []struct {
		name        string
		first, last time.Time
		want        int
	}{
		{"one month", on(2026, 11, 1), on(2026, 11, 30), 1},
		{"one month, the days the other way round", on(2026, 11, 30), on(2026, 11, 1), 1},
		{"the next month, a day later", on(2026, 11, 30), on(2026, 12, 1), 2},
		{"the same month a year later", on(2026, 11, 14), on(2027, 11, 14), 13},
		{"across the end of a year", on(2026, 11, 1), on(2027, 2, 1), 4},
		{"a whole year", on(2026, 1, 15), on(2026, 12, 15), 12},
		{"ten years", on(2020, 1, 1), on(2029, 12, 31), 120},
		{"a month before", on(2026, 11, 1), on(2026, 10, 31), 0},
		{"a year before", on(2026, 11, 1), on(2025, 11, 1), 0},
		{"the month a time falls on in its own location", time.Date(2026, 11, 30, 23, 0, 0, 0, taipei), on(2026, 12, 1), 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := model.CountMonths(tt.first, tt.last); got != tt.want {
				t.Errorf("CountMonths(%s, %s) = %d, want %d", tt.first.Format(time.DateOnly), tt.last.Format(time.DateOnly), got, tt.want)
			}
		})
	}
}

func TestMonthsBetweenListsEachMonthOfTheRangeOldestFirstAsItsFirstDay(t *testing.T) {
	tests := []struct {
		name        string
		first, last time.Time
		want        []string
	}{
		{"one month", on(2026, 11, 17), on(2026, 11, 2), []string{"2026-11"}},
		{"within a year", on(2026, 3, 31), on(2026, 6, 1), []string{"2026-03", "2026-04", "2026-05", "2026-06"}},
		{"across the end of a year", on(2026, 11, 1), on(2027, 2, 28), []string{"2026-11", "2026-12", "2027-01", "2027-02"}},
		{"across a leap February", on(2028, 1, 31), on(2028, 3, 1), []string{"2028-01", "2028-02", "2028-03"}},
		{"a month before the first", on(2026, 11, 1), on(2026, 10, 31), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.MonthsBetween(tt.first, tt.last)

			if !slices.Equal(months(got), tt.want) {
				t.Errorf("MonthsBetween = %v, want %v", months(got), tt.want)
			}
			for _, m := range got {
				if m.Day() != 1 || m.Hour() != 0 || m.Location() != time.UTC {
					t.Errorf("%s is not the first day of a month at midnight UTC", m)
				}
			}
		})
	}

	// As many as there are to count: a range of ten years.
	if got := model.MonthsBetween(on(2020, 1, 1), on(2029, 12, 1)); len(got) != 120 || got[0].Month() != time.January || got[119].Month() != time.December {
		t.Errorf("a range of ten years has %d months, want 120 from January to December", len(got))
	}
}

// monthRow is spending on the category, of the amount, in the month.
func monthRow(month time.Time, category model.Category, amount string) model.MonthCategorySpending {
	return model.MonthCategorySpending{Month: month, Spending: spend(category, amount)}
}

func TestTotalsByMonthAddsUpTheRowsOfEachMonthAndFillsTheMonthsThatHaveNoneWithZero(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	rows := []model.MonthCategorySpending{
		monthRow(on(2026, 11, 1), food, "100.5"),
		monthRow(on(2026, 11, 1), lunch, "20"),
		monthRow(on(2026, 11, 1), rent, "5000"),
		monthRow(on(2027, 1, 1), food, "70"),
		monthRow(on(2027, 1, 1), food, "0.25"), // a category with two rows is added up as well
	}

	// From the middle of one month to the middle of another, across the end of a year.
	got := model.TotalsByMonth(on(2026, 11, 20), on(2027, 2, 3), rows)

	want := []struct{ month, total string }{
		{"2026-11", "5120.5"},
		{"2026-12", "0"},
		{"2027-01", "70.25"},
		{"2027-02", "0"},
	}
	if len(got) != len(want) {
		t.Fatalf("totals = %+v, want %d months", got, len(want))
	}
	for i, w := range want {
		if got[i].Month.Format("2006-01") != w.month || !got[i].Total.Equal(dec(w.total)) {
			t.Errorf("month %d = %s, %s; want %s, %s", i+1, got[i].Month.Format("2006-01"), got[i].Total, w.month, w.total)
		}
		if !got[i].Month.Equal(model.MonthOf(got[i].Month)) {
			t.Errorf("month %d is %s, want the first day of the month", i+1, got[i].Month)
		}
	}
}

func TestTotalsByMonthIsOneTotalForOneMonthAndNoneForAReversedRangeAndLeavesOutRowsOfOtherMonths(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rows := []model.MonthCategorySpending{
		monthRow(on(2026, 10, 1), food, "1"),
		monthRow(on(2026, 11, 1), food, "10"),
		monthRow(on(2026, 12, 1), food, "100"),
	}

	// The days of one month the other way round are one month.
	one := model.TotalsByMonth(on(2026, 11, 30), on(2026, 11, 1), rows)
	if len(one) != 1 || one[0].Month.Format("2006-01") != "2026-11" || !one[0].Total.Equal(dec("10")) {
		t.Errorf("totals of one month = %+v, want 2026-11, 10: the rows of the months around it are in none", one)
	}

	if got := model.TotalsByMonth(on(2026, 12, 1), on(2026, 10, 1), rows); len(got) != 0 {
		t.Errorf("totals of a reversed range = %+v, want none", got)
	}

	if got := model.TotalsByMonth(on(2027, 1, 1), on(2027, 1, 31), rows); len(got) != 1 || !got[0].Total.IsZero() {
		t.Errorf("totals of a month with no row = %+v, want it there with zero", got)
	}
}

func TestSpendingOfMonthRanksTheCategoriesLargestFirstThenByIdAndAddsThemUp(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	fun := topLevel(t, model.DirectionExpense, "娛樂")
	fees := topLevel(t, model.DirectionExpense, "手續費")
	// Two with the same amount, which only their ids put in order.
	same := []model.Category{fun, fees}
	slices.SortFunc(same, func(a, b model.Category) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	rows := []model.MonthCategorySpending{
		monthRow(on(2026, 11, 1), lunch, "20"),
		monthRow(on(2026, 11, 1), same[1], "50"),
		monthRow(on(2026, 11, 1), rent, "5000"),
		monthRow(on(2026, 11, 1), same[0], "50"),
		monthRow(on(2026, 11, 1), food, "100.25"),
		monthRow(on(2026, 10, 1), rent, "9999"), // another month
	}

	got, total := model.SpendingOfMonth(on(2026, 11, 17), rows)

	wantOrder := []model.Category{rent, food, same[0], same[1], lunch}
	if len(got) != len(wantOrder) {
		t.Fatalf("categories = %+v, want %d", got, len(wantOrder))
	}
	for i, c := range wantOrder {
		if got[i].CategoryID != c.ID {
			t.Errorf("category %d is %s, want %s: the largest first, and for the same amount by id", i+1, got[i].CategoryID, c.Name)
		}
	}
	if !total.Equal(dec("5220.25")) {
		t.Errorf("total = %s, want 5220.25: the rows of November, and not those of October", total)
	}
	// A subcategory keeps its parent, a top-level category has none.
	if got[4].ParentID == nil || *got[4].ParentID != food.ID {
		t.Errorf("lunch has the parent %v, want food", got[4].ParentID)
	}
	if got[0].ParentID != nil {
		t.Errorf("rent has the parent %v, want none", got[0].ParentID)
	}
}

func TestSpendingOfMonthAddsUpTheRowsOfOneCategoryAndHasNothingForAMonthWithNoRow(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rows := []model.MonthCategorySpending{
		monthRow(on(2026, 11, 1), food, "10.10"),
		monthRow(on(2026, 11, 1), food, "0.20"),
		monthRow(on(2026, 11, 1), food, "5"),
	}

	got, total := model.SpendingOfMonth(on(2026, 11, 1), rows)
	if len(got) != 1 || got[0].CategoryID != food.ID || !got[0].Amount.Equal(dec("15.30")) || !total.Equal(dec("15.30")) {
		t.Errorf("spending = %+v, total %s, want the one category with 15.30: three rows of it added up", got, total)
	}

	got, total = model.SpendingOfMonth(on(2026, 12, 1), rows)
	if len(got) != 0 || !total.IsZero() {
		t.Errorf("spending of a month with no row = %+v, total %s, want none and zero", got, total)
	}
	if got, total = model.SpendingOfMonth(on(2026, 11, 1), nil); len(got) != 0 || !total.IsZero() {
		t.Errorf("spending of no rows = %+v, total %s, want none and zero", got, total)
	}
}

func TestSpendingOfMonthTakesTheMonthAsItsOwnLocationReadsIt(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rows := []model.MonthCategorySpending{monthRow(on(2026, 11, 1), food, "10")}

	// 00:30 on the 1st of December in Taipei is the 30th of November in UTC: the month is
	// December, as the zone of the time reads it.
	if got, _ := model.SpendingOfMonth(time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), rows); len(got) != 0 {
		t.Errorf("spending of the month a Taipei time falls on = %+v, want none of November's", got)
	}
	if got, _ := model.SpendingOfMonth(time.Date(2026, 11, 30, 23, 0, 0, 0, taipei), rows); len(got) != 1 {
		t.Errorf("spending of November through a Taipei time = %+v, want the row", got)
	}
}
