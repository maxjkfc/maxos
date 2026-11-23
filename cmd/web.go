package cmd

import (
	"log/slog"

	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	financeweb "github.com/maxjkfc/maxos/app/finance/adapter/in/web"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// webRegistrarGroup is the Fx value group that collects the page registrar of every
// context, as registrarGroup does for the MCP tools. Adding a context's pages to the
// site means providing its registrar into this group.
const webRegistrarGroup = `group:"web.registrars"`

// WebModule provides the registry of the pages under /app/, fed by the registrar of
// every context that has pages. The pages call the use cases of the contexts, so the
// app must include CatalogModule and FinanceModule too; they tell today by the clock
// that InfraModule provides.
func WebModule() fx.Option {
	return fx.Provide(
		fx.Annotate(newFinanceWebRegistrar, fx.ResultTags(webRegistrarGroup)),
		fx.Annotate(newWebRegistry, fx.ParamTags(``, webRegistrarGroup)),
	)
}

func newWebRegistry(logger *slog.Logger, registrars []webserver.Registrar) *webserver.Registry {
	return webserver.NewRegistry(logger, registrars)
}

// financeWebPorts is what the pages of the finance context call, as Fx fills it: the
// use cases of the finance and catalog contexts, and the clock. The adapter takes
// the same list as a plain struct (financeweb.Ports), so that it does not know Fx.
type financeWebPorts struct {
	fx.In

	Clock financeclock.Clock

	GetPreviewBalances  usecase.GetPreviewBalances
	GetDealings         usecase.GetDealings
	ListTransactions    usecase.ListTransactions
	GetTransaction      usecase.GetTransaction
	GetInstalmentPlan   usecase.GetInstalmentPlan
	ListInstalmentPlans usecase.ListInstalmentPlans
	GetCardStatement    usecase.GetCardStatement
	ListCardStatements  usecase.ListCardStatements
	GetAreaBudgets      usecase.GetAreaBudgets
	GetProjectBudgets   usecase.GetProjectBudgets
	GetSpending         usecase.GetSpending
	GetSpendingTrend    usecase.GetSpendingTrend
	GetDraftSummary     usecase.GetDraftSummary

	ListAccounts     usecase.ListAccounts
	ListInstitutions usecase.ListInstitutions
	ListInstruments  usecase.ListInstruments
	ListCategories   usecase.ListCategories

	ListAreas    catalogusecase.ListAreas
	ListProjects catalogusecase.ListProjects
	ListParties  catalogusecase.ListParties
	ListTags     catalogusecase.ListTags
}

func newFinanceWebRegistrar(p financeWebPorts) webserver.Registrar {
	return financeweb.NewRegistrar(financeweb.Ports{
		Clock:               p.Clock,
		GetPreviewBalances:  p.GetPreviewBalances,
		GetDealings:         p.GetDealings,
		ListTransactions:    p.ListTransactions,
		GetTransaction:      p.GetTransaction,
		GetInstalmentPlan:   p.GetInstalmentPlan,
		ListInstalmentPlans: p.ListInstalmentPlans,
		GetCardStatement:    p.GetCardStatement,
		ListCardStatements:  p.ListCardStatements,
		GetAreaBudgets:      p.GetAreaBudgets,
		GetProjectBudgets:   p.GetProjectBudgets,
		GetSpending:         p.GetSpending,
		GetSpendingTrend:    p.GetSpendingTrend,
		GetDraftSummary:     p.GetDraftSummary,
		ListAccounts:        p.ListAccounts,
		ListInstitutions:    p.ListInstitutions,
		ListInstruments:     p.ListInstruments,
		ListCategories:      p.ListCategories,
		ListAreas:           p.ListAreas,
		ListProjects:        p.ListProjects,
		ListParties:         p.ListParties,
		ListTags:            p.ListTags,
	})
}
