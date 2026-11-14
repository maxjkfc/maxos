package model

import (
	"time"

	"github.com/google/uuid"
)

// Institution is the organisation that keeps accounts for me: a bank, a broker,
// an exchange. How fine to cut it is up to me.
type Institution struct {
	ID   uuid.UUID
	Name string
	// CreatedAt is assigned when the institution is stored; it is zero before
	// then.
	CreatedAt time.Time
	// ArchivedAt is nil while the institution is in use. An archived institution
	// stays for the accounts that already belong to it, but new ones may not.
	ArchivedAt *time.Time
}

// NewInstitution returns an institution with the given name, trimmed of the
// whitespace around it. It returns ErrNameEmpty when nothing is left of the
// name.
func NewInstitution(id uuid.UUID, name string) (Institution, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Institution{}, err
	}
	return Institution{ID: id, Name: name}, nil
}

// IsArchived reports whether the institution is archived.
func (i Institution) IsArchived() bool {
	return i.ArchivedAt != nil
}
