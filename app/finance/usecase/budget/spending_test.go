package budget_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// What these tests are about is what the use cases ask of the repo and what they make of
// the rows it gives: the repo is a fake that gives the rows a test sets, whatever it is
// asked, so what counts as spent, which days a statement takes and how it groups them are
// not tested here but with the Postgres repo, and what is made of the rows is the model's,
// which has its own tests. The tests here say which days are asked for (the clock, and
// the one place that works out how far spending is read), that the repo is asked only
// when there is something to ask, that its error goes on, and that the use cases put
// what they get in the shape of their output.

// spending reads what was spent in the month, and fails the test when it cannot.
func (e *env) spending(month time.Time) usecase.GetSpendingOutput {
	e.t.Helper()
	out, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{Month: month})
	if err != nil {
		e.t.Fatalf("GetSpending(%s): %v", month.Format("2006-01"), err)
	}
	return out
}

// topLevelLines writes the top-level categories of what was spent one to a line, in the
// order they come: "food 500 (own 300): lunch 200, dinner 0", the subcategories in the
// order they come too.
func (e *env) topLevelLines(out usecase.GetSpendingOutput) []string {
	names := e.categories.categories
	lines := make([]string, len(out.TopLevel))
	for i, c := range out.TopLevel {
		lines[i] = fmt.Sprintf("%s %s (own %s)", names[c.CategoryID].Name, c.Total, c.Own)
		for j, child := range c.Children {
			sep := ", "
			if j == 0 {
				sep = ": "
			}
			lines[i] += fmt.Sprintf("%s%s %s", sep, names[child.CategoryID].Name, child.Amount)
		}
	}
	return lines
}

// trend reads what was spent in each month from the first to the last, and fails
// the test when it cannot.
func (e *env) trend(from, to time.Time) usecase.GetSpendingTrendOutput {
	e.t.Helper()
	out, err := e.svc.GetSpendingTrend(context.Background(), usecase.GetSpendingTrendInput{From: from, To: to})
	if err != nil {
		e.t.Fatalf("GetSpendingTrend(%s, %s): %v", from.Format("2006-01"), to.Format("2006-01"), err)
	}
	return out
}

// trendLines writes the months of a trend one to a line, "2026-11 150.5".
func trendLines(out usecase.GetSpendingTrendOutput) []string {
	lines := make([]string, len(out.Months))
	for i, m := range out.Months {
		lines[i] = fmt.Sprintf("%s %s", m.Month.Format("2006-01"), m.Total)
	}
	return lines
}

func wantLines(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// ---- GetSpending ----

func TestGetSpendingNeedsAMonthAndNamesItByTheFirstDayOfTheMonthTheRequestFallsIn(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{})
	if !errors.Is(err, model.ErrMonthRequired) {
		t.Errorf("err = %v, want ErrMonthRequired", err)
	}
	if len(e.budgets.asked) != 0 {
		t.Errorf("the repo was asked %v, want it not asked when there is no month", e.asked())
	}

	for _, month := range []time.Time{day(2026, 11, 1), day(2026, 11, 30), time.Date(2026, 11, 30, 23, 0, 0, 0, taipei)} {
		out := e.spending(month)
		if !out.Month.Equal(day(2026, 11, 1)) || out.Month.Location() != time.UTC {
			t.Errorf("month = %s for %s, want 2026-11-01 as a date at midnight UTC", out.Month, month)
		}
	}
	// 00:30 on the 1st of December in Taipei is the 30th of November in UTC: the month is
	// the one the time falls on as its own location reads it.
	if out := e.spending(time.Date(2026, 12, 1, 0, 30, 0, 0, taipei)); !out.Month.Equal(day(2026, 12, 1)) {
		t.Errorf("month = %s, want December: the month a Taipei time falls on", out.Month)
	}
}

func TestAMonthIsAskedForFromItsFirstDayToItsLastOrToTodayInTheTimezoneOfTheClockWhicheverComesFirst(t *testing.T) {
	e := newEnv(t)

	tests := []struct {
		name string
		now  time.Time
		want []string // none for a month that has not begun
	}{
		{"before the month", time.Date(2026, 10, 31, 12, 0, 0, 0, taipei), nil},
		{"the first day of the month", time.Date(2026, 11, 1, 0, 0, 0, 0, taipei), []string{"2026-11-01..2026-11-01"}},
		{"in the middle of it", time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), []string{"2026-11-01..2026-11-14"}},
		// It is still the 14th in UTC then: the day is the one in the timezone of the clock.
		{"just after midnight in Taipei, the day after", time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), []string{"2026-11-01..2026-11-15"}},
		{"the last day of the month", time.Date(2026, 11, 30, 18, 0, 0, 0, taipei), []string{"2026-11-01..2026-11-30"}},
		{"the first day of the next month: its expenses are not this month's", time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), []string{"2026-11-01..2026-11-30"}},
		{"long after", time.Date(2027, 6, 1, 9, 0, 0, 0, taipei), []string{"2026-11-01..2026-11-30"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.now = tt.now
			e.budgets.asked = nil

			e.spending(day(2026, 11, 15)) // any day of the month will do

			e.wantAsked(tt.want...)
		})
	}
}

