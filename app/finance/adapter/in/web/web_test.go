package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/webtest"
)

// populated is a world with something for every page to show.
func populated() *fake {
	f := newFake()
	t := lunchTransaction(1, date(2026, time.November, 14), cash, "120")
	f.listed = []usecase.Transaction{t}
	f.byID[t.ID] = t
	f.balances = []usecase.PreviewBalance{held(cash, twd, "880")}
	f.statement = usecase.CardStatement{
		CardStatementSummary: usecase.CardStatementSummary{From: date(2026, time.November, 6), ClosingOn: date(2026, time.December, 5)},
	}
	f.statements = []usecase.CardStatementSummary{f.statement.CardStatementSummary}
	return f
}

// pathOf is the path of a route that the world of populated has a page for.
func pathOf(t *testing.T, pattern string) string {
	t.Helper()
	switch pattern {
	case "/app/{$}":
		return "/app/"
	case "/app/transactions/{id}":
		return "/app/transactions/" + seqID(1).String()
	case "/app/cards/{account}":
		return "/app/cards/" + card.String()
	case "/app/static/app.css", "/app", "/app/":
		return strings.ReplaceAll(pattern, "{$}", "")
	}
	if strings.Contains(pattern, "{") {
		t.Fatalf("route %s has a wildcard that the test does not know how to fill", pattern)
	}
	return pattern
}

// TestTheRoutes pins the pages there are: a page that is added must be added here,
// which is how every test that goes over all the routes comes to cover it.
func TestTheRoutes(t *testing.T) {
	s := populated().site(t)
	var got []string
	for _, r := range s.pages.Routes() {
		got = append(got, r.Method+" "+r.Pattern)
	}
	slices.Sort(got)
	want := []string{
		"GET /app",
		"GET /app/",
		"GET /app/{$}",
		"GET /app/budgets",
		"GET /app/cards",
		"GET /app/cards/{account}",
		"GET /app/drafts",
		"GET /app/spending",
		"GET /app/static/app.css",
		"GET /app/transactions",
		"GET /app/transactions/{id}",
	}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("routes =\n%v\nwant\n%v", got, want)
	}
}

func TestARequestWithoutAValidTokenIsRefusedByEveryRoute(t *testing.T) {
	f := populated()
	s := f.site(t)
	future := time.Now().Add(time.Hour).Unix()
	past := time.Now().Add(-time.Hour).Unix()
	otherAudience := s.access.Sign(t, map[string]any{
		"aud": []string{"another-application"}, "iss": webtest.Team, "sub": "x", "email": "max@example.com", "exp": future,
	})
	expired := s.access.Sign(t, map[string]any{
		"aud": []string{webtest.AUD}, "iss": webtest.Team, "sub": "x", "email": "max@example.com", "exp": past,
	})

	requests := map[string]func(target string) *http.Request{
		"no token": func(target string) *http.Request { return httptest.NewRequest(http.MethodGet, target, nil) },
		"a token that is not a token": func(target string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set(webtest.Header, "not-a-jwt")
			return r
		},
		"the wrong audience": func(target string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set(webtest.Header, otherAudience)
			return r
		},
		"an expired token": func(target string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.Header.Set(webtest.Header, expired)
			return r
		},
		// The cookie Access sets is deliberately not honoured, only the header.
		"the cookie of Access and no header": func(target string) *http.Request {
			r := httptest.NewRequest(http.MethodGet, target, nil)
			r.AddCookie(&http.Cookie{Name: "CF_Authorization", Value: s.access.Token(t, "max@example.com")})
			return r
		},
	}
	for _, route := range s.pages.Routes() {
		path := pathOf(t, route.Pattern)
		for name, make := range requests {
			t.Run(name+" "+route.Pattern, func(t *testing.T) {
				res := s.do(make(path))
				if res.Code != http.StatusUnauthorized {
					t.Fatalf("GET %s = %d, want 401", path, res.Code)
				}
				if res.Body != "unauthorized\n" {
					t.Errorf("body = %q: it is not the refusal of /mcp", res.Body)
				}
			})
		}
	}
	for _, path := range []string{"/app/nowhere", "/app/transactions/nope"} {
		if res := s.do(httptest.NewRequest(http.MethodGet, path, nil)); res.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401: a path that is not a page must not say it is not there", path, res.Code)
		}
	}
	if n := f.total(); n != 0 {
		t.Errorf("%d use cases ran for requests that had no valid token", n)
	}
}

