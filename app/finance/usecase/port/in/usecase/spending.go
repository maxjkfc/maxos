package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Spending is what the budgets count as spent, read without an area or a project
// in the way: the base amount of the expense postings of confirmed transactions,
// of every kind of transaction (a fee or the interest of an instalment period is an
// expense like any other), whose day is not after today, which the clock of the
// application says. The month an expense belongs to is the month of the day it
// happened on. Drafts and void transactions count nowhere, rewards and incomes do
// not reduce it, and a discount is no posting: an expense is what was paid, and
// only my own share of it (what I paid for others is a dealing). It is the same
// definition as the budgets', so what is spent in a month here is what the areas
// spent in it, projects included (SpentWithProjects of GetAreaBudgets), and the
// expenses that have no area, which no area counts.

// MaxSpendingTrendMonths is the most months GetSpendingTrend takes in one range.
const MaxSpendingTrendMonths = 120

// GetSpending returns what was spent in a month, by category. Only the days of the
// month up to today count: a month that has not begun has none, and the current one
// has what is made by today.
//
// A month that is not given is model.ErrMonthRequired.
type GetSpending interface {
	GetSpending(ctx context.Context, in GetSpendingInput) (GetSpendingOutput, error)
}

// GetSpendingInput is the month to read.
type GetSpendingInput struct {
	// Month is required. Only the month it falls on counts, as its own location
	// reads it: any day of the month will do.
	Month time.Time
}

// CategorySpending is what was spent on one category in a month.
type CategorySpending struct {
	CategoryID uuid.UUID
	// ParentID is the top-level category that CategoryID is a subcategory of; nil
	// when CategoryID is a top-level category itself. It is how a caller adds the
	// subcategories up under their parent.
	ParentID *uuid.UUID
	// Amount is in the base currency, whatever the instrument of the expenses was:
	// the amount of the day each was spent on fixed it.
	Amount decimal.Decimal
}

// GetSpendingOutput holds what was spent in the month.
type GetSpendingOutput struct {
	// Month is the first day of the month, as a date at midnight UTC.
	Month time.Time
	// Total is all that was spent in the month: the Amount of every category added
	// up, whether the expense belongs to an area, a project, both or neither.
	Total decimal.Decimal
	// Categories are the categories that something was spent on in the month, the
	// archived ones too, the largest Amount first and, for the same Amount, by
	// category id. A category that nothing was spent on is not in it.
	Categories []CategorySpending
}

// GetSpendingTrend returns what was spent in each month of a range of months, as
// one total for each, with the same definition of spent as GetSpending. A month
// that nothing was spent in, or that has not begun, is there with a total of zero.
//
// A range that has no From or no To is model.ErrMonthRequired, one that ends before
// it starts model.ErrDateRangeReversed, and one of more than MaxSpendingTrendMonths
// months model.ErrSpendingRangeTooWide.
type GetSpendingTrend interface {
	GetSpendingTrend(ctx context.Context, in GetSpendingTrendInput) (GetSpendingTrendOutput, error)
}

// GetSpendingTrendInput is the range of months to read.
type GetSpendingTrendInput struct {
	// From and To are the first and the last month of the range, both included and
	// both required. Only the month each falls on counts, as its own location reads
	// it: any day of the month will do.
	From, To time.Time
}

// MonthSpending is what was spent in one month.
type MonthSpending struct {
	// Month is the first day of the month, as a date at midnight UTC.
	Month time.Time
	// Total is in the base currency.
	Total decimal.Decimal
}

// GetSpendingTrendOutput holds the totals of the months.
type GetSpendingTrendOutput struct {
	// Months has one entry for each month from the first to the last, oldest first,
	// none left out.
	Months []MonthSpending
}
