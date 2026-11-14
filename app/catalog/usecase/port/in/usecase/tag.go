package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Tag is a tag as the use cases return it.
type Tag struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	// ArchivedAt is nil while the tag is in use.
	ArchivedAt *time.Time
}

// CreateTag adds a tag.
type CreateTag interface {
	CreateTag(ctx context.Context, in CreateTagInput) (Tag, error)
}

// CreateTagInput is a tag to add.
type CreateTagInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// tag, archived or not, may have the same name.
	Name string
}

// RenameTag changes the name of a tag.
type RenameTag interface {
	RenameTag(ctx context.Context, in RenameTagInput) (Tag, error)
}

// RenameTagInput names the tag and its new name.
type RenameTagInput struct {
	ID uuid.UUID
	// Name is checked like the name of CreateTagInput.
	Name string
}

// ArchiveTag retires a tag: it stays on the transactions that already carry it,
// but new transactions may no longer use it. Archiving an archived tag changes
// nothing and returns it as it is.
type ArchiveTag interface {
	ArchiveTag(ctx context.Context, id uuid.UUID) (Tag, error)
}

// ListTags lists the tags.
type ListTags interface {
	ListTags(ctx context.Context, in ListTagsInput) (ListTagsOutput, error)
}

// ListTagsInput filters the listing.
type ListTagsInput struct {
	// IncludeArchived lists the archived tags too; by default only those in use.
	IncludeArchived bool
}

// ListTagsOutput holds the tags, by name.
type ListTagsOutput struct {
	Tags []Tag
}

// GetTag returns one tag, archived or not.
type GetTag interface {
	GetTag(ctx context.Context, id uuid.UUID) (Tag, error)
}
