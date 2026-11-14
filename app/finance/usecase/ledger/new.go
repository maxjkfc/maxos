package ledger

import (
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
)

// New returns the ledger use cases. They need the repos of what a transaction
// refers to, the catalog to check the projects, areas, parties and tags it
// names, to find the project of a day, to find the default area of a project and
// to list the parties (their names put the dealings in order), and a transactor
// to keep what they write together.
func New(
	transactor tx.Transactor,
	transactions repo.TransactionRepo,
	accounts repo.AccountRepo,
	instruments repo.InstrumentRepo,
	categories repo.CategoryRepo,
	references catalogusecase.VerifyReferences,
	defaults catalogusecase.ResolveProjectDefaults,
	projectAreas catalogusecase.ResolveProjectArea,
	listParties catalogusecase.ListParties,
) *Service {
	return &Service{
		tx:           transactor,
		transactions: transactions,
		accounts:     accounts,
		instruments:  instruments,
		categories:   categories,
		references:   references,
		defaults:     defaults,
		projectAreas: projectAreas,
		listParties:  listParties,
	}
}

// Module provides the use cases to the Fx graph as usecase.RecordExpense,
// RecordIncome, RecordTransfer, RecordExchange, RecordDealing, RecordReward,
// SetOpeningBalance, Reconcile, ConfirmTransactions, DiscardDraft,
// VoidTransaction, GetTransaction, ListTransactions, GetBalances and
// GetDealings, all backed by one Service. It needs a tx.Transactor, a
// repo.TransactionRepo, a repo.AccountRepo, a repo.InstrumentRepo, a
// repo.CategoryRepo, and the catalog's usecase.VerifyReferences,
// usecase.ResolveProjectDefaults, usecase.ResolveProjectArea and
// usecase.ListParties.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.RecordExpense)),
		fx.As(new(usecase.RecordIncome)),
		fx.As(new(usecase.RecordTransfer)),
		fx.As(new(usecase.RecordExchange)),
		fx.As(new(usecase.RecordDealing)),
		fx.As(new(usecase.RecordReward)),
		fx.As(new(usecase.SetOpeningBalance)),
		fx.As(new(usecase.Reconcile)),
		fx.As(new(usecase.ConfirmTransactions)),
		fx.As(new(usecase.DiscardDraft)),
		fx.As(new(usecase.VoidTransaction)),
		fx.As(new(usecase.GetTransaction)),
		fx.As(new(usecase.ListTransactions)),
		fx.As(new(usecase.GetBalances)),
		fx.As(new(usecase.GetDealings)),
	))
}
