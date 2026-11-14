package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/ledger"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

var day = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

func decPtr(s string) *decimal.Decimal {
	d := dec(s)
	return &d
}

// env is a ledger on fakes, with what the transactions it records refer to: two
// accounts, three instruments, four expense categories, an income category and
// the two system categories that migration 0005 seeds. Parties are added to the
// catalog by the tests that need them (refs.addParty). Its clock tells the
// afternoon of the day of the tests in the timezone of the application, which is
// ahead of UTC.
type env struct {
	t           *testing.T
	tx          *fakeTx
	txns        *fakeTransactions
	accounts    *fakeAccounts
	instruments *fakeInstruments
	categories  *fakeCategories
	refs        *fakeReferences
	projects    *fakeProjects
	clock       *fakeClock
	svc         *ledger.Service

	cash, bank                              model.Account
	twd, usd, btc                           model.Instrument
	food, household, fees, interest, salary model.Category
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		t:           t,
		tx:          &fakeTx{},
		txns:        newFakeTransactions(),
		accounts:    newFakeAccounts(),
		instruments: newFakeInstruments(),
		categories:  newFakeCategories(),
		refs:        newFakeReferences(),
		projects:    &fakeProjects{},
		clock:       &fakeClock{now: time.Date(2026, 11, 14, 15, 30, 0, 0, taipei)},
	}
	e.cash = e.accounts.add("cash")
	e.bank = e.accounts.add("bank")
	e.twd = e.instruments.add(model.Instrument{ID: model.BaseCurrencyID, Code: "TWD", Name: "TWD", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2})
	e.usd = e.instruments.add(model.Instrument{ID: uuid.Must(uuid.NewV7()), Code: "USD", Name: "USD", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2})
	e.btc = e.instruments.add(model.Instrument{ID: uuid.Must(uuid.NewV7()), Code: "BTC", Name: "BTC", Kind: model.InstrumentKindCrypto, Decimals: 8})
	e.food = e.categories.add(model.DirectionExpense, "food")
	e.household = e.categories.add(model.DirectionExpense, "household")
	e.fees = e.categories.add(model.DirectionExpense, "fees")
	e.interest = e.categories.add(model.DirectionExpense, "interest")
	e.salary = e.categories.add(model.DirectionIncome, "salary")
	for _, id := range []uuid.UUID{model.UnrecordedExpenseCategoryID, model.UnrecordedIncomeCategoryID} {
		direction := model.DirectionExpense
		if id == model.UnrecordedIncomeCategoryID {
			direction = model.DirectionIncome
		}
		e.categories.categories[id] = model.Category{ID: id, Direction: direction, Name: "未記錄"}
	}
	e.svc = ledger.New(e.tx, e.txns, e.accounts, e.instruments, e.categories, e.refs, e.projects, e.refs, e.refs, e.clock)
	return e
}

// names says what to call each id the tests write postings with.
func (e *env) names() map[uuid.UUID]string {
	names := map[uuid.UUID]string{
		e.cash.ID: "cash", e.bank.ID: "bank", e.twd.ID: "TWD", e.usd.ID: "USD", e.btc.ID: "BTC",
		e.food.ID: "food", e.household.ID: "household", e.fees.ID: "fees", e.interest.ID: "interest", e.salary.ID: "salary",
		model.UnrecordedExpenseCategoryID: "未記錄", model.UnrecordedIncomeCategoryID: "未記錄",
	}
	for id, name := range e.refs.partyNames {
		names[id] = name
	}
	return names
}

func signed(d decimal.Decimal) string {
	if d.IsPositive() {
		return "+" + d.String()
	}
	return d.String()
}

