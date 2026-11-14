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

// AccountRepo implements repo.AccountRepo on the fin_account table.
type AccountRepo struct {
	q *dbgen.Queries
}

var _ repo.AccountRepo = (*AccountRepo)(nil)

// NewAccountRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewAccountRepo(pool *pgxpool.Pool) *AccountRepo {
	return &AccountRepo{q: dbgen.New(pkgpostgres.NewDB(pool))}
}

// Insert stores account. The table's CHECK constraint is the last guard of the
// kind and its foreign key the last guard of the institution: a value that
// breaks one comes back as an error from the database.
func (r *AccountRepo) Insert(ctx context.Context, account model.Account) (model.Account, error) {
	row, err := r.q.InsertAccount(ctx, dbgen.InsertAccountParams{
		ID:            account.ID,
		Name:          account.Name,
		InstitutionID: nullUUID(account.InstitutionID),
		Kind:          string(account.Kind),
	})
	if err != nil {
		return model.Account{}, mapError(err)
	}
	return toAccount(row), nil
}

// Get returns the account with the given id.
func (r *AccountRepo) Get(ctx context.Context, id uuid.UUID) (model.Account, error) {
	row, err := r.q.GetAccount(ctx, id)
	if err != nil {
		return model.Account{}, mapError(err)
	}
	return toAccount(row), nil
}

// List returns the accounts by name.
func (r *AccountRepo) List(ctx context.Context, includeArchived bool) ([]model.Account, error) {
	rows, err := r.q.ListAccounts(ctx, includeArchived)
	if err != nil {
		return nil, err
	}
	accounts := make([]model.Account, len(rows))
	for i, row := range rows {
		accounts[i] = toAccount(row)
	}
	return accounts, nil
}

// Archive archives the account; the first time it was archived is kept.
func (r *AccountRepo) Archive(ctx context.Context, id uuid.UUID) (model.Account, error) {
	row, err := r.q.ArchiveAccount(ctx, id)
	if err != nil {
		return model.Account{}, mapError(err)
	}
	return toAccount(row), nil
}

func toAccount(row dbgen.FinAccount) model.Account {
	return model.Account{
		ID:            row.ID,
		Name:          row.Name,
		InstitutionID: optionalUUID(row.InstitutionID),
		Kind:          model.AccountKind(row.Kind),
		CreatedAt:     row.CreatedAt,
		ArchivedAt:    row.ArchivedAt,
	}
}

// nullUUID returns the column value of an optional id.
func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

// optionalUUID returns the optional id a column value holds, nil for NULL.
func optionalUUID(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	return &id.UUID
}
