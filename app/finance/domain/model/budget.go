package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// A budget (預算) is the amount meant to be spent, only to compare what is spent
// with and to warn by: it sets nothing aside and blocks nothing. A project has one
// for the whole project; an area has one for each month, which does not carry over
// to the next. What is spent is the base amount of the confirmed expense postings
// (see Spending), so budgets are in the base currency.
//
// An allocation (分配) is a share of a budget for a category. What is spent falls
// into it by the category of the expense, with nothing to map by hand: an expense
// falls into the allocation of its own category, else of its parent category, else
// into none (it is unallocated). A category has no budget of its own.

// Allocation is a share of a budget for a category.
type Allocation struct {
	CategoryID uuid.UUID
	// Amount is greater than zero, and no finer than the base currency.
	Amount decimal.Decimal
}

// AllocationLine is an allocation to make, with the category it is for loaded:
// whether it can be allocated depends on what the category is.
type AllocationLine struct {
	// Category must be in use, have the direction expense, and not be a system
	// category: the expenses a budget is compared with are what was bought, and
	// what a reconciliation found missing is not.
	Category Category
	// Amount is greater than zero, and no finer than the base currency.
	Amount decimal.Decimal
}

// AreaBudget is what an area may spend in a month, from the month EffectiveFrom
// on: the budget of a month is the row of the area with the greatest
// EffectiveFrom that is not after the month, so a row stays in force until a
// later one replaces it (see BudgetsInForce).
type AreaBudget struct {
	ID     uuid.UUID
	AreaID uuid.UUID
	// EffectiveFrom is the first day of the month the budget takes effect in, as a
	// date at midnight UTC (see MonthOf).
	EffectiveFrom time.Time
	// Amount is what the area may spend in a month. It is not negative; nothing
	// means no budget from EffectiveFrom on, which ends an earlier one.
	Amount decimal.Decimal
	// Allocations divide Amount among categories. They add up to no more than
	// Amount. A repo lists them by the name of the category.
	Allocations []Allocation
	// CreatedAt is assigned when the budget is stored; it is zero before then.
	CreatedAt time.Time
}

// ProjectBudget is what a project may spend in all: one total for the whole
// project, whenever its expenses are made.
type ProjectBudget struct {
	ID        uuid.UUID
	ProjectID uuid.UUID
	// Amount is the total of the project. It is not negative. Unlike the budget of
	// an area, nothing here is a budget of nothing that is the same as having
	// none: a project that has none is a project with no budget.
	Amount decimal.Decimal
	// Allocations divide Amount among categories. They add up to no more than
	// Amount. A repo lists them by the name of the category.
	Allocations []Allocation
	// CreatedAt is assigned when the budget is stored; it is zero before then.
	CreatedAt time.Time
}

// NewAreaBudgetParams is the raw input of the budget of an area.
type NewAreaBudgetParams struct {
	ID uuid.UUID
	// AreaID is an id of a catalog entry. Whether it exists and is in use is for
	// the use case to check.
	AreaID uuid.UUID
	// Month is the month the budget takes effect in, and is required. Only the
	// month it falls on counts, as its own location reads it.
	Month time.Time
	// Amount is not negative, and no finer than the base currency.
	Amount decimal.Decimal
	// Allocations are the shares of Amount for categories, at most one for each
	// category and none for a category and its parent. They add up to no more than
	// Amount.
	Allocations []AllocationLine
	// BaseCurrency is the base currency, which the amounts are checked against.
	BaseCurrency Instrument
}

// NewAreaBudget validates the budget of an area and returns it, set for the month
// the time falls on.
func NewAreaBudget(p NewAreaBudgetParams) (AreaBudget, error) {
	if p.Month.IsZero() {
		return AreaBudget{}, ErrMonthRequired
	}
	allocations, err := checkBudget(p.BaseCurrency, p.Amount, p.Allocations)
	if err != nil {
		return AreaBudget{}, err
	}
	return AreaBudget{
		ID:            p.ID,
		AreaID:        p.AreaID,
		EffectiveFrom: MonthOf(p.Month),
		Amount:        p.Amount,
		Allocations:   allocations,
	}, nil
}

