package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/catalog/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/app/catalog/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// AreaRepo implements repo.AreaRepo on the catalog_area table.
type AreaRepo struct {
	q *dbgen.Queries
}

var _ repo.AreaRepo = (*AreaRepo)(nil)

// NewAreaRepo returns a repo that reads and writes through pool. Its statements
// join the transaction a context carries (see pkg/postgres).
func NewAreaRepo(pool *pgxpool.Pool) *AreaRepo {
	return &AreaRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores area.
func (r *AreaRepo) Insert(ctx context.Context, area model.Area) (model.Area, error) {
	row, err := r.q.InsertArea(ctx, dbgen.InsertAreaParams{ID: area.ID, Name: area.Name})
	if err != nil {
		return model.Area{}, mapError(err)
	}
	return toArea(row), nil
}

// Get returns the area with the given id.
func (r *AreaRepo) Get(ctx context.Context, id uuid.UUID) (model.Area, error) {
	row, err := r.q.GetArea(ctx, id)
	if err != nil {
		return model.Area{}, mapError(err)
	}
	return toArea(row), nil
}

// GetByIDs returns the areas that exist among ids.
func (r *AreaRepo) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Area, error) {
	rows, err := r.q.GetAreasByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return toAreas(rows), nil
}

// List returns the areas by name.
func (r *AreaRepo) List(ctx context.Context, includeArchived bool) ([]model.Area, error) {
	rows, err := r.q.ListAreas(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	return toAreas(rows), nil
}

// Rename sets the name of the area.
func (r *AreaRepo) Rename(ctx context.Context, id uuid.UUID, name string) (model.Area, error) {
	row, err := r.q.RenameArea(ctx, dbgen.RenameAreaParams{ID: id, Name: name})
	if err != nil {
		return model.Area{}, mapError(err)
	}
	return toArea(row), nil
}

// Archive archives the area; the first time it was archived is kept.
func (r *AreaRepo) Archive(ctx context.Context, id uuid.UUID) (model.Area, error) {
	row, err := r.q.ArchiveArea(ctx, id)
	if err != nil {
		return model.Area{}, mapError(err)
	}
	return toArea(row), nil
}

func toArea(row dbgen.CatalogArea) model.Area {
	return model.Area{
		ID:         row.ID,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}

func toAreas(rows []dbgen.CatalogArea) []model.Area {
	areas := make([]model.Area, len(rows))
	for i, row := range rows {
		areas[i] = toArea(row)
	}
	return areas
}
