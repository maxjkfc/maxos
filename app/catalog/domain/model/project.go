package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Project is one thing that has a start and an end and gets done, such as a
// trip.
//
// A project has two periods, both optional. Period is when the project takes
// place; ActivePeriod is the days of it that the project is actually under way,
// such as the days I am away on the trip. A transaction dated within the active
// period belongs to the project unless it says otherwise. When both are set,
// the active period lies within the period.
type Project struct {
	ID   uuid.UUID
	Name string
	// Period is nil while it is not set.
	Period *Period
	// ActivePeriod is nil while it is not set.
	ActivePeriod *Period
	// DefaultAreaID is the area the transactions of the project go to unless they
	// say otherwise; nil when the project has none. Whether the area exists and
	// is in use is for the use case to check: the project only holds its id.
	DefaultAreaID *uuid.UUID
	// CreatedAt is assigned when the project is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the project is in use. An archived project stays
	// for the transactions that already belong to it, but new ones may not.
	ArchivedAt *time.Time
}

// NewProject returns a project with the given name, trimmed of the whitespace
// around it, and no periods or default area. It returns ErrNameEmpty when
// nothing is left of the name.
func NewProject(id uuid.UUID, name string) (Project, error) {
	name, err := normalizeName(name)
	if err != nil {
		return Project{}, err
	}
	return Project{ID: id, Name: name}, nil
}

// Rename changes the name, which is trimmed and checked like the first one. On
// an error the project is unchanged.
func (p *Project) Rename(name string) error {
	name, err := normalizeName(name)
	if err != nil {
		return err
	}
	p.Name = name
	return nil
}

// SetPeriod sets the period of the project, or clears it when period is nil. It
// returns ErrActivePeriodOutsidePeriod, and changes nothing, when the active
// period would no longer lie within it.
func (p *Project) SetPeriod(period *Period) error {
	if period != nil && p.ActivePeriod != nil && !period.Covers(*p.ActivePeriod) {
		return fmt.Errorf("%w: active period %s, period %s", ErrActivePeriodOutsidePeriod, p.ActivePeriod, period)
	}
	p.Period = copyPeriod(period)
	return nil
}

// SetActivePeriod sets the active period of the project, or clears it when
// active is nil. It returns ErrActivePeriodOutsidePeriod, and changes nothing,
// when the period is set and does not cover it.
func (p *Project) SetActivePeriod(active *Period) error {
	if active != nil && p.Period != nil && !p.Period.Covers(*active) {
		return fmt.Errorf("%w: active period %s, period %s", ErrActivePeriodOutsidePeriod, active, p.Period)
	}
	p.ActivePeriod = copyPeriod(active)
	return nil
}

// IsArchived reports whether the project is archived.
func (p Project) IsArchived() bool {
	return p.ArchivedAt != nil
}

// copyPeriod returns a copy, so that a project never shares a period with its
// caller.
func copyPeriod(p *Period) *Period {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
