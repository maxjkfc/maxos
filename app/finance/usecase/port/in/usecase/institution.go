package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Institution is an institution as the use cases return it.
type Institution struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	// ArchivedAt is nil while the institution is in use.
	ArchivedAt *time.Time
}

// CreateInstitution adds an institution.
type CreateInstitution interface {
	CreateInstitution(ctx context.Context, in CreateInstitutionInput) (Institution, error)
}

// CreateInstitutionInput is an institution to add.
type CreateInstitutionInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// institution, archived or not, may have the same name.
	Name string
}

// ArchiveInstitution retires an institution: it stays for the accounts that
// already belong to it, but new accounts may no longer use it. Archiving an
// archived institution changes nothing and returns it as it is.
type ArchiveInstitution interface {
	ArchiveInstitution(ctx context.Context, id uuid.UUID) (Institution, error)
}

// ListInstitutions lists the institutions.
type ListInstitutions interface {
	ListInstitutions(ctx context.Context, in ListInstitutionsInput) (ListInstitutionsOutput, error)
}

// ListInstitutionsInput filters the listing.
type ListInstitutionsInput struct {
	// IncludeArchived lists the archived institutions too; by default only those
	// in use.
	IncludeArchived bool
}

// ListInstitutionsOutput holds the institutions, by name.
type ListInstitutionsOutput struct {
	Institutions []Institution
}

// GetInstitution returns one institution, archived or not.
type GetInstitution interface {
	GetInstitution(ctx context.Context, id uuid.UUID) (Institution, error)
}
