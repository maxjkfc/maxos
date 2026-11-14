// Package model holds the catalog entities (areas, projects, parties, tags) and
// the invariants they must keep.
package model

import (
	"time"

	"github.com/google/uuid"
)

// Area is an ongoing part of life that is never finished, such as work, family
// or play. A transaction belongs to at most one area.
type Area struct {
	ID   uuid.UUID
	Name string
	// CreatedAt is assigned when the area is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the area is in use. An archived area stays for
	// the transactions that already belong to it, but new ones may not.
	ArchivedAt *time.Time
}

// NewArea returns an area with the given name, trimmed of the whitespace around
// it. It returns ErrNameEmpty when nothing is left of the name.
func NewArea(id uuid.UUID, name string) (Area, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Area{}, err
	}
	return Area{ID: id, Name: name}, nil
}

// Rename changes the name, which is trimmed and checked like the first one. On
// an error the area is unchanged.
func (a *Area) Rename(name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	a.Name = name
	return nil
}

// IsArchived reports whether the area is archived.
func (a Area) IsArchived() bool {
	return a.ArchivedAt != nil
}
