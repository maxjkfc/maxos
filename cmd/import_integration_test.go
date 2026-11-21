package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// The export of these tests is invented: the accounts, the shops, the people and the
// amounts are not anyone's.

var mozeHeader = []string{"帳戶", "幣種", "記錄類型", "主類別", "子類別", "金額", "手續費", "折扣", "名稱", "商家", "日期", "時間", "專案", "描述", "標籤", "對象"}

// mozeRow builds a row of the export from the fields it has, by the name of the column.
func mozeRow(fields map[string]string) []string {
	row := make([]string, len(mozeHeader))
	for i, h := range mozeHeader {
		row[i] = fields[h]
	}
	return row
}

// r is a row: what, where, in what currency, how much, when, and what else.
func r(typ, account, currency, amount, when string, more ...string) []string {
	f := map[string]string{"記錄類型": typ, "帳戶": account, "幣種": currency, "金額": amount}
	if when != "" {
		f["日期"], f["時間"] = strings.ReplaceAll(when[:10], "-", "/"), when[11:]
	}
	for i := 0; i+1 < len(more); i += 2 {
		f[more[i]] = more[i+1]
	}
	return mozeRow(f)
}

// mozeExport is the CSV of an export: a byte order mark, the header, the rows, with
// the quoting the format has (a description of several lines, a comma in a name).
func mozeExport(t *testing.T) []byte {
	t.Helper()
	cat := func(main, sub string) []string { return []string{"主類別", main, "子類別", sub} }
	with := func(base []string, more ...string) []string {
		return append(base, more...)
	}
	rows := [][]string{
		r("初始金額", "Fake Cash", "TWD", "1000", ""),
		r("初始金額", "Fake Bank", "TWD", "5000", ""),
		r("初始金額", "Fake Bank USD", "USD", "200", ""),
		r("初始金額", "Fake 信用卡", "TWD", "0", ""),
		r("初始金額", "Fake Exchange USDT", "USDT", "300", ""),
		r("初始金額", "Fake Exchange BTC", "BTC", "0.5", ""),

		// January
		r("支出", "Fake 信用卡", "TWD", "-120", "2026-01-05 10:00", with(cat("飲食", "午餐"),
			"手續費", "-2", "名稱", "麵, 小菜", "商家", "Fake Shop", "專案", "生活開銷", "標籤", "#家庭;#菜單",
			"描述", "◎ 麵 ($100) ✕ 1\n◎ \"湯\" ($20) ✕ 1")...),
		r("支出", "Fake Cash", "TWD", "-100", "2026-01-05 12:30", with(cat("飲食", "午餐"), "手續費", "-1", "折扣", "20")...),
		r("支出", "Fake 信用卡", "TWD", "-100", "2026-01-06 09:00", with(cat("飲食", "午餐"), "折扣", "100", "名稱", "咖啡")...),
		r("支出", "Fake Bank USD", "USD", "-10", "2026-01-06 18:00", cat("飲食", "午餐")...),
		r("支出", "Fake 信用卡", "TWD", "-500", "2026-01-07 12:00", with(cat("飲食", "午餐"), "手續費", "-5", "專案", "公司代付", "名稱", "團膳")...),
		r("收入", "Fake Bank", "TWD", "50000", "2026-01-10 09:00", cat("收入", "薪水")...),
		r("收入", "Fake Bank", "TWD", "1200", "2026-01-10 09:05", with(cat("收入", "收款"), "名稱", "有人付款")...),
		r("收入", "Fake 信用卡", "TWD", "30", "2026-01-11 12:00", cat("收入", "信用卡抵扣")...),
		r("紅利回饋", "Fake Bank USD", "USD", "1.5", "2026-01-12 12:00", cat("紅利回饋", "紅利回饋")...),
		r("應收款項", "Fake Cash", "TWD", "-300", "2026-01-13 12:00", with(cat("應收款項", "借出"), "手續費", "-5", "對象", "Amy")...),
		r("應收款項", "Fake Cash", "TWD", "100", "2026-01-13 13:00", with(cat("應收款項", "借出"), "對象", "Amy")...),
		r("退款", "Fake 信用卡", "TWD", "450", "2026-01-14 12:00", with(cat("購物", "書籍"), "名稱", "退書")...),
		r("手續費", "Fake Bank", "TWD", "-30", "2026-01-14 13:00", cat("手續費", "手續費")...),
		r("支出", "Fake Cash", "TWD", "-40", "2026-01-15 10:00", cat("飲食", "午餐")...),
		r("餘額調整", "Fake Cash", "TWD", "-20", "2026-01-15 12:00", cat("餘額調整", "餘額調整")...),
		r("支出", "Fake Cash", "TWD", "-10", "2026-01-15 18:00", cat("飲食", "午餐")...),
		r("轉出", "Fake Bank", "TWD", "-1000", "2026-01-20 12:00", with(cat("信用卡繳款", "信用卡繳款"), "手續費", "-15")...),
		r("轉入", "Fake 信用卡", "TWD", "1000", "2026-01-20 12:00", cat("信用卡繳款", "信用卡繳款")...),
		r("轉出", "Fake Bank", "TWD", "-3000", "2026-01-21 12:00", cat("轉帳", "兌換")...),
		r("轉入", "Fake Bank USD", "USD", "100", "2026-01-21 12:00", cat("轉帳", "兌換")...),
		r("轉出", "Fake Exchange USDT", "USDT", "-100", "2026-01-22 12:00", cat("轉帳", "兌換")...),
		r("轉入", "Fake Exchange BTC", "BTC", "0.002", "2026-01-22 12:00", cat("轉帳", "兌換")...),
		r("轉出", "Fake Exchange USDT", "USDT", "-50", "2026-01-22 13:00", cat("轉帳", "兌換")...),
		r("轉入", "Fake Exchange BTC", "BTC", "0", "2026-01-22 13:00", cat("轉帳", "兌換")...),
		r("餘額調整", "Fake Exchange BTC", "BTC", "0.001", "2026-01-26 12:00", cat("餘額調整", "餘額調整")...),
		r("餘額調整", "Fake Bank USD", "USD", "-2.5", "2026-01-27 12:00", cat("餘額調整", "餘額調整")...),

		// February
		r("帳單分期", "Fake 信用卡", "TWD", "600", "2026-02-05 23:59", with(cat("帳單分期", "帳單分期"), "名稱", "帳單分期")...),
		r("帳單分期", "Fake 信用卡分期", "TWD", "600", "2026-02-05 23:59", cat("帳單分期", "帳單分期")...),
		r("帳單分期", "Fake 信用卡分期", "TWD", "-600", "2026-02-05 23:59", cat("帳單分期", "帳單分期")...),
		r("支出", "Fake 信用卡", "TWD", "-1056", "2026-02-10 12:00", cat("個人", "投資")...),
		r("收入", "Fake 證券戶", "TWD", "24", "2026-02-11 12:00", with(cat("收入", "投資"), "標籤", "#配息")...),
		r("餘額調整", "Fake 證券戶", "TWD", "500", "2026-02-12 12:00", cat("餘額調整", "餘額調整")...),
		r("支出", "Fake Wallet JPY", "JPY", "-1000", "2026-02-14 12:00", cat("飲食", "午餐")...),
		r("利息", "Fake 信用卡分期", "TWD", "-6", "2026-02-15 23:59", cat("利息支出", "利息支出")...),
		r("分期還款", "Fake 信用卡分期", "TWD", "-200", "2026-02-15 23:59", cat("帳單分期", "帳單分期")...),
		r("轉出", "Fake Cash", "TWD", "-77", "2026-02-25 12:00", cat("轉帳", "轉帳")...),
		r("餘額調整", "Fake Cash", "TWD", "0", "2026-02-28 12:00", cat("餘額調整", "餘額調整")...),

		// March, and the last periods of the plan
		r("支出", "Fake 信用卡", "TWD", "-300", "2026-03-02 12:00", cat("購物", "書籍")...),
		r("收入", "Fake Bank", "TWD", "100", "2026-03-05 12:00", cat("收入", "薪水")...),
		r("餘額調整", "Fake Bank", "TWD", "25", "2026-03-10 12:00", cat("餘額調整", "餘額調整")...),
		r("利息", "Fake 信用卡分期", "TWD", "-4", "2026-03-15 23:59", cat("利息支出", "利息支出")...),
		r("分期還款", "Fake 信用卡分期", "TWD", "-200", "2026-03-15 23:59", cat("帳單分期", "帳單分期")...),
		r("利息", "Fake 信用卡分期", "TWD", "-2", "2026-04-15 23:59", cat("利息支出", "利息支出")...),
		r("分期還款", "Fake 信用卡分期", "TWD", "-200", "2026-04-15 23:59", cat("帳單分期", "帳單分期")...),
	}

	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	if err := w.Write(mozeHeader); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if err := w.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

const mozeRates = `# Invented rates for a test.
date,instrument,twd_per_unit
2026-01-01,USD,30
2026-01-01,JPY,0.2
`

// importEnv is a database of its own, and a directory that has the export and the
// rates, as the command line finds them by default.
type importEnv struct {
	t    *testing.T
	pool *pgxpool.Pool
	dir  string
}

func newImportEnv(t *testing.T) importEnv {
	t.Helper()
	pool := testpg.New(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "MOZE_20260401_000000.csv"), mozeExport(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rates.csv"), []byte(mozeRates), 0o600); err != nil {
		t.Fatal(err)
	}

	// The commands connect by what the environment says, as they do for the user: the
	// database of this test is the one the test URL names, by its own name.
	admin, err := url.Parse(os.Getenv("MAXOS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	admin.Path = "/" + pool.Config().ConnConfig.Database
	t.Setenv("MAXOS_DATABASE_URL", admin.String())
	t.Setenv(importDirEnv, dir)
	t.Setenv("MAXOS_TIMEZONE", "Asia/Taipei")
	return importEnv{t: t, pool: pool, dir: dir}
}

// moze runs `maxos import moze <args>` and returns what it printed and how it ended.
func (e importEnv) moze(args ...string) (string, error) {
	e.t.Helper()
	root := newRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(append([]string{"import", "moze"}, args...))
	err := root.Execute()
	return out.String(), err
}

func (e importEnv) must(args ...string) string {
	e.t.Helper()
	out, err := e.moze(args...)
	if err != nil {
		e.t.Fatalf("maxos import moze %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// ledger gives the use cases to look at what the import made.
type ledger struct {
	fx.In

	GetBalances         usecase.GetBalances
	ListAccounts        usecase.ListAccounts
	ListInstruments     usecase.ListInstruments
	ListTransactions    usecase.ListTransactions
	RecordExpense       usecase.RecordExpense
	ConfirmTransactions usecase.ConfirmTransactions
	ListCategories      usecase.ListCategories
}

type fixedClock struct{}

func (fixedClock) Now() time.Time { return time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC) }

func (e importEnv) ledger() ledger {
	e.t.Helper()
	var l ledger
	app := fx.New(
		fx.NopLogger,
		fx.Supply(e.pool),
		fx.Provide(func() financeclock.Clock { return fixedClock{} }),
		CatalogModule(),
		FinanceModule(),
		fx.Populate(&l),
	)
	if err := app.Err(); err != nil {
		e.t.Fatalf("assemble the ledger: %v", err)
	}
	return l
}

func (e importEnv) balance(l ledger, account, code string, asOf time.Time) decimal.Decimal {
	e.t.Helper()
	ctx := context.Background()
	accounts, err := l.ListAccounts.ListAccounts(ctx, usecase.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		e.t.Fatal(err)
	}
	instruments, err := l.ListInstruments.ListInstruments(ctx, usecase.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		e.t.Fatal(err)
	}
	var accountID, instrumentID uuid.UUID
	for _, a := range accounts.Accounts {
		if a.Name == account {
			accountID = a.ID
		}
	}
	for _, i := range instruments.Instruments {
		if i.Code == code {
			instrumentID = i.ID
		}
	}
	out, err := l.GetBalances.GetBalances(ctx, usecase.GetBalancesInput{AccountID: &accountID, AsOf: &asOf})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, b := range out.Balances {
		if b.InstrumentID == instrumentID {
			return b.Quantity
		}
	}
	return decimal.Zero
}

func TestImportFromPlanToVerifyOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	mapping := filepath.Join(e.dir, "mapping.yaml")

	// plan: with no mapping it makes one of guesses, writes no data, and says so.
	report := filepath.Join(e.dir, "report.md")
	out := e.must("plan", "--out", report)
	for _, want := range []string{"已建立對照檔", "# guessed", "無法記錄的列"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan printed no %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(mapping); err != nil {
		t.Fatalf("plan made no mapping: %v", err)
	}
	if b, err := os.ReadFile(report); err != nil || !strings.Contains(string(b), "每月底的預期餘額") {
		t.Errorf("report = %v, %q", err, b)
	}

	// The mapping is the user's now. Everything plan guessed is right for this export,
	// except that the card of the instalments is the card, which the guess knew.
	data, err := os.ReadFile(mapping)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Fake 信用卡分期") {
		t.Fatal("the mapping lacks the account of the instalments")
	}
	again := e.must("plan")
	if strings.Contains(again, "新增") || strings.Contains(again, "已建立對照檔") {
		t.Errorf("a second plan adds to the mapping again:\n%s", again)
	}

	// apply: drafts, and the reference data they need; a second run adds nothing.
	first := e.must("apply")
	if !strings.Contains(first, "失敗 0 筆") || strings.Contains(first, "新增 0 筆，已存在") {
		t.Errorf("apply printed:\n%s", first)
	}
	if second := e.must("apply"); !strings.Contains(second, "草稿：新增 0 筆") || !strings.Contains(second, "參考資料：新增 0 項") {
		t.Errorf("a second apply printed:\n%s", second)
	}

	l := e.ledger()
	jan31 := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	if got := e.balance(l, "Fake Cash", "TWD", jan31); !got.IsZero() {
		t.Errorf("cash before anything is confirmed = %s: a draft counts toward nothing", got)
	}
	if st := e.must("status"); !strings.Contains(st, "2026-01") {
		t.Errorf("status printed:\n%s", st)
	}

	// confirm: the months are in order.
	if out, err := e.moze("confirm", "--month", "2026-02"); err == nil || !strings.Contains(out, "請先確認 2026-01") {
		t.Errorf("confirming February first: err = %v, printed:\n%s", err, out)
	}
	january := e.must("confirm", "--month", "2026-01")
	if !strings.Contains(january, "全部吻合") && !strings.Contains(january, "已說明") {
		t.Errorf("confirm printed:\n%s", january)
	}
	e.must("confirm", "--month", "2026-02")

	// What the ledger holds is what the export says, but for the rows that cannot be
	// recorded: cash is 77 above it, which a transfer out with no transfer in left.
	feb28 := time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)
	if got := e.balance(l, "Fake Cash", "TWD", feb28); !got.Equal(decimal.RequireFromString("644")) {
		t.Errorf("cash on 28 February = %s, want 644", got)
	}
	if got := e.balance(l, "Fake Bank USD", "USD", jan31); !got.Equal(decimal.RequireFromString("289")) {
		t.Errorf("Bank USD on 31 January = %s, want 289: 200, -10, +1.5, +100, and the adjustment of -2.5", got)
	}

	verify := e.must("verify")
	if !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 2 個") {
		t.Errorf("verify printed:\n%s", verify)
	}

	// A confirmed transaction that is not the import's, which the export does not have,
	// is a difference that nothing explains.
	ctx := context.Background()
	accounts, err := l.ListAccounts.ListAccounts(ctx, usecase.ListAccountsInput{})
	if err != nil {
		t.Fatal(err)
	}
	var cash, twd uuid.UUID
	for _, a := range accounts.Accounts {
		if a.Name == "Fake Cash" {
			cash = a.ID
		}
	}
	instruments, err := l.ListInstruments.ListInstruments(ctx, usecase.ListInstrumentsInput{})
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range instruments.Instruments {
		if i.Code == "TWD" {
			twd = i.ID
		}
	}
	if cash == uuid.Nil || twd == uuid.Nil {
		t.Fatalf("accounts %v, instruments %v", accounts.Accounts, instruments.Instruments)
	}
	groceries, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "not-the-import-0001", OccurredOn: feb28},
		AccountID:     &cash, InstrumentID: twd,
		Lines: []usecase.CategoryLine{{CategoryID: e.anExpenseCategory(l), Amount: decimal.RequireFromString("13")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	if _, err := l.ConfirmTransactions.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{groceries.Transaction.ID}}); err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}
	out, err = e.moze("verify")
	if err == nil || !strings.Contains(out, "無法解釋的差異共 1 項") {
		t.Errorf("verify with a stray transaction: err = %v, printed:\n%s", err, out)
	}
}

// anExpenseCategory is the id of a category of expenses the import made.
func (e importEnv) anExpenseCategory(l ledger) uuid.UUID {
	e.t.Helper()
	out, err := l.ListCategories.ListCategories(context.Background(), usecase.ListCategoriesInput{Direction: "expense"})
	if err != nil || len(out.Categories) == 0 {
		e.t.Fatalf("find a category: %d, %v", len(out.Categories), err)
	}
	return out.Categories[0].ID
}

func TestImportRefusesToApplyBeforeTheMappingIsReviewed(t *testing.T) {
	e := newImportEnv(t)
	out, err := e.moze("apply")
	if err == nil || !strings.Contains(out, "plan 還有要處理的項目") {
		t.Fatalf("apply with no mapping: err = %v, printed:\n%s", err, out)
	}
	listed, err := e.ledger().ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{})
	if err != nil || len(listed.Transactions) != 0 {
		t.Errorf("transactions = %d, %v, want none: nothing is written before the plan is clear", len(listed.Transactions), err)
	}
}
