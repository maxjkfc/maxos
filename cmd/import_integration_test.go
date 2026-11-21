package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"go.uber.org/fx"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/internal/testpg"
	"github.com/maxjkfc/maxos/pkg/config"
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
		r("初始金額", "Fake Purse", "TWD", "500", ""),

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
		// A trip, in an account of its own: the rows of the trip name a project, and the
		// expense of one of its days that is not the trip's does not.
		r("支出", "Fake Purse", "TWD", "-10", "2026-02-18 09:00", with(cat("飲食", "午餐"), "名稱", "Trip lunch", "專案", "2026-02 Fake Trip")...),
		r("支出", "Fake Purse", "TWD", "-10", "2026-02-19 09:00", with(cat("飲食", "午餐"), "名稱", "Trip lunch", "專案", "2026-02 Fake Trip")...),
		r("支出", "Fake Purse", "TWD", "-20", "2026-02-19 12:00", with(cat("飲食", "午餐"), "名稱", "Elsewhere lunch")...),
		r("支出", "Fake Purse", "TWD", "-10", "2026-02-20 09:00", with(cat("飲食", "午餐"), "名稱", "Trip lunch", "專案", "2026-02 Fake Trip")...),
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
	t.Setenv(config.MozeDirVariable, dir)
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
	VoidTransaction     usecase.VoidTransaction
	DiscardDraft        usecase.DiscardDraft
	GetDealings         usecase.GetDealings
	ListParties         catalog.ListParties
	ListProjects        catalog.ListProjects
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

// owed is what the party owes me in the instrument, by the confirmed dealings of the ledger.
func (e importEnv) owed(l ledger, party, code string) decimal.Decimal {
	e.t.Helper()
	ctx := context.Background()
	parties, err := l.ListParties.ListParties(ctx, catalog.ListPartiesInput{IncludeArchived: true})
	if err != nil {
		e.t.Fatal(err)
	}
	instruments, err := l.ListInstruments.ListInstruments(ctx, usecase.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		e.t.Fatal(err)
	}
	var partyID, instrumentID uuid.UUID
	for _, p := range parties.Parties {
		if p.Name == party {
			partyID = p.ID
		}
	}
	for _, i := range instruments.Instruments {
		if i.Code == code {
			instrumentID = i.ID
		}
	}
	if partyID == uuid.Nil || instrumentID == uuid.Nil {
		e.t.Fatalf("the ledger has no party %q, or no instrument %q", party, code)
	}
	out, err := l.GetDealings.GetDealings(ctx, usecase.GetDealingsInput{PartyID: &partyID})
	if err != nil {
		e.t.Fatal(err)
	}
	for _, d := range out.Dealings {
		if d.InstrumentID == instrumentID {
			return d.Quantity
		}
	}
	return decimal.Zero
}

