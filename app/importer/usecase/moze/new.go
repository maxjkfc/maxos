// Package moze implements the use cases of the MOZE import: planning what an export
// comes to in the ledger, recording it as drafts, confirming it month by month, and
// checking the balances against the export.
//
// The ledger is used only through the in-ports of the finance and catalog contexts:
// the import makes reference data and records transactions with the use cases the
// ledger offers to everyone, so it cannot record anything that is not a draft, or
// that the ledger would refuse from anyone else.
package moze

import (
	"time"

	"go.uber.org/fx"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/rates"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/source"
)

// Inputs are the ports an import reads its files through.
type Inputs struct {
	fx.In

	Source  source.Source
	Mapping mapping.Store
	Rates   rates.Store
}

// Ledger is what the import calls: the finance and the catalog use cases.
type Ledger struct {
	fx.In

	CreateInstrument     finance.CreateInstrument
	ListInstruments      finance.ListInstruments
	CreateInstitution    finance.CreateInstitution
	ListInstitutions     finance.ListInstitutions
	CreateAccount        finance.CreateAccount
	ListAccounts         finance.ListAccounts
	CreateCategory       finance.CreateCategory
	ListCategories       finance.ListCategories
	RecordExpense        finance.RecordExpense
	RecordIncome         finance.RecordIncome
	RecordTransfer       finance.RecordTransfer
	RecordExchange       finance.RecordExchange
	RecordTrade          finance.RecordTrade
	RecordDealing        finance.RecordDealing
	RecordReward         finance.RecordReward
	RecordBillInstalment finance.RecordBillInstalment
	SetOpeningBalance    finance.SetOpeningBalance
	Reconcile            finance.Reconcile
	ConfirmTransactions  finance.ConfirmTransactions
	DiscardDraft         finance.DiscardDraft
	ListTransactions     finance.ListTransactions
	GetBalances          finance.GetBalances

	CreateArea    catalog.CreateArea
	ListAreas     catalog.ListAreas
	CreateProject catalog.CreateProject
	ListProjects  catalog.ListProjects
	CreateParty   catalog.CreateParty
	ListParties   catalog.ListParties
	CreateTag     catalog.CreateTag
	ListTags      catalog.ListTags
}

// Planner implements usecase.Plan. It needs no ledger, so a plan can be made with no
// database at all.
type Planner struct {
	inputs Inputs
}

// NewPlanner returns the plan use case.
func NewPlanner(in Inputs) *Planner {
	return &Planner{inputs: in}
}

// Importer implements usecase.Apply, Confirm, Verify and Status.
type Importer struct {
	inputs Inputs
	ledger Ledger
	// loc is where the user lives: the time of day of a row is read in it.
	loc *time.Location
}

// NewImporter returns the use cases that work on the ledger. loc is the timezone
// the times of the rows are in, which is the one of the application.
func NewImporter(in Inputs, ledger Ledger, loc *time.Location) *Importer {
	return &Importer{inputs: in, ledger: ledger, loc: loc}
}

var (
	_ usecase.Plan    = (*Planner)(nil)
	_ usecase.Apply   = (*Importer)(nil)
	_ usecase.Confirm = (*Importer)(nil)
	_ usecase.Verify  = (*Importer)(nil)
	_ usecase.Status  = (*Importer)(nil)
)

// Module provides the use cases to the Fx graph: usecase.Plan, which needs the
// source, mapping and rates ports, and usecase.Apply, Confirm, Verify and Status,
// which need the finance and catalog use cases too and the *time.Location of the
// application. Fx makes what is asked of it, so a command that only plans needs
// none of the ledger.
func Module() fx.Option {
	return fx.Provide(
		fx.Annotate(NewPlanner, fx.As(new(usecase.Plan))),
		fx.Annotate(NewImporter,
			fx.As(new(usecase.Apply)),
			fx.As(new(usecase.Confirm)),
			fx.As(new(usecase.Verify)),
			fx.As(new(usecase.Status)),
		),
	)
}
