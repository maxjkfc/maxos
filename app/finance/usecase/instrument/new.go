package instrument

import (
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// New returns the instrument use cases.
func New(r repo.InstrumentRepo) *Service {
	return &Service{repo: r}
}

// Module provides the use cases to the Fx graph as usecase.CreateInstrument,
// ArchiveInstrument, ListInstruments and GetInstrument, all backed by one
// Service. It needs a repo.InstrumentRepo.
func Module() fx.Option {
	return fx.Provide(fx.Annotate(New,
		fx.As(new(usecase.CreateInstrument)),
		fx.As(new(usecase.ArchiveInstrument)),
		fx.As(new(usecase.ListInstruments)),
		fx.As(new(usecase.GetInstrument)),
	))
}
