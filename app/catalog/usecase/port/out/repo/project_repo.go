package repo

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// ProjectRepo stores projects. Each Set method writes only what its name says,
// so that two changes made at about the same time do not overwrite each other.
type ProjectRepo interface {
	// Insert stores project, with its periods and default area, and returns it as
	// stored, including the fields the store assigns (CreatedAt). It writes
	// nothing and returns model.ErrAlreadyExists when the name is taken.
	Insert(ctx context.Context, project model.Project) (model.Project, error)
	// Get returns the project, archived or not, or model.ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (model.Project, error)
	// GetByIDs returns the projects that exist among ids, archived or not, in no
	// particular order. An id with no project is left out, not an error.
	GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Project, error)
	// List returns the projects by name, those in use and, when includeArchived is
	// set, the archived ones too.
	List(ctx context.Context, includeArchived bool) ([]model.Project, error)
	// Rename sets the name of the project and returns the project, or returns
	// model.ErrNotFound, or model.ErrAlreadyExists when the name is taken.
	Rename(ctx context.Context, id uuid.UUID, name string) (model.Project, error)
	// SetPeriod sets the period of the project, or clears it when period is nil,
	// and returns the project, or returns model.ErrNotFound.
	SetPeriod(ctx context.Context, id uuid.UUID, period *model.Period) (model.Project, error)
	// SetActivePeriod sets the active period of the project, or clears it when
	// period is nil, and returns the project, or returns model.ErrNotFound.
	SetActivePeriod(ctx context.Context, id uuid.UUID, period *model.Period) (model.Project, error)
	// SetDefaultArea sets the default area of the project and returns the
	// project, or returns model.ErrNotFound.
	SetDefaultArea(ctx context.Context, id uuid.UUID, areaID uuid.UUID) (model.Project, error)
	// Archive archives the project and returns it, or returns model.ErrNotFound. A
	// project that is already archived is returned as it is: the time it was
	// archived does not change.
	Archive(ctx context.Context, id uuid.UUID) (model.Project, error)
	// ListActiveOn returns the projects in use whose active period includes day,
	// the one that starts first, then by id. day is a date as model.DateOf
	// returns it.
	ListActiveOn(ctx context.Context, day time.Time) ([]model.Project, error)
}
