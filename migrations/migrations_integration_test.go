package migrations_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/maxjkfc/maxos/internal/testpg"
	"github.com/maxjkfc/maxos/migrations"
)

func TestMain(m *testing.M) {
	os.Exit(testpg.Run(m))
}

// The two system categories 0005 seeds.
const (
	unrecordedExpense = "00000000-0000-7000-8000-000000000101"
	unrecordedIncome  = "00000000-0000-7000-8000-000000000102"
)

// newProvider returns a goose provider on the pool, which the test database has
// every migration applied to.
func newProvider(t *testing.T, pool *pgxpool.Pool) *goose.Provider {
	t.Helper()
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatalf("goose provider: %v", err)
	}
	return provider
}

func version(t *testing.T, provider *goose.Provider) int64 {
	t.Helper()
	v, err := provider.GetDBVersion(context.Background())
	if err != nil {
		t.Fatalf("get the version: %v", err)
	}
	return v
}

// pgError returns the error of the database that err is made of, and fails the
// test when it is not made of one.
func pgError(t *testing.T, err error) *pgconn.PgError {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v, want an error of the database", err)
	}
	return pgErr
}

// schema describes the ledger tables as the catalog of the database knows them:
// columns, constraints, indexes and triggers, and the rows the migrations seed.
// Two databases with the same description have the same ledger schema.
func schema(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	queries := []struct{ title, sql string }{
		// The position is the rank among the columns of the table, not the number the
		// database gave the column (ordinal_position): that number is never reused,
		// so a column that is dropped and added again has a higher one, though it is
		// still where it was.
		{"column", `SELECT table_name, rank() OVER (PARTITION BY table_name ORDER BY ordinal_position),
				column_name, data_type, is_nullable,
				COALESCE(column_default, ''), is_generated, COALESCE(generation_expression, '')
			FROM information_schema.columns
			WHERE table_schema = 'public' AND table_name LIKE 'fin\_%'
			ORDER BY table_name, ordinal_position`},
		{"constraint", `SELECT conrelid::regclass::text, conname, pg_get_constraintdef(oid)
			FROM pg_constraint
			WHERE connamespace = 'public'::regnamespace AND conrelid::regclass::text LIKE 'fin\_%'
			ORDER BY 1, 2`},
		{"index", `SELECT tablename, indexname, indexdef FROM pg_indexes
			WHERE schemaname = 'public' AND tablename LIKE 'fin\_%'
			ORDER BY 1, 2`},
		{"trigger", `SELECT tgrelid::regclass::text, tgname, pg_get_triggerdef(oid)
			FROM pg_trigger
			WHERE NOT tgisinternal AND tgrelid::regclass::text LIKE 'fin\_%'
			ORDER BY 1, 2`},
		{"function", `SELECT proname, pg_get_functiondef(oid) FROM pg_proc
			WHERE pronamespace = 'public'::regnamespace AND proname LIKE 'fin\_%'
			ORDER BY 1`},
		{"category", `SELECT id::text, direction, COALESCE(parent_id::text, ''), name, archived_at IS NOT NULL
			FROM fin_category ORDER BY id`},
		{"instrument", `SELECT id::text, code FROM fin_instrument ORDER BY id`},
	}

	var lines []string
	for _, q := range queries {
		rows, err := pool.Query(context.Background(), q.sql)
		if err != nil {
			t.Fatalf("describe the %ss: %v", q.title, err)
		}
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				rows.Close()
				t.Fatalf("describe the %ss: %v", q.title, err)
			}
			lines = append(lines, fmt.Sprintf("%s %v", q.title, values))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("describe the %ss: %v", q.title, err)
		}
	}
	return lines
}

