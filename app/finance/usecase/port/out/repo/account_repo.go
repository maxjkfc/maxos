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
	// Lock locks the account's row until the database transaction the context
	// carries ends: another transaction that locks the same account waits for it,
	// and then sees what it committed. It is how use cases that read about an
	// account and then write make sure that nobody else does the same meanwhile.
	// Recording a posting to the account is not held up by it. It returns
	// model.ErrNotFound when there is no such account.
	Lock(ctx context.Context, id uuid.UUID) error
}
