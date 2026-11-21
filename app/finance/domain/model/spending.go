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
// on a day that is not after today, of every kind of transaction. The repo adds
// them up once, for each month, area, project and category (SpendingRow); the reads
// of the areas, of the projects and of a month are what the functions below make of
// those rows, and never count anything of their own. The days that count for the
// months a read is about are SpendingRange's, and the month an expense belongs to is
// the month of the day it happened on (MonthOf).

// SpendingRow is what was spent on a category in a month, in an area and in a
// project: Spending, for the transactions that happened in the month and belong to
// the area and to the project.
type SpendingRow struct {
	// Month is the first day of the month, as a date at midnight UTC (see MonthOf).
	Month time.Time
	// AreaID and ProjectID are the area and the project the transactions belong to;
	// nil when they belong to none.
	AreaID, ProjectID *uuid.UUID
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
func TotalsByMonth(first, last time.Time, rows []SpendingRow) []MonthTotal {
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

// AreaSpendingOfMonth returns what the rows say was spent in the areas in the month
// the time falls on: for each area and category, and whether the transactions belong
// to a project, with the rows that have the same three added up, so that the
// transactions of every project of an area are one. What belongs to no area is in
// none of them, and the rows of other months are not counted. The result is in the
// order its entries first come in the rows.
func AreaSpendingOfMonth(month time.Time, rows []SpendingRow) []AreaSpending {
	type key struct {
		area, category uuid.UUID
		inProject      bool
	}
	var (
		spending []AreaSpending
		index    = make(map[key]int) // the place of each key in spending
	)
	for _, row := range rows {
		if row.AreaID == nil || !sameMonth(month, row.Month) {
			continue
		}
		k := key{*row.AreaID, row.CategoryID, row.ProjectID != nil}
		i, seen := index[k]
		if !seen {
			i = len(spending)
			index[k] = i
			spending = append(spending, AreaSpending{
				AreaID: k.area, InProject: k.inProject, Spending: Spending{CategoryID: k.category, ParentID: row.ParentID},
			})
		}
		spending[i].Amount = spending[i].Amount.Add(row.Amount)
	}
	return spending
}

// ProjectSpendingOf returns what the rows say was spent in the projects, whatever
// the month and the area: for each project and category, with the rows that have the
// same two added up. What belongs to no project is in none of them. The result is in
// the order its entries first come in the rows.
func ProjectSpendingOf(rows []SpendingRow) []ProjectSpending {
	type key struct{ project, category uuid.UUID }
	var (
		spending []ProjectSpending
		index    = make(map[key]int) // the place of each key in spending
	)
	for _, row := range rows {
		if row.ProjectID == nil {
			continue
		}
		k := key{*row.ProjectID, row.CategoryID}
		i, seen := index[k]
		if !seen {
			i = len(spending)
			index[k] = i
			spending = append(spending, ProjectSpending{
				ProjectID: k.project, Spending: Spending{CategoryID: k.category, ParentID: row.ParentID},
			})
		}
		spending[i].Amount = spending[i].Amount.Add(row.Amount)
	}
	return spending
}

// TopLevelSpending is what was spent on a top-level category in a month, and on the
// subcategories under it. A subcategory is never alone: it is under its parent,
// whether anything was spent on the parent itself or not.
type TopLevelSpending struct {
	CategoryID uuid.UUID
	// Total is all that was spent on the category and under it: Own and the Amount of
	// every one of Children.
	Total decimal.Decimal
	// Own is what was spent on the category itself, on none of its subcategories.
	Own decimal.Decimal
	// Children are the subcategories that something was spent on, the largest Amount
	// first and, for the same Amount, by category id. Each is a Spending of which
	// ParentID is CategoryID.
	Children []Spending
}

// TopLevelSpendingOfMonth returns what the rows say was spent in the month the time
// falls on, for each top-level category, and what was spent in all, which is the
// totals of the categories added up. The rows of one category, a row for each area and
// project it was spent in, are added up; a subcategory goes under its parent; the rows
// of other months are not counted, and a category that has no row in the month, and
// none of its subcategories, is not in the result. The categories are the largest
// Total first and, for the same Total, by category id.
func TopLevelSpendingOfMonth(month time.Time, rows []SpendingRow) ([]TopLevelSpending, decimal.Decimal) {
	byCategory := make(map[uuid.UUID]Spending)
	for _, row := range rows {
		if !sameMonth(month, row.Month) {
			continue
		}
		s, seen := byCategory[row.CategoryID]
		if !seen {
			s = Spending{CategoryID: row.CategoryID, ParentID: row.ParentID}
		}
		s.Amount = s.Amount.Add(row.Amount)
		byCategory[row.CategoryID] = s
	}

	var (
		topLevel []TopLevelSpending
		index    = make(map[uuid.UUID]int) // the place of each top-level category in topLevel
		total    decimal.Decimal
	)
	for _, s := range byCategory {
		top := s.CategoryID
		if s.ParentID != nil {
			top = *s.ParentID
		}
		i, seen := index[top]
		if !seen {
			i = len(topLevel)
			index[top] = i
			topLevel = append(topLevel, TopLevelSpending{CategoryID: top})
		}
		topLevel[i].Total = topLevel[i].Total.Add(s.Amount)
		if s.ParentID == nil {
			topLevel[i].Own = s.Amount
		} else {
			topLevel[i].Children = append(topLevel[i].Children, s)
		}
		total = total.Add(s.Amount)
	}
	for _, t := range topLevel {
		slices.SortFunc(t.Children, func(a, b Spending) int {
			return cmp.Or(b.Amount.Cmp(a.Amount), bytes.Compare(a.CategoryID[:], b.CategoryID[:]))
		})
	}
	slices.SortFunc(topLevel, func(a, b TopLevelSpending) int {
		return cmp.Or(b.Total.Cmp(a.Total), bytes.Compare(a.CategoryID[:], b.CategoryID[:]))
	})
	return topLevel, total
}

// sameMonth reports whether the two times fall on the same month, each as its own
// location reads it.
func sameMonth(a, b time.Time) bool {
	ay, am, _ := a.Date()
	by, bm, _ := b.Date()
	return ay == by && am == bm
}
