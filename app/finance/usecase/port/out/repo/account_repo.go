package repo

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// AccountRepo stores accounts.
type AccountRepo interface {
	// Insert stores account and returns it as stored, including the fields the
	// store assigns (CreatedAt). It writes nothing and returns
	// model.ErrAlreadyExists when the name is taken.
	Insert(ctx context.Context, account model.Account) (model.Account, error)
	// Get returns the account, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Account, error)
	// List returns the accounts by name, those in use and, when includeArchived is
	// set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Account, error)
	// Archive archives the account and returns it, or returns model.ErrNotFound. An
	// account that is already archived is returned as it is: the time it was
	// archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Account, error)
}