// wantSameSchema fails the test, naming the lines that differ, unless the two
// descriptions are the same.
func wantSameSchema(t *testing.T, name string, got, want []string) {
	t.Helper()
	if slices.Equal(got, want) {
		return
	}
	var diff []string
	for _, line := range got {
		if !slices.Contains(want, line) {
			diff = append(diff, "  only in "+name+": "+line)
		}
	}
	for _, line := range want {
		if !slices.Contains(got, line) {
			diff = append(diff, "  missing from "+name+": "+line)
		}
	}
	t.Fatalf("%s is not the schema it should be:\n%s", name, strings.Join(diff, "\n"))
}

func TestMigration0005GoesDownToTheSchemaOf0004AndUpAgain(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	provider := newProvider(t, pool)
	if v := version(t, provider); v != 5 {
		t.Fatalf("the test database is at version %d, want 5: this test is about 0005", v)
	}
	atFive := schema(t, pool)

	// What the migrations up to 0004 make, on their own: taken by going all the way
	// down and up again, so that it is a database where 0005 never was.
	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("down to the start: %v", err)
	}
	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatalf("up to 0004: %v", err)
	}
	atFour := schema(t, pool)
	if slices.Equal(atFour, atFive) {
		t.Fatal("the schema at 0004 is the schema at 0005: the test sees nothing of the migration")
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0005: %v", err)
	}
	wantSameSchema(t, "the schema after up", schema(t, pool), atFive)

	if _, err := provider.DownTo(ctx, 4); err != nil {
		t.Fatalf("down to 0004: %v", err)
	}
	if v := version(t, provider); v != 4 {
		t.Fatalf("version after down = %d, want 4", v)
	}
	wantSameSchema(t, "the schema after down", schema(t, pool), atFour)

	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up to 0005 again: %v", err)
	}
	wantSameSchema(t, "the schema after up again", schema(t, pool), atFive)
}

// posting is a posting of the transaction seedLedger stores. A field that is
// left empty is a column that is left NULL; account and category say which of the
// fixtures it is made to, and systemCategory is the id of a system category.
type posting struct {
	book                 string
	account, category    string
	party                bool
	quantity             string
	baseAmount, discount string
	systemCategory       string
}

