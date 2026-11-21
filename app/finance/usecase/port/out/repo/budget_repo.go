package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// BudgetRepo stores the budgets of areas and of projects with their allocations,
// and adds up what was spent, which is what they are compared with.
//
// What was spent is the base amount of the expense postings of confirmed
// transactions: the postings of every kind of transaction, so a fee or the interest
// of an instalment period is spent like any other expense, and no others, so a
// reward, which is a book of its own, and a discount, which is no posting, do not
// reduce it. Only a transaction that is confirmed counts, not a draft or a void
// one. It is the share that is mine, as an expense posting is: what I paid for
// others is a dealing posting, which is not here.
//
// A repo does not open a database transaction to write in, so the use case
// decides what is written together. The Save methods need one: each writes a
// budget in several statements, which must be all done or all undone.
type BudgetRepo interface {
	// SaveAreaBudget stores the budget of the area from the month, replacing the
	// amount and the allocations of the budget the area has from that month, if it
	// has one, and keeping the budgets it has from other months. It returns the
	// budget as stored: the one that was there keeps its id and the time it was
	// created, whatever the budget given has. It does not check the budget; the
	// model does. It must be called in a database transaction.
	SaveAreaBudget(ctx context.Context, budget model.AreaBudget) (model.AreaBudget, error)
	// SaveProjectBudget stores the budget of the project, replacing the amount and
	// the allocations of the budget the project has, if it has one. It returns the
	// budget as stored, with the same rules as SaveAreaBudget. It must be called in
	// a database transaction.
	SaveProjectBudget(ctx context.Context, budget model.ProjectBudget) (model.ProjectBudget, error)
	// DeleteProjectBudget deletes the budget of the project, with its allocations,
	// or returns model.ErrNotFound when the project has none.
	DeleteProjectBudget(ctx context.Context, projectID uuid.UUID) error
	// AreaBudgets returns every budget of every area, those that are in force and
	// those that were replaced or are not in force yet, by area id and then by the
	// month they take effect in. It is for the use case to tell which is in force
	// in a month (model.BudgetsInForce). Each has its allocations, by the name of
	// the category and then by its id. It is one statement, so a budget and its
	// allocations agree.
	AreaBudgets(ctx context.Context) ([]model.AreaBudget, error)
	// ProjectBudgets returns the budgets of the projects, by project id, with their
	// allocations as AreaBudgets has them: the budget of the project when projectID
	// is set, which is none for a project with no budget, and every project's
	// otherwise.
	ProjectBudgets(ctx context.Context, projectID *uuid.UUID) ([]model.ProjectBudget, error)
	// AreaSpending adds up what was spent in the areas on the days from from to to,
	// both days included, by the day the transaction happened on: for each area, each
	// category, and whether the transaction belongs to a project. A transaction with
	// no area is not in it. The order is by area id, then category id, then the
	// transactions with no project before those that have one. from and to are dates
	// as model.DateOf makes them.
	AreaSpending(ctx context.Context, from, to time.Time) ([]model.AreaSpending, error)
	// ProjectSpending adds up what was spent in the projects, on the days up to to,
	// that day included, whenever they began: for each project and category. A
	// transaction with no project is not in it. It is the spending of the project
	// when projectID is set and every project's otherwise. The order is by project
	// id, then category id. to is a date as model.DateOf makes it.
	ProjectSpending(ctx context.Context, projectID *uuid.UUID, to time.Time) ([]model.ProjectSpending, error)
	// Spending adds up what was spent on the days from from to to, both days
	// included, by the month of the day the transaction happened on and the category:
	// a row for each month and category that has something spent on it, with the
	// parent of the category. It is the same definition of spent as AreaSpending and
	// ProjectSpending, the same transactions and the same postings, but with no area
	// or project to group by and no area to keep out: a transaction counts whether it
	// belongs to an area, a project, both or neither, so the spending of a month is
	// what the areas spent in it, projects included, and the expenses that have no
	// area. The order is by month, then category id. from and to are dates as
	// model.DateOf makes them, and a range of days may cover several months.
	Spending(ctx context.Context, from, to time.Time) ([]model.MonthCategorySpending, error)
}
