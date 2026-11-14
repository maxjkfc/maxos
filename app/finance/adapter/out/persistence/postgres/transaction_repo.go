package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// TransactionRepo implements repo.TransactionRepo on the fin_transaction,
// fin_posting and fin_transaction_tag tables.
type TransactionRepo struct {
	// db runs the statements written by hand (see queries.go); q runs the
	// generated ones. Both join the transaction a context carries.
	db *pkgpostgres.DB
	q  *dbgen.Queries
}

var _ repo.TransactionRepo = (*TransactionRepo)(nil)

// NewTransactionRepo returns a repo that reads and writes through pool. Its
// statements join the transaction a context carries (see pkg/postgres).
func NewTransactionRepo(pool *pgxpool.Pool) *TransactionRepo {
	db := pkgpostgres.NewDB(pool)
	return &TransactionRepo{db: db, q: dbgen.New(db)}
}

// Insert stores the transaction row, then its postings and its tags, each in a
// statement of its own: the use case puts them in one database transaction. The
// table's CHECK constraints are the last guard of what a posting must carry, and
// the deferred trigger of fin_transaction and fin_posting the last guard of the
// balance; it fails the commit of a transaction whose postings do not add up to
// zero in each instrument.
//
// A new transaction is a draft: it has no confirmed time to store, and the
// table refuses a row with any other status (fin_transaction_status_times_check).
// Confirming goes through SetStatus.
//
// The insert of the row is INSERT ... ON CONFLICT DO NOTHING: it returns no row
// (pgx.ErrNoRows) when the idempotency key is stored, and then nothing else is
// written. When a concurrent database transaction holds the key without having
// committed, the insert waits until it has, so what "stored" means is settled by
// then.
func (r *TransactionRepo) Insert(ctx context.Context, txn model.Transaction) (model.Transaction, error) {
	row, err := r.q.InsertTransaction(ctx, dbgen.InsertTransactionParams{
		ID:             txn.ID,
		Kind:           string(txn.Kind),
		Status:         string(txn.Status),
		OccurredOn:     txn.OccurredOn,
		OccurredAt:     txn.OccurredAt,
		Description:    txn.Description,
		Merchant:       txn.Merchant,
		ProjectID:      nullUUID(txn.ProjectID),
		AreaID:         nullUUID(txn.AreaID),
		IdempotencyKey: txn.IdempotencyKey,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.Transaction{}, repo.ErrDuplicateKey
	}
	if err != nil {
		return model.Transaction{}, fmt.Errorf("insert transaction: %w", err)
	}

	for _, p := range txn.Postings {
		id, err := uuid.NewV7()
		if err != nil {
			return model.Transaction{}, fmt.Errorf("generate posting id: %w", err)
		}
		err = r.q.InsertPosting(ctx, dbgen.InsertPostingParams{
			ID:            id,
			TransactionID: txn.ID,
			Seq:           int16(p.Seq),
			Book:          string(p.Book),
			AccountID:     nullUUID(p.AccountID),
			CategoryID:    nullUUID(p.CategoryID),
			InstrumentID:  p.InstrumentID,
			Quantity:      p.Quantity,
			BaseAmount:    nullDecimal(p.BaseAmount),
			Memo:          p.Memo,
		})
		if err != nil {
			return model.Transaction{}, fmt.Errorf("insert posting %d: %w", p.Seq, err)
		}
	}
	for _, tagID := range txn.TagIDs {
		err := r.q.InsertTransactionTag(ctx, dbgen.InsertTransactionTagParams{TransactionID: txn.ID, TagID: tagID})
		if err != nil {
			return model.Transaction{}, fmt.Errorf("insert tag %s: %w", tagID, err)
		}
	}

	stored := toTransaction(row)
	stored.Postings = slices.Clone(txn.Postings)
	stored.TagIDs = slices.Clone(txn.TagIDs)
	return stored, nil
}

// Get returns the transaction with the given id.
func (r *TransactionRepo) Get(ctx context.Context, id uuid.UUID) (model.Transaction, error) {
	row, err := r.q.GetTransaction(ctx, id)
	if err != nil {
		return model.Transaction{}, mapError(err)
	}
	return r.loadOne(ctx, row)
}

// GetForUpdate returns the transaction with the given id and locks its row until
// the database transaction ends.
func (r *TransactionRepo) GetForUpdate(ctx context.Context, id uuid.UUID) (model.Transaction, error) {
	row, err := r.q.GetTransactionForUpdate(ctx, id)
	if err != nil {
		return model.Transaction{}, mapError(err)
	}
	return r.loadOne(ctx, row)
}

// GetByIdempotencyKey returns the transaction stored under the key.
func (r *TransactionRepo) GetByIdempotencyKey(ctx context.Context, key string) (model.Transaction, error) {
	row, err := r.q.GetTransactionByIdempotencyKey(ctx, key)
	if err != nil {
		return model.Transaction{}, mapError(err)
	}
	return r.loadOne(ctx, row)
}

// SetStatus moves the transaction to the status and stamps the time it got there.
// The table's CHECK constraints are the last guard of the order of the statuses:
// a move that skips the confirmation comes back as an error from the database.
func (r *TransactionRepo) SetStatus(ctx context.Context, id uuid.UUID, status model.Status) (model.Transaction, error) {
	row, err := r.q.SetTransactionStatus(ctx, dbgen.SetTransactionStatusParams{ID: id, Status: string(status)})
	if err != nil {
		return model.Transaction{}, mapError(err)
	}
	return r.loadOne(ctx, row)
}

// Delete deletes the transaction; its postings and tags go with it.
func (r *TransactionRepo) Delete(ctx context.Context, id uuid.UUID) error {
	deleted, err := r.q.DeleteTransaction(ctx, id)
	if err != nil {
		return err
	}
	if deleted == 0 {
		return model.ErrNotFound
	}
	return nil
}

// List returns the transactions that match the filter, newest first. The order
// and the paging are decided by the statement of listIDs; the rows themselves,
// and their postings and tags, are then read by the generated queries.
//
// It reads all of it in one snapshot (see WithinSnapshot in pkg/postgres), so
// the rows are those of the ids, as the filter matched them: a transaction
// deleted or changed meanwhile is neither missing from the page nor in it with a
// status the filter does not allow. There are as many rows as ids, which is what
// lets the use case tell from the number it gets whether another page follows.
func (r *TransactionRepo) List(ctx context.Context, filter repo.TransactionFilter) ([]model.Transaction, error) {
	var txns []model.Transaction
	err := r.db.WithinSnapshot(ctx, func(ctx context.Context) error {
		ids, err := r.listIDs(ctx, filter)
		if err != nil {
			return fmt.Errorf("list transaction ids: %w", err)
		}
		if len(ids) == 0 {
			return nil
		}
		rows, err := r.q.GetTransactionsByIDs(ctx, ids)
		if err != nil {
			return fmt.Errorf("get transactions: %w", err)
		}

		byID := make(map[uuid.UUID]dbgen.FinTransaction, len(rows))
		for _, row := range rows {
			byID[row.ID] = row
		}
		ordered := make([]dbgen.FinTransaction, len(ids))
		for i, id := range ids {
			row, ok := byID[id]
			if !ok {
				// Impossible in one snapshot. It is what a list read outside one,
				// in a transaction that is not REPEATABLE READ, can come to, and
				// a page that quietly lacks a transaction is wrong.
				return fmt.Errorf("transaction %s was listed and is gone: the list was not read in one snapshot", id)
			}
			ordered[i] = row
		}
		txns, err = r.load(ctx, ordered)
		return err
	})
	if err != nil {
		return nil, err
	}
	return txns, nil
}

// Balances adds up the quantities of the postings to accounts, of the confirmed
// transactions only, for each account and instrument; for one account when
// accountID is set.
func (r *TransactionRepo) Balances(ctx context.Context, accountID *uuid.UUID) ([]model.Balance, error) {
	rows, err := r.q.ListBalances(ctx, nullUUID(accountID))
	if err != nil {
		return nil, err
	}
	balances := make([]model.Balance, len(rows))
	for i, row := range rows {
		balances[i] = model.Balance{AccountID: row.AccountID, InstrumentID: row.InstrumentID, Quantity: row.Quantity}
	}
	return balances, nil
}

// loadOne returns the transaction a row holds, with its postings and tags.
func (r *TransactionRepo) loadOne(ctx context.Context, row dbgen.FinTransaction) (model.Transaction, error) {
	txns, err := r.load(ctx, []dbgen.FinTransaction{row})
	if err != nil {
		return model.Transaction{}, err
	}
	return txns[0], nil
}

// load returns the transactions the rows hold, in their order, each with its
// postings (by position) and tags (by id). It reads the postings and the tags of
// all of them at once.
func (r *TransactionRepo) load(ctx context.Context, rows []dbgen.FinTransaction) ([]model.Transaction, error) {
	if len(rows) == 0 {
		return nil, nil
	}
	ids := make([]uuid.UUID, len(rows))
	index := make(map[uuid.UUID]int, len(rows))
	txns := make([]model.Transaction, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
		index[row.ID] = i
		txns[i] = toTransaction(row)
	}

	postings, err := r.q.ListPostings(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list postings: %w", err)
	}
	for _, p := range postings {
		i := index[p.TransactionID]
		txns[i].Postings = append(txns[i].Postings, toPosting(p))
	}

	tags, err := r.q.ListTransactionTags(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	for _, tag := range tags {
		i := index[tag.TransactionID]
		txns[i].TagIDs = append(txns[i].TagIDs, tag.TagID)
	}
	return txns, nil
}

// toTransaction maps a row to the transaction it holds, without its postings and
// tags.
func toTransaction(row dbgen.FinTransaction) model.Transaction {
	return model.Transaction{
		ID:             row.ID,
		Kind:           model.Kind(row.Kind),
		Status:         model.Status(row.Status),
		OccurredOn:     row.OccurredOn,
		OccurredAt:     row.OccurredAt,
		Description:    row.Description,
		Merchant:       row.Merchant,
		ProjectID:      optionalUUID(row.ProjectID),
		AreaID:         optionalUUID(row.AreaID),
		IdempotencyKey: row.IdempotencyKey,
		CreatedAt:      row.CreatedAt,
		ConfirmedAt:    row.ConfirmedAt,
		VoidedAt:       row.VoidedAt,
	}
}

// toPosting maps a row to a posting. The id of the row is a surrogate key: the
// model knows a posting by its position in its transaction.
func toPosting(row dbgen.FinPosting) model.Posting {
	p := model.Posting{
		Seq:          int(row.Seq),
		Book:         model.Book(row.Book),
		AccountID:    optionalUUID(row.AccountID),
		CategoryID:   optionalUUID(row.CategoryID),
		InstrumentID: row.InstrumentID,
		Quantity:     row.Quantity,
		Memo:         row.Memo,
	}
	if row.BaseAmount.Valid {
		base := row.BaseAmount.Decimal
		p.BaseAmount = &base
	}
	return p
}

// nullDecimal returns the column value of an optional amount.
func nullDecimal(d *decimal.Decimal) decimal.NullDecimal {
	if d == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *d, Valid: true}
}