func TestAMonthThatHasNotBegunHasNothingAndTheRepoIsNotAsked(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 12, 3), nil, nil, food, "500") // what the repo holds, and would give if it were asked

	out := e.spending(day(2026, 12, 20))

	if !out.Month.Equal(day(2026, 12, 1)) || !out.Total.IsZero() || len(out.TopLevel) != 0 {
		t.Errorf("spending = %+v, want December with no total and no category: it has not begun", out)
	}
	e.wantAsked()

	// Once it has begun it is asked for, up to the day it is.
	e.clock.setDay(2026, time.December, 3)
	out = e.spending(day(2026, 12, 20))
	wantMoney(t, "total on the 3rd", out.Total, "500")
	e.wantAsked("2026-12-01..2026-12-03")
}

func TestSpendingIsByTopLevelCategoryWithTheSubcategoriesUnderItWhetherOrNotAnythingWasSpentOnTheParent(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	dinner := e.category("晚餐", &food)
	rent := e.category("房租", nil)
	transport := e.category("交通", nil)
	taxi := e.category("計程車", &transport)
	e.category("娛樂", nil) // nothing is spent on it, and it is not in the output
	e.spend(day(2026, 11, 2), nil, nil, food, "300")
	e.spend(day(2026, 11, 2), nil, nil, lunch, "200")
	e.spend(day(2026, 11, 3), &work, &trip, lunch, "10") // lunch again, in an area and a project: added up
	e.spend(day(2026, 11, 4), nil, nil, rent, "5000")
	e.spend(day(2026, 11, 5), &work, nil, taxi, "40") // only on a subcategory: its parent has none of its own
	e.spend(day(2026, 11, 6), nil, nil, dinner, "220")

	out := e.spending(day(2026, 11, 1))

	wantLines(t, "top-level categories", e.topLevelLines(out),
		"房租 5000 (own 5000)",
		"飲食 730 (own 300): 晚餐 220, 午餐 210",
		"交通 40 (own 0): 計程車 40",
	)
	wantMoney(t, "total", out.Total, "5770")
}

func TestSpendingIsListedLargestTotalFirstAndForTheSameTotalByCategoryIdAndTheTotalIsWhatTheyAddUpTo(t *testing.T) {
	e := newEnv(t)
	categories := []model.Category{
		e.category("飲食", nil), e.category("房租", nil), e.category("娛樂", nil), e.category("交通", nil), e.category("手續費", nil),
	}
	lunch := e.category("午餐", &categories[0])
	// Three of them are spent the same on, in an order that is not that of their ids, so
	// that only the rule can put them in order.
	same := []model.Category{categories[2], categories[3], categories[4]}
	slices.SortFunc(same, func(a, b model.Category) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	e.spend(day(2026, 11, 2), nil, nil, same[2], "50.50")
	e.spend(day(2026, 11, 2), nil, nil, same[0], "50.5")
	e.spend(day(2026, 11, 3), nil, nil, same[1], "50.50")
	e.spend(day(2026, 11, 3), nil, nil, categories[0], "5000")
	e.spend(day(2026, 11, 3), nil, nil, lunch, "0.01")
	e.spend(day(2026, 11, 4), nil, nil, categories[1], "99999.99")
	e.spend(day(2026, 11, 5), nil, nil, categories[0], "0.01")

	out := e.spending(day(2026, 11, 1))

	want := []model.Category{categories[1], categories[0], same[0], same[1], same[2]}
	if len(out.TopLevel) != len(want) {
		t.Fatalf("top-level categories = %v, want %d", e.topLevelLines(out), len(want))
	}
	for i, c := range want {
		if out.TopLevel[i].CategoryID != c.ID {
			t.Errorf("category %d is %s, want %s: the largest total first, and for the same total by id", i+1, e.categories.categories[out.TopLevel[i].CategoryID].Name, c.Name)
		}
	}
	wantMoney(t, "the largest", out.TopLevel[0].Total, "99999.99")
	wantMoney(t, "food: two expenses of it and one of lunch added up", out.TopLevel[1].Total, "5000.02")
	wantMoney(t, "food: what was spent on food itself", out.TopLevel[1].Own, "5000.01")
	wantMoney(t, "total", out.Total, "105151.51")
	sum := decimal.Zero
	for _, c := range out.TopLevel {
		sum = sum.Add(c.Total)
	}
	wantMoney(t, "the total is what the top-level categories add up to", out.Total, sum.String())
}

func TestSpendingOfAMonthWithNothingSpentIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)

	out := e.spending(day(2026, 11, 1))

	if !out.Month.Equal(day(2026, 11, 1)) || !out.Total.IsZero() || len(out.TopLevel) != 0 {
		t.Errorf("spending = %+v, want November with nothing in it", out)
	}
	e.wantAsked("2026-11-01..2026-11-14")
}