// lines writes the postings of a transaction the way the design table does.
func (e *env) lines(txn usecase.Transaction) []string {
	names := e.names()
	out := make([]string, 0, len(txn.Postings))
	for _, p := range txn.Postings {
		target := p.Book
		switch {
		case p.AccountID != nil:
			target += "(" + names[*p.AccountID] + ")"
		case p.CategoryID != nil:
			target += "(" + names[*p.CategoryID] + ")"
		case p.PartyID != nil:
			target += "(" + names[*p.PartyID] + ")"
		}
		line := fmt.Sprintf("%s %s %s", target, signed(p.Quantity), names[p.InstrumentID])
		if p.BaseAmount != nil {
			line += ", base " + signed(*p.BaseAmount)
		}
		if p.Discount != nil {
			line += ", discount " + p.Discount.String()
		}
		out = append(out, line)
	}
	return out
}

func wantLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("got\n  %q\nwant\n  %q", got, want)
	}
}

func (e *env) details(key string) usecase.RecordDetails {
	return usecase.RecordDetails{IdempotencyKey: key, OccurredOn: day}
}

// lunch is an expense of 120 TWD on food, paid from cash.
func (e *env) lunch(key string) usecase.RecordExpenseInput {
	return usecase.RecordExpenseInput{
		RecordDetails: e.details(key),
		AccountID:     &e.cash.ID,
		InstrumentID:  e.twd.ID,
		Lines:         []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("120")}},
	}
}

// recordLunch records a lunch, which is a draft, and fails the test when that is
// refused.
func (e *env) recordLunch(key string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), e.lunch(key))
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return out.Transaction
}

// confirmedLunch records a lunch and confirms it, the one way there is for a
// transaction to become confirmed, and fails the test when either is refused.
func (e *env) confirmedLunch(key string) usecase.Transaction {
	e.t.Helper()
	draft := e.recordLunch(key)
	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{draft.ID}})
	if err != nil {
		e.t.Fatalf("ConfirmTransactions(%q): %v", key, err)
	}
	return out.Transactions[0]
}

func TestRecordExpenseRecordsADraftInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("lunch-20261114"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	got := out.Transaction
	if out.Duplicate || got.Kind != "expense" || got.Status != "draft" || got.ConfirmedAt != nil || got.VoidedAt != nil {
		t.Fatalf("output = %+v, want a new draft expense", out)
	}
	if got.ID == uuid.Nil || got.CreatedAt.IsZero() || !got.OccurredOn.Equal(day) || got.IdempotencyKey != "lunch-20261114" {
		t.Errorf("transaction = %+v, want an id, the time the store assigned, the day and the key", got)
	}
	wantLines(t, e.lines(got), "account(cash) -120 TWD", "expense(food) +120 TWD, base +120")
	// The fakes refuse a write outside a database transaction, so that it took one
	// is already shown by the insert; and it took only one.
	if e.tx.calls != 1 || e.txns.inserts != 1 {
		t.Errorf("%d database transactions and %d inserts, want 1 and 1", e.tx.calls, e.txns.inserts)
	}
	if stored := e.txns.byID[got.ID]; stored.Status != model.StatusDraft || len(stored.Postings) != 2 {
		t.Errorf("stored = %+v, want the draft with its postings", stored)
	}
}

