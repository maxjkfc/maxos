package cmd_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/testpg"
	"github.com/maxjkfc/maxos/internal/webtest"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
)

// webApp is the app as `serve` runs it, minus the listening: the modules it is made of
// (the inbox, the catalog, the finance ledger, the MCP tools, the pages and the
// handler that mounts both behind Access), on a real PostgreSQL. It differs in two
// things a test has to: the clock says a day that the test chose, and the Access
// verifier trusts a key of the test's own, for real Access cannot be reached. The
// handler and the use cases the test records the ledger with come out of the same
// graph.
type webApp struct {
	fx.In

	Handler http.Handler

	CreateInstitution usecase.CreateInstitution
	CreateInstrument  usecase.CreateInstrument
	CreateAccount     usecase.CreateAccount
	CreateCategory    usecase.CreateCategory
	CreateArea        catalogusecase.CreateArea
	CreateParty       catalogusecase.CreateParty

	SetOpeningBalance usecase.SetOpeningBalance
	RecordExpense     usecase.RecordExpense
	RecordIncome      usecase.RecordIncome
	RecordTransfer    usecase.RecordTransfer
	RecordDealing     usecase.RecordDealing
	Confirm           usecase.ConfirmTransactions
	SetAreaBudget     usecase.SetAreaBudget
}

func newWebApp(t *testing.T, clk financeclock.Clock, access *webtest.Access) webApp {
	t.Helper()
	var app webApp
	fxApp := fx.New(
		fx.NopLogger,
		fx.Supply(testpg.New(t)),
		fx.Provide(
			func() financeclock.Clock { return clk },
			func() *slog.Logger { return slog.New(slog.DiscardHandler) },
			func() *cfaccess.Verifier { return access.Verifier() },
		),
		cmd.InboxModule(),
		cmd.CatalogModule(),
		cmd.FinanceModule(),
		cmd.MCPModule(),
		cmd.WebModule(),
		cmd.HTTPModule(),
		fx.Populate(&app),
	)
	if err := fxApp.Err(); err != nil {
		t.Fatalf("assemble the modules: %v", err)
	}
	return app
}

