package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// CategoryRepo stores categories.
type CategoryRepo interface {
	// Insert stores category and returns it as stored, including the fields the
	// store assigns (CreatedAt). It writes nothing and returns
	// model.ErrAlreadyExists when a category of the same direction and under the
	// same parent (a top-level one, when the category has no parent) has the name.
	Insert(ctx context.Context, category model.Category) (model.Category, error)
	// Get returns the category, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Category, error)
	// List returns the categories by direction and name, top-level and subcategories
	// together. An empty direction lists both directions; includeArchived adds
	// the archived categories to those in use.
	List(ctx context.Context, direction model.Direction, includeArchived bool) ([]model.Category, error)
	// Archive archives the category, and its subcategories when it has some, and
	// returns the category, or returns model.ErrNotFound. A category that is
	// already archived is returned as it is: the time it was archived does not
	// change. It is one statement, so the category and its subcategories are
	// archived together or not at all.
	Archive(ctx context.Context, id uuid.UUID) (model.Category, error)
}
