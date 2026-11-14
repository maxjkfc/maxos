package listrecent

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

// New returns the ListRecent use case.
func New(r repo.InboxRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use case to the Fx graph as usecase.ListRecent. It needs
// a repo.InboxRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New, fx.As(new(usecase.ListRecent))))
}
