package model

import (
	"bytes"
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// What was spent has one definition, whatever it is read by: the base amount of
// the expense postings (see Spending) of the confirmed transactions that happened
// on a day that is not after today, of every kind of transaction. The reads differ
// in what they group it by, an area, a project or a month, and never in what they
// count; the days that count for the months a read is about are SpendingRange's,
// and the month an expense belongs to is the month of the day it happened on
// (MonthOf).

// MonthCategorySpending is what was spent on a category in a month: Spending, for
// the transactions that happened in the month, whether they belong to an area or a
// project or to neither.
type MonthCategorySpending struct {
	// Month is the first day of the month, as a date at midnight UTC (see MonthOf).
	Month time.Time
	Spending
}

// MonthTotal is what was spent in a month, all categories together, in the base
// currency.
type MonthTotal struct {
	// Month is the first day of the month, as a date at midnight UTC (see MonthOf).
	Month time.Time
	Total decimal.Decimal
}

// SpendingRange returns the days that count as spent by today, for the months from
// the one first falls on to the one last falls on: from the first day of the first
// month through the last day of the last month or today, whichever comes first, both
// days included. What is dated after today counts from its day, so the days after
// it are not asked for. ok is false when no day counts yet, because the first month
// has not begun: there is nothing to ask for. today is a date as DateOf makes it.
//
// It is the one place that says how far what was spent is read, for the budgets of
// an area, for one month's spending and for the trend of several.
func SpendingRange(first, last, today time.Time) (from, through time.Time, ok bool) {
	from = MonthOf(first)
	through = LastDayOf(last)
	if today.Before(through) {
		through = today
	}
	return from, through, !through.Before(from)
}

// CountMonths returns how many months there are from the month first falls on to
// the month last falls on, both counted, each as its own location reads it: 1 when
// they are the same month, 0 when last falls in an earlier month than first.
func CountMonths(first, last time.Time) int {
	n := (last.Year()-first.Year())*12 + int(last.Month()) - int(first.Month()) + 1
	return max(n, 0)
}

// MonthsBetween returns the months from the one first falls on to the one last falls
// on, both included, oldest first, each as its first day at midnight UTC (see
// MonthOf). There are none when last falls in an earlier month than first.
func MonthsBetween(first, last time.Time) []time.Time {
	months := make([]time.Time, CountMonths(first, last))
	start := MonthOf(first)
	for i := range months {
		months[i] = start.AddDate(0, i, 0)
	}
	return months
}

// TotalsByMonth adds up the rows by their month, for each month from the one first
// falls on to the one last falls on, both included, oldest first. A month with no
// row has a total of zero, so that no month of the range is left out; a row of a
// month outside the range is in none of them. There are no totals when last falls in
// an earlier month than first.
func TotalsByMonth(first, last time.Time, rows []MonthCategorySpending) []MonthTotal {
	months := MonthsBetween(first, last)
	totals := make([]MonthTotal, len(months))
	for i, month := range months {
		totals[i].Month = month
	}
	for _, row := range rows {
		if i := CountMonths(first, row.Month) - 1; i >= 0 && i < len(totals) {
			totals[i].Total = totals[i].Total.Add(row.Amount)
		}
	}
	return totals
}

// SpendingOfMonth adds up what the rows say was spent on each category in the month
// the time falls on, and returns the categories with what was spent on each, the
// largest first and, for the same amount, by category id, with what was spent in all.
// A category may have several rows, which are added up; the rows of other months are
// not counted, and a category that has no row in the month is not in the result.
func SpendingOfMonth(month time.Time, rows []MonthCategorySpending) ([]Spending, decimal.Decimal) {
	var (
		categories []Spending
		index      = make(map[uuid.UUID]int) // category id to its place in categories
		total      decimal.Decimal
	)
	for _, row := range rows {
		if !sameMonth(month, row.Month) {
			continue
		}
		i, seen := index[row.CategoryID]
		if !seen {
			i = len(categories)
			index[row.CategoryID] = i
			categories = append(categories, Spending{CategoryID: row.CategoryID, ParentID: row.ParentID})
		}
		categories[i].Amount = categories[i].Amount.Add(row.Amount)
		total = total.Add(row.Amount)
	}
	slices.SortFunc(categories, func(a, b Spending) int {
		return cmp.Or(b.Amount.Cmp(a.Amount), bytes.Compare(a.CategoryID[:], b.CategoryID[:]))
	})
	return categories, total
}

// sameMonth reports whether the two times fall on the same month, each as its own
// location reads it.
func sameMonth(a, b time.Time) bool {
	ay, am, _ := a.Date()
	by, bm, _ := b.Date()
	return ay == by && am == bm
}
