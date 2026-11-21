package budget

import (
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
)

// New returns the budget use cases. They need the repo of the budgets, which also
// adds up what was spent, the repos of the categories and of the instruments to
// check the allocations and the amounts, the catalog to check the areas and
// projects that budgets are set for and to put the ones it reads in the order the
// catalog lists them, a transactor to keep what they write together, and a
// clock, which is the only way they tell what today is.
func New(
	transactor tx.Transactor,
	budgets repo.BudgetRepo,
	categories repo.CategoryRepo,
	instruments repo.InstrumentRepo,
	references catalogusecase.VerifyReferences,
	listAreas catalogusecase.ListAreas,
	listProjects catalogusecase.ListProjects,
	clk clock.Clock,
) *Service {
	return &Service{
		tx:           transactor,
		budgets:      budgets,
		categories:   categories,
		instruments:  instruments,
		references:   references,
		listAreas:    listAreas,
		listProjects: listProjects,
		clock:        clk,
	}
}

// Module provides the use cases to the Fx graph as usecase.SetAreaBudget,
// SetProjectBudget, RemoveProjectBudget, GetAreaBudgets and GetProjectBudgets, all
// backed by one Service. It needs a tx.Transactor, a repo.BudgetRepo, a
// repo.CategoryRepo, a repo.InstrumentRepo, a clock.Clock, and the catalog's
// usecase.VerifyReferences, usecase.ListAreas and usecase.ListProjects.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.SetAreaBudget)),
		fx.As(new(usecase.SetProjectBudget)),
		fx.As(new(usecase.RemoveProjectBudget)),
		fx.As(new(usecase.GetAreaBudgets)),
		fx.As(new(usecase.GetProjectBudgets)),
	))
}
