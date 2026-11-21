package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

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
//
// A transaction of an instalment plan stores its place in it, which the table
// checks (fin_transaction_plan_*): a period refers to the origin of its plan, so
// the origin is stored before the periods.
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
		PlanID:         nullUUID(txn.PlanID),
		PlanSeq:        planSeqColumn(txn.PlanSeq),
		PostedOn:       txn.PostedOn,
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
			PartyID:       nullUUID(p.PartyID),
			InstrumentID:  p.InstrumentID,
			Quantity:      p.Quantity,
			BaseAmount:    nullDecimal(p.BaseAmount),
			Discount:      nullDecimal(p.Discount),
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

// GetPlan returns the instalment plan whose origin has the id: the origin, and
// the periods in the order they fall due. The rows of the plan are read by one
// statement, so they agree on what status they have. An id that no plan has, a
// period's among them, is model.ErrNotFound.
func (r *TransactionRepo) GetPlan(ctx context.Context, planID uuid.UUID) (model.InstalmentPlan, error) {
	rows, err := r.q.ListPlanTransactions(ctx, nullUUID(&planID))
	if err != nil {
		return model.InstalmentPlan{}, fmt.Errorf("list the transactions of plan %s: %w", planID, err)
	}
	if len(rows) == 0 {
		return model.InstalmentPlan{}, model.ErrNotFound
	}
	txns, err := r.load(ctx, rows)
	if err != nil {
		return model.InstalmentPlan{}, err
	}
	if !txns[0].IsPlanOrigin() || txns[0].ID != planID {
		// The table does not let a period refer to anything but a stored transaction,
		// and an origin is the transaction its plan is named after; this is what it
		// would take to get here, and not what a plan looks like.
		return model.InstalmentPlan{}, fmt.Errorf("instalment plan %s has no origin: its first transaction is %s", planID, txns[0].ID)
	}
	return model.InstalmentPlan{Origin: txns[0], Periods: txns[1:]}, nil
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

// SetPostedOn sets the posting date of the transaction, or clears it with nil. The
// table's CHECK constraints are the last guard of the date: one before the day the
// transaction happened, and any on a transaction of an instalment plan, come back
// as an error from the database.
func (r *TransactionRepo) SetPostedOn(ctx context.Context, id uuid.UUID, postedOn *time.Time) (model.Transaction, error) {
	row, err := r.q.SetTransactionPostedOn(ctx, dbgen.SetTransactionPostedOnParams{ID: id, PostedOn: postedOn})
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
		txns, err = r.loadByIDs(ctx, ids)
		return err
	})
	if err != nil {
		return nil, err
	}
	return txns, nil
}

// loadByIDs returns the transactions of the ids, in the order of the ids, each
// with its postings and tags. It must be called in the snapshot the ids were read
// in: there are as many transactions as ids.
func (r *TransactionRepo) loadByIDs(ctx context.Context, ids []uuid.UUID) ([]model.Transaction, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := r.q.GetTransactionsByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("get transactions: %w", err)
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
			return nil, fmt.Errorf("transaction %s was listed and is gone: the list was not read in one snapshot", id)
		}
		ordered[i] = row
	}
	return r.load(ctx, ordered)
}

// Balances adds up the quantities of the postings to accounts, of the confirmed
// transactions that happened on or before the day, for each account and
// instrument: the account postings are what the account holds, and the
// instalment postings what has not fallen due of its instalment plans. For one
// account, or one instrument, or both, when the filter says so.
func (r *TransactionRepo) Balances(ctx context.Context, filter repo.BalanceFilter) ([]model.Balance, error) {
	rows, err := r.q.ListBalances(ctx, dbgen.ListBalancesParams{
		Day:          filter.Day,
		AccountID:    nullUUID(filter.AccountID),
		InstrumentID: nullUUID(filter.InstrumentID),
	})
	if err != nil {
		return nil, err
	}
	balances := make([]model.Balance, len(rows))
	for i, row := range rows {
		balances[i] = model.Balance{
			AccountID:          row.AccountID,
			InstrumentID:       row.InstrumentID,
			Quantity:           row.Quantity,
			UnbilledInstalment: row.UnbilledInstalment,
		}
	}
	return balances, nil
}

// PendingDrafts counts the drafts that happened on or before the day and have a
// posting to the account in the instrument.
func (r *TransactionRepo) PendingDrafts(ctx context.Context, accountID, instrumentID uuid.UUID, day time.Time) (int, error) {
	n, err := r.q.CountPendingDrafts(ctx, dbgen.CountPendingDraftsParams{
		AccountID:    nullUUID(&accountID),
		InstrumentID: instrumentID,
		Day:          day,
	})
	return int(n), err
}