// NewProjectBudgetParams is the raw input of the budget of a project.
type NewProjectBudgetParams struct {
	ID uuid.UUID
	// ProjectID is an id of a catalog entry. Whether it exists and is in use is for
	// the use case to check.
	ProjectID uuid.UUID
	// Amount is not negative, and no finer than the base currency.
	Amount decimal.Decimal
	// Allocations are the shares of Amount for categories, with the rules of
	// NewAreaBudgetParams.Allocations.
	Allocations []AllocationLine
	// BaseCurrency is the base currency, which the amounts are checked against.
	BaseCurrency Instrument
}

// NewProjectBudget validates the budget of a project and returns it.
func NewProjectBudget(p NewProjectBudgetParams) (ProjectBudget, error) {
	allocations, err := checkBudget(p.BaseCurrency, p.Amount, p.Allocations)
	if err != nil {
		return ProjectBudget{}, err
	}
	return ProjectBudget{ID: p.ID, ProjectID: p.ProjectID, Amount: p.Amount, Allocations: allocations}, nil
}

// checkBudget checks the amount of a budget and its allocations, which are the
// same for both kinds, and returns the allocations as a budget keeps them, in the
// order given. The first thing wrong is what it returns; the errors of an
// allocation name which one it is, by its position, counted from 1.
func checkBudget(base Instrument, amount decimal.Decimal, lines []AllocationLine) ([]Allocation, error) {
	if amount.IsNegative() {
		return nil, fmt.Errorf("%w: got %s", ErrBudgetAmountNegative, amount)
	}
	if err := base.CheckQuantity(amount); err != nil {
		return nil, err
	}

	allocations := make([]Allocation, 0, len(lines))
	position := make(map[uuid.UUID]int, len(lines)) // category id to its allocation, counted from 1
	total := decimal.Zero
	for i, line := range lines {
		n := i + 1
		c := line.Category
		if c.IsArchived() {
			return nil, fmt.Errorf("allocation %d: category %q: %w", n, c.Name, ErrArchived)
		}
		if c.IsSystem() {
			return nil, fmt.Errorf("allocation %d: category %q: %w", n, c.Name, ErrSystemCategory)
		}
		if c.Direction != DirectionExpense {
			return nil, fmt.Errorf("allocation %d: %w: category %q is for %s, and a budget is compared with expenses",
				n, ErrWrongCategoryDirection, c.Name, c.Direction)
		}
		if first, repeated := position[c.ID]; repeated {
			return nil, fmt.Errorf("allocation %d: %w: category %q is allocation %d too", n, ErrDuplicateAllocation, c.Name, first)
		}
		position[c.ID] = n
		if err := checkAmount(base, line.Amount); err != nil {
			return nil, fmt.Errorf("allocation %d: %w", n, err)
		}
		total = total.Add(line.Amount)
		allocations = append(allocations, Allocation{CategoryID: c.ID, Amount: line.Amount})
	}

	// Not in the loop above: the parent may come after its child.
	for i, line := range lines {
		if line.Category.ParentID == nil {
			continue
		}
		if parent, allocated := position[*line.Category.ParentID]; allocated {
			return nil, fmt.Errorf("allocation %d: %w: category %q is a subcategory of the one in allocation %d",
				i+1, ErrParentAndChildAllocated, line.Category.Name, parent)
		}
	}

	if total.GreaterThan(amount) {
		return nil, fmt.Errorf("%w: they add up to %s, and the budget is %s", ErrAllocationsExceedBudget, total, amount)
	}
	return allocations, nil
}

// MonthOf returns the first day of the month the time falls on, as its own
// location reads it, as a date at midnight UTC (see DateOf). It is how a month is
// named: the month a budget of an area is set for and read for.
func MonthOf(t time.Time) time.Time {
	y, m, _ := t.Date()
	return time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
}

// LastDayOf returns the last day of the month the time falls on, as a date at
// midnight UTC.
func LastDayOf(t time.Time) time.Time {
	return MonthOf(t).AddDate(0, 1, -1)
}

