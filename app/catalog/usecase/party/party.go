// Package party implements the party use cases: create, rename, archive, list and
// get.
package party

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// Service implements usecase.CreateParty, RenameParty, ArchiveParty, ListParties
// and GetParty.
type Service struct {
	repo repo.PartyRepo
}

var (
	_ usecase.CreateParty  = (*Service)(nil)
	_ usecase.RenameParty  = (*Service)(nil)
	_ usecase.ArchiveParty = (*Service)(nil)
	_ usecase.ListParties  = (*Service)(nil)
	_ usecase.GetParty     = (*Service)(nil)
)

// CreateParty validates the name and stores the party.
func (s *Service) CreateParty(ctx context.Context, in usecase.CreatePartyInput) (usecase.Party, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Party{}, fmt.Errorf("generate party id: %w", err)
	}
	party, err := model.NewParty(id, in.Name)
	if err != nil {
		return usecase.Party{}, err
	}
	stored, err := s.repo.Insert(ctx, party)
	if err != nil {
		return usecase.Party{}, fmt.Errorf("create party %q: %w", party.Name, err)
	}
	return toParty(stored), nil
}

// RenameParty validates the new name and stores it.
func (s *Service) RenameParty(ctx context.Context, in usecase.RenamePartyInput) (usecase.Party, error) {
	party, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return usecase.Party{}, fmt.Errorf("rename party %s: %w", in.ID, err)
	}
	if err := party.Rename(in.Name); err != nil {
		return usecase.Party{}, err
	}
	stored, err := s.repo.Rename(ctx, in.ID, party.Name)
	if err != nil {
		return usecase.Party{}, fmt.Errorf("rename party %s: %w", in.ID, err)
	}
	return toParty(stored), nil
}

// ArchiveParty archives the party. An archived party stays archived: asking again
// returns it as it is.
func (s *Service) ArchiveParty(ctx context.Context, id uuid.UUID) (usecase.Party, error) {
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Party{}, fmt.Errorf("archive party %s: %w", id, err)
	}
	return toParty(stored), nil
}

// ListParties returns the parties by name.
func (s *Service) ListParties(ctx context.Context, in usecase.ListPartiesInput) (usecase.ListPartiesOutput, error) {
	parties, err := s.repo.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListPartiesOutput{}, fmt.Errorf("list parties: %w", err)
	}
	out := usecase.ListPartiesOutput{Parties: make([]usecase.Party, 0, len(parties))}
	for _, party := range parties {
		out.Parties = append(out.Parties, toParty(party))
	}
	return out, nil
}

// GetParty returns the party, archived or not.
func (s *Service) GetParty(ctx context.Context, id uuid.UUID) (usecase.Party, error) {
	party, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Party{}, fmt.Errorf("get party %s: %w", id, err)
	}
	return toParty(party), nil
}

func toParty(p model.Party) usecase.Party {
	return usecase.Party{
		ID:         p.ID,
		Name:       p.Name,
		CreatedAt:  p.CreatedAt,
		ArchivedAt: p.ArchivedAt,
	}
}