func TestEveryKindIsRecordedThroughItsConstructorInTheDomain(t *testing.T) {
	tests := []struct {
		name     string
		record   func(e *env) (usecase.RecordOutput, error)
		kind     string
		postings []string
	}{
		{
			name: "an income",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordIncome(context.Background(), usecase.RecordIncomeInput{
					RecordDetails: e.details("income-0000001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
					Lines: []usecase.CategoryLine{{CategoryID: e.salary.ID, Amount: dec("50000")}},
				})
			},
			kind:     "income",
			postings: []string{"account(bank) +50000 TWD", "income(salary) -50000 TWD, base -50000"},
		},
		{
			name: "a transfer",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
					RecordDetails: e.details("transfer-00001"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
					InstrumentID: e.twd.ID, Amount: dec("3000"),
				})
			},
			kind:     "transfer",
			postings: []string{"account(bank) -3000 TWD", "account(cash) +3000 TWD"},
		},
		{
			name: "an exchange",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordExchange(context.Background(), usecase.RecordExchangeInput{
					RecordDetails: e.details("exchange-00001"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
					FromInstrumentID: e.usd.ID, ToInstrumentID: e.twd.ID, FromAmount: dec("3000"), ToAmount: dec("93000"),
				})
			},
			kind: "exchange",
			postings: []string{
				"account(bank) -3000 USD", "conversion +3000 USD", "conversion -93000 TWD", "account(cash) +93000 TWD",
			},
		},
		{
			name: "an opening balance",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.SetOpeningBalance(context.Background(), usecase.SetOpeningBalanceInput{
					RecordDetails: e.details("opening-000001"), AccountID: e.cash.ID, InstrumentID: e.twd.ID, Amount: dec("1000"),
				})
			},
			kind:     "opening",
			postings: []string{"account(cash) +1000 TWD", "opening -1000 TWD"},
		},
		{
			name: "a trade that buys",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordTrade(context.Background(), usecase.RecordTradeInput{
					RecordDetails: e.details("trade-buy-0001"), Direction: "buy",
					AccountID: e.cash.ID, InstrumentID: e.btc.ID, Quantity: dec("0.1"),
					CashAccountID: e.bank.ID, CurrencyID: e.twd.ID, Amount: dec("300000"),
				})
			},
			kind: "trade",
			postings: []string{
				"account(bank) -300000 TWD", "conversion +300000 TWD", "conversion -0.1 BTC", "account(cash) +0.1 BTC",
			},
		},
		{
			name: "a trade that sells",
			record: func(e *env) (usecase.RecordOutput, error) {
				return e.svc.RecordTrade(context.Background(), usecase.RecordTradeInput{
					RecordDetails: e.details("trade-sell-001"), Direction: "sell",
					AccountID: e.cash.ID, InstrumentID: e.btc.ID, Quantity: dec("0.1"),
					CashAccountID: e.bank.ID, CurrencyID: e.twd.ID, Amount: dec("320000"),
					Fees: []usecase.Fee{{AccountID: e.bank.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")}},
				})
			},
			kind: "trade",
			postings: []string{
				"account(cash) -0.1 BTC", "conversion +0.1 BTC", "conversion -320000 TWD", "account(bank) +320000 TWD",
				"account(bank) -15 TWD", "expense(fees) +15 TWD, base +15",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)

			out, err := tt.record(e)
			if err != nil {
				t.Fatalf("record: %v", err)
			}

			if out.Duplicate || out.Transaction.Kind != tt.kind || out.Transaction.Status != "draft" {
				t.Fatalf("output = %+v, want a new draft %s", out, tt.kind)
			}
			wantLines(t, e.lines(out.Transaction), tt.postings...)
			if e.tx.calls != 1 || e.txns.inserts != 1 {
				t.Errorf("%d database transactions and %d inserts, want 1 and 1", e.tx.calls, e.txns.inserts)
			}
		})
	}
}

func TestRecordExpenseInAForeignCurrencyKeepsTheBaseAmountGiven(t *testing.T) {
	e := newEnv(t)
	in := e.lunch("usd-lunch-00001")
	in.AccountID = &e.bank.ID
	in.InstrumentID = e.usd.ID
	in.Lines = []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("100"), BaseAmount: decPtr("3200")}}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	wantLines(t, e.lines(out.Transaction), "account(bank) -100 USD", "expense(food) +100 USD, base +3200")

	in.IdempotencyKey = "usd-lunch-00002"
	in.Lines[0].BaseAmount = nil
	_, err = e.svc.RecordExpense(context.Background(), in)
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Fatalf("err = %v, want ErrBaseAmountRequired", err)
	}
	if e.txns.inserts != 1 {
		t.Errorf("%d transactions stored, want only the first", e.txns.inserts)
	}
}