func TestEveryRouteServesAPageToASignedInUser(t *testing.T) {
	s := populated().site(t)
	for _, route := range s.pages.Routes() {
		path := pathOf(t, route.Pattern)
		t.Run(route.Pattern, func(t *testing.T) {
			res := s.get(path)
			want := http.StatusOK
			if route.Pattern == "/app" {
				want = http.StatusMovedPermanently
			}
			if res.Code != want {
				t.Fatalf("GET %s = %d, want %d:\n%s", path, res.Code, want, res.Body)
			}
		})
	}
	// The signed-in email is in the header of a page.
	page := s.page("/app/")
	wantIn(t, page, "<strong>max@example.com</strong>")
}

func TestOnlyGETIsServed(t *testing.T) {
	f := populated()
	s := f.site(t)
	for _, route := range s.pages.Routes() {
		path := pathOf(t, route.Pattern)
		for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
			t.Run(method+" "+route.Pattern, func(t *testing.T) {
				res := s.do(s.access.Request(t, method, path, "max@example.com"))
				if res.Code != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s = %d, want 405", method, path, res.Code)
				}
				if got := res.Header.Get("Allow"); got != "GET, HEAD" {
					t.Errorf("Allow = %q", got)
				}
			})
		}
	}
	if n := f.total(); n != 0 {
		t.Errorf("%d use cases ran for requests with a method that is refused", n)
	}
	if res := s.do(s.access.Request(t, http.MethodHead, "/app/", "max@example.com")); res.Code != http.StatusOK {
		t.Errorf("HEAD /app/ = %d, want 200", res.Code)
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	f := populated()
	f.failures["GetTransaction"] = nil
	s := f.site(t)

	targets := []string{
		"/app/nowhere",                     // 404
		"/app/transactions/not-an-id",      // 404
		"/app/transactions?month=nonsense", // 400
		"/app/budgets?month=2026-13",       // 400
		"/app/cards/" + spareCard.String(), // an account with no statements
	}
	for _, route := range s.pages.Routes() {
		targets = append(targets, pathOf(t, route.Pattern))
	}
	check := func(what string, res response) {
		t.Helper()
		want := map[string]string{
			"X-Content-Type-Options": "nosniff",
			"Referrer-Policy":        "no-referrer",
			"Cache-Control":          "no-store",
		}
		for name, value := range want {
			if got := res.Header.Get(name); got != value {
				t.Errorf("%s: %s = %q, want %q", what, name, got, value)
			}
		}
		csp := res.Header.Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'self'") || strings.Contains(csp, "unsafe-inline") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: Content-Security-Policy = %q", what, csp)
		}
		if strings.Contains(res.Body, "<script") || strings.Contains(res.Body, " style=") || strings.Contains(res.Body, "<style") {
			t.Errorf("%s has what the content security policy forbids", what)
		}
		// The namespace of inline SVG is a name, not an address that is fetched.
		body := strings.ReplaceAll(res.Body, `xmlns="http://www.w3.org/2000/svg"`, "")
		for _, external := range []string{"http://", "https://", "//cdn", "@import", "src=", "url("} {
			if strings.Contains(body, external) {
				t.Errorf("%s refers to %q: a page loads nothing from outside", what, external)
			}
		}
	}
	for _, target := range targets {
		check("GET "+target, s.get(target))
	}
	check("a request without a token", s.do(httptest.NewRequest(http.MethodGet, "/app/", nil)))
	check("POST", s.do(s.access.Request(t, http.MethodPost, "/app/", "max@example.com")))
}

