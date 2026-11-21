package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// SpendingFilter selects what BudgetRepo.Spending adds up; every filter that is set
// must hold.
type SpendingFilter struct {
	// From and To are the days that count, both included: the days the transactions
	// happened on. They are dates as model.DateOf makes them. A nil From leaves the
	// first day open: every day up to To counts, whenever it was.
	From *time.Time
	To   time.Time
	// ProjectID limits what is added up to the transactions of that project; nil
	// leaves it open.
	ProjectID *uuid.UUID
}

// BudgetRepo stores the budgets of areas and of projects with their allocations,
// and adds up what was spent, which is what they are compared with.
//
// What was spent is the base amount of the expense postings of confirmed
// transactions: the postings of every kind of transaction, so a fee or the interest
// of an instalment period is spent like any other expense, and no others, so a
// reward, which is a book of its own, and a discount, which is no posting, do not
// reduce it. Only a transaction that is confirmed counts, not a draft or a void
// one. It is the share that is mine, as an expense posting is: what I paid for
// others is a dealing posting, which is not here. Spending is the one place that
// says so: every read of what was spent, by area, by project, by category or as a
// total, is made of its rows.
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
	// Spending adds up what was spent on the days of the filter, in one statement: a row
	// for each month (the first day of the month of the day the transaction happened on),
	// area, project and category that something was spent on, with the parent of the
	// category. The area and the project are nil for the expenses that belong to none, so
	// that every expense is in a row, whether it belongs to an area, a project, both or
	// neither. The model makes what is read of the rows (model.AreaSpendingOfMonth,
	// model.ProjectSpendingOf, model.TopLevelSpendingOfMonth, model.TotalsByMonth), and
	// the use case asks for the days it needs. The rows come in no order that matters.
	Spending(ctx context.Context, filter SpendingFilter) ([]model.SpendingRow, error)
}
