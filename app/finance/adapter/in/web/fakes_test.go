package web

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/webtest"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// userZone is where the user of the tests lives, as an offset, so that no test needs
// a timezone database.
var userZone = time.FixedZone("UTC+8", 8*60*60)

// fake is every use case the pages call, answering from what a test puts in it, and
// counting the calls. It is also the clock.
type fake struct {
	now   time.Time
	calls map[string]int
	// failures makes the use case of that name return the error.
	failures map[string]error

	instruments  []usecase.Instrument
	institutions []usecase.Institution
	accounts     []usecase.Account
	categories   []usecase.Category
	areas        []catalogusecase.Area
	projects     []catalogusecase.Project
	parties      []catalogusecase.Party
	tags         []catalogusecase.Tag

	balances []usecase.Balance
	dealings []usecase.Dealing
	plans    []usecase.InstalmentPlanSummary
	drafts   usecase.GetDraftSummaryOutput

	// listed are the transactions ListTransactions returns, and listedIn what it was
	// last asked.
	listed   []usecase.Transaction
	hasMore  bool
	listedIn usecase.ListTransactionsInput
	byID     map[uuid.UUID]usecase.Transaction
	plan     usecase.InstalmentPlan

	statements  []usecase.CardStatementSummary
	statementIn []usecase.ListCardStatementsInput
	statement   usecase.CardStatement
	cardIn      usecase.GetCardStatementInput

	areaBudgets    usecase.GetAreaBudgetsOutput
	projectBudgets usecase.GetProjectBudgetsOutput
	budgetMonth    time.Time

	spent   usecase.GetSpendingOutput
	trend   usecase.GetSpendingTrendOutput
	trendIn usecase.GetSpendingTrendInput
}

func (f *fake) Now() time.Time { return f.now }

// call counts a call, and returns the error the test made it fail with.
func (f *fake) call(name string) error {
	f.calls[name]++
	return f.failures[name]
}

