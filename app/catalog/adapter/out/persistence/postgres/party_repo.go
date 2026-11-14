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

// PartyRepo implements repo.PartyRepo on the catalog_party table.
type PartyRepo struct {
	q *dbgen.Queries
}

var _ repo.PartyRepo = (*PartyRepo)(nil)

// NewPartyRepo returns a repo that reads and writes through pool. Its statements
// join the transaction a context carries (see pkg/postgres).
func NewPartyRepo(pool *pgxpool.Pool) *PartyRepo {
	return &PartyRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores party.
func (r *PartyRepo) Insert(ctx context.Context, party model.Party) (model.Party, error) {
	row, err := r.q.InsertParty(ctx, dbgen.InsertPartyParams{ID: party.ID, Name: party.Name})
	if err != nil {
		return model.Party{}, mapError(err)
	}
	return toParty(row), nil
}

// Get returns the party with the given id.
func (r *PartyRepo) Get(ctx context.Context, id uuid.UUID) (model.Party, error) {
	row, err := r.q.GetParty(ctx, id)
	if err != nil {
		return model.Party{}, mapError(err)
	}
	return toParty(row), nil
}

// GetByIDs returns the parties that exist among ids.
func (r *PartyRepo) GetByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Party, error) {
	rows, err := r.q.GetPartiesByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return toParties(rows), nil
}

// List returns the parties by name.
func (r *PartyRepo) List(ctx context.Context, includeArchived bool) ([]model.Party, error) {
	rows, err := r.q.ListParties(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	return toParties(rows), nil
}

// Rename sets the name of the party.
func (r *PartyRepo) Rename(ctx context.Context, id uuid.UUID, name string) (model.Party, error) {
	row, err := r.q.RenameParty(ctx, dbgen.RenamePartyParams{ID: id, Name: name})
	if err != nil {
		return model.Party{}, mapError(err)
	}
	return toParty(row), nil
}

// Archive archives the party; the first time it was archived is kept.
func (r *PartyRepo) Archive(ctx context.Context, id uuid.UUID) (model.Party, error) {
	row, err := r.q.ArchiveParty(ctx, id)
	if err != nil {
		return model.Party{}, mapError(err)
	}
	return toParty(row), nil
}

func toParty(row dbgen.CatalogParty) model.Party {
	return model.Party{
		ID:         row.ID,
		Name:       row.Name,
		CreatedAt:  row.CreatedAt,
		ArchivedAt: row.ArchivedAt,
	}
}

func toParties(rows []dbgen.CatalogParty) []model.Party {
	parties := make([]model.Party, len(rows))
	for i, row := range rows {
		parties[i] = toParty(row)
	}
	return parties
}