// BudgetsInForce returns the budget of each area in the month the time falls on.
// Of the budgets of an area that are not after the month, which is those whose
// EffectiveFrom is the month or an earlier one, the one with the greatest
// EffectiveFrom is in force: a later budget does not apply to an earlier month.
// A budget of nothing is in force like any other, and means the area has no
// budget from then on, so an area whose budget in force is one is not in the
// result. The budgets may be in any order, and an area may have none.
func BudgetsInForce(budgets []AreaBudget, month time.Time) map[uuid.UUID]AreaBudget {
	month = MonthOf(month)
	latest := make(map[uuid.UUID]AreaBudget)
	for _, b := range budgets {
		if DateOf(b.EffectiveFrom).After(month) {
			continue
		}
		if current, found := latest[b.AreaID]; !found || DateOf(b.EffectiveFrom).After(DateOf(current.EffectiveFrom)) {
			latest[b.AreaID] = b
		}
	}
	for areaID, b := range latest {
		if b.Amount.IsZero() {
			delete(latest, areaID)
		}
	}
	return latest
}

// Spending is what was spent on a category: the base amounts of the confirmed
// expense postings to it, added up. It is in the base currency, whatever the
// instrument of the expenses was, as the amount of the day it was spent fixed it.
type Spending struct {
	CategoryID uuid.UUID
	// ParentID is the parent of the category; nil for a top-level category. It is
	// where the spending falls when the category has no allocation of its own.
	ParentID *uuid.UUID
	Amount   decimal.Decimal
}

// AreaSpending is what was spent on a category in an area, by the transactions
// that belong to a project or by those that do not.
type AreaSpending struct {
	AreaID uuid.UUID
	Spending
	// InProject says that the transactions belong to a project: what is spent in a
	// project counts toward the budget of the project and not toward that of the
	// area, though it still counts in what the area has spent.
	InProject bool
}

// ProjectSpending is what was spent on a category in a project.
type ProjectSpending struct {
	ProjectID uuid.UUID
	Spending
}

// AllocationStatus is where an allocation stands: how much was meant to be spent
// on its category, and how much has been.
type AllocationStatus struct {
	CategoryID uuid.UUID
	Amount     decimal.Decimal
	Spent      decimal.Decimal
}

// BudgetStatus is how what has been spent compares with a budget.
type BudgetStatus struct {
	// Budget is the amount of the budget; nil when there is none.
	Budget *decimal.Decimal
	// Spent is all that was spent.
	Spent decimal.Decimal
	// Allocations are those of the budget in the order they were given, each with
	// what fell into it. An allocation nothing fell into has a Spent of zero.
	Allocations []AllocationStatus
	// Unallocated is what fell into no allocation. Spent is what fell into the
	// allocations and this.
	Unallocated decimal.Decimal
	// Over is true when there is a budget and Spent is more than it. Spending the
	// whole of it is not over.
	Over bool
}

// NewBudgetStatus works out how the spending compares with the budget of the
// amount that is divided by the allocations. A nil budget is no budget: nothing
// is over then, and, as there are no allocations to fall into, all of it is
// unallocated. Each spending falls into the allocation of its own category, else
// the allocation of its parent category, else into none.
func NewBudgetStatus(budget *decimal.Decimal, allocations []Allocation, spending []Spending) BudgetStatus {
	status := BudgetStatus{Budget: clonePtr(budget), Allocations: make([]AllocationStatus, len(allocations))}
	index := make(map[uuid.UUID]int, len(allocations))
	for i, a := range allocations {
		status.Allocations[i] = AllocationStatus{CategoryID: a.CategoryID, Amount: a.Amount}
		index[a.CategoryID] = i
	}

	for _, s := range spending {
		status.Spent = status.Spent.Add(s.Amount)
		i, allocated := index[s.CategoryID]
		if !allocated && s.ParentID != nil {
			i, allocated = index[*s.ParentID]
		}
		if allocated {
			status.Allocations[i].Spent = status.Allocations[i].Spent.Add(s.Amount)
		} else {
			status.Unallocated = status.Unallocated.Add(s.Amount)
		}
	}
	status.Over = budget != nil && status.Spent.GreaterThan(*budget)
	return status
}
