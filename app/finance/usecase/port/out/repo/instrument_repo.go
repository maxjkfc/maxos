// Package repo declares how the finance use cases store and read instruments,
// institutions, accounts, categories and transactions. A repo reports a missing
// entry as model.ErrNotFound and a name (a code, for an instrument) that is taken
// as model.ErrAlreadyExists.
package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// InstrumentRepo stores instruments.
type InstrumentRepo interface {
	// Insert stores instrument and returns it as stored, including the fields the
	// store assigns (CreatedAt). It writes nothing and returns
	// model.ErrAlreadyExists when the code is taken.
	Insert(ctx context.Context, instrument model.Instrument) (model.Instrument, error)
	// Get returns the instrument, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Instrument, error)
	// List returns the instruments by code, those in use and, when includeArchived
	// is set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Instrument, error)
	// Archive archives the instrument and returns it, or returns model.ErrNotFound.
	// An instrument that is already archived is returned as it is: the time it was
	// archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Instrument, error)
}
