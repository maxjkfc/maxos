// Package repo declares how the inbox use cases store and read inbox items.
package repo

import (
	"context"
	"errors"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
)

var (
	// ErrDuplicateKey is returned by Insert when the idempotency key is already
	// stored.
	ErrDuplicateKey = errors.New("idempotency key already exists")
	// ErrNotFound is returned when no stored item matches.
	ErrNotFound = errors.New("inbox item not found")
)

// InboxRepo stores inbox items.
type InboxRepo interface {
	// Insert stores item and returns it as stored, including the fields the
	// store assigns (ReceivedAt). It writes nothing and returns ErrDuplicateKey
	// when item.IdempotencyKey is already stored.
	Insert(ctx context.Context, item model.InboxItem) (model.InboxItem, error)
	// GetByKey returns the item stored under key, or ErrNotFound.
	GetByKey(ctx context.Context, key string) (model.InboxItem, error)
	// ListRecent returns at most limit items, newest first.
	ListRecent(ctx context.Context, limit int) ([]model.InboxItem, error)
}