// Dealings adds up the quantities of the dealing postings, of the confirmed
// transactions that happened on or before the day, for each party and
// instrument; for one party when partyID is set.
func (r *TransactionRepo) Dealings(ctx context.Context, partyID *uuid.UUID, day time.Time) ([]model.Dealing, error) {
	rows, err := r.q.ListDealings(ctx, dbgen.ListDealingsParams{Day: day, PartyID: nullUUID(partyID)})
	if err != nil {
		return nil, err
	}
	dealings := make([]model.Dealing, len(rows))
	for i, row := range rows {
		dealings[i] = model.Dealing{PartyID: row.PartyID, InstrumentID: row.InstrumentID, Quantity: row.Quantity}
	}
	return dealings, nil
}

// InstalmentPlans returns where each confirmed instalment plan stands on the day,
// newest first. The statement adds up what the plans and their periods say; which
// of them are finished is for the use case.
func (r *TransactionRepo) InstalmentPlans(ctx context.Context, accountID *uuid.UUID, day time.Time) ([]model.InstalmentPlanSummary, error) {
	rows, err := r.q.ListInstalmentPlans(ctx, dbgen.ListInstalmentPlansParams{Day: day, AccountID: nullUUID(accountID)})
	if err != nil {
		return nil, err
	}
	plans := make([]model.InstalmentPlanSummary, len(rows))
	for i, row := range rows {
		nextDueOn, err := optionalDay(row.NextDueOn)
		if err != nil {
			return nil, fmt.Errorf("plan %s: next due day: %w", row.PlanID, err)
		}
		plans[i] = model.InstalmentPlanSummary{
			PlanID:       row.PlanID,
			AccountID:    row.AccountID,
			InstrumentID: row.InstrumentID,
			Principal:    row.Principal,
			Charged:      row.Charged,
			NextDueOn:    nextDueOn,
		}
	}
	return plans, nil
}

// StatementFigures adds up what each instrument did on each of the statements of
// the account: one statement of SQL for all of them (ListStatementFigures), which
// is what keeps the Closing of one statement the Previous of the next. The figures
// of a period are those with its position, in the order of the instrument codes.
func (r *TransactionRepo) StatementFigures(ctx context.Context, accountID uuid.UUID, periods []model.StatementPeriod, today time.Time) ([][]model.StatementFigures, error) {
	if len(periods) == 0 {
		return nil, nil
	}
	froms := make([]time.Time, len(periods))
	closings := make([]time.Time, len(periods))
	for i, p := range periods {
		froms[i], closings[i] = p.From, p.ClosingOn
	}
	rows, err := r.q.ListStatementFigures(ctx, dbgen.ListStatementFiguresParams{
		Today:       today,
		FromDays:    froms,
		ClosingDays: closings,
		AccountID:   nullUUID(&accountID),
	})
	if err != nil {
		return nil, err
	}
	figures := make([][]model.StatementFigures, len(periods))
	for _, row := range rows {
		i := int(row.Idx) - 1
		if i < 0 || i >= len(figures) {
			return nil, fmt.Errorf("figures of statement %d, and only %d were asked for", row.Idx, len(periods))
		}
		figures[i] = append(figures[i], model.NewStatementFigures(row.InstrumentID, row.Previous, row.Charges, row.Credits))
	}
	return figures, nil
}

// Statement reads one statement: the figures, the transactions on it and the
// drafts waiting for it, in one snapshot (see WithinSnapshot in pkg/postgres), so
// that the transactions listed are those the figures add up. What the statement
// counts stops at the day it closes or today, whichever is earlier; the drafts do
// not stop at today.
func (r *TransactionRepo) Statement(ctx context.Context, f repo.StatementFilter) (repo.StatementContent, error) {
	var content repo.StatementContent
	err := r.db.WithinSnapshot(ctx, func(ctx context.Context) error {
		figures, err := r.StatementFigures(ctx, f.AccountID, []model.StatementPeriod{f.Period}, f.Today)
		if err != nil {
			return fmt.Errorf("read the figures of the statement: %w", err)
		}
		content.Figures = figures[0]

		countedTo := f.Period.ClosingOn
		if f.Today.Before(countedTo) {
			countedTo = f.Today
		}
		ids, err := r.q.ListStatementTransactionIDs(ctx, dbgen.ListStatementTransactionIDsParams{
			FromDay:   f.Period.From,
			ToDay:     countedTo,
			AccountID: nullUUID(&f.AccountID),
		})
		if err != nil {
			return fmt.Errorf("list the transactions of the statement: %w", err)
		}
		if content.Transactions, err = r.loadByIDs(ctx, ids); err != nil {
			return err
		}

		drafts, err := r.q.CountStatementDrafts(ctx, dbgen.CountStatementDraftsParams{
			FromDay:    f.Period.From,
			ClosingDay: f.Period.ClosingOn,
			AccountID:  nullUUID(&f.AccountID),
		})
		if err != nil {
			return fmt.Errorf("count the drafts of the statement: %w", err)
		}
		content.Drafts = int(drafts)
		return nil
	})
	if err != nil {
		return repo.StatementContent{}, err
	}
	return content, nil
}

