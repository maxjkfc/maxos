package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/internal/testpg"
	pkgpostgres "github.com/maxjkfc/maxos/pkg/postgres"
)

// The SQLSTATE of a unique violation; the others are in instrument_repo_integration_test.go.
const uniqueViolation = "23505"

var ledgerDay = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

// ledgerEnv is a database holding what transactions point at: two accounts, the
// base currency and USD, an expense category and an income category. The names
// are ASCII, so that the order they are listed in does not depend on a collation.
type ledgerEnv struct {
	t            *testing.T
	pool         *pgxpool.Pool
	db           *pkgpostgres.DB
	txns         *postgres.TransactionRepo
	cash, bank   model.Account
	twd, usd     model.Instrument
	food, salary model.Category
}

func newLedgerEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	ctx := context.Background()
	pool := testpg.New(t)

	instruments := postgres.NewInstrumentRepo(pool)
	twd, err := instruments.Get(ctx, model.BaseCurrencyID)
	if err != nil {
		t.Fatalf("Get the base currency: %v", err)
	}
	usd, err := model.NewInstrument(model.NewInstrumentParams{
		ID: uuid.Must(uuid.NewV7()), Code: "USD", Name: "US dollar", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	if usd, err = instruments.Insert(ctx, usd); err != nil {
		t.Fatalf("Insert USD: %v", err)
	}

	accounts := postgres.NewAccountRepo(pool)
	categories := postgres.NewCategoryRepo(pool)
	return &ledgerEnv{
		t:      t,
		pool:   pool,
		db:     pkgpostgres.NewDB(pool),
		txns:   postgres.NewTransactionRepo(pool),
		cash:   insertAccount(t, accounts, "cash"),
		bank:   insertAccount(t, accounts, "bank"),
		twd:    twd,
		usd:    usd,
		food:   insertCategory(t, categories, model.DirectionExpense, nil, "food"),
		salary: insertCategory(t, categories, model.DirectionIncome, nil, "salary"),
	}
}

func (e *ledgerEnv) names() map[uuid.UUID]string {
	return map[uuid.UUID]string{
		e.cash.ID: "cash", e.bank.ID: "bank", e.twd.ID: "TWD", e.usd.ID: "USD", e.food.ID: "food", e.salary.ID: "salary",
	}
}

func (e *ledgerEnv) header(key string, day time.Time) model.Header {
	return model.Header{ID: uuid.Must(uuid.NewV7()), IdempotencyKey: key, OccurredOn: day}
}

// expenseOf makes the draft of an expense of the amount of TWD, paid from cash,
// with the header.
func (e *ledgerEnv) expenseOf(h model.Header, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.CategorizedParams{
		Header:       h,
		Account:      e.cash,
		Instrument:   e.twd,
		BaseCurrency: e.twd,
		Lines:        []model.CategoryLine{{Category: e.food, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("NewExpense: %v", err)
	}
	return txn
}

func (e *ledgerEnv) expense(key, amount string) model.Transaction {
	e.t.Helper()
	return e.expenseOf(e.header(key, ledgerDay), amount)
}

// opening makes the draft of the opening balance of an account in TWD.
func (e *ledgerEnv) opening(key string, account model.Account, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewOpeningBalance(model.OpeningBalanceParams{
		Header: e.header(key, ledgerDay), Account: account, Instrument: e.twd, Amount: dec(amount),
	})
	if err != nil {
		e.t.Fatalf("NewOpeningBalance: %v", err)
	}
	return txn
}

// storeConfirmed stores the draft and confirms it with SetStatus, which is how a
// transaction gets confirmed: Insert stores drafts only. It returns it confirmed.
func (e *ledgerEnv) storeConfirmed(txn model.Transaction) model.Transaction {
	e.t.Helper()
	stored := e.store(txn)
	return e.setStatus(stored.ID, model.StatusConfirmed)
}

// inTx runs fn in a database transaction, the way a use case does.
func (e *ledgerEnv) inTx(fn func(ctx context.Context) error) error {
	return e.db.WithinTx(context.Background(), fn)
}

// store inserts the transaction in a database transaction of its own and returns
// it as stored.
func (e *ledgerEnv) store(txn model.Transaction) model.Transaction {
	e.t.Helper()
	var stored model.Transaction
	err := e.inTx(func(ctx context.Context) error {
		var err error
		stored, err = e.txns.Insert(ctx, txn)
		return err
	})
	if err != nil {
		e.t.Fatalf("store transaction %q: %v", txn.IdempotencyKey, err)
	}
	return stored
}

func (e *ledgerEnv) get(id uuid.UUID) model.Transaction {
	e.t.Helper()
	txn, err := e.txns.Get(context.Background(), id)
	if err != nil {
		e.t.Fatalf("Get %s: %v", id, err)
	}
	return txn
}

func (e *ledgerEnv) setStatus(id uuid.UUID, status model.Status) model.Transaction {
	e.t.Helper()
	txn, err := e.txns.SetStatus(context.Background(), id, status)
	if err != nil {
		e.t.Fatalf("SetStatus(%s): %v", status, err)
	}
	return txn
}

// count returns what a query that selects one count returns.
func (e *ledgerEnv) count(query string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		e.t.Fatalf("%s: %v", query, err)
	}
	return n
}

// lines writes the postings of a transaction one to a line, with the names of
// the fixtures, so that two transactions compare as text.
func (e *ledgerEnv) lines(txn model.Transaction) []string {
	names := e.names()
	out := make([]string, 0, len(txn.Postings))
	for _, p := range txn.Postings {
		target := string(p.Book)
		if p.AccountID != nil {
			target += "(" + names[*p.AccountID] + ")"
		}
		if p.CategoryID != nil {
			target += "(" + names[*p.CategoryID] + ")"
		}
		line := fmt.Sprintf("%d %s %s %s", p.Seq, target, p.Quantity, names[p.InstrumentID])
		if p.BaseAmount != nil {
			line += fmt.Sprintf(" base %s", *p.BaseAmount)
		}
		if p.Memo != "" {
			line += fmt.Sprintf(" memo %q", p.Memo)
		}
		out = append(out, line)
	}
	return out
}

func samePtr(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

// wantSame checks that got is the transaction want, in everything the caller
// gives; the fields the store assigns are left to the test.
func (e *ledgerEnv) wantSame(name string, got, want model.Transaction) {
	e.t.Helper()
	if got.ID != want.ID || got.Kind != want.Kind || got.Status != want.Status ||
		!got.OccurredOn.Equal(want.OccurredOn) || !sameTime(got.OccurredAt, want.OccurredAt) ||
		got.Description != want.Description || got.Merchant != want.Merchant || got.IdempotencyKey != want.IdempotencyKey ||
		!samePtr(got.ProjectID, want.ProjectID) || !samePtr(got.AreaID, want.AreaID) ||
		!slices.Equal(got.TagIDs, want.TagIDs) {
		e.t.Errorf("%s = %+v, want %+v", name, got, want)
	}
	if g, w := e.lines(got), e.lines(want); !slices.Equal(g, w) {
		e.t.Errorf("%s postings:\n got %q\nwant %q", name, g, w)
	}
}

// balances returns the balances of the account, or of every account for nil, one
// to a line.
func (e *ledgerEnv) balances(accountID *uuid.UUID) []string {
	e.t.Helper()
	got, err := e.txns.Balances(context.Background(), accountID)
	if err != nil {
		e.t.Fatalf("Balances: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got))
	for i, b := range got {
		lines[i] = fmt.Sprintf("%s %s %s", names[b.AccountID], names[b.InstrumentID], b.Quantity)
	}
	return lines
}

func (e *ledgerEnv) wantBalances(accountID *uuid.UUID, want ...string) {
	e.t.Helper()
	if got := e.balances(accountID); !slices.Equal(got, want) {
		e.t.Fatalf("balances = %q, want %q", got, want)
	}
}

func TestInsertRoundTripsATransactionWithItsPostingsAndTags(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	project, area := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	tagA, tagB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	at := time.Date(2026, 11, 14, 12, 30, 15, 0, time.UTC)

	h := e.header("usd-dinner-0001", ledgerDay)
	h.OccurredAt = &at
	h.Description = "晚餐"
	h.Merchant = "Shake Shack"
	h.ProjectID, h.AreaID = &project, &area
	h.TagIDs = []uuid.UUID{tagB, tagA}
	txn, err := model.NewExpense(model.CategorizedParams{
		Header:       h,
		Account:      e.bank,
		Instrument:   e.usd,
		BaseCurrency: e.twd,
		Lines: []model.CategoryLine{
			{Category: e.food, Amount: dec("100.50"), BaseAmount: decPtr("3216.15"), Memo: "burger"},
			{Category: e.food, Amount: dec("20.25"), BaseAmount: decPtr("648.10")},
		},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	stored := e.store(txn)
	if stored.CreatedAt.IsZero() || stored.ConfirmedAt != nil || stored.VoidedAt != nil {
		t.Errorf("stored = %+v, want a created time the database assigned, and no confirmed or voided time", stored)
	}
	e.wantSame("Insert", stored, txn)
	e.wantSame("Get", e.get(txn.ID), txn)
	byKey, err := e.txns.GetByIdempotencyKey(ctx, "usd-dinner-0001")
	if err != nil {
		t.Fatalf("GetByIdempotencyKey: %v", err)
	}
	e.wantSame("GetByIdempotencyKey", byKey, txn)

	wantLines := []string{
		"1 account(bank) -120.75 USD",
		`2 expense(food) 100.5 USD base 3216.15 memo "burger"`,
		"3 expense(food) 20.25 USD base 648.1",
	}
	if got := e.lines(e.get(txn.ID)); !slices.Equal(got, wantLines) {
		t.Errorf("postings = %q, want %q: every amount exactly as it was given", got, wantLines)
	}
}

func TestInsertReturnsErrDuplicateKeyAndWritesNothingForAKnownKey(t *testing.T) {
	e := newLedgerEnv(t)
	first := e.store(e.expense("same-key-0001", "120"))
	postings := e.count("SELECT count(*) FROM fin_posting")

	err := e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, e.expense("same-key-0001", "999"))
		return err
	})
	if !errors.Is(err, repo.ErrDuplicateKey) {
		t.Fatalf("err = %v, want ErrDuplicateKey", err)
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 1 {
		t.Errorf("%d transactions stored, want 1", got)
	}
	if got := e.count("SELECT count(*) FROM fin_posting"); got != postings {
		t.Errorf("%d postings stored, want the %d of the first", got, postings)
	}
	byKey, err := e.txns.GetByIdempotencyKey(context.Background(), "same-key-0001")
	if err != nil || byKey.ID != first.ID {
		t.Fatalf("GetByIdempotencyKey = %v, %v, want the first transaction", byKey.ID, err)
	}
}

func TestConcurrentInsertsOfOneKeyStoreOneTransaction(t *testing.T) {
	e := newLedgerEnv(t)
	const callers = 6
	const key = "concurrent-key-1"

	// Each caller tries to record its own transaction under the one key, as the
	// ledger use cases do: insert, and on a duplicate read what the winner stored.
	txns := make([]model.Transaction, callers)
	for i := range txns {
		txns[i] = e.expense(key, "120")
	}
	type result struct {
		id       uuid.UUID
		inserted bool
		err      error
	}
	results := make([]result, callers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			results[i].err = e.inTx(func(ctx context.Context) error {
				_, err := e.txns.Insert(ctx, txns[i])
				switch {
				case err == nil:
					results[i].inserted = true
					results[i].id = txns[i].ID
					// Keep the key uncommitted for a while, so that the others reach
					// their insert while it is.
					time.Sleep(150 * time.Millisecond)
					return nil
				case errors.Is(err, repo.ErrDuplicateKey):
					existing, err := e.txns.GetByIdempotencyKey(ctx, key)
					if err != nil {
						return err
					}
					results[i].id = existing.ID
					return nil
				default:
					return err
				}
			})
		}()
	}
	close(start)
	wg.Wait()

	inserted := 0
	var winner uuid.UUID
	for i, r := range results {
		if r.err != nil {
			t.Fatalf("caller %d: %v", i, r.err)
		}
		if r.inserted {
			inserted++
			winner = r.id
		}
	}
	if inserted != 1 {
		t.Fatalf("%d callers stored their transaction, want exactly 1", inserted)
	}
	for i, r := range results {
		if r.id != winner {
			t.Errorf("caller %d got transaction %s, want the one that was stored, %s", i, r.id, winner)
		}
	}
	if got := e.count("SELECT count(*) FROM fin_transaction WHERE idempotency_key = $1", key); got != 1 {
		t.Errorf("%d transactions under the key, want 1", got)
	}
	if got := e.count("SELECT count(*) FROM fin_posting"); got != 2 {
		t.Errorf("%d postings stored, want the 2 of the one transaction", got)
	}
}

func TestABalanceCountsConfirmedTransactionsOnly(t *testing.T) {
	e := newLedgerEnv(t)
	e.storeConfirmed(e.opening("opening-cash-1", e.cash, "1000"))

	spend := e.store(e.expense("lunch-0000001", "120"))
	e.wantBalances(nil, "cash TWD 1000")

	confirmed := e.setStatus(spend.ID, model.StatusConfirmed)
	if confirmed.Status != model.StatusConfirmed || confirmed.ConfirmedAt == nil || confirmed.VoidedAt != nil {
		t.Fatalf("confirmed = %+v, want confirmed with its confirmed time", confirmed)
	}
	e.wantBalances(nil, "cash TWD 880")

	voided := e.setStatus(spend.ID, model.StatusVoid)
	if voided.Status != model.StatusVoid || voided.ConfirmedAt == nil || voided.VoidedAt == nil {
		t.Fatalf("voided = %+v, want void, keeping its confirmed time and with its voided time", voided)
	}
	e.wantBalances(nil, "cash TWD 1000")
	if got := e.lines(e.get(spend.ID)); len(got) != 2 {
		t.Errorf("a void transaction has postings %q, want its two kept as a record", got)
	}
}

func TestABalanceIsPerAccountAndInstrument(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	e.storeConfirmed(e.opening("opening-cash-1", e.cash, "1000"))
	e.storeConfirmed(e.opening("opening-bank-1", e.bank, "5000"))

	transfer, err := model.NewTransfer(model.TransferParams{
		Header: e.header("transfer-00001", ledgerDay), From: e.bank, To: e.cash, Instrument: e.twd, Amount: dec("300"),
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	e.storeConfirmed(transfer)

	exchange, err := model.NewExchange(model.ExchangeParams{
		Header:         e.header("exchange-00001", ledgerDay),
		FromAccount:    e.bank,
		ToAccount:      e.bank,
		FromInstrument: e.twd,
		ToInstrument:   e.usd,
		FromAmount:     dec("3200"),
		ToAmount:       dec("100"),
	})
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	e.storeConfirmed(exchange)

	// A draft is in none of it.
	e.store(e.expense("draft-00000001", "50"))

	e.wantBalances(nil, "bank TWD 1500", "bank USD 100", "cash TWD 1300")
	e.wantBalances(&e.cash.ID, "cash TWD 1300")
	e.wantBalances(&e.bank.ID, "bank TWD 1500", "bank USD 100")

	// Postings that cancel out leave a balance of zero, which is still listed.
	e.storeConfirmed(e.expense("spend-all-0001", "1300"))
	e.wantBalances(&e.cash.ID, "cash TWD 0")

	unknown := uuid.Must(uuid.NewV7())
	if got, err := e.txns.Balances(ctx, &unknown); err != nil || len(got) != 0 {
		t.Errorf("Balances of an account with nothing = %v, %v, want none", got, err)
	}
}

func TestDeletingADraftTakesItsPostingsAndTagsWithIt(t *testing.T) {
	e := newLedgerEnv(t)
	tag := uuid.Must(uuid.NewV7())
	h := e.header("discard-000001", ledgerDay)
	h.TagIDs = []uuid.UUID{tag}
	draft := e.store(e.expenseOf(h, "120"))
	other := e.store(e.expense("keep-00000001", "30"))
	if got := e.count("SELECT count(*) FROM fin_posting WHERE transaction_id = $1", draft.ID); got != 2 {
		t.Fatalf("%d postings stored for the draft, want 2", got)
	}

	// In a database transaction, so that the balance guard runs on the commit.
	err := e.inTx(func(ctx context.Context) error { return e.txns.Delete(ctx, draft.ID) })
	if err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := e.txns.Get(context.Background(), draft.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	if got := e.count("SELECT count(*) FROM fin_posting WHERE transaction_id = $1", draft.ID); got != 0 {
		t.Errorf("%d postings left, want none", got)
	}
	if got := e.count("SELECT count(*) FROM fin_transaction_tag WHERE transaction_id = $1", draft.ID); got != 0 {
		t.Errorf("%d tags left, want none", got)
	}
	if got := e.count("SELECT count(*) FROM fin_posting WHERE transaction_id = $1", other.ID); got != 2 {
		t.Errorf("%d postings left for another transaction, want its 2", got)
	}
	if err := e.txns.Delete(context.Background(), draft.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Delete again: err = %v, want ErrNotFound", err)
	}
}

func TestChangesInOneDatabaseTransactionAreAllOrNothing(t *testing.T) {
	e := newLedgerEnv(t)
	first := e.store(e.expense("confirm-all-001", "10"))
	second := e.store(e.expense("confirm-all-002", "20"))
	voided := e.storeConfirmed(e.expense("confirm-all-003", "30"))
	e.setStatus(voided.ID, model.StatusVoid)

	// Confirming a void transaction is refused by the table, which stops the whole
	// of what was done in the database transaction: the two drafts confirmed
	// before it are drafts again.
	err := e.inTx(func(ctx context.Context) error {
		for _, id := range []uuid.UUID{first.ID, second.ID, voided.ID} {
			if _, err := e.txns.SetStatus(ctx, id, model.StatusConfirmed); err != nil {
				return err
			}
		}
		return nil
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_status_times_check")

	for _, id := range []uuid.UUID{first.ID, second.ID} {
		if got := e.get(id); got.Status != model.StatusDraft || got.ConfirmedAt != nil {
			t.Errorf("transaction %s = %s, confirmed at %v, want it still a draft", id, got.Status, got.ConfirmedAt)
		}
	}
	if got := e.get(voided.ID); got.Status != model.StatusVoid {
		t.Errorf("the void transaction = %s, want it left as it was", got.Status)
	}
	e.wantBalances(nil)

	// The same changes without the failing one go through together.
	err = e.inTx(func(ctx context.Context) error {
		for _, id := range []uuid.UUID{first.ID, second.ID} {
			if _, err := e.txns.SetStatus(ctx, id, model.StatusConfirmed); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("confirm the two drafts: %v", err)
	}
	e.wantBalances(nil, "cash TWD -30")
}

func TestUnknownTransactionsAreNotFound(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	unknown := uuid.Must(uuid.NewV7())

	if _, err := e.txns.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := e.txns.GetForUpdate(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("GetForUpdate: err = %v, want ErrNotFound", err)
	}
	if _, err := e.txns.GetByIdempotencyKey(ctx, "no-such-key-001"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("GetByIdempotencyKey: err = %v, want ErrNotFound", err)
	}
	if _, err := e.txns.SetStatus(ctx, unknown, model.StatusConfirmed); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("SetStatus: err = %v, want ErrNotFound", err)
	}
	if err := e.txns.Delete(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Delete: err = %v, want ErrNotFound", err)
	}
}

func TestGetForUpdateHoldsTheRowUntilTheDatabaseTransactionEnds(t *testing.T) {
	e := newLedgerEnv(t)
	draft := e.store(e.expense("lock-00000001", "120"))

	locked := make(chan struct{})
	holder := make(chan error, 1)
	go func() {
		holder <- e.inTx(func(ctx context.Context) error {
			if _, err := e.txns.GetForUpdate(ctx, draft.ID); err != nil {
				return err
			}
			close(locked)
			time.Sleep(400 * time.Millisecond)
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-holder:
		t.Fatalf("the holder ended before it locked the row: %v", err)
	}

	// Another connection that changes the row has to wait for the lock to go.
	start := time.Now()
	e.setStatus(draft.ID, model.StatusConfirmed)
	waited := time.Since(start)
	if err := <-holder; err != nil {
		t.Fatalf("the holder: %v", err)
	}
	if waited < 250*time.Millisecond {
		t.Errorf("SetStatus returned after %v, want it to wait for the lock held for 400ms", waited)
	}
}

func TestTheDatabaseRefusesATransactionThatDoesNotBalance(t *testing.T) {
	e := newLedgerEnv(t)
	cash, food := e.cash.ID, e.food.ID
	const guard = "fin_transaction_balanced"

	raw := func(key string, postings ...model.Posting) model.Transaction {
		return model.Transaction{
			ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft,
			OccurredOn: ledgerDay, IdempotencyKey: key, Postings: postings,
		}
	}
	insert := func(txn model.Transaction) error {
		return e.inTx(func(ctx context.Context) error {
			_, err := e.txns.Insert(ctx, txn)
			return err
		})
	}
	account := func(quantity string) model.Posting {
		return model.Posting{Seq: 1, Book: model.BookAccount, AccountID: &cash, InstrumentID: e.twd.ID, Quantity: dec(quantity)}
	}
	expense := func(quantity string) model.Posting {
		return model.Posting{Seq: 2, Book: model.BookExpense, CategoryID: &food, InstrumentID: e.twd.ID, Quantity: dec(quantity), BaseAmount: decPtr(quantity)}
	}

	t.Run("postings that do not add up", func(t *testing.T) {
		err := insert(raw("unbalanced-0001", account("-120"), expense("100")))
		wantConstraint(t, err, checkViolation, guard)
	})
	t.Run("an instrument that does not add up while another does", func(t *testing.T) {
		usd := model.Posting{Seq: 3, Book: model.BookConversion, InstrumentID: e.usd.ID, Quantity: dec("5")}
		err := insert(raw("unbalanced-0002", account("-120"), expense("120"), usd))
		wantConstraint(t, err, checkViolation, guard)
	})
	t.Run("no postings", func(t *testing.T) {
		wantConstraint(t, insert(raw("unbalanced-0003")), checkViolation, guard)
	})
	t.Run("outside a database transaction, where the postings cannot follow the row", func(t *testing.T) {
		balanced := raw("unbalanced-0004", account("-120"), expense("120"))
		_, err := e.txns.Insert(context.Background(), balanced)
		wantConstraint(t, err, checkViolation, guard)
	})
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
		t.Fatalf("%d transactions stored by the failed inserts, want none", got)
	}

	if err := insert(raw("balanced-00001", account("-120"), expense("120"))); err != nil {
		t.Fatalf("a balanced transaction: %v", err)
	}
	t.Run("removing a posting from a stored transaction", func(t *testing.T) {
		_, err := e.pool.Exec(context.Background(), "DELETE FROM fin_posting WHERE seq = 2")
		wantConstraint(t, err, checkViolation, guard)
	})
	t.Run("changing a quantity of a stored transaction", func(t *testing.T) {
		_, err := e.pool.Exec(context.Background(), "UPDATE fin_posting SET quantity = quantity * 2, base_amount = base_amount * 2 WHERE seq = 2")
		wantConstraint(t, err, checkViolation, guard)
	})
	if got := e.count("SELECT count(*) FROM fin_posting"); got != 2 {
		t.Errorf("%d postings, want the 2 of the balanced transaction left as they were", got)
	}
}

func TestThePostingTableChecksWhatEachBookNeeds(t *testing.T) {
	e := newLedgerEnv(t)
	cash, food, salary := e.cash.ID, e.food.ID, e.salary.ID
	unknown := uuid.Must(uuid.NewV7())
	twd := e.twd.ID

	account := model.Posting{Seq: 1, Book: model.BookAccount, AccountID: &cash, InstrumentID: twd, Quantity: dec("1")}
	expense := model.Posting{Seq: 1, Book: model.BookExpense, CategoryID: &food, InstrumentID: twd, Quantity: dec("1"), BaseAmount: decPtr("1")}
	income := model.Posting{Seq: 1, Book: model.BookIncome, CategoryID: &salary, InstrumentID: twd, Quantity: dec("-1"), BaseAmount: decPtr("-1")}
	conversion := model.Posting{Seq: 1, Book: model.BookConversion, InstrumentID: twd, Quantity: dec("1")}
	opening := model.Posting{Seq: 1, Book: model.BookOpening, InstrumentID: twd, Quantity: dec("-1")}
	with := func(p model.Posting, change func(p *model.Posting)) model.Posting {
		change(&p)
		return p
	}

	tests := []struct {
		name       string
		postings   []model.Posting
		sqlState   string
		constraint string
	}{
		{"an account posting without an account", []model.Posting{with(account, func(p *model.Posting) { p.AccountID = nil })}, checkViolation, "fin_posting_account_id_check"},
		{"an account posting with a category", []model.Posting{with(account, func(p *model.Posting) { p.CategoryID = &food })}, checkViolation, "fin_posting_category_id_check"},
		{"an account posting with a base amount", []model.Posting{with(account, func(p *model.Posting) { p.BaseAmount = decPtr("1") })}, checkViolation, "fin_posting_base_amount_check"},
		{"an expense posting without a category", []model.Posting{with(expense, func(p *model.Posting) { p.CategoryID = nil })}, checkViolation, "fin_posting_category_id_check"},
		{"an expense posting without a base amount", []model.Posting{with(expense, func(p *model.Posting) { p.BaseAmount = nil })}, checkViolation, "fin_posting_base_amount_check"},
		{"an expense posting with an account", []model.Posting{with(expense, func(p *model.Posting) { p.AccountID = &cash })}, checkViolation, "fin_posting_account_id_check"},
		{"an income posting without a category", []model.Posting{with(income, func(p *model.Posting) { p.CategoryID = nil })}, checkViolation, "fin_posting_category_id_check"},
		{"an income posting without a base amount", []model.Posting{with(income, func(p *model.Posting) { p.BaseAmount = nil })}, checkViolation, "fin_posting_base_amount_check"},
		{"a conversion posting with an account", []model.Posting{with(conversion, func(p *model.Posting) { p.AccountID = &cash })}, checkViolation, "fin_posting_account_id_check"},
		{"a conversion posting with a category", []model.Posting{with(conversion, func(p *model.Posting) { p.CategoryID = &food })}, checkViolation, "fin_posting_category_id_check"},
		{"a conversion posting with a base amount", []model.Posting{with(conversion, func(p *model.Posting) { p.BaseAmount = decPtr("1") })}, checkViolation, "fin_posting_base_amount_check"},
		{"an opening posting with a base amount", []model.Posting{with(opening, func(p *model.Posting) { p.BaseAmount = decPtr("-1") })}, checkViolation, "fin_posting_base_amount_check"},
		{"a posting made to a book that is not one", []model.Posting{with(conversion, func(p *model.Posting) { p.Book = "dealing" })}, checkViolation, "fin_posting_book_check"},
		{"a posting that moves nothing", []model.Posting{with(conversion, func(p *model.Posting) { p.Quantity = dec("0") })}, checkViolation, "fin_posting_quantity_check"},
		{"a base amount of the other sign", []model.Posting{with(expense, func(p *model.Posting) { p.BaseAmount = decPtr("-1") })}, checkViolation, "fin_posting_base_amount_sign_check"},
		{"a base amount of zero", []model.Posting{with(expense, func(p *model.Posting) { p.BaseAmount = decPtr("0") })}, checkViolation, "fin_posting_base_amount_sign_check"},
		{"two postings in one position", []model.Posting{account, with(opening, func(p *model.Posting) { p.Seq = 1 })}, uniqueViolation, "fin_posting_transaction_id_seq_key"},
		{"an account that does not exist", []model.Posting{with(account, func(p *model.Posting) { p.AccountID = &unknown })}, foreignKeyViolation, "fin_posting_account_id_fkey"},
		{"a category that does not exist", []model.Posting{with(expense, func(p *model.Posting) { p.CategoryID = &unknown })}, foreignKeyViolation, "fin_posting_category_id_fkey"},
		{"an instrument that does not exist", []model.Posting{with(account, func(p *model.Posting) { p.InstrumentID = unknown })}, foreignKeyViolation, "fin_posting_instrument_id_fkey"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn := model.Transaction{
				ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft,
				OccurredOn: ledgerDay, IdempotencyKey: fmt.Sprintf("check-posting-%03d", i), Postings: tt.postings,
			}

			// The literals skip the model on purpose: the table is the last guard.
			err := e.inTx(func(ctx context.Context) error {
				_, err := e.txns.Insert(ctx, txn)
				return err
			})
			wantConstraint(t, err, tt.sqlState, tt.constraint)
		})
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
		t.Errorf("%d transactions stored, want none: each rolled back with its posting", got)
	}
}

func TestTheTransactionTableChecksKindAndStatus(t *testing.T) {
	e := newLedgerEnv(t)
	valid := e.expense("check-kind-0001", "120")

	badKind := valid
	badKind.Kind = "trade"
	err := e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, badKind)
		return err
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_kind_check")

	badStatus := valid
	badStatus.Status = "pending"
	err = e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, badStatus)
		return err
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_status_check")

	// A transaction is stored as a draft: one that says it is confirmed has no
	// confirmed time to show, and the table refuses it. Confirming is SetStatus.
	confirmed := valid
	confirmed.Status = model.StatusConfirmed
	err = e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, confirmed)
		return err
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_status_times_check")

	stored := e.store(valid)
	_, err = e.txns.SetStatus(context.Background(), stored.ID, "pending")
	wantConstraint(t, err, checkViolation, "fin_transaction_status_check")
	// A draft is confirmed before it is voided.
	_, err = e.txns.SetStatus(context.Background(), stored.ID, model.StatusVoid)
	wantConstraint(t, err, checkViolation, "fin_transaction_status_times_check")

	if got := e.get(stored.ID); got.Status != model.StatusDraft || got.VoidedAt != nil {
		t.Errorf("transaction = %s, voided at %v, want the draft left as it was", got.Status, got.VoidedAt)
	}
}

func TestListFiltersAndPagesNewestFirst(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	project1, project2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	area1 := uuid.Must(uuid.NewV7())
	tag := uuid.Must(uuid.NewV7())
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

	expense := func(key string, d int, project, area *uuid.UUID, confirmed bool) model.Transaction {
		h := e.header(key, day(d))
		h.ProjectID, h.AreaID = project, area
		if confirmed {
			return e.storeConfirmed(e.expenseOf(h, "10"))
		}
		return e.store(e.expenseOf(h, "10"))
	}
	t1 := expense("list-00000001", 10, &project1, &area1, false)
	t2 := expense("list-00000002", 12, &project1, nil, true)
	t3 := expense("list-00000003", 12, &project2, &area1, true)

	transfer, err := model.NewTransfer(model.TransferParams{
		Header: e.header("list-00000004", day(14)), From: e.cash, To: e.bank, Instrument: e.twd, Amount: dec("5"),
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	t4 := e.storeConfirmed(transfer)

	h := e.header("list-00000005", day(15))
	h.TagIDs = []uuid.UUID{tag}
	income, err := model.NewIncome(model.CategorizedParams{
		Header: h, Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("100")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	t5 := e.store(income)

	list := func(t *testing.T, f repo.TransactionFilter) []uuid.UUID {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 100
		}
		got, err := e.txns.List(ctx, f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		ids := make([]uuid.UUID, len(got))
		for i, txn := range got {
			ids[i] = txn.ID
		}
		return ids
	}
	date := func(d int) *time.Time {
		v := day(d)
		return &v
	}
	ids := func(txns ...model.Transaction) []uuid.UUID {
		out := make([]uuid.UUID, len(txns))
		for i, txn := range txns {
			out[i] = txn.ID
		}
		return out
	}

	tests := []struct {
		name   string
		filter repo.TransactionFilter
		want   []uuid.UUID
	}{
		{"everything, newest first and the latest made first within a day", repo.TransactionFilter{}, ids(t5, t4, t3, t2, t1)},
		{"drafts", repo.TransactionFilter{Status: model.StatusDraft}, ids(t5, t1)},
		{"confirmed", repo.TransactionFilter{Status: model.StatusConfirmed}, ids(t4, t3, t2)},
		{"void", repo.TransactionFilter{Status: model.StatusVoid}, ids()},
		{"from a day on, both ends of a range included", repo.TransactionFilter{From: date(12), To: date(14)}, ids(t4, t3, t2)},
		{"from a day on", repo.TransactionFilter{From: date(13)}, ids(t5, t4)},
		{"up to a day", repo.TransactionFilter{To: date(11)}, ids(t1)},
		{"one day", repo.TransactionFilter{From: date(12), To: date(12)}, ids(t3, t2)},
		{"a project", repo.TransactionFilter{ProjectID: &project1}, ids(t2, t1)},
		{"an area", repo.TransactionFilter{AreaID: &area1}, ids(t3, t1)},
		{"a project and an area", repo.TransactionFilter{ProjectID: &project1, AreaID: &area1}, ids(t1)},
		{"an account that is in some of them", repo.TransactionFilter{AccountID: &e.bank.ID}, ids(t5, t4)},
		{"an account that is in all but one", repo.TransactionFilter{AccountID: &e.cash.ID}, ids(t4, t3, t2, t1)},
		{"an account and a status", repo.TransactionFilter{AccountID: &e.bank.ID, Status: model.StatusConfirmed}, ids(t4)},
		{"the first page", repo.TransactionFilter{Limit: 2}, ids(t5, t4)},
		{"the second page", repo.TransactionFilter{Limit: 2, Offset: 2}, ids(t3, t2)},
		{"the last page", repo.TransactionFilter{Limit: 2, Offset: 4}, ids(t1)},
		{"a page past the end", repo.TransactionFilter{Limit: 2, Offset: 5}, ids()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(t, tt.filter); !slices.Equal(got, tt.want) {
				t.Fatalf("List = %v, want %v", got, tt.want)
			}
		})
	}

	// What is listed is whole: with its postings and tags.
	got, err := e.txns.List(ctx, repo.TransactionFilter{AccountID: &e.bank.ID, Status: model.StatusDraft, Limit: 10})
	if err != nil || len(got) != 1 {
		t.Fatalf("List = %v, %v, want the income", got, err)
	}
	e.wantSame("List", got[0], income)
}

// afterStatement is a pgx tracer that runs hook as the first statement that
// begins with prefix has been read to its end, and before the next statement of
// the call is sent. It puts a change made on another connection exactly between
// two statements of one call, with no sleep to hope that it lands there.
type afterStatement struct {
	prefix string
	hook   func()
	once   sync.Once
	ran    bool
}

type statementKey struct{}

func (a *afterStatement) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, statementKey{}, data.SQL)
}

func (a *afterStatement) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if sql, _ := ctx.Value(statementKey{}).(string); strings.HasPrefix(sql, a.prefix) {
		a.once.Do(func() {
			a.ran = true
			a.hook()
		})
	}
}

// listInterruptedBy returns what List returns for the filter when change is made
// by another connection after the statement that lists the ids and before the
// next one.
func (e *ledgerEnv) listInterruptedBy(filter repo.TransactionFilter, change func()) []model.Transaction {
	e.t.Helper()
	tracer := &afterStatement{prefix: "SELECT t.id FROM fin_transaction", hook: change}
	cfg := e.pool.Config()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		e.t.Fatalf("open a pool with a tracer: %v", err)
	}
	defer pool.Close()

	got, err := postgres.NewTransactionRepo(pool).List(context.Background(), filter)
	if err != nil {
		e.t.Fatalf("List: %v", err)
	}
	if !tracer.ran {
		e.t.Fatal("the change was never made: no statement listed the ids")
	}
	return got
}

// A transaction deleted after its id was listed is still in the list: the page is
// read in one snapshot. Were the rows read after the delete, the page would lack
// it, and the use case, which asks for one transaction more than a page holds to
// know that another page follows, would take the extra one for part of the page
// and tell the caller there are no more pages while there are.
func TestListKeepsATransactionDeletedWhileItIsRead(t *testing.T) {
	e := newLedgerEnv(t)
	var txns [4]model.Transaction // the 1st to the 4th, listed newest first
	for i := range txns {
		h := e.header(fmt.Sprintf("snapshot-del-%02d", i), time.Date(2026, 11, i+1, 0, 0, 0, 0, time.UTC))
		txns[i] = e.store(e.expenseOf(h, "10"))
	}

	// A page of two, and the one more that says another page follows.
	got := e.listInterruptedBy(repo.TransactionFilter{Limit: 3}, func() {
		_, err := e.pool.Exec(context.Background(), "DELETE FROM fin_transaction WHERE id = $1", txns[2].ID)
		if err != nil {
			t.Errorf("delete a transaction on another connection: %v", err)
		}
	})

	var gotIDs []uuid.UUID
	for _, txn := range got {
		gotIDs = append(gotIDs, txn.ID)
	}
	if want := []uuid.UUID{txns[3].ID, txns[2].ID, txns[1].ID}; !slices.Equal(gotIDs, want) {
		t.Fatalf("List = %v, want the three that were listed, %v, the deleted one among them", gotIDs, want)
	}
	if _, err := e.txns.Get(context.Background(), txns[2].ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get of the deleted transaction: err = %v, want it deleted by now", err)
	}
}

// A transaction whose status was changed after its id was listed has the status
// the filter matched in the list: it is not shown as something the filter does
// not allow.
func TestListShowsTheStatusTheFilterMatched(t *testing.T) {
	e := newLedgerEnv(t)
	var drafts [3]model.Transaction
	for i := range drafts {
		h := e.header(fmt.Sprintf("snapshot-status-%02d", i), time.Date(2026, 11, i+1, 0, 0, 0, 0, time.UTC))
		drafts[i] = e.store(e.expenseOf(h, "10"))
	}

	got := e.listInterruptedBy(repo.TransactionFilter{Status: model.StatusDraft, Limit: 10}, func() {
		e.setStatus(drafts[1].ID, model.StatusConfirmed)
	})

	if len(got) != len(drafts) {
		t.Fatalf("List returned %d transactions, want the 3 drafts that were listed", len(got))
	}
	for _, txn := range got {
		if txn.Status != model.StatusDraft {
			t.Errorf("transaction %s is %s in a list of drafts", txn.ID, txn.Status)
		}
	}
	if got := e.get(drafts[1].ID); got.Status != model.StatusConfirmed {
		t.Errorf("the transaction is %s now, want it confirmed by the other connection", got.Status)
	}
}
