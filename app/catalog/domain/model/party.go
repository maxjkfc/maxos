package model

import (
	"time"

	"github.com/google/uuid"
)

// Party is a person or an organisation I have dealings with in money and settle
// up with.
type Party struct {
	ID   uuid.UUID
	Name string
	// CreatedAt is assigned when the party is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the party is in use. An archived party stays for
	// the transactions that already name it, but new ones may not.
	ArchivedAt *time.Time
}

// NewParty returns a party with the given name, trimmed of the whitespace
// around it. It returns ErrNameEmpty when nothing is left of the name.
func NewParty(id uuid.UUID, name string) (Party, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Party{}, err
	}
	return Party{ID: id, Name: name}, nil
}

// Rename changes the name, which is trimmed and checked like the first one. On
// an error the party is unchanged.
func (p *Party) Rename(name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	p.Name = name
	return nil
}

// IsArchived reports whether the party is archived.
func (p Party) IsArchived() bool {
	return p.ArchivedAt != nil
}
