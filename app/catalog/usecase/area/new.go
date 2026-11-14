package area

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// New returns the area use cases.
func New(r repo.AreaRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateArea,
// RenameArea, ArchiveArea, ListAreas and GetArea, all backed by one Service. It
// needs a repo.AreaRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateArea)),
		fx.As(new(usecase.RenameArea)),
		fx.As(new(usecase.ArchiveArea)),
		fx.As(new(usecase.ListAreas)),
		fx.As(new(usecase.GetArea)),
	))
}
