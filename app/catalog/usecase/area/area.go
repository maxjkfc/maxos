// Package area implements the area use cases: create, rename, archive, list and
// get.
package area

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// Service implements usecase.CreateArea, RenameArea, ArchiveArea, ListAreas and
// GetArea.
type Service struct {
	repo repo.AreaRepo
}

var (
	_ usecase.CreateArea  = (*Service)(nil)
	_ usecase.RenameArea  = (*Service)(nil)
	_ usecase.ArchiveArea = (*Service)(nil)
	_ usecase.ListAreas   = (*Service)(nil)
	_ usecase.GetArea     = (*Service)(nil)
)

// CreateArea validates the name and stores the area.
func (s *Service) CreateArea(ctx context.Context, in usecase.CreateAreaInput) (usecase.Area, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Area{}, fmt.Errorf("generate area id: %w", err)
	}
	area, err := model.NewArea(id, in.Name)
	if err != nil {
		return usecase.Area{}, err
	}
	stored, err := s.repo.Insert(ctx, area)
	if err != nil {
		return usecase.Area{}, fmt.Errorf("create area %q: %w", area.Name, err)
	}
	return toArea(stored), nil
}

// RenameArea validates the new name and stores it.
func (s *Service) RenameArea(ctx context.Context, in usecase.RenameAreaInput) (usecase.Area, error) {
	area, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return usecase.Area{}, fmt.Errorf("rename area %s: %w", in.ID, err)
	}
	if err := area.Rename(in.Name); err != nil {
		return usecase.Area{}, err
	}
	stored, err := s.repo.Rename(ctx, in.ID, area.Name)
	if err != nil {
		return usecase.Area{}, fmt.Errorf("rename area %s: %w", in.ID, err)
	}
	return toArea(stored), nil
}

// ArchiveArea archives the area. An archived area stays archived: asking again
// returns it as it is.
func (s *Service) ArchiveArea(ctx context.Context, id uuid.UUID) (usecase.Area, error) {
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Area{}, fmt.Errorf("archive area %s: %w", id, err)
	}
	return toArea(stored), nil
}

// ListAreas returns the areas by name.
func (s *Service) ListAreas(ctx context.Context, in usecase.ListAreasInput) (usecase.ListAreasOutput, error) {
	areas, err := s.repo.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListAreasOutput{}, fmt.Errorf("list areas: %w", err)
	}
	out := usecase.ListAreasOutput{Areas: make([]usecase.Area, 0, len(areas))}
	for _, area := range areas {
		out.Areas = append(out.Areas, toArea(area))
	}
	return out, nil
}

// GetArea returns the area, archived or not.
func (s *Service) GetArea(ctx context.Context, id uuid.UUID) (usecase.Area, error) {
	area, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Area{}, fmt.Errorf("get area %s: %w", id, err)
	}
	return toArea(area), nil
}

func toArea(a model.Area) usecase.Area {
	return usecase.Area{
		ID:         a.ID,
		Name:       a.Name,
		CreatedAt:  a.CreatedAt,
		ArchivedAt: a.ArchivedAt,
	}
}