func TestWhenTheRepoFailsWhatWasSpentIsNotMadeUp(t *testing.T) {
	boom := errors.New("the database is down")
	e := newEnv(t)
	e.budgets.spendingErr = boom

	if _, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{Month: day(2026, 11, 1)}); !errors.Is(err, boom) {
		t.Errorf("GetSpending: err = %v, want what the repo returned", err)
	}
	_, err := e.svc.GetSpendingTrend(context.Background(), usecase.GetSpendingTrendInput{From: day(2026, 10, 1), To: day(2026, 11, 1)})
	if !errors.Is(err, boom) {
		t.Errorf("GetSpendingTrend: err = %v, want what the repo returned", err)
	}
	_, err = e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: day(2026, 11, 1)})
	if !errors.Is(err, boom) {
		t.Errorf("GetAreaBudgets: err = %v, want what the repo returned", err)
	}
	_, err = e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{})
	if !errors.Is(err, boom) {
		t.Errorf("GetProjectBudgets: err = %v, want what the repo returned", err)
	}
}

// What was spent is what the areas spent in a month, projects included, and the
// expenses that have no area: no area counts them, and the month has them. The areas
// and the month are read from the same rows.
func TestWhatWasSpentInAMonthIsWhatTheAreasSpentWithTheirProjectsPlusTheExpensesOfNoArea(t *testing.T) {
	e := newEnv(t)
	work, home := e.area("工作", false), e.area("家庭", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	rent := e.category("房租", nil)
	e.setArea(work, day(2026, 11, 1), "30000")
	e.spend(day(2026, 11, 2), &work, nil, food, "100")
	e.spend(day(2026, 11, 3), &work, &trip, food, "700") // in the area, and in a project
	e.spend(day(2026, 11, 4), &home, nil, rent, "55")
	e.spend(day(2026, 11, 5), nil, nil, food, "999")   // no area, no project
	e.spend(day(2026, 11, 6), nil, &trip, rent, "998") // a project that has no area

	inAreas := decimal.Zero
	for _, status := range e.areas(day(2026, 11, 1)) {
		inAreas = inAreas.Add(status.SpentWithProjects)
	}
	noArea := dec("999").Add(dec("998"))

	out := e.spending(day(2026, 11, 1))

	wantMoney(t, "what the areas spent, projects included", inAreas, "855")
	wantMoney(t, "spending is what the areas spent and what no area has", out.Total, inAreas.Add(noArea).String())
	wantMoney(t, "spending, as it is written down", out.Total, "2852")
	wantLines(t, "top-level categories", e.topLevelLines(out), "飲食 1799 (own 1799)", "房租 1053 (own 1053)")
}

// ---- GetSpendingTrend ----

func TestTheTrendHasAMonthForEachMonthOfTheRangeOldestFirstWithZeroForThoseWithNothingAcrossAYear(t *testing.T) {
	e := newEnv(t)
	e.clock.setDay(2027, time.February, 10)
	work := e.area("工作", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	e.spend(day(2026, 10, 1), nil, nil, food, "100")     // the first day of the first month
	e.spend(day(2026, 12, 31), &work, nil, lunch, "200") // the last day of the year
	e.spend(day(2026, 12, 5), nil, &trip, food, "50")    // whatever the area and the project are, it is a month's
	e.spend(day(2027, 1, 1), &work, &trip, rent, "5000")

	// Given by any day of the first and the last month.
	out := e.trend(day(2026, 10, 20), day(2027, 1, 3))

	wantLines(t, "months", trendLines(out), "2026-10 100", "2026-11 0", "2026-12 250", "2027-01 5000")
	for i, m := range out.Months {
		if m.Month.Day() != 1 || m.Month.Location() != time.UTC || (i > 0 && !m.Month.After(out.Months[i-1].Month)) {
			t.Errorf("month %d is %s, want the first day of a month at midnight UTC, each after the one before", i+1, m.Month)
		}
	}
}

func TestTheTrendAsksTheRepoOnceForTheWholeRangeToTheLastDayOfItsLastMonthOrToday(t *testing.T) {
	e := newEnv(t)

	tests := []struct {
		name       string
		today      time.Time
		from, to   time.Time
		wantRanges []string
		wantMonths int
	}{
		{"months that are over", day(2026, 11, 14), day(2026, 3, 9), day(2026, 6, 20), []string{"2026-03-01..2026-06-30"}, 4},
		{"up to the current month, which is partial", day(2026, 11, 14), day(2026, 9, 1), day(2026, 11, 30), []string{"2026-09-01..2026-11-14"}, 3},
		{"a range that goes on past today", day(2026, 11, 14), day(2026, 10, 1), day(2027, 2, 1), []string{"2026-10-01..2026-11-14"}, 5},
		{"across a year, in the past", day(2027, 5, 1), day(2026, 11, 1), day(2027, 2, 1), []string{"2026-11-01..2027-02-28"}, 4},
		{"across a leap February", day(2028, 6, 1), day(2028, 1, 15), day(2028, 2, 15), []string{"2028-01-01..2028-02-29"}, 2},
		{"one month", day(2026, 11, 14), day(2026, 11, 30), day(2026, 11, 1), []string{"2026-11-01..2026-11-14"}, 1},
		{"a range that has not begun", day(2026, 11, 14), day(2026, 12, 1), day(2027, 3, 1), nil, 4},
		{"a range that begins tomorrow", day(2026, 11, 30), day(2026, 12, 1), day(2027, 3, 1), nil, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.setDay(tt.today.Year(), tt.today.Month(), tt.today.Day())
			e.budgets.asked = nil

			out := e.trend(tt.from, tt.to)

			e.wantAsked(tt.wantRanges...)
			if len(out.Months) != tt.wantMonths {
				t.Errorf("%d months = %v, want %d: every month of the range, asked for or not", len(out.Months), trendLines(out), tt.wantMonths)
			}
		})
	}
}

func TestTheTrendOfAPartialCurrentMonthIsAskedForUpToTodayAndTheMonthsToComeAreZero(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 9, 10), nil, nil, food, "100")
	e.spend(day(2026, 11, 14), nil, nil, food, "10") // up to today, which is what the repo is asked for

	out := e.trend(day(2026, 9, 1), day(2026, 12, 1))

	wantLines(t, "months", trendLines(out), "2026-09 100", "2026-10 0", "2026-11 10", "2026-12 0")
	e.wantAsked("2026-09-01..2026-11-14")

	// Once the days have come, they are asked for.
	e.clock.setDay(2026, time.December, 3)
	e.budgets.asked = nil
	e.trend(day(2026, 9, 1), day(2026, 12, 1))
	e.wantAsked("2026-09-01..2026-12-03")
}

