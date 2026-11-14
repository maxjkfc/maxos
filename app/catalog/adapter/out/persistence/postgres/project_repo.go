package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/catalog/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// ProjectRepo implements repo.ProjectRepo on the catalog_project table.
type ProjectRepo struct {
	q *dbgen.Queries
}

var _ repo.ProjectRepo = (*ProjectRepo)(nil)

// NewProjectRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewProjectRepo(pool *pgxpool.Pool) *ProjectRepo {
	return &ProjectRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores project.
func (r *ProjectRepo) Insert(ctx context.Context, project model.Project) (model.Project, error) {
	startsOn, endsOn := ends(project.Period)
	activeFrom, activeTo := ends(project.ActivePeriod)
	row, err := r.q.InsertProject(ctx, dbgen.InsertProjectParams{
		ID:            project.ID,
		Name:          project.Name,
		StartsOn:      startsOn,
		EndsOn:        endsOn,
		ActiveFrom:    activeFrom,
		ActiveTo:      activeTo,
		DefaultAreaID: nullUUID(project.DefaultAreaID),
	})
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// Get returns the project with the given id.
func (r *ProjectRepo) Get(ctx context.Context, id uuid.UUID) (model.Project, error) {
	row, err := r.q.GetProject(ctx, id)
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// GetByIDs returns the projects that exist among ids.
func (r *ProjectRepo) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Project, error) {
	rows, err := r.q.GetProjectsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return toProjects(rows)
}

// List returns the projects by name.
func (r *ProjectRepo) List(ctx context.Context, includeArchived bool) ([]model.Project, error) {
	rows, err := r.q.ListProjects(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	return toProjects(rows)
}

// Rename sets the name of the project.
func (r *ProjectRepo) Rename(ctx context.Context, id uuid.UUID, name string) (model.Project, error) {
	row, err := r.q.RenameProject(ctx, dbgen.RenameProjectParams{ID: id, Name: name})
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// SetPeriod sets or clears the period of the project. The table's CHECK
// constraints are the last guard of the period rules: a period that breaks one
// comes back as an error from the database.
func (r *ProjectRepo) SetPeriod(ctx context.Context, id uuid.UUID, period *model.Period) (model.Project, error) {
	startsOn, endsOn := ends(period)
	row, err := r.q.SetProjectPeriod(ctx, dbgen.SetProjectPeriodParams{ID: id, StartsOn: startsOn, EndsOn: endsOn})
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// SetActivePeriod sets or clears the active period of the project. See
// SetPeriod for the CHECK constraints.
func (r *ProjectRepo) SetActivePeriod(ctx context.Context, id uuid.UUID, period *model.Period) (model.Project, error) {
	activeFrom, activeTo := ends(period)
	row, err := r.q.SetProjectActivePeriod(ctx, dbgen.SetProjectActivePeriodParams{ID: id, ActiveFrom: activeFrom, ActiveTo: activeTo})
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// SetDefaultArea sets the default area of the project.
func (r *ProjectRepo) SetDefaultArea(ctx context.Context, id uuid.UUID, areaID uuid.UUID) (model.Project, error) {
	row, err := r.q.SetProjectDefaultArea(ctx, dbgen.SetProjectDefaultAreaParams{ID: id, DefaultAreaID: nullUUID(&areaID)})
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// Archive archives the project; the first time it was archived is kept.
func (r *ProjectRepo) Archive(ctx context.Context, id uuid.UUID) (model.Project, error) {
	row, err := r.q.ArchiveProject(ctx, id)
	if err != nil {
		return model.Project{}, mapError(err)
	}
	return toProject(row)
}

// ListActiveOn returns the projects in use whose active period includes day.
func (r *ProjectRepo) ListActiveOn(ctx context.Context, day time.Time) ([]model.Project, error) {
	rows, err := r.q.ListProjectsActiveOn(ctx, day)
	if err != nil {
		return nil, err
	}
	return toProjects(rows)
}

func toProject(row dbgen.CatalogProject) (model.Project, error) {
	period, err := toPeriod(row.StartsOn, row.EndsOn)
	if err != nil {
		return model.Project{}, fmt.Errorf("project %s: period: %w", row.ID, err)
	}
	active, err := toPeriod(row.ActiveFrom, row.ActiveTo)
	if err != nil {
		return model.Project{}, fmt.Errorf("project %s: active period: %w", row.ID, err)
	}
	project := model.Project{
		ID:           row.ID,
		Name:         row.Name,
		Period:       period,
		ActivePeriod: active,
		CreatedAt:    row.CreatedAt,
		ArchivedAt:   row.ArchivedAt,
	}
	if row.DefaultAreaID.Valid {
		areaID := row.DefaultAreaID.UUID
		project.DefaultAreaID = &areaID
	}
	return project, nil
}

func toProjects(rows []dbgen.CatalogProject) ([]model.Project, error) {
	projects := make([]model.Project, len(rows))
	for i, row := range rows {
		project, err := toProject(row)
		if err != nil {
			return nil, err
		}
		projects[i] = project
	}
	return projects, nil
}

// toPeriod rebuilds a period from its two columns. The table's CHECK constraints
// allow no row with one end, or with the ends reversed, so an error here means
// the data was changed behind the constraints' back.
func toPeriod(from, to *time.Time) (*model.Period, error) {
	switch {
	case from == nil && to == nil:
		return nil, nil
	case from == nil || to == nil:
		return nil, errors.New("only one end is set")
	}
	period, err := model.NewPeriod(*from, *to)
	if err != nil {
		return nil, err
	}
	return &period, nil
}

// ends returns the two column values of a period, both nil for no period.
func ends(p *model.Period) (from, to *time.Time) {
	if p == nil {
		return nil, nil
	}
	return &p.From, &p.To
}

// nullUUID returns the column value of an optional id.
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}
