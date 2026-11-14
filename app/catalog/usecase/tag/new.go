package tag

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// New returns the tag use cases.
func New(r repo.TagRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateTag, RenameTag,
// ArchiveTag, ListTags and GetTag, all backed by one Service. It needs a
// repo.TagRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateTag)),
		fx.As(new(usecase.RenameTag)),
		fx.As(new(usecase.ArchiveTag)),
		fx.As(new(usecase.ListTags)),
		fx.As(new(usecase.GetTag)),
	))
}
