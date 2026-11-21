package model_test

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

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

// rowOf is what was spent on the category, of the amount, in the month, in the area
// and in the project; an area or a project that is nil is one that the expenses
// belong to none of.
func rowOf(month time.Time, area, project *uuid.UUID, category model.Category, amount string) model.SpendingRow {
	return model.SpendingRow{Month: month, AreaID: area, ProjectID: project, Spending: spend(category, amount)}
}

// newID is the id of an area or a project, which are only ids to the model.
func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// spendingLine writes what was spent on a category as "owner category amount", with
// where the category is when it is a subcategory.
func spendingLine(names map[uuid.UUID]string, owner uuid.UUID, s model.Spending) string {
	line := fmt.Sprintf("%s %s %s", names[owner], names[s.CategoryID], s.Amount)
	if s.ParentID != nil {
		line += " (under " + names[*s.ParentID] + ")"
	}
	return line
}

// areaLines writes what was spent in the areas, one to a line: "work food 150.75",
// with " in a project" when the expenses belong to one.
func areaLines(names map[uuid.UUID]string, spending []model.AreaSpending) []string {
	lines := make([]string, len(spending))
	for i, s := range spending {
		lines[i] = spendingLine(names, s.AreaID, s.Spending)
		if s.InProject {
			lines[i] += " in a project"
		}
	}
	return lines
}

// projectLines writes what was spent in the projects, one to a line: "trip food 11".
func projectLines(names map[uuid.UUID]string, spending []model.ProjectSpending) []string {
	lines := make([]string, len(spending))
	for i, s := range spending {
		lines[i] = spendingLine(names, s.ProjectID, s.Spending)
	}
	return lines
}

// topLevelLines writes the top-level categories, one to a line, in the order they
// come: "food 220 (own 100): dinner 70, lunch 50".
func topLevelLines(names map[uuid.UUID]string, topLevel []model.TopLevelSpending) []string {
	lines := make([]string, len(topLevel))
	for i, t := range topLevel {
		lines[i] = fmt.Sprintf("%s %s (own %s)", names[t.CategoryID], t.Total, t.Own)
		for j, c := range t.Children {
			sep := ": "
			if j > 0 {
				sep = ", "
			}
			lines[i] += fmt.Sprintf("%s%s %s", sep, names[c.CategoryID], c.Amount)
		}
	}
	return lines
}