func TestRecordExpenseSplitsAPaymentAmongCategories(t *testing.T) {
	e := newEnv(t)
	in := e.lunch("split-000000001")
	in.Lines = []usecase.CategoryLine{
		{CategoryID: e.food.ID, Amount: dec("300")},
		{CategoryID: e.household.ID, Amount: dec("150")},
		{CategoryID: e.food.ID, Amount: dec("50")},
	}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	wantLines(t, e.lines(out.Transaction),
		"account(cash) -500 TWD",
		"expense(food) +300 TWD, base +300",
		"expense(household) +150 TWD, base +150",
		"expense(food) +50 TWD, base +50",
	)
	if e.categories.gets != 2 {
		t.Errorf("%d categories loaded, want each of the two once", e.categories.gets)
	}
}

func TestRecordRefusesWhatTheRequestGetsWrongAndStoresNothing(t *testing.T) {
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordExpenseInput)
		want   error
	}{
		{"an account that does not exist", func(e *env, in *usecase.RecordExpenseInput) { in.AccountID = &unknown }, model.ErrNotFound},
		{"an instrument that does not exist", func(e *env, in *usecase.RecordExpenseInput) { in.InstrumentID = unknown }, model.ErrNotFound},
		{"a category that does not exist", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].CategoryID = unknown }, model.ErrNotFound},
		{"an archived account", func(e *env, in *usecase.RecordExpenseInput) { e.accounts.archive(e.cash.ID) }, model.ErrArchived},
		{"an archived instrument", func(e *env, in *usecase.RecordExpenseInput) { e.instruments.archive(e.twd.ID) }, model.ErrArchived},
		{"an archived category", func(e *env, in *usecase.RecordExpenseInput) { e.categories.archive(e.food.ID) }, model.ErrArchived},
		{"a category of income", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].CategoryID = e.salary.ID }, model.ErrWrongCategoryDirection},
		{"a zero amount", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].Amount = dec("-5") }, model.ErrAmountNotPositive},
		{"too many decimals", func(e *env, in *usecase.RecordExpenseInput) { in.Lines[0].Amount = dec("1.234") }, model.ErrTooManyDecimals},
		{"no lines", func(e *env, in *usecase.RecordExpenseInput) { in.Lines = nil }, model.ErrNoLines},
		{"no date", func(e *env, in *usecase.RecordExpenseInput) { in.OccurredOn = time.Time{} }, model.ErrDateRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := e.lunch("refused-0000001")
			tt.mutate(e, &in)

			out, err := e.svc.RecordExpense(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if out.Transaction.ID != uuid.Nil || e.txns.inserts != 0 || len(e.txns.byID) != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestRecordNamesWhatItCouldNotFind(t *testing.T) {
	e := newEnv(t)
	unknownAccount, unknownCategory := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	in := e.lunch("names-000000001")
	in.AccountID = &unknownAccount
	_, err := e.svc.RecordExpense(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "account "+unknownAccount.String()) {
		t.Errorf("err = %v, want it to name the account", err)
	}

	in = e.lunch("names-000000002")
	in.Lines = append(in.Lines, usecase.CategoryLine{CategoryID: unknownCategory, Amount: dec("1")})
	_, err = e.svc.RecordExpense(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "line 2") || !strings.Contains(err.Error(), unknownCategory.String()) {
		t.Errorf("err = %v, want it to name the line and the category", err)
	}
}

func TestRecordRefusesABadKeyBeforeItTouchesAnything(t *testing.T) {
	e := newEnv(t)

	for _, key := range []string{"", "short", "   1234567   "} {
		var keyErr *model.IdempotencyKeyLengthError
		_, err := e.svc.RecordExpense(context.Background(), e.lunch(key))
		if !errors.As(err, &keyErr) {
			t.Errorf("key %q: err = %v, want an *IdempotencyKeyLengthError", key, err)
		}
	}
	if e.tx.calls != 0 {
		t.Errorf("%d database transactions opened, want none", e.tx.calls)
	}
}

func TestRecordWithAKeyUsedBeforeReturnsTheTransactionRecordedThen(t *testing.T) {
	e := newEnv(t)
	first := e.recordLunch("lunch-20261114")

	// Whatever the second request says, it is the first one that stands. It is not
	// even checked: the account is archived by now, and that does not matter to a
	// request that asks for nothing new.
	e.accounts.archive(e.cash.ID)
	again := e.lunch("lunch-20261114")
	again.Lines[0].Amount = dec("999")
	out, err := e.svc.RecordExpense(context.Background(), again)
	if err != nil {
		t.Fatalf("RecordExpense with the key again: %v", err)
	}

	if !out.Duplicate || out.Transaction.ID != first.ID || out.Transaction.Status != "draft" {
		t.Fatalf("output = %+v, want the first transaction, still a draft, marked as a duplicate", out)
	}
	wantLines(t, e.lines(out.Transaction), "account(cash) -120 TWD", "expense(food) +120 TWD, base +120")
	if e.txns.inserts != 1 || len(e.txns.byID) != 1 || len(e.txns.statusSet) != 0 {
		t.Errorf("%d inserts, %d stored, %d status changes, want nothing written the second time", e.txns.inserts, len(e.txns.byID), len(e.txns.statusSet))
	}
}

func TestRecordTreatsTheKeyAsOneKeyForEveryKind(t *testing.T) {
	e := newEnv(t)
	expense := e.recordLunch("shared-key-0001")

	out, err := e.svc.SetOpeningBalance(context.Background(), usecase.SetOpeningBalanceInput{
		RecordDetails: e.details("shared-key-0001"), AccountID: e.cash.ID, InstrumentID: e.twd.ID, Amount: dec("1000"),
	})
	if err != nil {
		t.Fatalf("SetOpeningBalance: %v", err)
	}
	if !out.Duplicate || out.Transaction.ID != expense.ID {
		t.Fatalf("output = %+v, want the expense recorded under the key", out)
	}
}

func TestRecordReturnsWhatAConcurrentCallRecordedUnderTheKey(t *testing.T) {
	e := newEnv(t)
	winner := model.Transaction{
		ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft, OccurredOn: day,
		IdempotencyKey: "raced-key-00001", CreatedAt: epoch,
	}
	// The other call commits between the look-up of the key and the insert.
	e.txns.concurrent = &winner

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("raced-key-00001"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if !out.Duplicate || out.Transaction.ID != winner.ID {
		t.Fatalf("output = %+v, want the transaction of the call that got there first", out)
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored by this call, want none", e.txns.inserts)
	}
}

func TestRecordReportsAStoreFailure(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("connection reset")
	e.txns.insertErr = boom

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("lunch-20261114"))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure of the store", err)
	}
	if out.Duplicate || out.Transaction.ID != uuid.Nil {
		t.Errorf("output = %+v, want nothing", out)
	}
}

func TestRecordTakesTheProjectOfTheDayAndItsDefaultArea(t *testing.T) {
	e := newEnv(t)
	project, area := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	e.projects.out = catalogusecase.ResolveProjectDefaultsOutput{
		Project:     &catalogusecase.Project{ID: project, Name: "東京旅行"},
		DefaultArea: &catalogusecase.Area{ID: area, Name: "玩樂"},
	}

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("tokyo-lunch-001"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	got := out.Transaction
	if got.ProjectID == nil || *got.ProjectID != project || got.AreaID == nil || *got.AreaID != area {
		t.Fatalf("project %v, area %v, want %s and %s", got.ProjectID, got.AreaID, project, area)
	}
	if len(e.projects.days) != 1 || !e.projects.days[0].Equal(day) {
		t.Errorf("asked the catalog about %v, want the day of the transaction, %v", e.projects.days, day)
	}
}

func TestRecordKeepsAnAreaTheCallerGaveOverTheDefaultOfTheProject(t *testing.T) {
	e := newEnv(t)
	project, defaultArea := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	e.projects.out = catalogusecase.ResolveProjectDefaultsOutput{
		Project:     &catalogusecase.Project{ID: project},
		DefaultArea: &catalogusecase.Area{ID: defaultArea},
	}
	chosen := e.refs.addArea(false)
	in := e.lunch("tokyo-lunch-001")
	in.AreaID = &chosen

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	got := out.Transaction
	if got.AreaID == nil || *got.AreaID != chosen {
		t.Fatalf("area = %v, want the one the caller gave, %s, and not the default %s", got.AreaID, chosen, defaultArea)
	}
	if got.ProjectID == nil || *got.ProjectID != project {
		t.Errorf("project = %v, want the project of the day, %s, which the caller left open", got.ProjectID, project)
	}
	if len(e.refs.calls) != 1 || !slices.Equal(e.refs.calls[0].AreaIDs, []uuid.UUID{chosen}) {
		t.Errorf("catalog was asked to verify %+v, want the area the caller gave", e.refs.calls)
	}
}

func TestRecordLeavesTheAreaOpenWhenTheProjectHasNoDefaultArea(t *testing.T) {
	e := newEnv(t)
	project := uuid.Must(uuid.NewV7())
	e.projects.out = catalogusecase.ResolveProjectDefaultsOutput{Project: &catalogusecase.Project{ID: project}}

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("tokyo-lunch-001"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if got := out.Transaction; got.ProjectID == nil || *got.ProjectID != project || got.AreaID != nil {
		t.Fatalf("project %v, area %v, want the project and no area", got.ProjectID, got.AreaID)
	}
}

func TestRecordLeavesProjectAndAreaOpenWhenNoProjectIsActive(t *testing.T) {
	e := newEnv(t)

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("lunch-20261114"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if got := out.Transaction; got.ProjectID != nil || got.AreaID != nil {
		t.Fatalf("project %v, area %v, want neither", got.ProjectID, got.AreaID)
	}
}

func TestRecordDoesNotFailWhenSeveralProjectsAreActiveOnTheDay(t *testing.T) {
	e := newEnv(t)
	e.projects.err = fmt.Errorf("find the projects: %w", &catalogusecase.AmbiguousProjectsError{
		Date: day, Projects: []catalogusecase.Project{{Name: "東京旅行"}, {Name: "出差"}},
	})
	chosen := e.refs.addArea(false)

	out, err := e.svc.RecordExpense(context.Background(), e.lunch("lunch-20261114"))
	if err != nil {
		t.Fatalf("RecordExpense: %v, want the write to go on without a project", err)
	}
	if got := out.Transaction; got.ProjectID != nil || got.AreaID != nil {
		t.Fatalf("project %v, area %v, want neither set", got.ProjectID, got.AreaID)
	}

	// An area the caller gave is theirs, whatever happens to the project.
	in := e.lunch("lunch-20261115")
	in.AreaID = &chosen
	out, err = e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense with an area: %v", err)
	}
	if got := out.Transaction; got.ProjectID != nil || got.AreaID == nil || *got.AreaID != chosen {
		t.Fatalf("project %v, area %v, want only the area the caller gave", got.ProjectID, got.AreaID)
	}
}

func TestRecordTakesTheDefaultAreaOfTheProjectTheCallerNames(t *testing.T) {
	e := newEnv(t)
	area := e.refs.addArea(false)
	named := e.refs.addProject(false)
	e.refs.setDefaultArea(named, area)
	in := e.lunch("lunch-20261114")
	in.ProjectID = &named

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if got := out.Transaction; got.ProjectID == nil || *got.ProjectID != named || got.AreaID == nil || *got.AreaID != area {
		t.Fatalf("project %v, area %v, want the project the caller named, %s, and its default area, %s", got.ProjectID, got.AreaID, named, area)
	}
	if len(e.projects.days) != 0 {
		t.Errorf("asked the catalog about the days %v, want it not asked: the caller named the project", e.projects.days)
	}
	if len(e.refs.calls) != 1 || !slices.Equal(e.refs.calls[0].ProjectIDs, []uuid.UUID{named}) || len(e.refs.calls[0].AreaIDs) != 0 {
		t.Errorf("catalog was asked to verify %+v, want only the project the caller named", e.refs.calls)
	}
}

func TestRecordKeepsTheAreaTheCallerGaveOverTheDefaultOfTheProjectTheyName(t *testing.T) {
	e := newEnv(t)
	defaultArea, chosen := e.refs.addArea(false), e.refs.addArea(false)
	named := e.refs.addProject(false)
	e.refs.setDefaultArea(named, defaultArea)
	in := e.lunch("lunch-20261114")
	in.ProjectID = &named
	in.AreaID = &chosen

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if got := out.Transaction; got.ProjectID == nil || *got.ProjectID != named || got.AreaID == nil || *got.AreaID != chosen {
		t.Fatalf("project %v, area %v, want the project %s and the area the caller gave, %s, and not the default %s",
			got.ProjectID, got.AreaID, named, chosen, defaultArea)
	}
}

func TestRecordLeavesTheAreaOpenWhenTheProjectTheCallerNamesHasNoDefaultArea(t *testing.T) {
	e := newEnv(t)
	named := e.refs.addProject(false)
	// The project of the day has a default area, which is not the named project's.
	e.projects.out = catalogusecase.ResolveProjectDefaultsOutput{
		Project:     &catalogusecase.Project{ID: uuid.Must(uuid.NewV7())},
		DefaultArea: &catalogusecase.Area{ID: uuid.Must(uuid.NewV7())},
	}
	in := e.lunch("lunch-20261114")
	in.ProjectID = &named

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if got := out.Transaction; got.ProjectID == nil || *got.ProjectID != named || got.AreaID != nil {
		t.Fatalf("project %v, area %v, want the project the caller named and no area", got.ProjectID, got.AreaID)
	}
	if len(e.projects.days) != 0 {
		t.Errorf("asked the catalog about the days %v, want it not asked", e.projects.days)
	}
}

func TestRecordLeavesTheAreaOpenWhenTheDefaultAreaOfTheNamedProjectIsArchived(t *testing.T) {
	e := newEnv(t)
	archived := e.refs.addArea(true)
	named := e.refs.addProject(false)
	e.refs.setDefaultArea(named, archived)
	in := e.lunch("lunch-20261114")
	in.ProjectID = &named

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v, want the write to go on without the area", err)
	}

	if got := out.Transaction; got.ProjectID == nil || *got.ProjectID != named || got.AreaID != nil {
		t.Fatalf("project %v, area %v, want the project and no area: a transaction cannot go to an archived area", got.ProjectID, got.AreaID)
	}
}

func TestRecordFailsWhenTheCatalogCannotGiveTheDefaultAreaOfTheNamedProject(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("catalog is down")
	e.refs.resolveAreaErr = boom
	named := e.refs.addProject(false)
	in := e.lunch("lunch-20261114")
	in.ProjectID = &named

	_, err := e.svc.RecordExpense(context.Background(), in)

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure of the catalog", err)
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored, want none", e.txns.inserts)
	}
}

func TestRecordFailsWhenTheCatalogCannotFindTheProjectOfTheDay(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("catalog is down")
	e.projects.err = boom

	_, err := e.svc.RecordExpense(context.Background(), e.lunch("lunch-20261114"))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure of the catalog", err)
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored, want none", e.txns.inserts)
	}
}