func TestAPageThatIsNotThereIsAPage(t *testing.T) {
	f := populated()
	s := f.site(t)
	unknown := seqID(999).String()
	tests := []struct {
		target     string
		wantStatus int
		wantText   string
	}{
		{"/app/nowhere", http.StatusNotFound, "找不到這個頁面"},
		{"/app/transactions/" + unknown, http.StatusNotFound, "找不到這筆交易"},
		{"/app/transactions/not-an-id", http.StatusNotFound, "找不到這筆交易"},
		{"/app/cards/not-an-id", http.StatusNotFound, "找不到這個帳戶"},
		{"/app/cards/" + spareCard.String(), http.StatusNotFound, "沒有帳單"},
		{"/app/cards/" + unknown, http.StatusNotFound, "找不到這個帳戶"},

		{"/app/transactions?month=2026-13", http.StatusBadRequest, "month 必須是"},
		{"/app/transactions?month=nov", http.StatusBadRequest, "month 必須是"},
		{"/app/transactions?month=2026-1", http.StatusBadRequest, "month 必須是"},
		{"/app/transactions?month=1899-12", http.StatusBadRequest, "month 必須是"},
		{"/app/transactions?month=3000-01", http.StatusBadRequest, "month 必須是"},
		{"/app/transactions?account=nope", http.StatusBadRequest, "account 不是有效的 id"},
		{"/app/transactions?page=0", http.StatusBadRequest, "page 必須是"},
		{"/app/transactions?page=x", http.StatusBadRequest, "page 必須是"},
		{"/app/budgets?month=x", http.StatusBadRequest, "month 必須是"},
		{"/app/spending?month=2026-00", http.StatusBadRequest, "month 必須是"},
		{"/app/cards/" + card.String() + "?closing=2026-02-30", http.StatusBadRequest, "closing 必須是"},
		{"/app/cards/" + card.String() + "?closing=yesterday", http.StatusBadRequest, "closing 必須是"},
	}
	f.failures["GetCardStatement"] = nil
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			f.failures["GetCardStatement"] = nil
			switch {
			case strings.Contains(tt.target, spareCard.String()):
				f.failures["GetCardStatement"] = fmt.Errorf("card: %w", model.ErrNoStatements)
			case strings.Contains(tt.target, unknown) && strings.HasPrefix(tt.target, "/app/cards/"):
				f.failures["GetCardStatement"] = fmt.Errorf("account: %w", model.ErrNotFound)
			}
			res := s.get(tt.target)
			if res.Code != tt.wantStatus {
				t.Fatalf("GET %s = %d, want %d:\n%s", tt.target, res.Code, tt.wantStatus, res.Body)
			}
			wantIn(t, res.Body, tt.wantText, "<nav", "狀態碼 "+fmt.Sprint(tt.wantStatus))
		})
	}
}

func TestWhatTheUseCaseRefusesIsTheUsersToSeeAndTheRestIsNot(t *testing.T) {
	f := populated()
	s := f.site(t)
	tests := []struct {
		name       string
		use        string
		err        error
		target     string
		wantStatus int
		wantText   string
	}{
		{"a status that is none", "ListTransactions", fmt.Errorf("list: %w", model.ErrInvalidStatus), "/app/transactions?status=nope", http.StatusBadRequest, "status 必須是"},
		{"a kind that is none", "ListTransactions", fmt.Errorf("list: %w", model.ErrInvalidKind), "/app/transactions?kind=nope", http.StatusBadRequest, "kind 不是已知的交易種類"},
		{"a failure of the database", "GetPreviewBalances", fmt.Errorf("get preview balances: connection to db-host-secret refused"), "/app/", http.StatusInternalServerError, "伺服器發生錯誤"},
		{"a failure in a list of names", "ListAccounts", fmt.Errorf("list accounts: connection to db-host-secret refused"), "/app/drafts", http.StatusInternalServerError, "伺服器發生錯誤"},
		{"a failure of the catalog", "ListAreas", fmt.Errorf("list areas: connection to db-host-secret refused"), "/app/budgets", http.StatusInternalServerError, "伺服器發生錯誤"},
		{"a failure of the spending", "GetSpending", fmt.Errorf("spending: connection to db-host-secret refused"), "/app/spending", http.StatusInternalServerError, "伺服器發生錯誤"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clear(f.failures)
			f.failures[tt.use] = tt.err
			res := s.get(tt.target)
			if res.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d:\n%s", res.Code, tt.wantStatus, res.Body)
			}
			wantIn(t, res.Body, tt.wantText)
			wantNotIn(t, res.Body, "db-host-secret", tt.use)
		})
	}
}

