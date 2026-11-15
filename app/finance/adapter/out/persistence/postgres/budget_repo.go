package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// BudgetRepo implements repo.BudgetRepo on the fin_budget and
// fin_budget_allocation tables, and on the postings of the expense book for what
// has been spent.
type BudgetRepo struct {
	q *dbgen.Queries
}

var _ repo.BudgetRepo = (*BudgetRepo)(nil)

// NewBudgetRepo returns a repo that reads and writes through pool. Its statements
// join the transaction a context carries (see pkg/postgres).
func NewBudgetRepo(pool *pgxpool.Pool) *BudgetRepo {
	return &BudgetRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// SaveAreaBudget stores the budget of the area from the month, or replaces the
// amount of the one the area has from that month, and then replaces its
// allocations, in three statements the use case puts in one database transaction.
// The table's constraints are the last guard of the month (the first day of one),
// of the amounts and of the allocations (one for each category, which must exist).
func (r *BudgetRepo) SaveAreaBudget(ctx context.Context, budget model.AreaBudget) (model.AreaBudget, error) {
	row, err := r.q.UpsertAreaBudget(ctx, dbgen.UpsertAreaBudgetParams{
		ID:            budget.ID,
		AreaID:        budget.AreaID,
		EffectiveFrom: budget.EffectiveFrom,
		Amount:        budget.Amount,
	})
	if err != nil {
		return model.AreaBudget{}, fmt.Errorf("store the budget: %w", err)
	}
	if err := r.replaceAllocations(ctx, row.ID, budget.Allocations); err != nil {
		return model.AreaBudget{}, err
	}
	stored := budget
	stored.ID, stored.CreatedAt = row.ID, row.CreatedAt
	return stored, nil
}

// SaveProjectBudget stores the budget of the project, or replaces the amount of the
// one it has, and then replaces its allocations, as SaveAreaBudget does.
func (r *BudgetRepo) SaveProjectBudget(ctx context.Context, budget model.ProjectBudget) (model.ProjectBudget, error) {
	row, err := r.q.UpsertProjectBudget(ctx, dbgen.UpsertProjectBudgetParams{
		ID:        budget.ID,
		ProjectID: budget.ProjectID,
		Amount:    budget.Amount,
	})
	if err != nil {
		return model.ProjectBudget{}, fmt.Errorf("store the budget: %w", err)
	}
	if err := r.replaceAllocations(ctx, row.ID, budget.Allocations); err != nil {
		return model.ProjectBudget{}, err
	}
	stored := budget
	stored.ID, stored.CreatedAt = row.ID, row.CreatedAt
	return stored, nil
}

// replaceAllocations deletes the allocations the budget has and inserts the ones
// given, in the order given.
func (r *BudgetRepo) replaceAllocations(ctx context.Context, budgetID uuid.UUID, allocations []model.Allocation) error {
	if err := r.q.DeleteBudgetAllocations(ctx, budgetID); err != nil {
		return fmt.Errorf("delete the allocations: %w", err)
	}
	for _, a := range allocations {
		err := r.q.InsertBudgetAllocation(ctx, dbgen.InsertBudgetAllocationParams{
			BudgetID:   budgetID,
			CategoryID: a.CategoryID,
			Amount:     a.Amount,
		})
		if err != nil {
			return fmt.Errorf("insert the allocation of category %s: %w", a.CategoryID, err)
		}
	}
	return nil
}

// DeleteProjectBudget deletes the budget of the project; its allocations go with
// it.
func (r *BudgetRepo) DeleteProjectBudget(ctx context.Context, projectID uuid.UUID) error {
	n, err := r.q.DeleteProjectBudget(ctx, projectID)
	if err != nil {
		return err
	}
	if n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// AreaBudgets returns every budget of every area, with its allocations. The
// statement has a row for each allocation, ordered so that those of one budget
// are together, and one row for a budget that has none.
func (r *BudgetRepo) AreaBudgets(ctx context.Context) ([]model.AreaBudget, error) {
	rows, err := r.q.ListAreaBudgets(ctx)
	if err != nil {
		return nil, err
	}
	var budgets []model.AreaBudget
	for _, row := range rows {
		if n := len(budgets); n == 0 || budgets[n-1].ID != row.ID {
			budgets = append(budgets, model.AreaBudget{
				ID:            row.ID,
				AreaID:        row.AreaID,
				EffectiveFrom: row.EffectiveFrom,
				Amount:        row.Amount,
				CreatedAt:     row.CreatedAt,
			})
		}
		if row.AllocationCategoryID.Valid {
			last := &budgets[len(budgets)-1]
			last.Allocations = append(last.Allocations, model.Allocation{
				CategoryID: row.AllocationCategoryID.UUID,
				Amount:     row.AllocationAmount.Decimal,
			})
		}
	}
	return budgets, nil
}

// ProjectBudgets returns the budgets of the projects, or of one, with their
// allocations, as AreaBudgets does.
func (r *BudgetRepo) ProjectBudgets(ctx context.Context, projectID *uuid.UUID) ([]model.ProjectBudget, error) {
	rows, err := r.q.ListProjectBudgets(ctx, nullUUID(projectID))
	if err != nil {
		return nil, err
	}
	var budgets []model.ProjectBudget
	for _, row := range rows {
		if n := len(budgets); n == 0 || budgets[n-1].ID != row.ID {
			budgets = append(budgets, model.ProjectBudget{
				ID:        row.ID,
				ProjectID: row.ProjectID,
				Amount:    row.Amount,
				CreatedAt: row.CreatedAt,
			})
		}
		if row.AllocationCategoryID.Valid {
			last := &budgets[len(budgets)-1]
			last.Allocations = append(last.Allocations, model.Allocation{
				CategoryID: row.AllocationCategoryID.UUID,
				Amount:     row.AllocationAmount.Decimal,
			})
		}
	}
	return budgets, nil
}

// AreaSpending adds up the base amounts of the expense postings of the confirmed
// transactions of the areas that happened from the day from to the day to, for each
// area, category and whether the transaction belongs to a project.
func (r *BudgetRepo) AreaSpending(ctx context.Context, from, to time.Time) ([]model.AreaSpending, error) {
	rows, err := r.q.ListAreaSpending(ctx, dbgen.ListAreaSpendingParams{FromDay: from, ToDay: to})
	if err != nil {
		return nil, err
	}
	spending := make([]model.AreaSpending, len(rows))
	for i, row := range rows {
		spending[i] = model.AreaSpending{
			AreaID:    row.AreaID,
			InProject: row.InProject,
			Spending:  model.Spending{CategoryID: row.CategoryID, ParentID: optionalUUID(row.ParentID), Amount: row.Amount},
		}
	}
	return spending, nil
}

// ProjectSpending adds up the base amounts of the expense postings of the
// confirmed transactions of the projects that happened up to the day to, for each
// project and category; of one project when projectID is set.
func (r *BudgetRepo) ProjectSpending(ctx context.Context, projectID *uuid.UUID, to time.Time) ([]model.ProjectSpending, error) {
	rows, err := r.q.ListProjectSpending(ctx, dbgen.ListProjectSpendingParams{ToDay: to, ProjectID: nullUUID(projectID)})
	if err != nil {
		return nil, err
	}
	spending := make([]model.ProjectSpending, len(rows))
	for i, row := range rows {
		spending[i] = model.ProjectSpending{
			ProjectID: row.ProjectID,
			Spending:  model.Spending{CategoryID: row.CategoryID, ParentID: optionalUUID(row.ParentID), Amount: row.Amount},
		}
	}
	return spending, nil
}
