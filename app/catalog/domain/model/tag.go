package model

import (
	"time"

	"github.com/google/uuid"
)

// Tag is a free word stuck on transactions, any number per transaction. It only
// filters; nothing is computed from it.
type Tag struct {
	ID   uuid.UUID
	Name string
	// CreatedAt is assigned when the tag is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the tag is in use. An archived tag stays on the
	// transactions that already carry it, but new ones may not.
	ArchivedAt *time.Time
}

// NewTag returns a tag with the given name, trimmed of the whitespace around
// it. It returns ErrNameEmpty when nothing is left of the name.
func NewTag(id uuid.UUID, name string) (Tag, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Tag{}, err
	}
	return Tag{ID: id, Name: name}, nil
}

// Rename changes the name, which is trimmed and checked like the first one. On
// an error the tag is unchanged.
func (t *Tag) Rename(name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	t.Name = name
	return nil
}

// IsArchived reports whether the tag is archived.
func (t Tag) IsArchived() bool {
	return t.ArchivedAt != nil
}