// review is the user looking at the guesses of plan, and taking their marks away.
func (e importEnv) review() {
	e.t.Helper()
	path := filepath.Join(e.dir, "mapping.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	reviewed := regexp.MustCompile(`[ \t]*# guessed`).ReplaceAll(data, nil)
	if err := os.WriteFile(path, reviewed, 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// override is the user changing what the mapping says of one row.
func (e importEnv) override(rowKey, entry string) {
	e.t.Helper()
	path := filepath.Join(e.dir, "mapping.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "\nrows:\n") {
		text += "\nrows:\n"
	}
	if err := os.WriteFile(path, []byte(text+"  "+rowKey+": "+entry+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// rowKey is the key of the row of the export that has the name.
func (e importEnv) rowKey(rowName string) string {
	e.t.Helper()
	rows, err := file.NewMozeCSV().Rows(context.Background(), filepath.Join(e.dir, "MOZE_20260401_000000.csv"))
	if err != nil {
		e.t.Fatal(err)
	}
	for _, r := range rows {
		if r.Name == rowName {
			return r.Key
		}
	}
	e.t.Fatalf("the export has no row called %q", rowName)
	return ""
}

// transactions are all the transactions of the ledger.
func (e importEnv) transactions(l ledger) []usecase.Transaction {
	e.t.Helper()
	var all []usecase.Transaction
	for offset := 0; ; {
		page, err := l.ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{Limit: usecase.MaxListLimit, Offset: offset})
		if err != nil {
			e.t.Fatal(err)
		}
		all = append(all, page.Transactions...)
		offset += len(page.Transactions)
		if !page.HasMore || len(page.Transactions) == 0 {
			return all
		}
	}
}

// ofRow are the transactions of the ledger that were recorded from the row.
func (e importEnv) ofRow(l ledger, rowKey string) []usecase.Transaction {
	e.t.Helper()
	var out []usecase.Transaction
	for _, tx := range e.transactions(l) {
		if strings.HasPrefix(tx.IdempotencyKey, "moze:") && strings.Contains(tx.IdempotencyKey, rowKey) {
			out = append(out, tx)
		}
	}
	return out
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
	b, err := os.ReadFile(report)
	if err != nil || !strings.Contains(string(b), "每月底的預期餘額") {
		t.Errorf("report = %v, %q", err, b)
	}
	// What plan says is in Chinese: no run of three English words, not even in the lines
	// of the report that are not the names of rows (a table is data, in the export's own
	// words).
	english := regexp.MustCompile(`[A-Za-z]{2,} [A-Za-z]{2,} [A-Za-z]{2,}`)
	for _, text := range []string{out, string(b)} {
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, "|") && english.MatchString(line) {
				t.Errorf("plan says something in English: %q", line)
			}
		}
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
	if strings.Contains(string(data), "active_period") {
		t.Error("the mapping has an active period: the import never sets one")
	}

	// apply does not go on while the guesses are marked: what the ledger is made of
	// cannot be changed afterwards, so the user looks at each of them first.
	if out, err := e.moze("apply"); err == nil || !strings.Contains(out, "# guessed") {
		t.Fatalf("apply with guesses nobody looked at: err = %v, printed:\n%s", err, out)
	}
	if got := e.transactions(e.ledger()); len(got) != 0 {
		t.Fatalf("a refused apply recorded %d transactions", len(got))
	}
	e.review()
	again := e.must("plan")
	if strings.Contains(again, "新增") || strings.Contains(again, "已建立對照檔") {
		t.Errorf("a second plan adds to the mapping again:\n%s", again)
	}

	// apply: drafts, and the reference data they need; a second run adds nothing.
	first := e.must("apply")
	if !strings.Contains(first, "失敗 0 筆") || strings.Contains(first, "新增 0 筆，已更新") {
		t.Errorf("apply printed:\n%s", first)
	}
	if second := e.must("apply"); !strings.Contains(second, "草稿：新增 0 筆，已更新 0 筆") || !strings.Contains(second, "參考資料：新增 0 項") {
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

	// The import never sets the active period of a project, so no transaction is put in
	// the trip that its row does not name, though it is dated in the trip.
	projects, err := l.ListProjects.ListProjects(context.Background(), catalog.ListProjectsInput{})
	if err != nil || len(projects.Projects) != 1 || projects.Projects[0].ActivePeriod != nil {
		t.Errorf("projects = %+v, %v, want the trip with no active period", projects.Projects, err)
	}
	for _, tx := range e.transactions(l) {
		switch tx.Description {
		case "Trip lunch":
			if tx.ProjectID == nil {
				t.Error("a lunch of the trip is in no project")
			}
		case "Elsewhere lunch":
			if tx.ProjectID != nil {
				t.Error("a lunch that is not the trip's was put in the trip")
			}
		}
	}

	// A plan of instalments is a unit like the others: when what it says changes (here
	// the category of its interest), the ledger discards its origin with all its periods,
	// and apply records the plan again, as a whole.
	var instalments int
	for _, tx := range e.transactions(l) {
		if tx.PlanID != nil && tx.PlanSeq != nil && *tx.PlanSeq == 0 {
			instalments++
		}
	}
	text, err := os.ReadFile(mapping)
	if err != nil {
		t.Fatal(err)
	}
	changed := regexp.MustCompile(`(?m)^(\s*interest_category:\s*).*$`).ReplaceAll(text, []byte("${1}利息支出（另一類）"))
	if string(changed) == string(text) {
		t.Fatalf("the mapping has no interest category to change:\n%s", text)
	}
	if err := os.WriteFile(mapping, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if updated := e.must("apply"); !strings.Contains(updated, "已更新 1 筆") || !strings.Contains(updated, "分期 1") {
		t.Errorf("apply after the interest category changed printed:\n%s", updated)
	}
	var after, periods int
	for _, tx := range e.transactions(l) {
		if tx.PlanID != nil {
			periods++
			if tx.PlanSeq != nil && *tx.PlanSeq == 0 {
				after++
			}
		}
	}
	if instalments != 1 || after != 1 || periods != 4 {
		t.Errorf("plans = %d before and %d after, transactions of plans = %d, want one plan of an origin and three periods, replaced, not added to", instalments, after, periods)
	}

	// The user looks at a row again after apply: the 收款 was Amy paying back. What the
	// mapping says reaches the draft, and the month that is confirmed has it.
	paid := e.rowKey("有人付款")
	if got := e.ofRow(l, paid); len(got) != 1 || got[0].Kind != "income" {
		t.Fatalf("before the override the row is %+v, want an income", got)
	}
	e.override(paid, "{treatment: dealing, party: Amy}")
	if updated := e.must("apply"); !strings.Contains(updated, "已更新 1 筆") {
		t.Errorf("apply after the override printed:\n%s", updated)
	}
	if got := e.ofRow(l, paid); len(got) != 1 || got[0].Kind != "dealing" || got[0].Status != "draft" {
		t.Errorf("after the override the row is %+v, want one dealing draft and no income", got)
	}

	// confirm: the months are in order.
	if out, err := e.moze("confirm", "--month", "2026-02"); err == nil || !strings.Contains(out, "請先確認 2026-01") {
		t.Errorf("confirming February first: err = %v, printed:\n%s", err, out)
	}
	january := e.must("confirm", "--month", "2026-01")
	if !strings.Contains(january, "全部吻合") && !strings.Contains(january, "已說明") {
		t.Errorf("confirm printed:\n%s", january)
	}
	if got := e.ofRow(l, paid); len(got) != 1 || got[0].Kind != "dealing" || got[0].Status != "confirmed" {
		t.Errorf("after confirm the row is %+v, want the dealing to be what is confirmed", got)
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

	// What was paid for the company is one dealing of all that left the card for it, the 500 of
	// the lunch and the 5 of its fee, which is no fee expense of its own: the company owes 505.
	lunch := e.ofRow(l, e.rowKey("團膳"))
	if len(lunch) != 1 || lunch[0].Kind != "dealing" || lunch[0].Status != "confirmed" {
		t.Fatalf("the lunch paid for the company is %+v, want one confirmed dealing", lunch)
	}
	for _, p := range lunch[0].Postings {
		if p.CategoryID != nil || (p.AccountID != nil && !p.Quantity.Equal(decimal.RequireFromString("-505"))) {
			t.Errorf("posting %+v of the dealing, want the card to pay 505 and no posting of a category (the fee has none of its own)", p)
		}
	}
	if got := e.owed(l, "公司", "TWD"); !got.Equal(decimal.RequireFromString("505")) {
		t.Errorf("the company owes %s, want 505: what was paid for it and the fee of the row", got)
	}

	verify := e.must("verify")
	if !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 2 個") {
		t.Errorf("verify printed:\n%s", verify)
	}

	// The user changes their mind about a row of January that is confirmed: a confirmed
	// transaction is not changed, so apply says so and confirm and verify do not go on,
	// until the user voids it, and then apply records what the mapping says.
	noodles := e.rowKey("麵, 小菜")
	e.override(noodles, "{treatment: expense, category: 飲食/晚餐}")
	if out, err := e.moze("apply"); err == nil || !strings.Contains(out, "已確認的內容與對照不同 1 筆") {
		t.Errorf("apply with a confirmed transaction that differs: err = %v, printed:\n%s", err, out)
	}
	if out, err := e.moze("confirm", "--month", "2026-01"); err == nil || !strings.Contains(out, "已確認的內容與對照不同") {
		t.Errorf("confirm with a confirmed transaction that differs: err = %v, printed:\n%s", err, out)
	}
	if out, err := e.moze("verify"); err == nil || !strings.Contains(out, "已確認的內容與對照不同") {
		t.Errorf("verify with a confirmed transaction that differs: err = %v, printed:\n%s", err, out)
	}
	stale := e.ofRow(l, noodles)
	if len(stale) != 1 || stale[0].Status != "confirmed" {
		t.Fatalf("the row is %+v, want the confirmed expense, left alone", stale)
	}
	if _, err := l.VoidTransaction.VoidTransaction(context.Background(), stale[0].ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	if updated := e.must("apply"); !strings.Contains(updated, "已更新 1 筆") {
		t.Errorf("apply after the void printed:\n%s", updated)
	}
	e.must("confirm", "--month", "2026-01")
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 2 個") {
		t.Errorf("verify after the transaction was put right printed:\n%s", verify)
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
	if err == nil || !strings.Contains(out, "對照檔還有要處理的項目") || !strings.Contains(out, "fee_category") {
		t.Fatalf("apply with no mapping: err = %v, printed:\n%s", err, out)
	}
	listed, err := e.ledger().ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{})
	if err != nil || len(listed.Transactions) != 0 {
		t.Errorf("transactions = %d, %v, want none: nothing is written before the plan is clear", len(listed.Transactions), err)
	}
}
