package budget

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// GetAreaBudgets returns where each area stands in the month the request names.
// The budget of an area in the month is the one in force in it, which is for the
// domain to pick from all the budgets there are (model.BudgetsInForce). What was
// spent is what the repo adds up for the days of the month, up to today and no
// further: the days of a month that has not begun are not asked for at all, and a
// month that has ended is asked for to its last day.
//
// The areas are those that have a budget in force or spending, which the catalog
// puts in order by name, so that an area it does not list is an error and not
// guessed at.
func (s *Service) GetAreaBudgets(ctx context.Context, in usecase.GetAreaBudgetsInput) (usecase.GetAreaBudgetsOutput, error) {
	if in.Month.IsZero() {
		return usecase.GetAreaBudgetsOutput{}, model.ErrMonthRequired
	}
	month := model.MonthOf(in.Month)

	budgets, err := s.budgets.AreaBudgets(ctx)
	if err != nil {
		return usecase.GetAreaBudgetsOutput{}, fmt.Errorf("get the budgets of the areas: %w", err)
	}
	inForce := model.BudgetsInForce(budgets, month)

	through := model.LastDayOf(month)
	if today := s.today(); today.Before(through) {
		through = today
	}
	var spending []model.AreaSpending
	if !through.Before(month) {
		if spending, err = s.budgets.AreaSpending(ctx, month, through); err != nil {
			return usecase.GetAreaBudgetsOutput{}, fmt.Errorf("get what the areas spent in %s: %w", month.Format("2006-01"), err)
		}
	}

	spendingOf := make(map[uuid.UUID][]model.AreaSpending)
	areas := make(map[uuid.UUID]struct{}, len(inForce))
	for areaID := range inForce {
		areas[areaID] = struct{}{}
	}
	for _, sp := range spending {
		spendingOf[sp.AreaID] = append(spendingOf[sp.AreaID], sp)
		areas[sp.AreaID] = struct{}{}
	}

	ordered, err := s.areasInOrder(ctx, areas)
	if err != nil {
		return usecase.GetAreaBudgetsOutput{}, err
	}
	out := usecase.GetAreaBudgetsOutput{Month: month, Areas: make([]usecase.AreaBudgetStatus, len(ordered))}
	for i, areaID := range ordered {
		var budget *model.AreaBudget
		if b, ok := inForce[areaID]; ok {
			budget = &b
		}
		out.Areas[i] = areaStatus(areaID, budget, spendingOf[areaID])
	}
	return out, nil
}

// areaStatus works out where an area stands: what it spent against its budget, if
// it has one. What a project spent in the area is not compared with the budget of
// the area, nor does it fall into any allocation of it; it is only added to what
// the area spent altogether.
func areaStatus(areaID uuid.UUID, budget *model.AreaBudget, spending []model.AreaSpending) usecase.AreaBudgetStatus {
	var (
		amount      *decimal.Decimal
		allocations []model.Allocation
	)
	if budget != nil {
		amount, allocations = &budget.Amount, budget.Allocations
	}
	outsideProjects := make([]model.Spending, 0, len(spending))
	withProjects := decimal.Zero
	for _, sp := range spending {
		withProjects = withProjects.Add(sp.Amount)
		if !sp.InProject {
			outsideProjects = append(outsideProjects, sp.Spending)
		}
	}
	status := model.NewBudgetStatus(amount, allocations, outsideProjects)
	return usecase.AreaBudgetStatus{
		AreaID:            areaID,
		Budget:            status.Budget,
		Spent:             status.Spent,
		SpentWithProjects: withProjects,
		Allocations:       toAllocationStatuses(status.Allocations),
		Unallocated:       status.Unallocated,
		Over:              status.Over,
	}
}

