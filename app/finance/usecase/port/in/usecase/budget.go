package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// A budget (預算) is the amount meant to be spent, to compare what is spent with:
// nothing is reserved out of the money there is, and nothing is blocked when it
// is exceeded, it is only reported (Over). A project has one total for the whole
// project; an area has one for each month, which does not carry over. Budgets are
// in the base currency.
//
// What is spent is the base amount of the confirmed expenses (any kind of
// transaction: fees and interest are expenses too), of transactions that have
// happened by today, which the clock of the application says: one dated later
// counts from its day on. The month an expense belongs to is the month of the day
// it happened on. Drafts and void transactions count nowhere, and neither do
// rewards, which do not reduce an expense, nor discounts, which only record what
// the merchant took off: an expense is what was paid. What I paid for others is
// not an expense of mine; only my own share is. An expense that belongs to a
// project counts toward the budget of the project and not toward that of its
// area, but still counts in what the area has spent (SpentWithProjects).
//
// An allocation (分配) is a share of a budget for a category. What is spent falls
// into it by its category, by itself: into the allocation of the category of the
// expense, else into that of its parent category, else into none (Unallocated). A
// category has no budget of its own.

// Allocation is a share of a budget for an expense category.
type Allocation struct {
	CategoryID uuid.UUID
	// Amount is greater than zero, with no more decimal places than the base
	// currency.
	Amount decimal.Decimal
}

// AllocationStatus is where an allocation stands: what it is, and what has been
// spent into it.
type AllocationStatus struct {
	CategoryID uuid.UUID
	Amount     decimal.Decimal
	// Spent is what fell into the allocation: the expenses of the category, and of
	// its subcategories, that count toward the budget.
	Spent decimal.Decimal
}

// SetAreaBudget sets what an area may spend in a month, from that month on: the
// budget of any later month is this one until another is set for a later month,
// so a budget set once stays in force. Setting it again for the same month
// replaces the amount and the allocations of that month; budgets set for other
// months are left as they are, and a month does not look at a budget set for a
// later one. An amount of nothing means no budget from that month on, which ends
// an earlier one.
//
// The area must exist and not be archived. The amount is not negative. The
// allocations are of categories that exist, are not archived, are expense
// categories and are not a system category, at most one for each category, and
// none for a category together with its parent; they add up to no more than the
// amount. Nothing is stored when any of it is not so.
type SetAreaBudget interface {
	SetAreaBudget(ctx context.Context, in SetAreaBudgetInput) (AreaBudget, error)
}

// SetAreaBudgetInput is the budget of an area to set.
type SetAreaBudgetInput struct {
	AreaID uuid.UUID
	// Month is the month the budget takes effect in, and is required. Only the
	// month it falls on counts, as its own location reads it: any day of the month
	// will do.
	Month time.Time
	// Amount is what the area may spend in a month, in the base currency: not
	// negative, with no more decimal places than it. Nothing ends an earlier
	// budget.
	Amount decimal.Decimal
	// Allocations divide Amount among categories. They replace the allocations that
	// the budget of the month had, if it had any; none leaves the budget with no
	// allocation.
	Allocations []Allocation
}

// AreaBudget is the budget of an area as it was set. Its allocations are in the order
// they were given; GetAreaBudgets lists them by the name of the category.
type AreaBudget struct {
	AreaID uuid.UUID
	// From is the first day of the month the budget takes effect in, as a date at
	// midnight UTC.
	From        time.Time
	Amount      decimal.Decimal
	Allocations []Allocation
}

// SetProjectBudget sets what a project may spend in all: one total for the whole
// project, whenever its expenses were made. Setting it again replaces the amount
// and the allocations. A project has no budget until one is set, and
// RemoveProjectBudget takes it away; an amount of nothing is a budget of nothing,
// not the lack of one.
//
// The project must exist and not be archived. The rules of the amount and the
// allocations are those of SetAreaBudget. Nothing is stored when any of them is
// not met.
type SetProjectBudget interface {
	SetProjectBudget(ctx context.Context, in SetProjectBudgetInput) (ProjectBudget, error)
}

// SetProjectBudgetInput is the budget of a project to set.
type SetProjectBudgetInput struct {
	ProjectID uuid.UUID
	// Amount is the total of the project, in the base currency: not negative, with no
	// more decimal places than it.
	Amount decimal.Decimal
	// Allocations divide Amount among categories. They replace those of the budget
	// the project had, if it had one.
	Allocations []Allocation
}

// ProjectBudget is the budget of a project as it was set. Its allocations are in the
// order they were given; GetProjectBudgets lists them by the name of the category.
type ProjectBudget struct {
	ProjectID   uuid.UUID
	Amount      decimal.Decimal
	Allocations []Allocation
}

