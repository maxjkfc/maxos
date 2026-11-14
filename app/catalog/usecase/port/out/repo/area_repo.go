// Package repo declares how the catalog use cases store and read areas, projects,
// parties and tags. A repo reports a missing entry as model.ErrNotFound and a
// name that is taken as model.ErrAlreadyExists.
package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// AreaRepo stores areas.
type AreaRepo interface {
	// Insert stores area and returns it as stored, including the fields the store
	// assigns (CreatedAt). It writes nothing and returns model.ErrAlreadyExists
	// when the name is taken.
	Insert(ctx context.Context, area model.Area) (model.Area, error)
	// Get returns the area, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Area, error)
	// GetByIDs returns the areas that exist among ids, archived or not, in no
	// particular order. An id with no area is left out, not an error.
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Area, error)
	// List returns the areas by name, those in use and, when includeArchived is
	// set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Area, error)
	// Rename sets the name of the area and returns the area, or returns
	// model.ErrNotFound, or model.ErrAlreadyExists when the name is taken.
	Rename(ctx context.Context, id uuid.UUID, name string) (model.Area, error)
	// Archive archives the area and returns it, or returns model.ErrNotFound. An
	// area that is already archived is returned as it is: the time it was
	// archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Area, error)
}
