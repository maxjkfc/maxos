package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// ListRecent lists the most recently captured inbox items.
type ListRecent interface {
	ListRecent(ctx context.Context, in ListRecentInput) (ListRecentOutput, error)
}

// ListRecentInput limits the listing.
type ListRecentInput struct {
	// Limit is how many items to return. Zero means the default (10); other
	// values are brought into the range 1 to 50.
	Limit int
}

// ListRecentOutput holds the items, newest first.
type ListRecentOutput struct {
	Items []RecentItem
}

// RecentItem is an inbox item as listed.
type RecentItem struct {
	ID         uuid.UUID
	ReceivedAt time.Time
	// Source is the channel the item arrived through, e.g. "mcp-http".
	Source string
	// Actor is the identity that sent the item.
	Actor string
	Kind  string
	// Status is the processing state: received, processed or rejected.
	Status string
	// Text is the note text when the item has one, otherwise empty.
	Text string
}