// TestThePagesShowTheLedgerThroughTheRealWiring records a small ledger through the
// real use cases, confirms most of it, and reads every page over HTTP with a token
// that the verifier of the app accepts: the figures that the pages show are the ones
// that the ledger holds.
func TestThePagesShowTheLedgerThroughTheRealWiring(t *testing.T) {
	ctx := context.Background()
	clk := newTestClock(day) // the 14th of November 2026, noon in the zone of the user
	access := webtest.NewAccess(t)
	app := newWebApp(t, clk, access)

	n := 0
	details := func(on time.Time, description string) usecase.RecordDetails {
		n++
		return usecase.RecordDetails{IdempotencyKey: fmt.Sprintf("web-e2e-%04d", n), OccurredOn: on, Description: description}
	}
	var toConfirm []uuid.UUID
	confirm := func(txns ...usecase.Transaction) {
		for _, tx := range txns {
			toConfirm = append(toConfirm, tx.ID)
		}
	}
	twd := model.BaseCurrencyID

	// What the ledger refers to.
	usd := must(t, "CreateInstrument", func() (usecase.Instrument, error) {
		return app.CreateInstrument.CreateInstrument(ctx, usecase.CreateInstrumentInput{Code: "USD", Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2})
	}).ID
	institution := must(t, "CreateInstitution", func() (usecase.Institution, error) {
		return app.CreateInstitution.CreateInstitution(ctx, usecase.CreateInstitutionInput{Name: "好銀行"})
	}).ID
	account := func(name, kind string, inst *uuid.UUID, closing, due *int) uuid.UUID {
		return must(t, "CreateAccount "+name, func() (usecase.Account, error) {
			return app.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: name, Kind: kind, InstitutionID: inst, ClosingDay: closing, PaymentDueDay: due})
		}).ID
	}
	cash := account("現金", "cash", nil, nil, nil)
	bank := account("好銀行活期", "bank", &institution, nil, nil)
	card := account("好銀行信用卡", "card", &institution, new(5), new(20))
	category := func(direction, name string, parent *uuid.UUID) uuid.UUID {
		return must(t, "CreateCategory "+name, func() (usecase.Category, error) {
			return app.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: direction, ParentID: parent, Name: name})
		}).ID
	}
	food := category("expense", "飲食", nil)
	lunch := category("expense", "午餐", &food)
	rent := category("expense", "房租", nil)
	shopping := category("expense", "購物", nil)
	interest := category("expense", "利息", nil)
	salary := category("income", "薪水", nil)
	home := must(t, "CreateArea", func() (catalogusecase.Area, error) {
		return app.CreateArea.CreateArea(ctx, catalogusecase.CreateAreaInput{Name: "家庭"})
	}).ID
	alice := must(t, "CreateParty", func() (catalogusecase.Party, error) {
		return app.CreateParty.CreateParty(ctx, catalogusecase.CreatePartyInput{Name: "Alice"})
	}).ID

	line := func(c uuid.UUID, amount string) []usecase.CategoryLine {
		return []usecase.CategoryLine{{CategoryID: c, Amount: dec(amount)}}
	}
	expense := func(on time.Time, from uuid.UUID, c uuid.UUID, amount, description string, area *uuid.UUID) usecase.Transaction {
		d := details(on, description)
		d.AreaID = area
		return must(t, "RecordExpense "+description, func() (usecase.RecordOutput, error) {
			return app.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{RecordDetails: d, AccountID: &from, InstrumentID: twd, Lines: line(c, amount)})
		}).Transaction
	}

	// Opening balances, a salary, the rent, a lunch today, and what Alice owes me.
	for _, o := range []struct {
		account uuid.UUID
		amount  string
	}{{cash, "5000"}, {bank, "100000"}} {
		confirm(must(t, "SetOpeningBalance", func() (usecase.RecordOutput, error) {
			return app.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
				RecordDetails: details(dateOn(2026, time.October, 1), "期初餘額"), AccountID: o.account, InstrumentID: twd, Amount: dec(o.amount),
			})
		}).Transaction)
	}
	confirm(must(t, "RecordIncome", func() (usecase.RecordOutput, error) {
		return app.RecordIncome.RecordIncome(ctx, usecase.RecordIncomeInput{
			RecordDetails: details(dateOn(2026, time.November, 5), "十一月薪水"), AccountID: bank, InstrumentID: twd, Lines: line(salary, "60000"),
		})
	}).Transaction)
	rentTx := expense(dateOn(2026, time.November, 3), bank, rent, "15000", "十一月房租", &home)
	lunchTx := expense(day, cash, lunch, "120", "麵店午餐", nil)
	confirm(rentTx, lunchTx)
	confirm(must(t, "RecordDealing", func() (usecase.RecordOutput, error) {
		return app.RecordDealing.RecordDealing(ctx, usecase.RecordDealingInput{
			RecordDetails: details(dateOn(2026, time.November, 10), "借 Alice"), AccountID: cash, PartyID: alice, InstrumentID: twd, Amount: dec("1000"), Direction: "paid_out",
		})
	}).Transaction)

	// The card: two purchases on the statement that closed on the 5th, and a purchase in
	// three instalments with interest.
	confirm(
		expense(dateOn(2026, time.October, 20), card, food, "800", "超市", nil),
		expense(dateOn(2026, time.November, 2), card, food, "2500", "聚餐", nil),
	)
	var periods []usecase.InstalmentPeriod
	for _, due := range []time.Time{dateOn(2026, time.December, 8), dateOn(2027, time.January, 8), dateOn(2027, time.February, 8)} {
		periods = append(periods, usecase.InstalmentPeriod{DueOn: due, Principal: dec("2000"), Interest: new(dec("50"))})
	}
	origin := must(t, "RecordExpense with instalments", func() (usecase.RecordOutput, error) {
		return app.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
			RecordDetails: details(dateOn(2026, time.November, 8), "筆電分三期"), AccountID: &card, InstrumentID: twd, Lines: line(shopping, "6000"),
			Instalments: &usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &interest},
		})
	}).Transaction
	confirm(origin)

	// A purchase in dollars, which counts in the base currency by the amount of its day.
	usdPurchase := details(dateOn(2026, time.November, 6), "訂閱服務")
	confirm(must(t, "RecordExpense in USD", func() (usecase.RecordOutput, error) {
		return app.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
			RecordDetails: usdPurchase, AccountID: &bank, InstrumentID: usd,
			Lines: []usecase.CategoryLine{{CategoryID: shopping, Amount: dec("10"), BaseAmount: new(dec("320"))}},
		})
	}).Transaction)

	for len(toConfirm) > 0 {
		k := min(len(toConfirm), 100)
		if _, err := app.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: toConfirm[:k]}); err != nil {
			t.Fatalf("ConfirmTransactions: %v", err)
		}
		toConfirm = toConfirm[k:]
	}

	// Two drafts that wait: a lunch and a withdrawal.
	expense(dateOn(2026, time.November, 13), cash, lunch, "450", "草稿午餐", nil)
	must(t, "RecordTransfer", func() (usecase.RecordOutput, error) {
		return app.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
			RecordDetails: details(dateOn(2026, time.November, 12), "提款"), FromAccountID: bank, ToAccountID: cash, InstrumentID: twd, Amount: dec("3000"),
		})
	})

	// The budget of the area for November.
	must(t, "SetAreaBudget", func() (usecase.AreaBudget, error) {
		return app.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{
			AreaID: home, Month: dateOn(2026, time.November, 1), Amount: dec("20000"),
			Allocations: []usecase.Allocation{{CategoryID: rent, Amount: dec("15000")}, {CategoryID: food, Amount: dec("5000")}},
		})
	})

	srv := httptest.NewServer(app.Handler)
	t.Cleanup(srv.Close)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request := func(method, path string, token string) (*http.Response, string) {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if token != "" {
			req.Header.Set(webtest.Header, token)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("%s %s: read the body: %v", method, path, err)
		}
		return res, string(body)
	}
	token := access.Token(t, "max@example.com")
	page := func(path string) string {
		t.Helper()
		res, body := request(http.MethodGet, path, token)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200:\n%s", path, res.StatusCode, body)
		}
		return body
	}
	wantIn := func(what, body string, texts ...string) {
		t.Helper()
		for _, text := range texts {
			if !strings.Contains(body, text) {
				t.Errorf("%s lacks %q:\n%s", what, text, body)
			}
		}
	}
	// money is how a page writes an amount; html/template writes a plus sign as a
	// character reference.
	money := func(text, unit string) string {
		return strings.ReplaceAll(text, "+", "&#43;") + `<span class="unit"> ` + unit + `</span>`
	}

	t.Run("the overview", func(t *testing.T) {
		body := page("/app/")
		wantIn("the overview", body,
			"<strong>max@example.com</strong>",
			"資料截至今天（2026-11-14）",
			"<caption>好銀行</caption>", "<caption>沒有機構</caption>",
			// 5,000 less the lunch and what was lent: 3,880; the bank: 100,000 and the salary
			// less the rent; and the purchase in dollars on the same account, in its own line.
			money("3,880.00", "TWD"), money("145,000.00", "TWD"), money("−10.00", "USD"),
			// The card owes what it was charged, and the instalments that are not due yet are
			// listed apart from it.
			money("−3,300.00", "TWD"), `未到期分期　</span><span class="amt neg">−6,000.00`,
			"2026-12-08", money("6,000.00", "TWD"),
			// Alice owes me what I lent.
			">Alice</th>", money("1,000.00", "TWD"), "對方欠我",
			// Two drafts wait: a lunch of 450, and a withdrawal that moves 3,000 from one of my
			// accounts to another: it changes no total, so only the lunch is in what confirming
			// them would change, and the withdrawal is a line of its own.
			"有 <strong>2</strong> 筆草稿等著確認", money("450.00", "TWD"), money("−450.00", "TWD"),
			"自己帳戶之間的轉帳：1 筆",
		)
		if strings.Contains(body, "淨資產") {
			t.Error("the overview shows a net worth")
		}
	})

	t.Run("the transactions of the month", func(t *testing.T) {
		body := page("/app/transactions")
		wantIn("the transactions", body, "<strong>2026-11</strong>",
			"十一月房租", "麵店午餐", "十一月薪水", "筆電分三期", "訂閱服務", "草稿午餐", "提款", "借 Alice",
			money("−15,000.00", "TWD"), money("+60,000.00", "TWD"), money("−10.00", "USD"),
		)
		if got := strings.Count(body, `<tr role="row" class="draft">`); got != 2 {
			t.Errorf("%d draft rows, want 2", got)
		}
		// October has its own page.
		wantIn("October", page("/app/transactions?month=2026-10"), "超市", "期初餘額")
		// The filters narrow it.
		drafts := page("/app/transactions?status=draft")
		wantIn("the drafts", drafts, "草稿午餐", "提款")
		if strings.Contains(drafts, "十一月房租") {
			t.Error("a confirmed transaction is in the list of the drafts")
		}
		kind := page("/app/transactions?kind=income")
		wantIn("the incomes", kind, "十一月薪水")
		if strings.Contains(kind, "十一月房租") {
			t.Error("an expense is in the list of the incomes")
		}
		withCard := page("/app/transactions?account=" + card.String())
		wantIn("the card", withCard, "聚餐", "筆電分三期")
		wantIn("the card in October", page("/app/transactions?month=2026-10&account="+card.String()), "超市")
		if strings.Contains(withCard, "十一月房租") {
			t.Error("a transaction of another account is in the list of the card")
		}
	})

	t.Run("a transaction, and the plan it is part of", func(t *testing.T) {
		body := page("/app/transactions/" + origin.ID.String())
		wantIn("the origin of the plan", body, "<h2>支出", "筆電分三期", "分期計畫：分期起點，共 3 期", "分期第 1 期", "分期第 3 期", "2026-12-08")
		wantIn("the lunch", page("/app/transactions/"+lunchTx.ID.String()), "<h2>支出", "麵店午餐", `data-label="帳戶、類別或對象">午餐</td>`, money("−120.00", "TWD"))
	})

	t.Run("the drafts", func(t *testing.T) {
		body := page("/app/drafts")
		wantIn("the drafts", body,
			"共有 <strong>2</strong> 筆草稿等著確認", "<h2>2026-11 ", "好銀行活期", ">現金<",
			// The accounts have the legs of the withdrawal: 3,000 out of the one and in to the
			// other, and the cash the lunch of 450.
			money("3,000.00", "TWD"), money("450.00", "TWD"), money("−450.00", "TWD"),
			// The withdrawal moves money between two of my own accounts: it is a line of its
			// own, which takes the 3,000 out of the rows of the two accounts, in the column of
			// what leaves and in the one of what arrives, and the month has only what the lunch
			// changes.
			"自己帳戶之間的轉帳", `<td role="cell" data-label="流出" class="num"><span class="line"><span class="amt">`+money("−3,000.00", "TWD"),
			`<td role="cell" data-label="流入" class="num"><span class="line"><span class="amt">`+money("−3,000.00", "TWD"),
		)
		wantIn("the link of an account", body, `href="/app/transactions?account=`+cash.String()+`&amp;month=2026-11&amp;periods=none&amp;status=draft"`)
		// The link lists the drafts that the row counts: the lunch and the withdrawal, which
		// is a draft of the cash too.
		linked := page("/app/transactions?account=" + cash.String() + "&month=2026-11&periods=none&status=draft")
		wantIn("the drafts of the cash", linked, "草稿午餐", "提款", "這份清單只列分期計畫的起點")
	})

	t.Run("the statements of the card", func(t *testing.T) {
		wantIn("the cards", page("/app/cards"),
			"好銀行信用卡", "結帳日：每月 5 日；繳款日：每月 20 日", "?closing=2026-11-05", "2026-10-06 至 2026-11-05", "2026-11-20",
			money("3,300.00", "TWD"), money("−3,300.00", "TWD"),
		)
		statement := page("/app/cards/" + card.String() + "?closing=2026-11-05")
		wantIn("the statement", statement,
			"2026-10-06 至 2026-11-05", "<dt>繳款日期</dt><dd>2026-11-20</dd>", "超市", "聚餐",
			money("3,300.00", "TWD"), money("−3,300.00", "TWD"),
			// The charges are all food: the chart and its table agree.
			"<caption>TWD</caption>", `<th scope="row">飲食</th><td class="num"><span class="amt">3,300.00`, `id="charges-TWD-title"`,
		)
		if strings.Index(statement, "超市") > strings.Index(statement, "聚餐") {
			t.Error("the statement is not oldest first")
		}
		// No day asks for the one that is open: it carries the 3,300 and was charged nothing.
		wantIn("the open statement", page("/app/cards/"+card.String()), "未結帳", "2026-11-06 至 2026-12-05", money("−3,300.00", "TWD"))
	})

	t.Run("the budgets", func(t *testing.T) {
		body := page("/app/budgets")
		home := body[strings.Index(body, "<h3>家庭</h3>"):]
		wantIn("the budget of the area", home,
			money("20,000.00", "TWD"), money("15,000.00", "TWD"), "還剩", money("5,000.00", "TWD"), "已花預算的 75.0%",
			`<th scope="row" class="nowrap">房租</th>`, `<th scope="row" class="nowrap">飲食</th>`, `class="chart-fill"`,
		)
		if strings.Contains(home[:strings.Index(home, "</section>")], "badge-over") {
			t.Error("an area within its budget is marked over")
		}
	})

	t.Run("the spending", func(t *testing.T) {
		body := page("/app/spending")
		wantIn("the spending", body,
			// The rent 15,000, the lunch 120, the dinner 2,500, the laptop 6,000 on the day of
			// the purchase, and the 10 dollars as 320: the interest is not spent until a period
			// falls due.
			money("23,940.00", "TWD"),
			`<th scope="row">房租</th><td class="num"><span class="amt">15,000.00`,
			`<th scope="row">購物</th><td class="num"><span class="amt">6,320.00`,
			`<th scope="row">飲食</th><td class="num"><span class="amt">2,620.00`,
			`<th scope="row" class="indent">午餐</th>`,
			`class="chart chart-hbars"`, `class="chart chart-columns"`,
			// The months of the chart, and October's 800.
			`<th scope="row"><a href="/app/spending?month=2026-10">2026-10</a></th><td class="num"><span class="amt">800.00`,
			`<tr class="current" aria-current="true"><th scope="row"><a href="/app/spending?month=2026-11">2026-11</a></th><td class="num"><span class="amt">23,940.00`,
		)
		wantIn("October", page("/app/spending?month=2026-10"), money("800.00", "TWD"))
		wantIn("a month with nothing", page("/app/spending?month=2026-09"), "2026-09 沒有支出。")
	})

	t.Run("what is refused", func(t *testing.T) {
		for _, path := range []string{"/app", "/app/", "/app/transactions", "/app/cards/" + card.String(), "/app/nowhere", "/app/static/app.css", "/mcp"} {
			res, body := request(http.MethodGet, path, "")
			if res.StatusCode != http.StatusUnauthorized || body != "unauthorized\n" {
				t.Errorf("GET %s without a token = %d %q, want the 401 of /mcp", path, res.StatusCode, body)
			}
		}
		if res, _ := request(http.MethodGet, "/app", token); res.StatusCode != http.StatusMovedPermanently || res.Header.Get("Location") != "/app/" {
			t.Errorf("GET /app = %d to %q, want a redirect to /app/", res.StatusCode, res.Header.Get("Location"))
		}
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
			if res, _ := request(method, "/app/transactions", token); res.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("%s /app/transactions = %d, want 405", method, res.StatusCode)
			}
		}
		for path, want := range map[string]int{
			"/app/transactions/" + uuid.New().String(): http.StatusNotFound,
			"/app/transactions?month=2026-13":          http.StatusBadRequest,
			"/app/cards/" + cash.String():              http.StatusNotFound, // not a card
		} {
			if res, body := request(http.MethodGet, path, token); res.StatusCode != want {
				t.Errorf("GET %s = %d, want %d:\n%s", path, res.StatusCode, want, body)
			}
		}
		res, _ := request(http.MethodGet, "/app/", token)
		for name, want := range map[string]string{
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
		} {
			if got := res.Header.Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
		if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
			t.Errorf("Content-Security-Policy = %q", csp)
		}
	})
}

// must returns what a call made, and fails the test when it made an error.
func must[T any](t *testing.T, what string, call func() (T, error)) T {
	t.Helper()
	v, err := call()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	return v
}
