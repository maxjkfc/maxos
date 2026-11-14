package project

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// New returns the project use cases. They need the areas to check the default
// area of a project.
func New(projects repo.ProjectRepo, areas repo.AreaRepo) *Service {
	return &Service{projects: projects, areas: areas}
}

// Module provides the use cases to the Fx graph as usecase.CreateProject,
// RenameProject, ArchiveProject, ListProjects, GetProject, SetProjectPeriod,
// SetProjectActivePeriod, SetProjectDefaultArea and ResolveProjectDefaults, all
// backed by one Service. It needs a repo.ProjectRepo and a repo.AreaRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateProject)),
		fx.As(new(usecase.RenameProject)),
		fx.As(new(usecase.ArchiveProject)),
		fx.As(new(usecase.ListProjects)),
		fx.As(new(usecase.GetProject)),
		fx.As(new(usecase.SetProjectPeriod)),
		fx.As(new(usecase.SetProjectActivePeriod)),
		fx.As(new(usecase.SetProjectDefaultArea)),
		fx.As(new(usecase.ResolveProjectDefaults)),
	))
}
