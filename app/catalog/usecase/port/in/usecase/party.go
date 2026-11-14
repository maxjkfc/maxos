package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Party is a party as the use cases return it.
type Party struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	// ArchivedAt is nil while the party is in use.
	ArchivedAt *time.Time
}

// CreateParty adds a party.
type CreateParty interface {
	CreateParty(ctx context.Context, in CreatePartyInput) (Party, error)
}

// CreatePartyInput is a party to add.
type CreatePartyInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// party, archived or not, may have the same name.
	Name string
}

// RenameParty changes the name of a party.
type RenameParty interface {
	RenameParty(ctx context.Context, in RenamePartyInput) (Party, error)
}

// RenamePartyInput names the party and its new name.
type RenamePartyInput struct {
	ID uuid.UUID
	// Name is checked like the name of CreatePartyInput.
	Name string
}

// ArchiveParty retires a party: it stays for what already names it, but new
// transactions may no longer use it. Archiving an archived party changes
// nothing and returns it as it is.
type ArchiveParty interface {
	ArchiveParty(ctx context.Context, id uuid.UUID) (Party, error)
}

// ListParties lists the parties.
type ListParties interface {
	ListParties(ctx context.Context, in ListPartiesInput) (ListPartiesOutput, error)
}

// ListPartiesInput filters the listing.
type ListPartiesInput struct {
	// IncludeArchived lists the archived parties too; by default only those in
	// use.
	IncludeArchived bool
}

// ListPartiesOutput holds the parties, by name.
type ListPartiesOutput struct {
	Parties []Party
}

// GetParty returns one party, archived or not.
type GetParty interface {
	GetParty(ctx context.Context, id uuid.UUID) (Party, error)
}
