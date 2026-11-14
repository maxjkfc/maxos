package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Period is a run of calendar days, both ends included. Only the day each time
// falls on counts, as that time's own location reads it; the time of day is
// ignored. Both ends are required: a Period with a zero From or To is refused
// (ErrPeriodIncomplete), not read as the first day of the year 1. A period that
// is not set is a nil *Period.
type Period struct {
	From, To time.Time
}

// Project is a project as the use cases return it. The dates of its periods are
// at midnight UTC.
type Project struct {
	ID   uuid.UUID
	Name string
	// Period is when the project takes place; nil while it is not set.
	Period *Period
	// ActivePeriod is the days of it the project is actually under way; nil
	// while it is not set. When both are set it lies within Period.
	ActivePeriod *Period
	// DefaultAreaID is the area the transactions of the project go to unless they
	// say otherwise; nil when the project has none.
	DefaultAreaID *uuid.UUID
	CreatedAt     time.Time
	// ArchivedAt is nil while the project is in use.
	ArchivedAt *time.Time
}

// CreateProject adds a project.
type CreateProject interface {
	CreateProject(ctx context.Context, in CreateProjectInput) (Project, error)
}

// CreateProjectInput is a project to add. Everything but the name may be left
// out and set later.
type CreateProjectInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// project, archived or not, may have the same name.
	Name string
	// Period and ActivePeriod follow the rules of SetProjectPeriod and
	// SetProjectActivePeriod.
	Period       *Period
	ActivePeriod *Period
	// DefaultAreaID must be an area that exists and is not archived.
	DefaultAreaID *uuid.UUID
}

// RenameProject changes the name of a project.
type RenameProject interface {
	RenameProject(ctx context.Context, in RenameProjectInput) (Project, error)
}

// RenameProjectInput names the project and its new name.
type RenameProjectInput struct {
	ID uuid.UUID
	// Name is checked like the name of CreateProjectInput.
	Name string
}

// ArchiveProject retires a project: it stays for what already belongs to it, but
// new transactions may no longer use it, and it is no longer the project of any
// date (see ResolveProjectDefaults). Archiving an archived project changes
// nothing and returns it as it is.
type ArchiveProject interface {
	ArchiveProject(ctx context.Context, id uuid.UUID) (Project, error)
}

// ListProjects lists the projects.
type ListProjects interface {
	ListProjects(ctx context.Context, in ListProjectsInput) (ListProjectsOutput, error)
}

// ListProjectsInput filters the listing.
type ListProjectsInput struct {
	// IncludeArchived lists the archived projects too; by default only those in
	// use.
	IncludeArchived bool
}

// ListProjectsOutput holds the projects, by name.
type ListProjectsOutput struct {
	Projects []Project
}

// GetProject returns one project, archived or not.
type GetProject interface {
	GetProject(ctx context.Context, id uuid.UUID) (Project, error)
}

// SetProjectPeriod sets, or clears, when a project takes place. The end may not
// be before the start, and the active period, when there is one, must stay within
// the period: to move a project to other days, clear or move the active period
// first.
type SetProjectPeriod interface {
	SetProjectPeriod(ctx context.Context, in SetProjectPeriodInput) (Project, error)
}

// SetProjectPeriodInput names the project and its new period.
type SetProjectPeriodInput struct {
	ID uuid.UUID
	// Period is the new period; nil clears it.
	Period *Period
}

// SetProjectActivePeriod sets, or clears, the days a project is under way. The
// end may not be before the start, and when the project has a period, the active
// period must lie within it.
type SetProjectActivePeriod interface {
	SetProjectActivePeriod(ctx context.Context, in SetProjectActivePeriodInput) (Project, error)
}

// SetProjectActivePeriodInput names the project and its new active period.
type SetProjectActivePeriodInput struct {
	ID uuid.UUID
	// ActivePeriod is the new active period; nil clears it.
	ActivePeriod *Period
}

// SetProjectDefaultArea sets the area the transactions of a project go to unless
// they say otherwise. The area must exist and not be archived.
type SetProjectDefaultArea interface {
	SetProjectDefaultArea(ctx context.Context, in SetProjectDefaultAreaInput) (Project, error)
}

// SetProjectDefaultAreaInput names the project and its new default area.
type SetProjectDefaultAreaInput struct {
	ID     uuid.UUID
	AreaID uuid.UUID
}

// ResolveProjectDefaults finds the project a transaction dated on a given day
// belongs to when it names none, and the area that goes with it: the project
// whose active period covers the day, and its default area, as
// ResolveProjectArea finds it.
//
// Archived projects do not count. When more than one project is active on the
// day it returns an *AmbiguousProjectsError and the caller decides, usually by
// asking whoever made the transaction.
type ResolveProjectDefaults interface {
	ResolveProjectDefaults(ctx context.Context, in ResolveProjectDefaultsInput) (ResolveProjectDefaultsOutput, error)
}

// ResolveProjectDefaultsInput is the day to resolve.
type ResolveProjectDefaultsInput struct {
	// Date is the day; only the day Date falls on counts, as its own location
	// reads it (see Period).
	Date time.Time
}

// ResolveProjectDefaultsOutput is the project of the day and its area.
type ResolveProjectDefaultsOutput struct {
	// Project is nil when no project is active on the day.
	Project *Project
	// DefaultArea is the default area of Project. It is nil when there is no
	// Project, when the project has no default area, and when the area has been
	// archived since: a transaction cannot go to an archived area (see
	// ResolveProjectArea).
	DefaultArea *Area
}

// ResolveProjectArea finds the area the transactions of a project go to unless
// they say otherwise: the default area of the project, as long as it can still
// be used.
//
// It returns a nil area, and no error, when the project has no default area and
// when the area has been archived since: a transaction cannot go to an archived
// area, and the caller can still name another. It returns a *ReferenceError for
// a project that does not exist or is archived, as VerifyReferences does.
type ResolveProjectArea interface {
	ResolveProjectArea(ctx context.Context, projectID uuid.UUID) (*Area, error)
}
