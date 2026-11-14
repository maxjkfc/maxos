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

// InstitutionRepo implements repo.InstitutionRepo on the fin_institution table.
type InstitutionRepo struct {
	q *dbgen.Queries
}

var _ repo.InstitutionRepo = (*InstitutionRepo)(nil)

// NewInstitutionRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewInstitutionRepo(pool *pgxpool.Pool) *InstitutionRepo {
	return &InstitutionRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores institution.
func (r *InstitutionRepo) Insert(ctx context.Context, institution model.Institution) (model.Institution, error) {
	row, err := r.q.InsertInstitution(ctx, dbgen.InsertInstitutionParams{ID: institution.ID, Name: institution.Name})
	if err != nil {
		return model.Institution{}, mapError(err)
	}
	return toInstitution(row), nil
}

// Get returns the institution with the given id.
func (r *InstitutionRepo) Get(ctx context.Context, id uuid.UUID) (model.Institution, error) {
	row, err := r.q.GetInstitution(ctx, id)
	if err != nil {
		return model.Institution{}, mapError(err)
	}
	return toInstitution(row), nil
}

// List returns the institutions by name.
func (r *InstitutionRepo) List(ctx context.Context, includeArchived bool) ([]model.Institution, error) {
	rows, err := r.q.ListInstitutions(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	institutions := make([]model.Institution, len(rows))
	for i, row := range rows {
		institutions[i] = toInstitution(row)
	}
	return institutions, nil
}

// Archive archives the institution; the first time it was archived is kept.
func (r *InstitutionRepo) Archive(ctx context.Context, id uuid.UUID) (model.Institution, error) {
	row, err := r.q.ArchiveInstitution(ctx, id)
	if err != nil {
		return model.Institution{}, mapError(err)
	}
	return toInstitution(row), nil
}

func toInstitution(row dbgen.FinInstitution) model.Institution {
	return model.Institution{
		ID:         row.ID,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}
