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

// TagRepo implements repo.TagRepo on the catalog_tag table.
type TagRepo struct {
	q *dbgen.Queries
}

var _ repo.TagRepo = (*TagRepo)(nil)

// NewTagRepo returns a repo that reads and writes through pool. Its statements
// join the transaction a context carries (see pkg/postgres).
func NewTagRepo(pool *pgxpool.Pool) *TagRepo {
	return &TagRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores tag.
func (r *TagRepo) Insert(ctx context.Context, tag model.Tag) (model.Tag, error) {
	row, err := r.q.InsertTag(ctx, dbgen.InsertTagParams{ID: tag.ID, Name: tag.Name})
	if err != nil {
		return model.Tag{}, mapError(err)
	}
	return toTag(row), nil
}

// Get returns the tag with the given id.
func (r *TagRepo) Get(ctx context.Context, id uuid.UUID) (model.Tag, error) {
	row, err := r.q.GetTag(ctx, id)
	if err != nil {
		return model.Tag{}, mapError(err)
	}
	return toTag(row), nil
}

// GetByIDs returns the tags that exist among ids.
func (r *TagRepo) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Tag, error) {
	rows, err := r.q.GetTagsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return toTags(rows), nil
}

// List returns the tags by name.
func (r *TagRepo) List(ctx context.Context, includeArchived bool) ([]model.Tag, error) {
	rows, err := r.q.ListTags(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	return toTags(rows), nil
}

// Rename sets the name of the tag.
func (r *TagRepo) Rename(ctx context.Context, id uuid.UUID, name string) (model.Tag, error) {
	row, err := r.q.RenameTag(ctx, dbgen.RenameTagParams{ID: id, Name: name})
	if err != nil {
		return model.Tag{}, mapError(err)
	}
	return toTag(row), nil
}

// Archive archives the tag; the first time it was archived is kept.
func (r *TagRepo) Archive(ctx context.Context, id uuid.UUID) (model.Tag, error) {
	row, err := r.q.ArchiveTag(ctx, id)
	if err != nil {
		return model.Tag{}, mapError(err)
	}
	return toTag(row), nil
}

func toTag(row dbgen.CatalogTag) model.Tag {
	return model.Tag{
		ID:         row.ID,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}

func toTags(rows []dbgen.CatalogTag) []model.Tag {
	tags := make([]model.Tag, len(rows))
	for i, row := range rows {
		tags[i] = toTag(row)
	}
	return tags
}