// TestPagesIssueAFixedNumberOfCalls reads each page of a world with one row of
// everything and of a world with hundreds, and finds the same calls: what the page
// asks of the use cases does not grow with what it shows.
func TestPagesIssueAFixedNumberOfCalls(t *testing.T) {
	targets := map[string]string{
		"the overview":        "/app/",
		"the transactions":    "/app/transactions?month=2026-11",
		"a transaction":       "/app/transactions/" + seqID(1).String(),
		"the drafts":          "/app/drafts",
		"the cards":           "/app/cards",
		"a statement":         "/app/cards/" + card.String(),
		"the budgets":         "/app/budgets",
		"the spending":        "/app/spending",
		"the spending, later": "/app/spending?month=2024-03",
	}
	world := func(n int) *fake {
		f := populated()
		f.listed, f.balances, f.dealings, f.plans = nil, nil, nil, nil
		f.drafts = usecase.GetDraftSummaryOutput{}
		f.statements = nil
		f.statement.Transactions = nil
		f.areaBudgets, f.projectBudgets = usecase.GetAreaBudgetsOutput{}, usecase.GetProjectBudgetsOutput{}
		f.spent = usecase.GetSpendingOutput{}
		for i := range n {
			tx := lunchTransaction(1000+i, date(2026, time.November, 1+i%28), cash, "10")
			f.listed = append(f.listed, tx)
			f.byID[tx.ID] = tx
			// Drafts wait for every other account, and the count of them is not nil, so the
			// overview is of the balances with the drafts counted in, as it is when any wait.
			f.balances = append(f.balances, usecase.PreviewBalance{
				AccountID: seqID(2000 + i), InstrumentID: twd, Quantity: dec("1"), Confirmed: dec(fmt.Sprint(i % 2)), Drafts: dec(fmt.Sprint(1 - i%2)),
			})
			f.dealings = append(f.dealings, usecase.Dealing{PartyID: seqID(3000 + i), InstrumentID: twd, Quantity: dec("5")})
			f.plans = append(f.plans, usecase.InstalmentPlanSummary{PlanID: seqID(4000 + i), AccountID: card, InstrumentID: twd})
			month := date(2026, time.Month(1+i%12), 1)
			account := seqID(5000 + i)
			f.drafts.Months = append(f.drafts.Months, usecase.DraftMonth{Month: month, Count: 1, Accounts: []usecase.DraftAccount{{AccountID: &account, Count: 1}}})
			f.statements = append(f.statements, usecase.CardStatementSummary{
				From: date(2026, time.Month(1+i%12), 6), ClosingOn: date(2026, time.Month(1+i%12), 5).AddDate(0, 1, 0), Closed: i%2 == 0,
			})
			f.statement.Transactions = append(f.statement.Transactions, usecase.StatementTransaction{Transaction: tx, StatementDate: tx.OccurredOn})
			f.areaBudgets.Areas = append(f.areaBudgets.Areas, usecase.AreaBudgetStatus{AreaID: seqID(6000 + i)})
			f.projectBudgets.Projects = append(f.projectBudgets.Projects, usecase.ProjectBudgetStatus{ProjectID: seqID(7000 + i)})
			f.spent.TopLevel = append(f.spent.TopLevel, usecase.TopLevelSpending{CategoryID: seqID(8000 + i), Total: dec("1"), Own: dec("1")})
			f.accounts = append(f.accounts, usecase.Account{ID: seqID(2000 + i), Name: fmt.Sprintf("帳戶 %d", i), Kind: "bank"})
		}
		f.drafts.Count = n
		f.statement.Transactions = append([]usecase.StatementTransaction(nil), f.statement.Transactions...)
		return f
	}

	for name, target := range targets {
		t.Run(name, func(t *testing.T) {
			small, large := world(1), world(400)
			// The cards of the large world have a closing day too: each gets a call, which is
			// the number of cards and is the same in both.
			small.site(t).page(target)
			large.site(t).page(target)
			if fmt.Sprint(small.calls) != fmt.Sprint(large.calls) {
				t.Errorf("calls for one row of everything:\n%v\nfor four hundred:\n%v", small.calls, large.calls)
			}
			if n := small.total(); n > 14 {
				t.Errorf("%d calls for one page: that is a lot of calls for a page", n)
			}
		})
	}
}

// TestTodayIsWhatTheClockSays: the page of a month that is not named is the month
// today is in, in the timezone of the user, and today is what the clock says.
func TestTodayIsWhatTheClockSays(t *testing.T) {
	f := populated()
	// It is 00:30 of the first of December in Taipei, which is still the 30th of
	// November in UTC.
	f.now = time.Date(2026, time.December, 1, 0, 30, 0, 0, userZone)
	s := f.site(t)

	wantIn(t, s.page("/app/transactions"), "<strong>2026-12</strong>")
	if got := f.listedIn.From; got == nil || !got.Equal(date(2026, time.December, 1)) {
		t.Errorf("the list was asked from %v, want the first of December", got)
	}
	wantIn(t, s.page("/app/budgets"), "<strong>2026-12</strong>")
	if !f.budgetMonth.Equal(date(2026, time.December, 1)) {
		t.Errorf("the budgets were asked for %v", f.budgetMonth)
	}
	wantIn(t, s.page("/app/spending"), "<strong>2026-12</strong>")
	wantIn(t, s.page("/app/"), "資料截至今天（2026-12-01）")

	// A later day moves it.
	f.now = time.Date(2027, time.January, 3, 23, 59, 0, 0, userZone)
	wantIn(t, s.page("/app/transactions"), "<strong>2027-01</strong>")
}

func TestUUIDsAreNotMistakenForOthers(t *testing.T) {
	// The id of a transaction in the path is an id and nothing else.
	s := populated().site(t)
	for _, bad := range []string{"%00", "1", strings.Repeat("a", 500), uuid.Nil.String() + "x"} {
		res := s.get("/app/transactions/" + bad)
		if res.Code != http.StatusNotFound {
			t.Errorf("GET /app/transactions/%s = %d, want 404", bad, res.Code)
		}
	}
}