// GetProjectBudgets returns where each project stands: against its budget, if it
// has one, with all it has spent that has happened by today. For one project
// (ProjectID) there is nothing to put in order, so the catalog is asked about that
// project and not for the list of all of them; a project it does not know is
// model.ErrNotFound, whatever the repo holds, and the repo is not asked. An
// archived project is not one: what it spent still counts.
func (s *Service) GetProjectBudgets(ctx context.Context, in usecase.GetProjectBudgetsInput) (usecase.GetProjectBudgetsOutput, error) {
	if in.ProjectID != nil {
		if err := s.requireProject(ctx, *in.ProjectID); err != nil {
			return usecase.GetProjectBudgetsOutput{}, err
		}
	}

	budgets, err := s.budgets.ProjectBudgets(ctx, in.ProjectID)
	if err != nil {
		return usecase.GetProjectBudgetsOutput{}, fmt.Errorf("get the budgets of the projects: %w", err)
	}
	spending, err := s.budgets.ProjectSpending(ctx, in.ProjectID, s.today())
	if err != nil {
		return usecase.GetProjectBudgetsOutput{}, fmt.Errorf("get what the projects spent: %w", err)
	}

	budgetOf := make(map[uuid.UUID]model.ProjectBudget, len(budgets))
	projects := make(map[uuid.UUID]struct{}, len(budgets))
	for _, b := range budgets {
		budgetOf[b.ProjectID] = b
		projects[b.ProjectID] = struct{}{}
	}
	spendingOf := make(map[uuid.UUID][]model.Spending)
	for _, sp := range spending {
		spendingOf[sp.ProjectID] = append(spendingOf[sp.ProjectID], sp.Spending)
		projects[sp.ProjectID] = struct{}{}
	}

	var ordered []uuid.UUID
	if in.ProjectID != nil {
		ordered = slices.Collect(maps.Keys(projects)) // the one project, or none
	} else if ordered, err = s.projectsInOrder(ctx, projects); err != nil {
		return usecase.GetProjectBudgetsOutput{}, err
	}
	out := usecase.GetProjectBudgetsOutput{Projects: make([]usecase.ProjectBudgetStatus, len(ordered))}
	for i, projectID := range ordered {
		var (
			amount      *decimal.Decimal
			allocations []model.Allocation
		)
		if b, ok := budgetOf[projectID]; ok {
			amount, allocations = &b.Amount, b.Allocations
		}
		status := model.NewBudgetStatus(amount, allocations, spendingOf[projectID])
		out.Projects[i] = usecase.ProjectBudgetStatus{
			ProjectID:   projectID,
			Budget:      status.Budget,
			Spent:       status.Spent,
			Allocations: toAllocationStatuses(status.Allocations),
			Unallocated: status.Unallocated,
			Over:        status.Over,
		}
	}
	return out, nil
}

// requireProject checks with the catalog that the project exists. It is the one
// check of a project that lets an archived one through: what it spent still
// counts. A project the catalog does not know is model.ErrNotFound; the catalog's
// own *ReferenceError is not what the callers of the ledger can be expected to
// match. Any other failure of the catalog is returned as it is, with what was being
// done.
func (s *Service) requireProject(ctx context.Context, id uuid.UUID) error {
	err := s.references.VerifyReferences(ctx, catalogusecase.VerifyReferencesInput{ProjectIDs: []uuid.UUID{id}})
	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) {
		if err != nil {
			return fmt.Errorf("check project %s: %w", id, err)
		}
		return nil
	}
	for _, problem := range refErr.Problems {
		if !problem.Archived {
			return fmt.Errorf("project %s: %w", id, model.ErrNotFound)
		}
	}
	return nil
}

// areasInOrder returns the areas in the order the catalog lists them, by name,
// the archived ones too: the budget and the spending of an archived area still
// count. It does not ask the catalog for no area.
func (s *Service) areasInOrder(ctx context.Context, wanted map[uuid.UUID]struct{}) ([]uuid.UUID, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	listed, err := s.listAreas.ListAreas(ctx, catalogusecase.ListAreasInput{IncludeArchived: true})
	if err != nil {
		return nil, fmt.Errorf("list areas: %w", err)
	}
	return inListedOrder(wanted, listed.Areas, func(a catalogusecase.Area) uuid.UUID { return a.ID }, "area")
}

// projectsInOrder returns the projects in the order the catalog lists them, by
// name, the archived ones too. It does not ask the catalog for no project.
func (s *Service) projectsInOrder(ctx context.Context, wanted map[uuid.UUID]struct{}) ([]uuid.UUID, error) {
	if len(wanted) == 0 {
		return nil, nil
	}
	listed, err := s.listProjects.ListProjects(ctx, catalogusecase.ListProjectsInput{IncludeArchived: true})
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return inListedOrder(wanted, listed.Projects, func(p catalogusecase.Project) uuid.UUID { return p.ID }, "project")
}

// inListedOrder returns the wanted ids in the order the entries are listed in. It
// fails for a wanted id that no entry has, naming the one with the lowest id and
// what kind of entry it is: a budget or spending that belongs to something the
// catalog does not know is not put anywhere by guessing.
func inListedOrder[T any](wanted map[uuid.UUID]struct{}, listed []T, idOf func(T) uuid.UUID, what string) ([]uuid.UUID, error) {
	ordered := make([]uuid.UUID, 0, len(wanted))
	missing := maps.Clone(wanted)
	for _, entry := range listed {
		id := idOf(entry)
		if _, ok := missing[id]; ok {
			ordered = append(ordered, id)
			delete(missing, id)
		}
	}
	if len(missing) > 0 {
		first := slices.MinFunc(slices.Collect(maps.Keys(missing)), func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		return nil, fmt.Errorf("%s %s: %w", what, first, model.ErrNotFound)
	}
	return ordered, nil
}

func toAllocationStatuses(statuses []model.AllocationStatus) []usecase.AllocationStatus {
	out := make([]usecase.AllocationStatus, len(statuses))
	for i, a := range statuses {
		out[i] = usecase.AllocationStatus{CategoryID: a.CategoryID, Amount: a.Amount, Spent: a.Spent}
	}
	return out
}
