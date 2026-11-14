// Package instrument implements the instrument use cases: create, archive, list
// and get.
package instrument

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// Service implements usecase.CreateInstrument, ArchiveInstrument,
// ListInstruments and GetInstrument.
type Service struct {
	repo repo.InstrumentRepo
}

var (
	_ usecase.CreateInstrument  = (*Service)(nil)
	_ usecase.ArchiveInstrument = (*Service)(nil)
	_ usecase.ListInstruments   = (*Service)(nil)
	_ usecase.GetInstrument     = (*Service)(nil)
)

// CreateInstrument validates the instrument and stores it.
func (s *Service) CreateInstrument(ctx context.Context, in usecase.CreateInstrumentInput) (usecase.Instrument, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Instrument{}, fmt.Errorf("generate instrument id: %w", err)
	}
	instrument, err := model.NewInstrument(model.NewInstrumentParams{
		ID:         id,
		Code:       in.Code,
		Name:       in.Name,
		Kind:       model.InstrumentKind(in.Kind),
		IsCurrency: in.IsCurrency,
		Decimals:   in.Decimals,
	})
	if err != nil {
		return usecase.Instrument{}, err
	}
	stored, err := s.repo.Insert(ctx, instrument)
	if err != nil {
		return usecase.Instrument{}, fmt.Errorf("create instrument %q: %w", instrument.Code, err)
	}
	return toInstrument(stored), nil
}

// ArchiveInstrument archives the instrument, unless it is the base currency. An
// archived instrument stays archived: asking again returns it as it is.
func (s *Service) ArchiveInstrument(ctx context.Context, id uuid.UUID) (usecase.Instrument, error) {
	instrument, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Instrument{}, fmt.Errorf("archive instrument %s: %w", id, err)
	}
	if err := instrument.CheckArchivable(); err != nil {
		return usecase.Instrument{}, err
	}
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Instrument{}, fmt.Errorf("archive instrument %s: %w", id, err)
	}
	return toInstrument(stored), nil
}

// ListInstruments returns the instruments by code.
func (s *Service) ListInstruments(ctx context.Context, in usecase.ListInstrumentsInput) (usecase.ListInstrumentsOutput, error) {
	instruments, err := s.repo.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListInstrumentsOutput{}, fmt.Errorf("list instruments: %w", err)
	}
	out := usecase.ListInstrumentsOutput{Instruments: make([]usecase.Instrument, 0, len(instruments))}
	for _, instrument := range instruments {
		out.Instruments = append(out.Instruments, toInstrument(instrument))
	}
	return out, nil
}

// GetInstrument returns the instrument, archived or not.
func (s *Service) GetInstrument(ctx context.Context, id uuid.UUID) (usecase.Instrument, error) {
	instrument, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Instrument{}, fmt.Errorf("get instrument %s: %w", id, err)
	}
	return toInstrument(instrument), nil
}

func toInstrument(i model.Instrument) usecase.Instrument {
	return usecase.Instrument{
		ID:         i.ID,
		Code:       i.Code,
		Name:       i.Name,
		Kind:       string(i.Kind),
		IsCurrency: i.IsCurrency,
		Decimals:   i.Decimals,
		CreatedAt:  i.CreatedAt,
		ArchivedAt: i.ArchivedAt,
	}
}
