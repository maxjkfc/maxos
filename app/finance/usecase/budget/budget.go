// Package budget implements the budget use cases: setting the budgets of areas and
// of projects, with their allocations, taking the budget of a project away, and
// reading where areas and projects stand against their budgets.
package budget

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
)

// Service implements usecase.SetAreaBudget, SetProjectBudget, RemoveProjectBudget,
// GetAreaBudgets and GetProjectBudgets.
//
// A use case that writes does all of it, the reads that check the request
// included, in one database transaction. What today is, for the reads that count
// what has been spent up to it, is the clock's to say, and is not asked of
// anything else.
type Service struct {
	tx           tx.Transactor
	budgets      repo.BudgetRepo
	categories   repo.CategoryRepo
	instruments  repo.InstrumentRepo
	references   catalogusecase.VerifyReferences
	listAreas    catalogusecase.ListAreas
	listProjects catalogusecase.ListProjects
	clock        clock.Clock
}

var (
	_ usecase.SetAreaBudget       = (*Service)(nil)
	_ usecase.SetProjectBudget    = (*Service)(nil)
	_ usecase.RemoveProjectBudget = (*Service)(nil)
	_ usecase.GetAreaBudgets      = (*Service)(nil)
	_ usecase.GetProjectBudgets   = (*Service)(nil)
)

// SetAreaBudget checks that the area can be used and the allocations are
// allowed, and stores the budget for the month the request names, replacing the
// budget that month had, if any. An area that is archived can be given one budget
// only: the end of the one it has (see verifyArea).
func (s *Service) SetAreaBudget(ctx context.Context, in usecase.SetAreaBudgetInput) (usecase.AreaBudget, error) {
	if in.Month.IsZero() {
		return usecase.AreaBudget{}, model.ErrMonthRequired
	}
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.AreaBudget{}, fmt.Errorf("generate budget id: %w", err)
	}

	var stored model.AreaBudget
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.verifyArea(ctx, in); err != nil {
			return err
		}
		base, lines, err := s.allocationLines(ctx, in.Allocations)
		if err != nil {
			return err
		}
		budget, err := model.NewAreaBudget(model.NewAreaBudgetParams{
			ID:           id,
			AreaID:       in.AreaID,
			Month:        in.Month,
			Amount:       in.Amount,
			Allocations:  lines,
			BaseCurrency: base,
		})
		if err != nil {
			return err
		}
		if stored, err = s.budgets.SaveAreaBudget(ctx, budget); err != nil {
			return fmt.Errorf("set the budget of area %s from %s: %w", in.AreaID, budget.EffectiveFrom.Format(time.DateOnly), err)
		}
		return nil
	})
	if err != nil {
		return usecase.AreaBudget{}, err
	}
	return usecase.AreaBudget{
		AreaID:      stored.AreaID,
		From:        stored.EffectiveFrom,
		Amount:      stored.Amount,
		Allocations: toAllocations(stored.Allocations),
	}, nil
}

// verifyArea checks with the catalog that a budget can be set for the area: that
// it exists and is not archived. The one budget an area that has been archived
// since can be given is the end of the one it has, which is an amount of nothing
// with no allocation. The budget of a month is the latest one set for a month that
// is not after it, and GetAreaBudgets lists an archived area with it, for what the
// area spent still counts, so a budget that could not be ended would stay in force
// in every month to come. Ending a budget is no new use of the area, so the catalog
// is only asked that the area is there: what it says of one it does not have, and
// any other failure of its own, are what they are for every other budget.
func (s *Service) verifyArea(ctx context.Context, in usecase.SetAreaBudgetInput) error {
	err := s.references.VerifyReferences(ctx, catalogusecase.VerifyReferencesInput{AreaIDs: []uuid.UUID{in.AreaID}})
	if err == nil {
		return nil
	}
	var refErr *catalogusecase.ReferenceError
	if endsBudget(in) && errors.As(err, &refErr) && allArchived(refErr.Problems) {
		return nil
	}
	return fmt.Errorf("check the area: %w", err)
}