// seedLedger stores, in a database at version 5, an account, a category of
// expenses, and one transaction of the given kind with the given postings, all in
// the base currency. The party of a posting is an id that no table knows.
func seedLedger(t *testing.T, pool *pgxpool.Pool, kind string, postings ...posting) {
	t.Helper()
	ctx := context.Background()
	account, food, party, twd := uuid.New(), uuid.New(), uuid.New(), "00000000-0000-7000-8000-000000000001"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`INSERT INTO fin_account (id, name, kind) VALUES ($1, 'cash', 'cash')`, account)
	exec(`INSERT INTO fin_category (id, direction, name) VALUES ($1, 'expense', 'food')`, food)
	txn := uuid.New()
	exec(`INSERT INTO fin_transaction (id, kind, status, occurred_on, idempotency_key)
		VALUES ($1, $2, 'draft', '2026-11-14', 'migration-test-key')`, txn, kind)
	for i, p := range postings {
		var accountID, categoryID, partyID, base, discount any
		if p.account != "" {
			accountID = account
		}
		switch {
		case p.systemCategory != "":
			categoryID = p.systemCategory
		case p.category != "":
			categoryID = food
		}
		if p.party {
			partyID = party
		}
		if p.baseAmount != "" {
			base = p.baseAmount
		}
		if p.discount != "" {
			discount = p.discount
		}
		exec(`INSERT INTO fin_posting (id, transaction_id, seq, book, account_id, category_id, party_id,
				instrument_id, quantity, base_amount, discount)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9::numeric, $10::numeric, $11::numeric)`,
			uuid.New(), txn, i+1, p.book, accountID, categoryID, partyID, twd, p.quantity, base, discount)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func TestMigration0005RefusesToGoDownWhileTheLedgerHoldsWhatItAddedAndLeavesTheLedgerAlone(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		kind     string
		postings []posting
	}{
		{"a dealing", "dealing", []posting{
			{book: "account", account: "cash", quantity: "-100"},
			{book: "dealing", party: true, quantity: "100"},
		}},
		{"a reward", "reward", []posting{
			{book: "account", account: "cash", quantity: "50"},
			{book: "reward", quantity: "-50", baseAmount: "-50"},
		}},
		{"a reconciliation of a quantity", "reconciliation", []posting{
			{book: "account", account: "cash", quantity: "1"},
			{book: "adjustment", quantity: "-1"},
		}},
		{"an expense with a discount", "expense", []posting{
			{book: "account", account: "cash", quantity: "-450"},
			{book: "expense", category: "food", quantity: "450", baseAmount: "450", discount: "50"},
		}},
		{"an expense with a share", "expense", []posting{
			{book: "account", account: "cash", quantity: "-500"},
			{book: "expense", category: "food", quantity: "300", baseAmount: "300"},
			{book: "dealing", party: true, quantity: "200"},
		}},
		{"a transfer with a fee, which is an expense posting that is not in an expense", "transfer", []posting{
			{book: "account", account: "cash", quantity: "-1015"},
			{book: "account", account: "cash", quantity: "1000"},
			{book: "expense", category: "food", quantity: "15", baseAmount: "15"},
		}},
		{"a posting to a system category", "expense", []posting{
			{book: "account", account: "cash", quantity: "-120"},
			{book: "expense", systemCategory: unrecordedExpense, quantity: "120", baseAmount: "120"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := testpg.New(t)
			provider := newProvider(t, pool)
			seedLedger(t, pool, tt.kind, tt.postings...)
			before := schema(t, pool)

			_, err := provider.Down(ctx)

			if err == nil || !strings.Contains(err.Error(), "cannot roll back 0005_fin_dealings") {
				t.Fatalf("err = %v, want it to refuse and say which migration", err)
			}
			if v := version(t, provider); v != 5 {
				t.Errorf("version = %d after the refused Down, want 5", v)
			}
			wantSameSchema(t, "the schema after the refused Down", schema(t, pool), before)
			var transactions, postings int
			if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM fin_transaction), (SELECT count(*) FROM fin_posting)`).
				Scan(&transactions, &postings); err != nil {
				t.Fatalf("count: %v", err)
			}
			if transactions != 1 || postings != len(tt.postings) {
				t.Errorf("%d transactions and %d postings left, want the ledger untouched: 1 and %d", transactions, postings, len(tt.postings))
			}

			// Once the ledger holds none of it, it goes down.
			if _, err := pool.Exec(ctx, `DELETE FROM fin_transaction`); err != nil {
				t.Fatalf("delete the transactions: %v", err)
			}
			if _, err := provider.Down(ctx); err != nil {
				t.Fatalf("Down of an empty ledger: %v", err)
			}
			if v := version(t, provider); v != 4 {
				t.Errorf("version = %d, want 4", v)
			}
		})
	}
}

func TestMigration0005GoesDownOverOrdinaryExpensesAndTransfers(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	provider := newProvider(t, pool)
	// What 0004 could hold, which the migration must not mistake for its own.
	seedLedger(t, pool, "expense",
		posting{book: "account", account: "cash", quantity: "-120"},
		posting{book: "expense", category: "food", quantity: "120", baseAmount: "120"},
	)

	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("Down: %v, want it to go down: an ordinary expense is not what 0005 added", err)
	}

	var transactions, postings int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM fin_transaction), (SELECT count(*) FROM fin_posting)`).
		Scan(&transactions, &postings); err != nil {
		t.Fatalf("count: %v", err)
	}
	if transactions != 1 || postings != 2 {
		t.Errorf("%d transactions and %d postings left, want the expense kept: 1 and 2", transactions, postings)
	}
}

// The check that keeps a category out from under a system category has to be
// dropped to put one there. Down must still stop on it, with its own message:
// the foreign key would stop the delete of the system category too, and say
// less.
func TestMigration0005RefusesToGoDownWhileACategoryIsUnderASystemCategoryAndLeavesItAlone(t *testing.T) {
	ctx := context.Background()
	for _, tt := range []struct{ name, direction, parent string }{
		{"an expense category", "expense", unrecordedExpense},
		{"an income category", "income", unrecordedIncome},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool := testpg.New(t)
			provider := newProvider(t, pool)
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, query, args...); err != nil {
					t.Fatalf("%s: %v", query, err)
				}
			}
			// The check is put back, without a look at the rows that are there, so
			// that the database is what Down finds when it was added by hand: the
			// check is there and so is the category it keeps out.
			exec(`ALTER TABLE fin_category DROP CONSTRAINT fin_category_system_parent_check`)
			exec(`INSERT INTO fin_category (id, direction, parent_id, name) VALUES ($1, $2, $3, 'sub')`, uuid.New(), tt.direction, tt.parent)
			exec(fmt.Sprintf(`ALTER TABLE fin_category ADD CONSTRAINT fin_category_system_parent_check
				CHECK (parent_id IS NULL OR parent_id NOT IN ('%s', '%s')) NOT VALID`, unrecordedExpense, unrecordedIncome))
			before := schema(t, pool)

			_, err := provider.Down(ctx)

			pgErr := pgError(t, err)
			if pgErr.Code != "P0001" || !strings.Contains(pgErr.Message, "cannot roll back 0005_fin_dealings") {
				t.Fatalf("err = %v, want the refusal of Down, not the foreign key of the category", err)
			}
			if v := version(t, provider); v != 5 {
				t.Errorf("version = %d after the refused Down, want 5", v)
			}
			wantSameSchema(t, "the schema after the refused Down", schema(t, pool), before)

			// Once the category is gone, it goes down.
			exec(`DELETE FROM fin_category WHERE parent_id = $1`, tt.parent)
			if _, err := provider.Down(ctx); err != nil {
				t.Fatalf("Down once the category is gone: %v", err)
			}
			if v := version(t, provider); v != 4 {
				t.Errorf("version = %d, want 4", v)
			}
		})
	}
}

