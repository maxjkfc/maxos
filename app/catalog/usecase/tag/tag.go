// Package tag implements the tag use cases: create, rename, archive, list and
// get.
package tag

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
)

// Service implements usecase.CreateTag, RenameTag, ArchiveTag, ListTags and
// GetTag.
type Service struct {
	repo repo.TagRepo
}

var (
	_ usecase.CreateTag  = (*Service)(nil)
	_ usecase.RenameTag  = (*Service)(nil)
	_ usecase.ArchiveTag = (*Service)(nil)
	_ usecase.ListTags   = (*Service)(nil)
	_ usecase.GetTag     = (*Service)(nil)
)

// CreateTag validates the name and stores the tag.
func (s *Service) CreateTag(ctx context.Context, in usecase.CreateTagInput) (usecase.Tag, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("generate tag id: %w", err)
	}
	tag, err := model.NewTag(id, in.Name)
	if err != nil {
		return usecase.Tag{}, err
	}
	stored, err := s.repo.Insert(ctx, tag)
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("create tag %q: %w", tag.Name, err)
	}
	return toTag(stored), nil
}

// RenameTag validates the new name and stores it.
func (s *Service) RenameTag(ctx context.Context, in usecase.RenameTagInput) (usecase.Tag, error) {
	tag, err := s.repo.Get(ctx, in.ID)
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("rename tag %s: %w", in.ID, err)
	}
	if err := tag.Rename(in.Name); err != nil {
		return usecase.Tag{}, err
	}
	stored, err := s.repo.Rename(ctx, in.ID, tag.Name)
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("rename tag %s: %w", in.ID, err)
	}
	return toTag(stored), nil
}

// ArchiveTag archives the tag. An archived tag stays archived: asking again
// returns it as it is.
func (s *Service) ArchiveTag(ctx context.Context, id uuid.UUID) (usecase.Tag, error) {
	stored, err := s.repo.Archive(ctx, id)
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("archive tag %s: %w", id, err)
	}
	return toTag(stored), nil
}

// ListTags returns the tags by name.
func (s *Service) ListTags(ctx context.Context, in usecase.ListTagsInput) (usecase.ListTagsOutput, error) {
	tags, err := s.repo.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListTagsOutput{}, fmt.Errorf("list tags: %w", err)
	}
	out := usecase.ListTagsOutput{Tags: make([]usecase.Tag, 0, len(tags))}
	for _, tag := range tags {
		out.Tags = append(out.Tags, toTag(tag))
	}
	return out, nil
}

// GetTag returns the tag, archived or not.
func (s *Service) GetTag(ctx context.Context, id uuid.UUID) (usecase.Tag, error) {
	tag, err := s.repo.Get(ctx, id)
	if err != nil {
		return usecase.Tag{}, fmt.Errorf("get tag %s: %w", id, err)
	}
	return toTag(tag), nil
}

func toTag(t model.Tag) usecase.Tag {
	return usecase.Tag{
		ID:         t.ID,
		Name:       t.Name,
		CreatedAt:  t.CreatedAt,
		ArchivedAt: t.ArchivedAt,
	}
}