// DraftSummary reads the drafts that are waiting: how many there are in each month,
// how many of each month move each account, and what they would move of each
// instrument, as three statements in one snapshot (see WithinSnapshot in
// pkg/postgres). Read apart they could disagree, a draft that is confirmed between
// two of them being in one and not in the other; in one snapshot every number is of
// the same drafts.
func (r *TransactionRepo) DraftSummary(ctx context.Context) (model.DraftSummary, error) {
	var summary model.DraftSummary
	err := r.db.WithinSnapshot(ctx, func(ctx context.Context) error {
		months, err := r.q.ListDraftMonths(ctx)
		if err != nil {
			return fmt.Errorf("count the drafts of each month: %w", err)
		}
		accounts, err := r.q.ListDraftAccounts(ctx)
		if err != nil {
			return fmt.Errorf("count the drafts that move each account: %w", err)
		}
		amounts, err := r.q.ListDraftAmounts(ctx)
		if err != nil {
			return fmt.Errorf("add up what the drafts move: %w", err)
		}
		summary, err = draftSummaryOf(months, accounts, amounts)
		return err
	})
	if err != nil {
		return model.DraftSummary{}, err
	}
	return summary, nil
}

// draftSummaryOf puts the rows of the three statements of DraftSummary together:
// the accounts under their months, in the order of their rows, and the amounts under
// the account of the month they are of. A row that has no place to go means that the
// statements were not read in one snapshot.
func draftSummaryOf(months []dbgen.ListDraftMonthsRow, accounts []dbgen.ListDraftAccountsRow, amounts []dbgen.ListDraftAmountsRow) (model.DraftSummary, error) {
	summary := model.DraftSummary{Months: make([]model.DraftMonth, len(months))}
	monthAt := make(map[int64]int, len(months)) // a month, as the Unix time of its first day, to its place in summary.Months
	for i, row := range months {
		summary.Months[i] = model.DraftMonth{Month: row.Month, Count: int(row.Drafts)}
		monthAt[row.Month.Unix()] = i
	}

	// group is an account, or none, in a month; place is where it is in the summary.
	type group struct {
		month   int64
		account uuid.NullUUID
	}
	type place struct{ month, account int }
	accountAt := make(map[group]place, len(accounts))
	for _, row := range accounts {
		m, ok := monthAt[row.Month.Unix()]
		if !ok {
			return model.DraftSummary{}, fmt.Errorf("the drafts of %s that move accounts were not counted for the month: the statements were not read in one snapshot",
				row.Month.Format("2006-01"))
		}
		month := &summary.Months[m]
		accountAt[group{row.Month.Unix(), row.AccountID}] = place{month: m, account: len(month.Accounts)}
		month.Accounts = append(month.Accounts, model.DraftAccount{AccountID: optionalUUID(row.AccountID), Count: int(row.Drafts)})
	}

	for _, row := range amounts {
		at, ok := accountAt[group{row.Month.Unix(), uuid.NullUUID{UUID: row.AccountID, Valid: true}}]
		if !ok {
			return model.DraftSummary{}, fmt.Errorf("the drafts of %s that move account %s were not counted for it: the statements were not read in one snapshot",
				row.Month.Format("2006-01"), row.AccountID)
		}
		account := &summary.Months[at.month].Accounts[at.account]
		account.Amounts = append(account.Amounts, model.DraftAmount{
			InstrumentID:   row.InstrumentID,
			InstrumentCode: row.InstrumentCode,
			Outflow:        row.Outflow,
			Inflow:         row.Inflow,
		})
	}
	return summary, nil
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
		PlanID:         optionalUUID(row.PlanID),
		PlanSeq:        planSeq(row.PlanSeq),
		PostedOn:       row.PostedOn,
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
		PartyID:      optionalUUID(row.PartyID),
		InstrumentID: row.InstrumentID,
		Quantity:     row.Quantity,
		Memo:         row.Memo,
	}
	if row.BaseAmount.Valid {
		base := row.BaseAmount.Decimal
		p.BaseAmount = &base
	}
	if row.Discount.Valid {
		discount := row.Discount.Decimal
		p.Discount = &discount
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

// planSeqColumn returns the column value of the position in a plan, which is
// NULL for a transaction that is in none.
func planSeqColumn(seq *int) *int32 {
	if seq == nil {
		return nil
	}
	return new(int32(*seq))
}

// planSeq returns the position in a plan that a column holds, nil for NULL.
func planSeq(column *int32) *int {
	if column == nil {
		return nil
	}
	return new(int(*column))
}

// optionalDay returns the day that the value of a date column that may be NULL
// holds: the statement that reads it is an aggregate, whose type sqlc does not
// know, so it comes as it was scanned. NULL is nil.
func optionalDay(v any) (*time.Time, error) {
	switch day := v.(type) {
	case nil:
		return nil, nil
	case time.Time:
		return &day, nil
	default:
		return nil, fmt.Errorf("a %T, want a date", v)
	}
}