func TestTheTrendOfARangeThatHasNotBegunHasEveryMonthWithZeroAndAsksNothing(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 12, 3), nil, nil, food, "1000") // what the repo holds, and would give if it were asked

	out := e.trend(day(2026, 12, 1), day(2027, 2, 28))

	wantLines(t, "months", trendLines(out), "2026-12 0", "2027-01 0", "2027-02 0")
	e.wantAsked()
}

func TestTheTrendRefusesARangeThatIsNotOneAndAsksNothing(t *testing.T) {
	tooWide := func(months int) usecase.GetSpendingTrendInput {
		from := day(2016, 11, 1)
		return usecase.GetSpendingTrendInput{From: from, To: from.AddDate(0, months-1, 0)}
	}
	tests := []struct {
		name string
		in   usecase.GetSpendingTrendInput
		want error
	}{
		{"no first month", usecase.GetSpendingTrendInput{To: day(2026, 11, 1)}, model.ErrMonthRequired},
		{"no last month", usecase.GetSpendingTrendInput{From: day(2026, 11, 1)}, model.ErrMonthRequired},
		{"neither", usecase.GetSpendingTrendInput{}, model.ErrMonthRequired},
		{"a last month before the first", usecase.GetSpendingTrendInput{From: day(2026, 11, 1), To: day(2026, 10, 31)}, model.ErrDateRangeReversed},
		{"a year before", usecase.GetSpendingTrendInput{From: day(2026, 11, 1), To: day(2025, 11, 1)}, model.ErrDateRangeReversed},
		{"one month more than the most", tooWide(usecase.MaxSpendingTrendMonths + 1), model.ErrSpendingRangeTooWide},
		{"as many months as a date can name", usecase.GetSpendingTrendInput{From: day(2, 1, 1), To: day(9999, 12, 31)}, model.ErrSpendingRangeTooWide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)

			_, err := e.svc.GetSpendingTrend(context.Background(), tt.in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(e.budgets.asked) != 0 {
				t.Errorf("the repo was asked %v, want nothing asked for a range that is refused", e.asked())
			}
		})
	}
}

