package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// TagRepo stores tags.
type TagRepo interface {
	// Insert stores tag and returns it as stored, including the fields the store
	// assigns (CreatedAt). It writes nothing and returns model.ErrAlreadyExists
	// when the name is taken.
	Insert(ctx context.Context, tag model.Tag) (model.Tag, error)
	// Get returns the tag, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Tag, error)
	// GetByIDs returns the tags that exist among ids, archived or not, in no
	// particular order. An id with no tag is left out, not an error.
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Tag, error)
	// List returns the tags by name, those in use and, when includeArchived is
	// set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Tag, error)
	// Rename sets the name of the tag and returns the tag, or returns
	// model.ErrNotFound, or model.ErrAlreadyExists when the name is taken.
	Rename(ctx context.Context, id uuid.UUID, name string) (model.Tag, error)
	// Archive archives the tag and returns it, or returns model.ErrNotFound. A
	// tag that is already archived is returned as it is: the time it was archived
	// does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Tag, error)
}