// endsBudget reports whether the request takes the budget of the area away from its
// month on rather than setting one: an amount of nothing, with no allocation to
// divide it (SetAreaBudgetInput.Amount).
func endsBudget(in usecase.SetAreaBudgetInput) bool {
	return in.Amount.IsZero() && len(in.Allocations) == 0
}

// allArchived reports whether there are problems, and each is an entry that exists
// and is archived, as against one that does not exist.
func allArchived(problems []catalogusecase.ReferenceProblem) bool {
	for _, p := range problems {
		if !p.Archived {
			return false
		}
	}
	return len(problems) > 0
}

// SetProjectBudget checks that the project can be used and the allocations are
// allowed, and stores the budget, replacing the one the project had, if any.
func (s *Service) SetProjectBudget(ctx context.Context, in usecase.SetProjectBudgetInput) (usecase.ProjectBudget, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.ProjectBudget{}, fmt.Errorf("generate budget id: %w", err)
	}

	var stored model.ProjectBudget
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.references.VerifyReferences(ctx, catalogusecase.VerifyReferencesInput{ProjectIDs: []uuid.UUID{in.ProjectID}}); err != nil {
			return fmt.Errorf("check the project: %w", err)
		}
		base, lines, err := s.allocationLines(ctx, in.Allocations)
		if err != nil {
			return err
		}
		budget, err := model.NewProjectBudget(model.NewProjectBudgetParams{
			ID:           id,
			ProjectID:    in.ProjectID,
			Amount:       in.Amount,
			Allocations:  lines,
			BaseCurrency: base,
		})
		if err != nil {
			return err
		}
		if stored, err = s.budgets.SaveProjectBudget(ctx, budget); err != nil {
			return fmt.Errorf("set the budget of project %s: %w", in.ProjectID, err)
		}
		return nil
	})
	if err != nil {
		return usecase.ProjectBudget{}, err
	}
	return usecase.ProjectBudget{
		ProjectID:   stored.ProjectID,
		Amount:      stored.Amount,
		Allocations: toAllocations(stored.Allocations),
	}, nil
}

// RemoveProjectBudget deletes the budget of the project, which is one statement,
// and needs no database transaction. It does not ask the catalog: whether the
// project is in use is no concern of taking its budget away.
func (s *Service) RemoveProjectBudget(ctx context.Context, in usecase.RemoveProjectBudgetInput) error {
	if err := s.budgets.DeleteProjectBudget(ctx, in.ProjectID); err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("project %s has no budget: %w", in.ProjectID, err)
		}
		return fmt.Errorf("remove the budget of project %s: %w", in.ProjectID, err)
	}
	return nil
}

// allocationLines loads the base currency, which the amounts are checked against,
// and the category each allocation names, and returns them as the domain takes
// them. A category that does not exist is an error that names the allocation;
// whether it can be allocated is for the model.
func (s *Service) allocationLines(ctx context.Context, allocations []usecase.Allocation) (model.Instrument, []model.AllocationLine, error) {
	base, err := s.instruments.Get(ctx, model.BaseCurrencyID)
	if err != nil {
		return model.Instrument{}, nil, fmt.Errorf("base currency: %w", err)
	}
	lines := make([]model.AllocationLine, len(allocations))
	for i, a := range allocations {
		category, err := s.categories.Get(ctx, a.CategoryID)
		if err != nil {
			return model.Instrument{}, nil, fmt.Errorf("allocation %d: category %s: %w", i+1, a.CategoryID, err)
		}
		lines[i] = model.AllocationLine{Category: category, Amount: a.Amount}
	}
	return base, lines, nil
}

// today returns today: the day the clock says it is, in the timezone of the
// application, as a date at midnight UTC.
func (s *Service) today() time.Time {
	return model.DateOf(s.clock.Now())
}

func toAllocations(allocations []model.Allocation) []usecase.Allocation {
	out := make([]usecase.Allocation, len(allocations))
	for i, a := range allocations {
		out[i] = usecase.Allocation{CategoryID: a.CategoryID, Amount: a.Amount}
	}
	return out
}
