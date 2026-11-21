// Package web is the inbound web adapter of the finance context: the pages that show
// what the ledger holds, for reading. It only turns a request into a call of a use
// case and the answer into a page: the figures are the use cases', and what a page
// adds is how they are laid out, named and written. It depends on the in-ports of
// the finance context, of the catalog for the names of areas, projects and parties,
// and on pkg/webserver, which serves the pages behind the guard.
//
// Every page is read-only, and issues a fixed number of calls whatever number of
// rows it shows: the lists that name what a row points at are read once each, and
// the rows themselves come from one call.
package web

import (
	"embed"
	"time"

	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

//go:embed templates/*.gohtml
var templateFS embed.FS

// Ports are the use cases the pages call, and the clock that says what day it is.
// Fx fills it.
type Ports struct {
	fx.In

	// Clock is the only source of today: it is what makes a month "this month", and
	// the timezone it is read in is the one dates are shown in.
	Clock clock.Clock

	GetBalances         usecase.GetBalances
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

// handlers are the pages, which share the use cases they call.
type handlers struct {
	p Ports
}

// NewRegistrar returns the registrar that adds the pages of the finance context to
// the site: the overview, the transactions, the drafts, the statements of cards, the
// budgets and the spending.
func NewRegistrar(p Ports) webserver.Registrar {
	h := &handlers{p: p}
	return func(site *webserver.Site) {
		site.Templates(templateFS, "templates/*.gohtml")

		site.Nav(webserver.NavItem{Order: 10, Key: navOverview, Label: "總覽", Href: "/app/"})
		site.Nav(webserver.NavItem{Order: 20, Key: navTransactions, Label: "交易", Href: "/app/transactions"})
		site.Nav(webserver.NavItem{Order: 30, Key: navDrafts, Label: "草稿", Href: "/app/drafts"})
		site.Nav(webserver.NavItem{Order: 40, Key: navCards, Label: "信用卡帳單", Href: "/app/cards"})
		site.Nav(webserver.NavItem{Order: 50, Key: navBudgets, Label: "預算", Href: "/app/budgets"})
		site.Nav(webserver.NavItem{Order: 60, Key: navSpending, Label: "支出", Href: "/app/spending"})

		site.Get("/app/{$}", h.overview)
		site.Get("/app/transactions", h.transactions)
		site.Get("/app/transactions/{id}", h.transaction)
		site.Get("/app/drafts", h.drafts)
		site.Get("/app/cards", h.cards)
		site.Get("/app/cards/{account}", h.card)
		site.Get("/app/budgets", h.budgets)
		site.Get("/app/spending", h.spending)
	}
}

// The keys of the navigation.
const (
	navOverview     = "overview"
	navTransactions = "transactions"
	navDrafts       = "drafts"
	navCards        = "cards"
	navBudgets      = "budgets"
	navSpending     = "spending"
)

// today is the day it is for the user, as a date at midnight UTC.
func (h *handlers) today() time.Time {
	return model.DateOf(h.p.Clock.Now())
}

// location is the timezone of the user, which moments are shown in.
func (h *handlers) location() *time.Location {
	return h.p.Clock.Now().Location()
}