// RemoveProjectBudget takes away the budget of a project, with its allocations.
// The project need not exist any more, or be in use: an archived project's budget
// can go too. It returns model.ErrNotFound when the project has no budget.
type RemoveProjectBudget interface {
	RemoveProjectBudget(ctx context.Context, in RemoveProjectBudgetInput) error
}

// RemoveProjectBudgetInput names the project whose budget goes.
type RemoveProjectBudgetInput struct {
	ProjectID uuid.UUID
}

// GetAreaBudgets returns where each area stands in a month against its budget:
// every area that has a budget in force in the month or has confirmed expenses in
// it. The budget in force in a month is the one set for the greatest month that is
// not after it (see SetAreaBudget); an area whose budget in force is of nothing
// has none. What counts as spent in the month is the expenses made from its first
// day to its last, and not after today: a month that has not begun has none, and
// the current one has what is made by today.
type GetAreaBudgets interface {
	GetAreaBudgets(ctx context.Context, in GetAreaBudgetsInput) (GetAreaBudgetsOutput, error)
}

// GetAreaBudgetsInput is the month to read.
type GetAreaBudgetsInput struct {
	// Month is required. Only the month it falls on counts, as its own location
	// reads it: any day of the month will do.
	Month time.Time
}

// AreaBudgetStatus is where an area stands in a month.
type AreaBudgetStatus struct {
	AreaID uuid.UUID
	// Budget is the budget of the area in the month; nil when it has none.
	Budget *decimal.Decimal
	// Spent is the expenses of the area in the month that belong to no project: what
	// the budget is compared with.
	Spent decimal.Decimal
	// SpentWithProjects is all the expenses of the area in the month, those that
	// belong to a project too: the area's own statistic. It is Spent and what the
	// projects spent in the area that month.
	SpentWithProjects decimal.Decimal
	// Allocations are those of the budget in force, by the name of the category,
	// each with what of Spent fell into it. An area with no budget has none.
	Allocations []AllocationStatus
	// Unallocated is what of Spent fell into no allocation, such as an expense that
	// a reconciliation made to the category 未記錄. Spent is what fell into the
	// allocations and this. An area with no budget has no allocation, so all of
	// Spent is unallocated.
	Unallocated decimal.Decimal
	// Over is true when there is a budget and Spent is more than it: spending the
	// whole of it is not over. Nothing is blocked either way.
	Over bool
}

// GetAreaBudgetsOutput holds one status for each area that has a budget in force in
// the month or confirmed expenses in it, in the order the catalog lists its areas
// (by name), the archived ones too: what they spent still counts. Expenses with
// no area belong to none of them.
type GetAreaBudgetsOutput struct {
	// Month is the first day of the month the statuses are of, as a date at midnight
	// UTC.
	Month time.Time
	Areas []AreaBudgetStatus
}

// GetProjectBudgets returns where each project stands against its budget: every
// project that has a budget or confirmed expenses. What counts as spent is all
// the expenses of the project, whenever they were made, up to today.
type GetProjectBudgets interface {
	GetProjectBudgets(ctx context.Context, in GetProjectBudgetsInput) (GetProjectBudgetsOutput, error)
}

// GetProjectBudgetsInput says whose budgets to return.
type GetProjectBudgetsInput struct {
	// ProjectID limits the result to that project; nil returns every project's. A
	// project with neither a budget nor an expense has no status. The project must
	// be one the catalog has: for one it does not, the error is model.ErrNotFound
	// (with the id of the project), not an empty result. An archived project is one
	// the catalog has: what it spent still counts.
	ProjectID *uuid.UUID
}

// ProjectBudgetStatus is where a project stands.
type ProjectBudgetStatus struct {
	ProjectID uuid.UUID
	// Budget is the total of the project; nil when it has none. A budget of nothing
	// is not nil: it is a budget, and is over once anything is spent.
	Budget *decimal.Decimal
	// Spent is all the expenses of the project that have been made.
	Spent decimal.Decimal
	// Allocations are those of the budget, by the name of the category, each with
	// what of Spent fell into it. A project with no budget has none.
	Allocations []AllocationStatus
	// Unallocated is what of Spent fell into no allocation. Spent is what fell into
	// the allocations and this.
	Unallocated decimal.Decimal
	// Over is true when there is a budget and Spent is more than it.
	Over bool
}

// GetProjectBudgetsOutput holds one status for each project that has a budget or
// confirmed expenses, in the order the catalog lists its projects (by name), the
// archived ones too.
type GetProjectBudgetsOutput struct {
	Projects []ProjectBudgetStatus
}
