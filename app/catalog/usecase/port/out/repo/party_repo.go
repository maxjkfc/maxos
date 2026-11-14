package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// PartyRepo stores parties.
type PartyRepo interface {
	// Insert stores party and returns it as stored, including the fields the store
	// assigns (CreatedAt). It writes nothing and returns model.ErrAlreadyExists
	// when the name is taken.
	Insert(ctx context.Context, party model.Party) (model.Party, error)
	// Get returns the party, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Party, error)
	// GetByIDs returns the parties that exist among ids, archived or not, in no
	// particular order. An id with no party is left out, not an error.
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Party, error)
	// List returns the parties by name, those in use and, when includeArchived is
	// set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Party, error)
	// Rename sets the name of the party and returns the party, or returns
	// model.ErrNotFound, or model.ErrAlreadyExists when the name is taken.
	Rename(ctx context.Context, id uuid.UUID, name string) (model.Party, error)
	// Archive archives the party and returns it, or returns model.ErrNotFound. A
	// party that is already archived is returned as it is: the time it was
	// archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Party, error)
}
