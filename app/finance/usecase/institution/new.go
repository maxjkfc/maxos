package institution

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// New returns the institution use cases.
func New(r repo.InstitutionRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateInstitution,
// ArchiveInstitution, ListInstitutions and GetInstitution, all backed by one
// Service. It needs a repo.InstitutionRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateInstitution)),
		fx.As(new(usecase.ArchiveInstitution)),
		fx.As(new(usecase.ListInstitutions)),
		fx.As(new(usecase.GetInstitution)),
	))
}
