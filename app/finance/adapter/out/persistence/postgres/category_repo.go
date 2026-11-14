package postgres

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// CategoryRepo implements repo.CategoryRepo on the fin_category table.
type CategoryRepo struct {
	q *dbgen.Queries
}

var _ repo.CategoryRepo = (*CategoryRepo)(nil)

// NewCategoryRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewCategoryRepo(pool *pgxpool.Pool) *CategoryRepo {
	return &CategoryRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores category. The table's foreign key is the last guard of the two
// levels and of the direction of a subcategory: a parent that is itself a
// subcategory, that has the other direction or that does not exist comes back
// as an error from the database.
func (r *CategoryRepo) Insert(ctx context.Context, category model.Category) (model.Category, error) {
	row, err := r.q.InsertCategory(ctx, dbgen.InsertCategoryParams{
		ID:        category.ID,
		Direction: string(category.Direction),
		ParentID:  nullUUID(category.ParentID),
		Name:      category.Name,
	})
	if err != nil {
		return model.Category{}, mapError(err)
	}
	return toCategory(row), nil
}

// Get returns the category with the given id.
func (r *CategoryRepo) Get(ctx context.Context, id uuid.UUID) (model.Category, error) {
	row, err := r.q.GetCategory(ctx, id)
	if err != nil {
		return model.Category{}, mapError(err)
	}
	return toCategory(row), nil
}

// List returns the categories by direction and name; an empty direction lists
// both directions.
func (r *CategoryRepo) List(ctx context.Context, direction model.Direction, includeArchived bool) ([]model.Category, error) {
	rows, err := r.q.ListCategories(ctx, dbgen.ListCategoriesParams{
		IncludeArchived: includeArchived,
		Direction:       string(direction),
	})
	if err != nil {
		return nil, err
	}
	categories := make([]model.Category, len(rows))
	for i, row := range rows {
		categories[i] = toCategory(row)
	}
	return categories, nil
}

// Archive archives the category and its subcategories in one statement; the
// first time each was archived is kept. The statement returns every row it
// archived, of which the one with the given id is the category.
func (r *CategoryRepo) Archive(ctx context.Context, id uuid.UUID) (model.Category, error) {
	rows, err := r.q.ArchiveCategory(ctx, id)
	if err != nil {
		return model.Category{}, err
	}
	for _, row := range rows {
		if row.ID == id {
			return toCategory(row), nil
		}
	}
	return model.Category{}, model.ErrNotFound
}

// toCategory maps a row to a category. The generated columns is_top and
// parent_must_be_top say nothing parent_id does not, so they are left out.
func toCategory(row dbgen.FinCategory) model.Category {
	return model.Category{
		ID:         row.ID,
		Direction:  model.Direction(row.Direction),
		ParentID:   optionalUUID(row.ParentID),
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}
