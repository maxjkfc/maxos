package account

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// New returns the account use cases.
func New(accounts repo.AccountRepo, institutions repo.InstitutionRepo) *Service {
	return &Service{accounts: accounts, institutions: institutions}
}

// Module provides the use cases to the Fx graph as usecase.CreateAccount,
// ArchiveAccount, ListAccounts and GetAccount, all backed by one Service. It
// needs a repo.AccountRepo and a repo.InstitutionRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateAccount)),
		fx.As(new(usecase.ArchiveAccount)),
		fx.As(new(usecase.ListAccounts)),
		fx.As(new(usecase.GetAccount)),
	))
}
