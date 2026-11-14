package party

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// New returns the party use cases.
func New(r repo.PartyRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateParty,
// RenameParty, ArchiveParty, ListParties and GetParty, all backed by one
// Service. It needs a repo.PartyRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateParty)),
		fx.As(new(usecase.RenameParty)),
		fx.As(new(usecase.ArchiveParty)),
		fx.As(new(usecase.ListParties)),
		fx.As(new(usecase.GetParty)),
	))
}
