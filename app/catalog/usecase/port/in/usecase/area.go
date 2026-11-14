// Package usecase declares what the catalog context offers its callers: the use
// cases on areas, projects, parties and tags, and the checks other contexts make
// before they store the id of a catalog entry. It is the only part of the
// catalog that other contexts may depend on.
//
// The use cases return the errors of the catalog domain (ErrNameEmpty,
// ErrNotFound, ErrAlreadyExists, ErrArchived and the period errors, all in
// domain/model), wrapped with what was being done. Another context cannot
// import them, so what it must tell apart is declared here instead (see
// ReferenceError and AmbiguousProjectsError).
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Area is an area as the use cases return it.
type Area struct {
	ID        uuid.UUID
	Name      string
	CreatedAt time.Time
	// ArchivedAt is nil while the area is in use.
	ArchivedAt *time.Time
}

// CreateArea adds an area.
type CreateArea interface {
	CreateArea(ctx context.Context, in CreateAreaInput) (Area, error)
}

// CreateAreaInput is an area to add.
type CreateAreaInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// area, archived or not, may have the same name.
	Name string
}

// RenameArea changes the name of an area.
type RenameArea interface {
	RenameArea(ctx context.Context, in RenameAreaInput) (Area, error)
}

// RenameAreaInput names the area and its new name.
type RenameAreaInput struct {
	ID uuid.UUID
	// Name is checked like the name of CreateAreaInput.
	Name string
}

// ArchiveArea retires an area: it stays for what already belongs to it, but new
// transactions and projects may no longer use it. Archiving an archived area
// changes nothing and returns it as it is.
type ArchiveArea interface {
	ArchiveArea(ctx context.Context, id uuid.UUID) (Area, error)
}

// ListAreas lists the areas.
type ListAreas interface {
	ListAreas(ctx context.Context, in ListAreasInput) (ListAreasOutput, error)
}

// ListAreasInput filters the listing.
type ListAreasInput struct {
	// IncludeArchived lists the archived areas too; by default only those in use.
	IncludeArchived bool
}

// ListAreasOutput holds the areas, by name.
type ListAreasOutput struct {
	Areas []Area
}

// GetArea returns one area, archived or not.
type GetArea interface {
	GetArea(ctx context.Context, id uuid.UUID) (Area, error)
}