func TestMigration0005StopsWhereATopLevelCategoryIsAlreadyCalledUnrecorded(t *testing.T) {
	ctx := context.Background()
	for _, direction := range []string{"expense", "income"} {
		t.Run(direction, func(t *testing.T) {
			pool := testpg.New(t)
			provider := newProvider(t, pool)
			if _, err := provider.DownTo(ctx, 4); err != nil {
				t.Fatalf("down to 0004: %v", err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO fin_category (id, direction, name) VALUES ($1, $2, '未記錄')`, uuid.New(), direction); err != nil {
				t.Fatalf("insert the category: %v", err)
			}

			_, err := provider.Up(ctx)

			// The migration says what to do, in its own words: the unique index that
			// would have stopped it anyway says less.
			pgErr := pgError(t, err)
			if pgErr.Code != "P0001" || !strings.Contains(pgErr.Message, "cannot apply 0005_fin_dealings") ||
				!strings.Contains(pgErr.Message, "未記錄") || !strings.Contains(pgErr.Hint, "Rename") {
				t.Fatalf("err = %v, hint = %q, want the migration to refuse, name the category and say to rename it", err, pgErr.Hint)
			}
			if v := version(t, provider); v != 4 {
				t.Errorf("version = %d, want 4: the migration is rolled back whole", v)
			}
			var columns int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
				WHERE table_name = 'fin_posting' AND column_name IN ('party_id', 'discount')`).Scan(&columns); err != nil {
				t.Fatalf("count the columns: %v", err)
			}
			if columns != 0 {
				t.Errorf("%d of the new columns exist, want none: nothing of the migration is left", columns)
			}
		})
	}
}
