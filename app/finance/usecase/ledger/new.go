package ledger

import (
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
)

// New returns the ledger use cases. They need the repos of what a transaction
// refers to, the catalog to check the projects, areas and tags it names, to find
// the project of a day and to find the default area of a project, and a
// transactor to keep what they write together.
func New(
	transactor tx.Transactor,
	transactions repo.TransactionRepo,
	accounts repo.AccountRepo,
	instruments repo.InstrumentRepo,
	categories repo.CategoryRepo,
	references catalogusecase.VerifyReferences,
	defaults catalogusecase.ResolveProjectDefaults,
	projects catalogusecase.GetProject,
	areas catalogusecase.GetArea,
) *Service {
	return &Service{
		tx:           transactor,
		transactions: transactions,
		accounts:     accounts,
		instruments:  instruments,
		categories:   categories,
		references:   references,
		defaults:     defaults,
		projects:     projects,
		areas:        areas,
	}
}

// Module provides the use cases to the Fx graph as usecase.RecordExpense,
// RecordIncome, RecordTransfer, RecordExchange, SetOpeningBalance,
// ConfirmTransactions, DiscardDraft, VoidTransaction, GetTransaction,
// ListTransactions and GetBalances, all backed by one Service. It needs a
// tx.Transactor, a repo.TransactionRepo, a repo.AccountRepo, a
// repo.InstrumentRepo, a repo.CategoryRepo, and the catalog's
// usecase.VerifyReferences, usecase.ResolveProjectDefaults, usecase.GetProject
// and usecase.GetArea.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.RecordExpense)),
		fx.As(new(usecase.RecordIncome)),
		fx.As(new(usecase.RecordTransfer)),
		fx.As(new(usecase.RecordExchange)),
		fx.As(new(usecase.SetOpeningBalance)),
		fx.As(new(usecase.ConfirmTransactions)),
		fx.As(new(usecase.DiscardDraft)),
		fx.As(new(usecase.VoidTransaction)),
		fx.As(new(usecase.GetTransaction)),
		fx.As(new(usecase.ListTransactions)),
		fx.As(new(usecase.GetBalances)),
	))
}