func TestRecordRefusesProjectsAreasAndTagsThatCannotBeUsed(t *testing.T) {
	e := newEnv(t)
	unknownProject := uuid.Must(uuid.NewV7())
	archivedArea := e.refs.addArea(true)
	unknownTag, goodTag := uuid.Must(uuid.NewV7()), e.refs.addTag(false)
	in := e.lunch("lunch-20261114")
	in.ProjectID = &unknownProject
	in.AreaID = &archivedArea
	in.TagIDs = []uuid.UUID{goodTag, unknownTag}

	_, err := e.svc.RecordExpense(context.Background(), in)

	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) {
		t.Fatalf("err = %v, want the catalog's *ReferenceError", err)
	}
	want := []catalogusecase.ReferenceProblem{
		{Kind: catalogusecase.ReferenceProject, ID: unknownProject},
		{Kind: catalogusecase.ReferenceArea, ID: archivedArea, Archived: true},
		{Kind: catalogusecase.ReferenceTag, ID: unknownTag},
	}
	if !slices.Equal(refErr.Problems, want) {
		t.Errorf("problems = %+v, want every one of them reported: %+v", refErr.Problems, want)
	}
	if e.txns.inserts != 0 || len(e.projects.days) != 0 {
		t.Errorf("%d transactions stored, the catalog asked about %d days, want nothing more done", e.txns.inserts, len(e.projects.days))
	}
}

