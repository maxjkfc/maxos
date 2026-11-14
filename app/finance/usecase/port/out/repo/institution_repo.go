package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// InstitutionRepo stores institutions.
type InstitutionRepo interface {
	// Insert stores institution and returns it as stored, including the fields the
	// store assigns (CreatedAt). It writes nothing and returns
	// model.ErrAlreadyExists when the name is taken.
	Insert(ctx context.Context, institution model.Institution) (model.Institution, error)
	// Get returns the institution, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Institution, error)
	// List returns the institutions by name, those in use and, when
	// includeArchived is set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Institution, error)
	// Archive archives the institution and returns it, or returns
	// model.ErrNotFound. An institution that is already archived is returned as it
	// is: the time it was archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Institution, error)
}
