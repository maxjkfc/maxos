// Package institution implements the institution use cases: create, archive,
// list and get.
package institution

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// Service implements usecase.CreateInstitution, ArchiveInstitution,
// ListInstitutions and GetInstitution.
type Service struct {
	repo repo.InstitutionRepo
}

var (
	_ usecase.CreateInstitution  = (*Service)(nil)
	_ usecase.ArchiveInstitution = (*Service)(nil)
	_ usecase.ListInstitutions   = (*Service)(nil)
	_ usecase.GetInstitution     = (*Service)(nil)
)

// CreateInstitution validates the name and stores the institution.
func (s *Service) CreateInstitution(ctx context.Context, in usecase.CreateInstitutionInput) (usecase.Institution, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Institution{}, fmt.Errorf("generate institution id: %w", err)
	}
	institution, err := model.NewInstitution(id, in.Name)
	if err != nil {
		return usecase.Institution{}, err
	}
	stored, err := s.repo.Insert(ctx, institution)
	if err != nil {
		return usecase.Institution{}, fmt.Errorf("create institution %q: %w", institution.Name, err)
	}
	return toInstitution(stored), nil
}

// ArchiveInstitution archives the institution. An archived institution stays
// archived: asking again returns it as it is.
func (s *Service) ArchiveInstitution(ctx context.Context, id uuid.UUID) (usecase.Institution, error) {
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Institution{}, fmt.Errorf("archive institution %s: %w", id, err)
	}
	return toInstitution(stored), nil
}

// ListInstitutions returns the institutions by name.
func (s *Service) ListInstitutions(ctx context.Context, in usecase.ListInstitutionsInput) (usecase.ListInstitutionsOutput, error) {
	institutions, err := s.repo.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListInstitutionsOutput{}, fmt.Errorf("list institutions: %w", err)
	}
	out := usecase.ListInstitutionsOutput{Institutions: make([]usecase.Institution, 0, len(institutions))}
	for _, institution := range institutions {
		out.Institutions = append(out.Institutions, toInstitution(institution))
	}
	return out, nil
}

// GetInstitution returns the institution, archived or not.
func (s *Service) GetInstitution(ctx context.Context, id uuid.UUID) (usecase.Institution, error) {
	institution, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Institution{}, fmt.Errorf("get institution %s: %w", id, err)
	}
	return toInstitution(institution), nil
}

func toInstitution(i model.Institution) usecase.Institution {
	return usecase.Institution{
		ID:         i.ID,
		Name:       i.Name,
		CreatedAt:  i.CreatedAt,
		ArchivedAt: i.ArchivedAt,
	}
}
