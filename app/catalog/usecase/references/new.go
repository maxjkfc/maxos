package references

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// New returns the reference check.
func New(projects repo.ProjectRepo, areas repo.AreaRepo, parties repo.PartyRepo, tags repo.TagRepo) *Service {
	return &Service{projects: projects, areas: areas, parties: parties, tags: tags}
}

// Module provides the use case to the Fx graph as usecase.VerifyReferences. It
// needs a repo.ProjectRepo, repo.AreaRepo, repo.PartyRepo and repo.TagRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New, fx.As(new(usecase.VerifyReferences))))
}
