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

// Insert stores account. The table's CHECK constraints are the last guard of the
// kind and of the closing day and payment due day, and its foreign key the last
// guard of the institution: a value that breaks one comes back as an error from
// the database.
func (r *AccountRepo) Insert(ctx context.Context, account model.Account) (model.Account, error) {
	row, err := r.q.InsertAccount(ctx, dbgen.InsertAccountParams{
		ID:            account.ID,
		Name:          account.Name,
		InstitutionID: nullUUID(account.InstitutionID),
		Kind:          string(account.Kind),
		ClosingDay:    dayOfMonthColumn(account.ClosingDay),
		PaymentDueDay: dayOfMonthColumn(account.PaymentDueDay),
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

// Lock locks the account's row until the database transaction of the context
// ends (queries/account.sql says which lock). Outside a transaction it holds the
// row for no longer than the one statement, so it is of use only inside one.
func (r *AccountRepo) Lock(ctx context.Context, id uuid.UUID) error {
	if _, err := r.q.LockAccount(ctx, id); err != nil {
		return mapError(err)
	}
	return nil
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

// SetStatementDays replaces the closing day and the payment due day of the
// account. The table's CHECK constraints are the last guard of what they may be.
func (r *AccountRepo) SetStatementDays(ctx context.Context, id uuid.UUID, closingDay, paymentDueDay *int) (model.Account, error) {
	row, err := r.q.SetAccountStatementDays(ctx, dbgen.SetAccountStatementDaysParams{
		ID:            id,
		ClosingDay:    dayOfMonthColumn(closingDay),
		PaymentDueDay: dayOfMonthColumn(paymentDueDay),
	})
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
		ClosingDay:    dayOfMonth(row.ClosingDay),
		PaymentDueDay: dayOfMonth(row.PaymentDueDay),
	}
}

// dayOfMonthColumn returns the column value of an optional day of the month, which
// the table keeps in a smallint.
func dayOfMonthColumn(day *int) *int16 {
	if day == nil {
		return nil
	}
	return new(int16(*day))
}

// dayOfMonth returns the day of the month a column holds, nil for NULL.
func dayOfMonth(column *int16) *int {
	if column == nil {
		return nil
	}
	return new(int(*column))
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
