// Package category implements the category use cases: create, archive, list and
// get.
package category

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// Service implements usecase.CreateCategory, ArchiveCategory, ListCategories and
// GetCategory.
type Service struct {
	repo repo.CategoryRepo
}

var (
	_ usecase.CreateCategory  = (*Service)(nil)
	_ usecase.ArchiveCategory = (*Service)(nil)
	_ usecase.ListCategories  = (*Service)(nil)
	_ usecase.GetCategory     = (*Service)(nil)
)

// CreateCategory checks the parent, if there is one, validates the category and
// stores it.
func (s *Service) CreateCategory(ctx context.Context, in usecase.CreateCategoryInput) (usecase.Category, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Category{}, fmt.Errorf("generate category id: %w", err)
	}
	var parent *model.Category
	if in.ParentID != nil {
		found, err := s.repo.Get(ctx, *in.ParentID)
		if err != nil {
			return usecase.Category{}, fmt.Errorf("parent category %s: %w", *in.ParentID, err)
		}
		if found.IsArchived() {
			return usecase.Category{}, fmt.Errorf("parent category %s: %w", found.ID, model.ErrArchived)
		}
		parent = &found
	}
	category, err := model.NewCategory(model.NewCategoryParams{
		ID:        id,
		Direction: model.Direction(in.Direction),
		Name:      in.Name,
		Parent:    parent,
	})
	if err != nil {
		return usecase.Category{}, err
	}
	stored, err := s.repo.Insert(ctx, category)
	if err != nil {
		return usecase.Category{}, fmt.Errorf("create category %q: %w", category.Name, err)
	}
	return toCategory(stored), nil
}

// ArchiveCategory archives the category; the repo archives its subcategories
// with it. An archived category stays archived: asking again returns it as it
// is.
func (s *Service) ArchiveCategory(ctx context.Context, id uuid.UUID) (usecase.Category, error) {
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Category{}, fmt.Errorf("archive category %s: %w", id, err)
	}
	return toCategory(stored), nil
}

// ListCategories returns the categories by direction and name.
func (s *Service) ListCategories(ctx context.Context, in usecase.ListCategoriesInput) (usecase.ListCategoriesOutput, error) {
	direction := model.Direction(in.Direction)
	if in.Direction != "" && !direction.Valid() {
		return usecase.ListCategoriesOutput{}, fmt.Errorf("%w %q: want expense or income", model.ErrInvalidDirection, in.Direction)
	}
	categories, err := s.repo.List(ctx, direction, in.IncludeArchived)
	if err != nil {
		return usecase.ListCategoriesOutput{}, fmt.Errorf("list categories: %w", err)
	}
	out := usecase.ListCategoriesOutput{Categories: make([]usecase.Category, 0, len(categories))}
	for _, category := range categories {
		out.Categories = append(out.Categories, toCategory(category))
	}
	return out, nil
}

// GetCategory returns the category, archived or not.
func (s *Service) GetCategory(ctx context.Context, id uuid.UUID) (usecase.Category, error) {
	category, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Category{}, fmt.Errorf("get category %s: %w", id, err)
	}
	return toCategory(category), nil
}

func toCategory(c model.Category) usecase.Category {
	return usecase.Category{
		ID:         c.ID,
		Direction:  string(c.Direction),
		ParentID:   c.ParentID,
		Name:       c.Name,
		CreatedAt:  c.CreatedAt,
		ArchivedAt: c.ArchivedAt,
	}
}
