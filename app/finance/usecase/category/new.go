package category

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// New returns the category use cases.
func New(r repo.CategoryRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateCategory,
// ArchiveCategory, ListCategories and GetCategory, all backed by one Service. It
// needs a repo.CategoryRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateCategory)),
		fx.As(new(usecase.ArchiveCategory)),
		fx.As(new(usecase.ListCategories)),
		fx.As(new(usecase.GetCategory)),
	))
}