func TestRecordStoresTheTagsOnceAndInOrder(t *testing.T) {
	e := newEnv(t)
	a, b := e.refs.addTag(false), e.refs.addTag(false)
	if string(a[:]) > string(b[:]) {
		a, b = b, a
	}
	in := e.lunch("lunch-20261114")
	in.TagIDs = []uuid.UUID{b, a, b}

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if !slices.Equal(out.Transaction.TagIDs, []uuid.UUID{a, b}) {
		t.Fatalf("tags = %v, want %v", out.Transaction.TagIDs, []uuid.UUID{a, b})
	}
}

func TestRecordPassesWhatTheCallerKnowsAboutTheTransactionOn(t *testing.T) {
	e := newEnv(t)
	at := time.Date(2026, 11, 14, 12, 30, 0, 0, time.UTC)
	in := e.lunch("lunch-20261114")
	in.Description = " 午餐 "
	in.Merchant = " 便當店 "
	in.OccurredAt = &at

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	got := out.Transaction
	if got.Description != "午餐" || got.Merchant != "便當店" || got.OccurredAt == nil || !got.OccurredAt.Equal(at) {
		t.Fatalf("transaction = %+v, want the trimmed texts and the moment", got)
	}
}

func TestRecordExchangeRefusesAnInstrumentThatIsNotACurrency(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.RecordExchange(context.Background(), usecase.RecordExchangeInput{
		RecordDetails: e.details("exchange-00001"), FromAccountID: e.bank.ID, ToAccountID: e.bank.ID,
		FromInstrumentID: e.twd.ID, ToInstrumentID: e.btc.ID, FromAmount: dec("3000"), ToAmount: dec("0.001"),
	})

	if !errors.Is(err, model.ErrNotCurrency) {
		t.Fatalf("err = %v, want ErrNotCurrency: buying BTC is a trade, not an exchange", err)
	}
	if e.txns.inserts != 0 {
		t.Errorf("%d transactions stored, want none", e.txns.inserts)
	}
}

func TestRecordTransferMovesAnyInstrumentBetweenAccounts(t *testing.T) {
	e := newEnv(t)

	out, err := e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: e.details("transfer-00001"), FromAccountID: e.bank.ID, ToAccountID: e.cash.ID,
		InstrumentID: e.btc.ID, Amount: dec("0.12345678"),
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}

	wantLines(t, e.lines(out.Transaction), "account(bank) -0.12345678 BTC", "account(cash) +0.12345678 BTC")

	_, err = e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: e.details("transfer-00002"), FromAccountID: e.bank.ID, ToAccountID: e.bank.ID,
		InstrumentID: e.btc.ID, Amount: dec("1"),
	})
	if !errors.Is(err, model.ErrSameAccount) {
		t.Errorf("a transfer to the same account: err = %v, want ErrSameAccount", err)
	}
}