func TestTheTrendTakesTheMostMonthsAndOneMonthAndTheDaysOfOneMonthInEitherOrder(t *testing.T) {
	e := newEnv(t)
	most := usecase.MaxSpendingTrendMonths

	from := day(2016, 12, 1)
	out := e.trend(from, from.AddDate(0, most-1, 0)) // November 2026 is the last of them
	if lines := trendLines(out); len(lines) != most || lines[0] != "2016-12 0" || lines[most-1] != "2026-11 0" {
		t.Errorf("trend of the most months = %d months, want %d, from 2016-12 to 2026-11", len(lines), most)
	}
	e.wantAsked("2016-12-01..2026-11-14")

	// The two ends in the same month are one month, whichever day comes first.
	for _, ends := range [][2]time.Time{{day(2026, 11, 1), day(2026, 11, 30)}, {day(2026, 11, 30), day(2026, 11, 1)}} {
		wantLines(t, "trend of "+ends[0].Format(time.DateOnly)+" to "+ends[1].Format(time.DateOnly), trendLines(e.trend(ends[0], ends[1])), "2026-11 0")
	}
}

func TestTheTrendAgreesWithTheSpendingOfEachMonthItHas(t *testing.T) {
	e := newEnv(t)
	e.clock.setDay(2027, time.January, 9)
	work := e.area("工作", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	for _, expense := range []struct {
		on       time.Time
		area     *uuid.UUID
		project  *uuid.UUID
		category model.Category
		amount   string
	}{
		{day(2026, 10, 3), &work, nil, food, "120.5"},
		{day(2026, 10, 30), nil, nil, lunch, "80"},
		{day(2026, 11, 1), &work, &trip, rent, "5000"},
		{day(2026, 11, 30), nil, &trip, food, "7.25"},
		{day(2026, 12, 15), nil, nil, food, "300"},
		{day(2027, 1, 9), &work, nil, lunch, "40"},
	} {
		e.spend(expense.on, expense.area, expense.project, expense.category, expense.amount)
	}

	out := e.trend(day(2026, 9, 1), day(2027, 2, 1))

	wantLines(t, "months", trendLines(out), "2026-09 0", "2026-10 200.5", "2026-11 5007.25", "2026-12 300", "2027-01 40", "2027-02 0")
	for _, m := range out.Months {
		if spending := e.spending(m.Month); !spending.Total.Equal(m.Total) {
			t.Errorf("%s: the trend says %s, the spending of the month says %s, want them the same", m.Month.Format("2006-01"), m.Total, spending.Total)
		}
	}
}

// ---- One definition of how far spending is read ----

// The range of days of a month was worked out inside GetAreaBudgets, and is now the
// one definition the reads of spending share: the budgets of the areas ask for what
// they always did, and the spending of the same month asks for the same days.
func TestTheBudgetsOfAreasAndTheSpendingOfTheSameMonthAskForTheSameDays(t *testing.T) {
	tests := []struct {
		name  string
		today time.Time
		month time.Time
		want  []string // none for a month that has not begun
	}{
		{"the current month", day(2026, 11, 14), day(2026, 11, 3), []string{"2026-11-01..2026-11-14"}},
		{"a month that is over", day(2026, 11, 14), day(2026, 10, 3), []string{"2026-10-01..2026-10-31"}},
		{"February of a leap year", day(2028, 6, 1), day(2028, 2, 3), []string{"2028-02-01..2028-02-29"}},
		{"the first day of the month is today", day(2026, 11, 1), day(2026, 11, 20), []string{"2026-11-01..2026-11-01"}},
		{"a month that has not begun", day(2026, 11, 14), day(2026, 12, 1), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.clock.setDay(tt.today.Year(), tt.today.Month(), tt.today.Day())

			e.areas(tt.month)
			e.wantAsked(tt.want...)

			e.budgets.asked = nil
			e.spending(tt.month)
			e.wantAsked(tt.want...)
		})
	}
}