// total is how many calls of use cases there were.
func (f *fake) total() int {
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func (f *fake) GetBalances(context.Context, usecase.GetBalancesInput) (usecase.GetBalancesOutput, error) {
	return usecase.GetBalancesOutput{Balances: f.balances}, f.call("GetBalances")
}

func (f *fake) GetDealings(context.Context, usecase.GetDealingsInput) (usecase.GetDealingsOutput, error) {
	return usecase.GetDealingsOutput{Dealings: f.dealings}, f.call("GetDealings")
}

func (f *fake) ListInstalmentPlans(context.Context, usecase.ListInstalmentPlansInput) (usecase.ListInstalmentPlansOutput, error) {
	return usecase.ListInstalmentPlansOutput{Plans: f.plans}, f.call("ListInstalmentPlans")
}

func (f *fake) GetDraftSummary(context.Context, usecase.GetDraftSummaryInput) (usecase.GetDraftSummaryOutput, error) {
	return f.drafts, f.call("GetDraftSummary")
}

func (f *fake) ListTransactions(_ context.Context, in usecase.ListTransactionsInput) (usecase.ListTransactionsOutput, error) {
	f.listedIn = in
	return usecase.ListTransactionsOutput{Transactions: f.listed, HasMore: f.hasMore}, f.call("ListTransactions")
}

func (f *fake) GetTransaction(_ context.Context, id uuid.UUID) (usecase.Transaction, error) {
	if err := f.call("GetTransaction"); err != nil {
		return usecase.Transaction{}, err
	}
	t, ok := f.byID[id]
	if !ok {
		return usecase.Transaction{}, fmt.Errorf("transaction %s: %w", id, model.ErrNotFound)
	}
	return t, nil
}

func (f *fake) GetInstalmentPlan(context.Context, uuid.UUID) (usecase.InstalmentPlan, error) {
	return f.plan, f.call("GetInstalmentPlan")
}

func (f *fake) ListCardStatements(_ context.Context, in usecase.ListCardStatementsInput) (usecase.ListCardStatementsOutput, error) {
	f.statementIn = append(f.statementIn, in)
	return usecase.ListCardStatementsOutput{Statements: f.statements}, f.call("ListCardStatements")
}

func (f *fake) GetCardStatement(_ context.Context, in usecase.GetCardStatementInput) (usecase.CardStatement, error) {
	f.cardIn = in
	if err := f.call("GetCardStatement"); err != nil {
		return usecase.CardStatement{}, err
	}
	return f.statement, nil
}

func (f *fake) GetAreaBudgets(_ context.Context, in usecase.GetAreaBudgetsInput) (usecase.GetAreaBudgetsOutput, error) {
	f.budgetMonth = in.Month
	return f.areaBudgets, f.call("GetAreaBudgets")
}

func (f *fake) GetProjectBudgets(context.Context, usecase.GetProjectBudgetsInput) (usecase.GetProjectBudgetsOutput, error) {
	return f.projectBudgets, f.call("GetProjectBudgets")
}

func (f *fake) GetSpending(_ context.Context, in usecase.GetSpendingInput) (usecase.GetSpendingOutput, error) {
	out := f.spent
	out.Month = model.MonthOf(in.Month)
	return out, f.call("GetSpending")
}

func (f *fake) GetSpendingTrend(_ context.Context, in usecase.GetSpendingTrendInput) (usecase.GetSpendingTrendOutput, error) {
	f.trendIn = in
	if len(f.trend.Months) > 0 {
		return f.trend, f.call("GetSpendingTrend")
	}
	// A month for each of the range, with nothing spent.
	var months []usecase.MonthSpending
	for m := model.MonthOf(in.From); !m.After(model.MonthOf(in.To)); m = m.AddDate(0, 1, 0) {
		months = append(months, usecase.MonthSpending{Month: m})
	}
	return usecase.GetSpendingTrendOutput{Months: months}, f.call("GetSpendingTrend")
}

func (f *fake) ListAccounts(context.Context, usecase.ListAccountsInput) (usecase.ListAccountsOutput, error) {
	return usecase.ListAccountsOutput{Accounts: f.accounts}, f.call("ListAccounts")
}

func (f *fake) ListInstitutions(context.Context, usecase.ListInstitutionsInput) (usecase.ListInstitutionsOutput, error) {
	return usecase.ListInstitutionsOutput{Institutions: f.institutions}, f.call("ListInstitutions")
}

func (f *fake) ListInstruments(context.Context, usecase.ListInstrumentsInput) (usecase.ListInstrumentsOutput, error) {
	return usecase.ListInstrumentsOutput{Instruments: f.instruments}, f.call("ListInstruments")
}

func (f *fake) ListCategories(context.Context, usecase.ListCategoriesInput) (usecase.ListCategoriesOutput, error) {
	return usecase.ListCategoriesOutput{Categories: f.categories}, f.call("ListCategories")
}

func (f *fake) ListAreas(context.Context, catalogusecase.ListAreasInput) (catalogusecase.ListAreasOutput, error) {
	return catalogusecase.ListAreasOutput{Areas: f.areas}, f.call("ListAreas")
}

func (f *fake) ListProjects(context.Context, catalogusecase.ListProjectsInput) (catalogusecase.ListProjectsOutput, error) {
	return catalogusecase.ListProjectsOutput{Projects: f.projects}, f.call("ListProjects")
}

func (f *fake) ListParties(context.Context, catalogusecase.ListPartiesInput) (catalogusecase.ListPartiesOutput, error) {
	return catalogusecase.ListPartiesOutput{Parties: f.parties}, f.call("ListParties")
}

func (f *fake) ListTags(context.Context, catalogusecase.ListTagsInput) (catalogusecase.ListTagsOutput, error) {
	return catalogusecase.ListTagsOutput{Tags: f.tags}, f.call("ListTags")
}

// ports gives the pages the fake for every use case and for the clock.
func (f *fake) ports() Ports {
	return Ports{
		Clock:               f,
		GetBalances:         f,
		GetDealings:         f,
		ListTransactions:    f,
		GetTransaction:      f,
		GetInstalmentPlan:   f,
		ListInstalmentPlans: f,
		GetCardStatement:    f,
		ListCardStatements:  f,
		GetAreaBudgets:      f,
		GetProjectBudgets:   f,
		GetSpending:         f,
		GetSpendingTrend:    f,
		GetDraftSummary:     f,
		ListAccounts:        f,
		ListInstitutions:    f,
		ListInstruments:     f,
		ListCategories:      f,
		ListAreas:           f,
		ListProjects:        f,
		ListParties:         f,
		ListTags:            f,
	}
}

// The ids of the world of the tests.
var (
	twd  = model.BaseCurrencyID
	usd  = uuid.MustParse("00000000-0000-7000-8000-0000000000a1")
	jpy  = uuid.MustParse("00000000-0000-7000-8000-0000000000a2")
	btc  = uuid.MustParse("00000000-0000-7000-8000-0000000000a3")
	bank = uuid.MustParse("00000000-0000-7000-8000-0000000000b1")

	cash      = uuid.MustParse("00000000-0000-7000-8000-0000000000c1")
	checking  = uuid.MustParse("00000000-0000-7000-8000-0000000000c2")
	card      = uuid.MustParse("00000000-0000-7000-8000-0000000000c3")
	spareCard = uuid.MustParse("00000000-0000-7000-8000-0000000000c4")
	oldSaving = uuid.MustParse("00000000-0000-7000-8000-0000000000c5")
	emptyAcct = uuid.MustParse("00000000-0000-7000-8000-0000000000c6")

	food    = uuid.MustParse("00000000-0000-7000-8000-0000000000d1")
	lunch   = uuid.MustParse("00000000-0000-7000-8000-0000000000d2")
	rent    = uuid.MustParse("00000000-0000-7000-8000-0000000000d3")
	fees    = uuid.MustParse("00000000-0000-7000-8000-0000000000d4")
	interst = uuid.MustParse("00000000-0000-7000-8000-0000000000d5")
	salary  = uuid.MustParse("00000000-0000-7000-8000-0000000000d6")

	home = uuid.MustParse("00000000-0000-7000-8000-0000000000e1")
	work = uuid.MustParse("00000000-0000-7000-8000-0000000000e2")
	trip = uuid.MustParse("00000000-0000-7000-8000-0000000000e3")

	alice = uuid.MustParse("00000000-0000-7000-8000-0000000000f1")
	bob   = uuid.MustParse("00000000-0000-7000-8000-0000000000f2")
	tagA  = uuid.MustParse("00000000-0000-7000-8000-0000000000f3")
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func date(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// newFake is the world the pages are tested on, on the 21st of November 2026: four
// instruments, accounts of several kinds (one card with a closing day, one without,
// one archived), categories with a subcategory, two areas, a project and two
// parties. It has no transactions, balances or budgets: each test adds its own.
func newFake() *fake {
	return &fake{
		now:      time.Date(2026, time.November, 21, 10, 0, 0, 0, userZone),
		calls:    make(map[string]int),
		failures: make(map[string]error),
		byID:     make(map[uuid.UUID]usecase.Transaction),
		instruments: []usecase.Instrument{
			{ID: twd, Code: "TWD", Name: "新台幣", Kind: "fiat", IsCurrency: true, Decimals: 2},
			{ID: usd, Code: "USD", Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
			{ID: jpy, Code: "JPY", Name: "日圓", Kind: "fiat", IsCurrency: true, Decimals: 0},
			{ID: btc, Code: "BTC", Name: "比特幣", Kind: "crypto", Decimals: 8},
		},
		institutions: []usecase.Institution{{ID: bank, Name: "好銀行"}},
		accounts: []usecase.Account{
			{ID: card, Name: "信用卡", Kind: "card", InstitutionID: &bank, ClosingDay: new(5), PaymentDueDay: new(20)},
			{ID: spareCard, Name: "備用卡", Kind: "card", InstitutionID: &bank},
			{ID: emptyAcct, Name: "空帳戶", Kind: "bank", InstitutionID: &bank},
			{ID: cash, Name: "現金", Kind: "cash"},
			{ID: checking, Name: "活期戶頭", Kind: "bank", InstitutionID: &bank},
			{ID: oldSaving, Name: "舊存摺", Kind: "bank", ArchivedAt: new(date(2026, 1, 1))},
		},
		categories: []usecase.Category{
			{ID: food, Direction: "expense", Name: "飲食"},
			{ID: lunch, Direction: "expense", ParentID: &food, Name: "午餐"},
			{ID: rent, Direction: "expense", Name: "房租"},
			{ID: fees, Direction: "expense", Name: "手續費"},
			{ID: interst, Direction: "expense", Name: "利息"},
			{ID: salary, Direction: "income", Name: "薪水"},
		},
		areas:    []catalogusecase.Area{{ID: home, Name: "家庭"}, {ID: work, Name: "工作"}},
		projects: []catalogusecase.Project{{ID: trip, Name: "日本旅行"}},
		parties:  []catalogusecase.Party{{ID: alice, Name: "Alice"}, {ID: bob, Name: "Bob"}},
		tags:     []catalogusecase.Tag{{ID: tagA, Name: "報帳"}},
	}
}

// site makes the pages of the fake, behind a stand-in for Access, as serve does.
func (f *fake) site(t *testing.T) *site {
	t.Helper()
	access := webtest.NewAccess(t)
	pages, err := webserver.NewRegistry(slog.New(slog.DiscardHandler), []webserver.Registrar{NewRegistrar(f.ports())}).
		Pages(userFromAccess, access.Verifier().Middleware)
	if err != nil {
		t.Fatalf("build the pages: %v", err)
	}
	return &site{t: t, pages: pages, access: access}
}

// userFromAccess is how cmd names the user: the identity the verifier found.
func userFromAccess(r *http.Request) (webserver.User, bool) {
	id, ok := cfaccess.IdentityFrom(r.Context())
	if !ok {
		return webserver.User{}, false
	}
	return webserver.User{Name: id.Actor()}, true
}

// site is the pages of a fake, with the means to ask them for a page.
type site struct {
	t      *testing.T
	pages  *webserver.Pages
	access *webtest.Access
}

// response is what a request got.
type response struct {
	Code   int
	Header http.Header
	Body   string
}

// get asks for a page as the user max@example.com, who is signed in.
func (s *site) get(target string) response {
	s.t.Helper()
	return s.do(s.access.Request(s.t, http.MethodGet, target, "max@example.com"))
}

func (s *site) do(req *http.Request) response {
	s.t.Helper()
	rec := httptest.NewRecorder()
	s.pages.ServeHTTP(rec, req)
	return response{Code: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
}

// page is the body of a page that is there, which is read against the glossary: no
// page of any test uses a word that CONTEXT.md says to avoid.
func (s *site) page(target string) string {
	s.t.Helper()
	res := s.get(target)
	if res.Code != http.StatusOK {
		s.t.Fatalf("GET %s = %d, want 200:\n%s", target, res.Code, res.Body)
	}
	wantNoAvoidedWords(s.t, target, res.Body)
	return res.Body
}

// wantIn fails for each of the texts that the page does not have.
func wantIn(t *testing.T, page string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if !strings.Contains(page, text) {
			t.Errorf("the page lacks %q:\n%s", text, page)
		}
	}
}

// wantNotIn fails for each of the texts that the page has.
func wantNotIn(t *testing.T, page string, texts ...string) {
	t.Helper()
	for _, text := range texts {
		if strings.Contains(page, text) {
			t.Errorf("the page has %q:\n%s", text, page)
		}
	}
}