// wantLinesInAnyOrder fails the test unless the lines are the ones wanted, in any
// order.
func wantLinesInAnyOrder(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestTotalsByMonthAddsUpTheRowsOfEachMonthAndFillsTheMonthsThatHaveNoneWithZero(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	work, trip := newID(), newID()
	rows := []model.SpendingRow{
		rowOf(on(2026, 11, 1), nil, nil, food, "100.5"),
		rowOf(on(2026, 11, 1), &work, nil, lunch, "20"), // whatever the area and the project of the expenses are
		rowOf(on(2026, 11, 1), &work, &trip, rent, "5000"),
		rowOf(on(2027, 1, 1), nil, &trip, food, "70"),
		rowOf(on(2027, 1, 1), nil, nil, food, "0.25"), // a category with two rows is added up as well
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
	rows := []model.SpendingRow{
		rowOf(on(2026, 10, 1), nil, nil, food, "1"),
		rowOf(on(2026, 11, 1), nil, nil, food, "10"),
		rowOf(on(2026, 12, 1), nil, nil, food, "100"),
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

func TestAreaSpendingOfAMonthAddsUpTheRowsOfEachAreaAndCategoryAndKeepsWhatBelongsToAProjectApart(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	work, home, trip, move := newID(), newID(), newID(), newID()
	names := map[uuid.UUID]string{work: "work", home: "home", trip: "trip", move: "move", food.ID: "food", lunch.ID: "lunch"}
	november := on(2026, 11, 1)
	rows := []model.SpendingRow{
		rowOf(november, &work, nil, food, "120.5"),
		rowOf(november, &work, nil, food, "30.25"), // a second row of the same area and category: added up
		rowOf(november, &work, nil, lunch, "80"),
		rowOf(november, &work, &trip, food, "700"), // in the area and in a project: apart from the rest
		rowOf(november, &work, &move, food, "20"),  // in another project: the same, added up with the trip's
		rowOf(november, &home, nil, food, "55"),
	}

	got := model.AreaSpendingOfMonth(on(2026, 11, 17), rows)

	wantLinesInAnyOrder(t, "what the areas spent", areaLines(names, got),
		"work food 150.75",
		"work lunch 80 (under food)",
		"work food 720 in a project",
		"home food 55",
	)
}

func TestAreaSpendingOfAMonthHasNothingThatBelongsToNoAreaAndNothingOfOtherMonths(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	work, trip := newID(), newID()
	names := map[uuid.UUID]string{work: "work", trip: "trip", food.ID: "food"}
	rows := []model.SpendingRow{
		rowOf(on(2026, 11, 1), nil, nil, food, "999"),   // in no area
		rowOf(on(2026, 11, 1), nil, &trip, food, "998"), // in a project that has no area: in no area either
		rowOf(on(2026, 10, 1), &work, nil, food, "5000"),
		rowOf(on(2026, 12, 1), &work, &trip, food, "7000"),
		rowOf(on(2026, 11, 1), &work, nil, food, "1"),
	}

	wantLinesInAnyOrder(t, "November", areaLines(names, model.AreaSpendingOfMonth(on(2026, 11, 1), rows)), "work food 1")
	// The month is the one the time falls on as its own location reads it: 00:30 on the 1st of
	// December in Taipei is the 30th of November in UTC.
	wantLinesInAnyOrder(t, "December", areaLines(names, model.AreaSpendingOfMonth(time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), rows)),
		"work food 7000 in a project")
	if got := model.AreaSpendingOfMonth(on(2027, 1, 1), rows); len(got) != 0 {
		t.Errorf("area spending of a month with no row = %+v, want none", got)
	}
	if got := model.AreaSpendingOfMonth(on(2026, 11, 1), nil); len(got) != 0 {
		t.Errorf("area spending of no rows = %+v, want none", got)
	}
}

func TestProjectSpendingAddsUpTheRowsOfEachProjectAndCategoryWhateverTheMonthAndTheAreaAndLeavesOutWhatHasNoProject(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	work, home, trip, move := newID(), newID(), newID(), newID()
	names := map[uuid.UUID]string{work: "work", home: "home", trip: "trip", move: "move", food.ID: "food", lunch.ID: "lunch"}
	rows := []model.SpendingRow{
		rowOf(on(2025, 3, 1), nil, &trip, food, "1"),     // long ago, in no area
		rowOf(on(2026, 11, 1), &work, &trip, food, "10"), // in an area too
		rowOf(on(2026, 11, 1), &home, &trip, food, "5"),  // in another area: the same project's, added up
		rowOf(on(2026, 12, 1), &home, &trip, lunch, "100"),
		rowOf(on(2026, 11, 1), nil, &move, food, "1000"),
		rowOf(on(2026, 11, 1), &work, nil, food, "9999"), // in no project: in none
		rowOf(on(2026, 11, 1), nil, nil, food, "88888"),
	}

	got := model.ProjectSpendingOf(rows)

	wantLinesInAnyOrder(t, "what the projects spent", projectLines(names, got),
		"trip food 16",
		"trip lunch 100 (under food)",
		"move food 1000",
	)
	if got := model.ProjectSpendingOf(nil); len(got) != 0 {
		t.Errorf("project spending of no rows = %+v, want none", got)
	}
}

func TestTopLevelSpendingHasASubcategoryUnderItsParentWhetherOrNotAnythingWasSpentOnTheParent(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	dinner := subcategory(t, food, "晚餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	transport := topLevel(t, model.DirectionExpense, "交通")
	taxi := subcategory(t, transport, "計程車")
	names := map[uuid.UUID]string{
		food.ID: "飲食", lunch.ID: "午餐", dinner.ID: "晚餐", rent.ID: "房租", transport.ID: "交通", taxi.ID: "計程車",
	}
	november := on(2026, 11, 1)
	rows := []model.SpendingRow{
		rowOf(november, nil, nil, food, "100"), // spent on the parent, and on two of its subcategories
		rowOf(november, nil, nil, lunch, "50"),
		rowOf(november, nil, nil, dinner, "70"),
		rowOf(november, nil, nil, taxi, "40"),   // only on a subcategory: the parent has none of its own
		rowOf(november, nil, nil, rent, "5000"), // only on the parent
	}

	got, total := model.TopLevelSpendingOfMonth(on(2026, 11, 17), rows)

	want := []string{
		"房租 5000 (own 5000)",
		"飲食 220 (own 100): 晚餐 70, 午餐 50",
		"交通 40 (own 0): 計程車 40",
	}
	if lines := topLevelLines(names, got); !slices.Equal(lines, want) {
		t.Errorf("top-level spending:\n got %q\nwant %q", lines, want)
	}
	if !total.Equal(dec("5260")) {
		t.Errorf("total = %s, want 5260: the top-level totals added up", total)
	}
	// A subcategory is a child of its parent and has no row of its own at the top.
	for _, c := range got {
		if c.CategoryID == lunch.ID || c.CategoryID == dinner.ID || c.CategoryID == taxi.ID {
			t.Errorf("%s is at the top, want it under its parent", names[c.CategoryID])
		}
	}
}

func TestTopLevelSpendingIsLargestTotalFirstWhateverTheOwnAmountAndTheChildrenLargestFirstThenByIdAndTheTotalIsWhatTheyAddUpTo(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rent := topLevel(t, model.DirectionExpense, "房租")
	fun := topLevel(t, model.DirectionExpense, "娛樂")
	fees := topLevel(t, model.DirectionExpense, "手續費")
	subs := []model.Category{
		subcategory(t, food, "午餐"), subcategory(t, food, "晚餐"), subcategory(t, food, "宵夜"), subcategory(t, food, "早餐"),
	}
	// Two with the same total, and three subcategories with the same amount, which only their ids put in
	// order: given in an order that is not that of their ids.
	same := []model.Category{fun, fees}
	slices.SortFunc(same, func(a, b model.Category) int { return cmpIDs(a.ID, b.ID) })
	sameKids := []model.Category{subs[1], subs[2], subs[3]}
	slices.SortFunc(sameKids, func(a, b model.Category) int { return cmpIDs(a.ID, b.ID) })
	november := on(2026, 11, 1)
	rows := []model.SpendingRow{
		rowOf(november, nil, nil, sameKids[2], "30"),
		rowOf(november, nil, nil, sameKids[0], "30"),
		rowOf(november, nil, nil, sameKids[1], "30"),
		rowOf(november, nil, nil, subs[0], "80"),
		rowOf(november, nil, nil, food, "10"), // food: 10 of its own and 170 under it
		rowOf(november, nil, nil, rent, "105"),
		rowOf(november, nil, nil, same[1], "50"),
		rowOf(november, nil, nil, same[0], "50"),
	}

	got, total := model.TopLevelSpendingOfMonth(november, rows)

	// Food has the most in all, though the rent is more than its own 10; for the same total, by id.
	wantOrder := []uuid.UUID{food.ID, rent.ID, same[0].ID, same[1].ID}
	if len(got) != len(wantOrder) {
		t.Fatalf("%d top-level categories, want %d", len(got), len(wantOrder))
	}
	for i, id := range wantOrder {
		if got[i].CategoryID != id {
			t.Errorf("top-level category %d is %s, want %s: the largest total first, and for the same total by id", i+1, got[i].CategoryID, id)
		}
	}
	wantKids := []uuid.UUID{subs[0].ID, sameKids[0].ID, sameKids[1].ID, sameKids[2].ID}
	if len(got[0].Children) != len(wantKids) {
		t.Fatalf("food has %d children, want %d", len(got[0].Children), len(wantKids))
	}
	for i, id := range wantKids {
		if got[0].Children[i].CategoryID != id {
			t.Errorf("child %d of food is %s, want %s: the largest first, and for the same amount by id", i+1, got[0].Children[i].CategoryID, id)
		}
	}
	if !got[0].Total.Equal(dec("180")) || !got[0].Own.Equal(dec("10")) {
		t.Errorf("food: total %s, own %s, want 180 and 10", got[0].Total, got[0].Own)
	}
	if len(got[1].Children) != 0 {
		t.Errorf("rent has the children %+v, want none: nothing was spent under it", got[1].Children)
	}
	sum := dec("0")
	for _, c := range got {
		sum = sum.Add(c.Total)
	}
	if !total.Equal(dec("385")) || !total.Equal(sum) {
		t.Errorf("total = %s, the top-level totals add up to %s, want both 385", total, sum)
	}
}

func TestTopLevelSpendingAddsUpTheRowsOfOneCategoryWhateverTheirAreaAndProjectAndLeavesOutOtherMonths(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	work, trip := newID(), newID()
	names := map[uuid.UUID]string{food.ID: "飲食", lunch.ID: "午餐"}
	rows := []model.SpendingRow{
		rowOf(on(2026, 11, 1), nil, nil, food, "10.10"), // a row for each area and project the category was spent in
		rowOf(on(2026, 11, 1), &work, nil, food, "0.20"),
		rowOf(on(2026, 11, 1), &work, &trip, food, "5"),
		rowOf(on(2026, 11, 1), nil, &trip, lunch, "1"),
		rowOf(on(2026, 11, 1), &work, nil, lunch, "2"),
		rowOf(on(2026, 10, 1), nil, nil, food, "9999"), // another month
	}

	got, total := model.TopLevelSpendingOfMonth(on(2026, 11, 1), rows)

	if want := []string{"飲食 18.3 (own 15.3): 午餐 3"}; !slices.Equal(topLevelLines(names, got), want) {
		t.Errorf("top-level spending = %q, want %q", topLevelLines(names, got), want)
	}
	if !total.Equal(dec("18.3")) {
		t.Errorf("total = %s, want 18.3: the rows of November, and not those of October", total)
	}
}

func TestTopLevelSpendingOfAMonthWithNoRowIsNothingAndTakesTheMonthAsItsOwnLocationReadsIt(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	rows := []model.SpendingRow{rowOf(on(2026, 11, 1), nil, nil, food, "10")}

	if got, total := model.TopLevelSpendingOfMonth(on(2026, 12, 1), rows); len(got) != 0 || !total.IsZero() {
		t.Errorf("spending of a month with no row = %+v, total %s, want none and zero", got, total)
	}
	if got, total := model.TopLevelSpendingOfMonth(on(2026, 11, 1), nil); len(got) != 0 || !total.IsZero() {
		t.Errorf("spending of no rows = %+v, total %s, want none and zero", got, total)
	}
	// 00:30 on the 1st of December in Taipei is the 30th of November in UTC: the month is December, as the
	// zone of the time reads it.
	if got, _ := model.TopLevelSpendingOfMonth(time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), rows); len(got) != 0 {
		t.Errorf("spending of the month a Taipei time falls on = %+v, want none of November's", got)
	}
	if got, _ := model.TopLevelSpendingOfMonth(time.Date(2026, 11, 30, 23, 0, 0, 0, taipei), rows); len(got) != 1 {
		t.Errorf("spending of November through a Taipei time = %+v, want the row", got)
	}
}

// What is spent in a month is read three ways from the same rows: by area, by category
// and as a total. The three say the same: what the areas spent, projects included, and
// what no area has, is what the categories add up to, and what the month's total is.
func TestWhatTheAreasSpentPlusWhatNoAreaHasIsWhatTheCategoriesAndTheMonthTotalAddUpTo(t *testing.T) {
	food := topLevel(t, model.DirectionExpense, "飲食")
	lunch := subcategory(t, food, "午餐")
	rent := topLevel(t, model.DirectionExpense, "房租")
	work, home, trip, move := newID(), newID(), newID(), newID()
	november := on(2026, 11, 1)
	rows := []model.SpendingRow{
		rowOf(november, &work, nil, food, "100"),
		rowOf(november, &work, &trip, food, "700"), // in an area and a project
		rowOf(november, &home, nil, rent, "55"),
		rowOf(november, &work, nil, lunch, "7"),
		rowOf(november, nil, nil, food, "999"),   // in no area
		rowOf(november, nil, &move, rent, "998"), // in a project of no area
		rowOf(on(2026, 10, 1), &work, nil, food, "5000"),
	}

	inAreas := dec("0")
	for _, s := range model.AreaSpendingOfMonth(november, rows) {
		inAreas = inAreas.Add(s.Amount)
	}
	noArea := dec("0")
	for _, r := range rows {
		if r.AreaID == nil {
			noArea = noArea.Add(r.Amount)
		}
	}
	byCategory, total := model.TopLevelSpendingOfMonth(november, rows)
	sum := dec("0")
	for _, c := range byCategory {
		sum = sum.Add(c.Total)
	}
	monthly := model.TotalsByMonth(november, november, rows)

	if !inAreas.Equal(dec("862")) || !noArea.Equal(dec("1997")) {
		t.Fatalf("the areas spent %s and no area has %s, want 862 and 1997", inAreas, noArea)
	}
	for what, got := range map[string]string{
		"the total of the categories":        total.String(),
		"the categories added up":            sum.String(),
		"the total of the month in a series": monthly[0].Total.String(),
	} {
		if got != inAreas.Add(noArea).String() {
			t.Errorf("%s is %s, want what the areas spent and no area has, %s", what, got, inAreas.Add(noArea))
		}
	}
}

func cmpIDs(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }
