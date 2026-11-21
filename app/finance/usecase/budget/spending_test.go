package budget_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// spending reads what was spent in the month, and fails the test when it cannot.
func (e *env) spending(month time.Time) usecase.GetSpendingOutput {
	e.t.Helper()
	out, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{Month: month})
	if err != nil {
		e.t.Fatalf("GetSpending(%s): %v", month.Format("2006-01"), err)
	}
	return out
}

// spendingLines writes the categories of what was spent one to a line, in the order
// they come: "food 300", and "lunch 200 (under food)" for a subcategory.
func (e *env) spendingLines(out usecase.GetSpendingOutput) []string {
	names := e.categories.categories
	lines := make([]string, len(out.Categories))
	for i, c := range out.Categories {
		lines[i] = fmt.Sprintf("%s %s", names[c.CategoryID].Name, c.Amount)
		if c.ParentID != nil {
			lines[i] += " (under " + names[*c.ParentID].Name + ")"
		}
	}
	return lines
}

// trend reads what was spent in each month from the first to the last, and fails
// the test when it cannot.
func (e *env) trend(from, to time.Time) usecase.GetSpendingTrendOutput {
	e.t.Helper()
	out, err := e.svc.GetSpendingTrend(context.Background(), usecase.GetSpendingTrendInput{From: from, To: to})
	if err != nil {
		e.t.Fatalf("GetSpendingTrend(%s, %s): %v", from.Format("2006-01"), to.Format("2006-01"), err)
	}
	return out
}

// trendLines writes the months of a trend one to a line, "2026-11 150.5".
func trendLines(out usecase.GetSpendingTrendOutput) []string {
	lines := make([]string, len(out.Months))
	for i, m := range out.Months {
		lines[i] = fmt.Sprintf("%s %s", m.Month.Format("2006-01"), m.Total)
	}
	return lines
}

func wantLines(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// wantRanges fails the test unless the repo was asked for the ranges of days given,
// each as "2026-11-01..2026-11-14", and for no others.
func wantRanges(t *testing.T, asked [][2]time.Time, want ...string) {
	t.Helper()
	got := make([]string, len(asked))
	for i, r := range asked {
		got[i] = r[0].Format(time.DateOnly) + ".." + r[1].Format(time.DateOnly)
	}
	if !slices.Equal(got, want) {
		t.Errorf("the repo was asked for %q, want %q", got, want)
	}
}

// ---- GetSpending ----

func TestGetSpendingNeedsAMonthAndNamesItByTheFirstDayOfTheMonthTheRequestFallsIn(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{})
	if !errors.Is(err, model.ErrMonthRequired) {
		t.Errorf("err = %v, want ErrMonthRequired", err)
	}
	if len(e.budgets.spendingAsked) != 0 {
		t.Errorf("the repo was asked %v, want it not asked when there is no month", e.budgets.spendingAsked)
	}

	for _, month := range []time.Time{day(2026, 11, 1), day(2026, 11, 30), time.Date(2026, 11, 30, 23, 0, 0, 0, taipei)} {
		out := e.spending(month)
		if !out.Month.Equal(day(2026, 11, 1)) || out.Month.Location() != time.UTC {
			t.Errorf("month = %s for %s, want 2026-11-01 as a date at midnight UTC", out.Month, month)
		}
	}
	// 00:30 on the 1st of December in Taipei is the 30th of November in UTC: the month is
	// the one the time falls on as its own location reads it.
	if out := e.spending(time.Date(2026, 12, 1, 0, 30, 0, 0, taipei)); !out.Month.Equal(day(2026, 12, 1)) {
		t.Errorf("month = %s, want December: the month a Taipei time falls on", out.Month)
	}
}

func TestSpendingOfAMonthTakesItsFirstAndLastDayAndNeitherNeighbour(t *testing.T) {
	e := newEnv(t)
	e.clock.setDay(2026, time.December, 15) // the whole of November is past
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	// An amount for each day, so that what is counted is what the total adds up.
	for _, expense := range []struct {
		on     time.Time
		inArea bool
		amount string
	}{
		{day(2026, 10, 31), true, "1"},      // the day before the month
		{day(2026, 11, 1), true, "10"},      // its first day
		{day(2026, 11, 30), true, "100"},    // its last day
		{day(2026, 12, 1), true, "1000"},    // the day after the month
		{day(2026, 11, 15), false, "10000"}, // the middle of it, in no area
	} {
		var area *uuid.UUID
		if expense.inArea {
			area = &work
		}
		e.spend(expense.on, area, nil, food, expense.amount)
	}

	out := e.spending(day(2026, 11, 9))

	wantMoney(t, "total", out.Total, "10110")
	wantLines(t, "categories", e.spendingLines(out), "飲食 10110")
	wantRanges(t, e.budgets.spendingAsked, "2026-11-01..2026-11-30")
}

func TestTheCurrentMonthStopsAtTodayInTheTimezoneOfTheClockAndWhatIsDatedLaterCountsOnceItsDayHasCome(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	for _, expense := range []struct {
		on     time.Time
		amount string
	}{
		{day(2026, 10, 31), "1"},
		{day(2026, 11, 1), "10"},
		{day(2026, 11, 14), "100"},   // today, on the 14th
		{day(2026, 11, 15), "1000"},  // tomorrow
		{day(2026, 11, 30), "10000"}, // the last day of the month
		{day(2026, 12, 1), "100000"},
	} {
		e.spend(expense.on, nil, nil, food, expense.amount)
	}

	tests := []struct {
		name       string
		now        time.Time
		want       string
		wantRanges []string // the ranges the repo is asked for; none for a month that has not begun
	}{
		{"before the month", time.Date(2026, 10, 31, 12, 0, 0, 0, taipei), "0", nil},
		{"the first day of the month", time.Date(2026, 11, 1, 0, 0, 0, 0, taipei), "10", []string{"2026-11-01..2026-11-01"}},
		{"in the middle of it: what is dated later is not counted yet", time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), "110", []string{"2026-11-01..2026-11-14"}},
		// It is still the 14th in UTC then: the day is the one in the timezone of the clock.
		{"just after midnight in Taipei, the day after", time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), "1110", []string{"2026-11-01..2026-11-15"}},
		{"the last day of the month", time.Date(2026, 11, 30, 18, 0, 0, 0, taipei), "11110", []string{"2026-11-01..2026-11-30"}},
		{"the first day of the next month: its expense is not this month's", time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), "11110", []string{"2026-11-01..2026-11-30"}},
		{"long after", time.Date(2027, 6, 1, 9, 0, 0, 0, taipei), "11110", []string{"2026-11-01..2026-11-30"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.now = tt.now
			e.budgets.spendingAsked = nil

			got := e.spending(day(2026, 11, 15)) // any day of the month will do

			wantMoney(t, "total", got.Total, tt.want)
			wantRanges(t, e.budgets.spendingAsked, tt.wantRanges...)
		})
	}
}

func TestAMonthThatHasNotBegunHasNothingAndTheRepoIsNotAsked(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 12, 3), nil, nil, food, "500") // confirmed already, and dated in December

	out := e.spending(day(2026, 12, 20))

	if !out.Month.Equal(day(2026, 12, 1)) || !out.Total.IsZero() || len(out.Categories) != 0 {
		t.Errorf("spending = %+v, want December with no total and no category: it has not begun", out)
	}
	if len(e.budgets.spendingAsked) != 0 {
		t.Errorf("the repo was asked %v, want nothing asked for a month that has not begun", e.budgets.spendingAsked)
	}

	// Once it has begun it is asked for, up to the day it is.
	e.clock.setDay(2026, time.December, 3)
	out = e.spending(day(2026, 12, 20))
	wantMoney(t, "total on the 3rd", out.Total, "500")
	wantRanges(t, e.budgets.spendingAsked, "2026-12-01..2026-12-03")
}

func TestSpendingIsByCategoryAndASubcategoryComesWithItsParentAndATopLevelCategoryWithNone(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	e.spend(day(2026, 11, 2), nil, nil, lunch, "200")
	e.spend(day(2026, 11, 3), nil, nil, food, "300")
	e.spend(day(2026, 11, 4), nil, nil, rent, "5000")

	out := e.spending(day(2026, 11, 1))

	wantLines(t, "categories", e.spendingLines(out), "房租 5000", "飲食 300", "午餐 200 (under 飲食)")
	for _, c := range out.Categories {
		if c.CategoryID == lunch.ID {
			if c.ParentID == nil || *c.ParentID != food.ID {
				t.Errorf("lunch has the parent %v, want food", c.ParentID)
			}
		} else if c.ParentID != nil {
			t.Errorf("category %s has the parent %v, want none: it is a top-level one", e.categories.categories[c.CategoryID].Name, *c.ParentID)
		}
	}
}

func TestSpendingIsListedLargestFirstAndForTheSameAmountByCategoryIdAndTheTotalIsWhatTheyAddUpTo(t *testing.T) {
	e := newEnv(t)
	categories := []model.Category{
		e.category("飲食", nil), e.category("房租", nil), e.category("娛樂", nil), e.category("交通", nil), e.category("手續費", nil),
	}
	// Three of them are spent the same on, in an order that is not that of their ids, so
	// that only the rule can put them in order.
	same := []model.Category{categories[2], categories[3], categories[4]}
	slices.SortFunc(same, func(a, b model.Category) int { return bytes.Compare(a.ID[:], b.ID[:]) })
	e.spend(day(2026, 11, 2), nil, nil, same[2], "50.50")
	e.spend(day(2026, 11, 2), nil, nil, same[0], "50.5")
	e.spend(day(2026, 11, 3), nil, nil, same[1], "50.50")
	e.spend(day(2026, 11, 3), nil, nil, categories[0], "5000")
	e.spend(day(2026, 11, 4), nil, nil, categories[1], "99999.99")
	e.spend(day(2026, 11, 5), nil, nil, categories[0], "0.01")

	out := e.spending(day(2026, 11, 1))

	want := []model.Category{categories[1], categories[0], same[0], same[1], same[2]}
	if len(out.Categories) != len(want) {
		t.Fatalf("categories = %v, want %d", e.spendingLines(out), len(want))
	}
	for i, c := range want {
		if out.Categories[i].CategoryID != c.ID {
			t.Errorf("category %d is %s, want %s: the largest first, and for the same amount by id", i+1, e.categories.categories[out.Categories[i].CategoryID].Name, c.Name)
		}
	}
	wantMoney(t, "the largest", out.Categories[0].Amount, "99999.99")
	wantMoney(t, "food: two expenses of it added up", out.Categories[1].Amount, "5000.01")
	wantMoney(t, "total", out.Total, "105151.5")
	sum := decimal.Zero
	for _, c := range out.Categories {
		sum = sum.Add(c.Amount)
	}
	wantMoney(t, "the total is what the categories add up to", out.Total, sum.String())
}

func TestSpendingOfAMonthWithNothingSpentIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 10, 31), nil, nil, food, "10") // October's

	out := e.spending(day(2026, 11, 1))

	if !out.Month.Equal(day(2026, 11, 1)) || !out.Total.IsZero() || len(out.Categories) != 0 {
		t.Errorf("spending = %+v, want November with nothing in it", out)
	}
	wantRanges(t, e.budgets.spendingAsked, "2026-11-01..2026-11-14")
}

func TestWhenTheRepoFailsWhatWasSpentIsNotMadeUp(t *testing.T) {
	boom := errors.New("the database is down")
	e := newEnv(t)
	e.budgets.spendingErr = boom

	if _, err := e.svc.GetSpending(context.Background(), usecase.GetSpendingInput{Month: day(2026, 11, 1)}); !errors.Is(err, boom) {
		t.Errorf("GetSpending: err = %v, want what the repo returned", err)
	}
	_, err := e.svc.GetSpendingTrend(context.Background(), usecase.GetSpendingTrendInput{From: day(2026, 10, 1), To: day(2026, 11, 1)})
	if !errors.Is(err, boom) {
		t.Errorf("GetSpendingTrend: err = %v, want what the repo returned", err)
	}
}

// What was spent is what the areas spent in a month, projects included, and the
// expenses that have no area: no area counts them, and the month has them.
func TestWhatWasSpentInAMonthIsWhatTheAreasSpentWithTheirProjectsPlusTheExpensesOfNoArea(t *testing.T) {
	e := newEnv(t)
	work, home := e.area("工作", false), e.area("家庭", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	rent := e.category("房租", nil)
	e.setArea(work, day(2026, 11, 1), "30000")
	e.spend(day(2026, 11, 2), &work, nil, food, "100")
	e.spend(day(2026, 11, 3), &work, &trip, food, "700") // in the area, and in a project
	e.spend(day(2026, 11, 4), &home, nil, rent, "55")
	e.spend(day(2026, 11, 5), nil, nil, food, "999")      // no area, no project
	e.spend(day(2026, 11, 6), nil, &trip, rent, "998")    // a project that has no area
	e.spend(day(2026, 10, 31), &work, nil, food, "5000")  // not November's
	e.spend(day(2026, 11, 20), &work, nil, food, "10000") // dated after today

	inAreas := decimal.Zero
	for _, status := range e.areas(day(2026, 11, 1)) {
		inAreas = inAreas.Add(status.SpentWithProjects)
	}
	noArea := dec("999").Add(dec("998"))

	out := e.spending(day(2026, 11, 1))

	wantMoney(t, "what the areas spent, projects included", inAreas, "855")
	wantMoney(t, "spending is what the areas spent and what no area has", out.Total, inAreas.Add(noArea).String())
	wantMoney(t, "spending, as it is written down", out.Total, "2852")
	wantLines(t, "categories", e.spendingLines(out), "飲食 1799", "房租 1053")
}

// ---- What counts as spent: the fake adds the transactions up as the statement does ----

// txnWorld is what the transactions of the tests refer to: two accounts, the base
// currency, US dollars, and a party. It makes them as the domain does, as drafts.
type txnWorld struct {
	t          *testing.T
	cash, card model.Account
	twd, usd   model.Instrument
	friend     uuid.UUID
}

func newTxnWorld(t *testing.T) *txnWorld {
	t.Helper()
	return &txnWorld{
		t:      t,
		cash:   model.Account{ID: uuid.Must(uuid.NewV7()), Name: "cash", Kind: model.AccountKindCash},
		card:   model.Account{ID: uuid.Must(uuid.NewV7()), Name: "card", Kind: model.AccountKindCard},
		twd:    model.Instrument{ID: model.BaseCurrencyID, Code: model.BaseCurrencyCode, Name: "新台幣", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2},
		usd:    model.Instrument{ID: uuid.Must(uuid.NewV7()), Code: "USD", Name: "US dollar", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2},
		friend: uuid.Must(uuid.NewV7()),
	}
}

// header is the header of a transaction of the day, with a key of its own.
func (w *txnWorld) header(on time.Time) model.Header {
	id := uuid.Must(uuid.NewV7())
	return model.Header{ID: id, IdempotencyKey: id.String(), OccurredOn: on}
}

// ok returns the transaction, or fails the test with what the domain refused it for.
func (w *txnWorld) ok(txn model.Transaction, err error) model.Transaction {
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("the domain refused the transaction: %v", err)
	}
	return txn
}

// plan returns the transactions of the plan, the origin and then the periods, or fails
// the test with what the domain refused it for.
func (w *txnWorld) plan(plan model.InstalmentPlan, err error) []model.Transaction {
	w.t.Helper()
	if err != nil {
		w.t.Fatalf("the domain refused the plan: %v", err)
	}
	return plan.Transactions()
}

// expense is an expense of the amount of TWD on the category, paid from cash.
func (w *txnWorld) expense(on time.Time, c model.Category, amount string) model.Transaction {
	w.t.Helper()
	return w.ok(model.NewExpense(model.ExpenseParams{
		Header: w.header(on), Account: &w.cash, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: c, Amount: dec(amount)}},
	}))
}

// reconciliation is the draft that makes up for the difference a reconciliation of the
// cash finds between the balance the ledger has and the one the account has.
func (w *txnWorld) reconciliation(on time.Time, recorded, actual string) model.Transaction {
	w.t.Helper()
	r, err := model.Reconcile(model.ReconcileParams{
		Header: w.header(on), Account: w.cash, Instrument: w.twd, BaseCurrency: w.twd,
		RecordedBalance: dec(recorded), ActualBalance: dec(actual),
	})
	if err != nil || r.Transaction == nil {
		w.t.Fatalf("Reconcile(%s, %s) = %+v, %v, want a draft that makes up for the difference", recorded, actual, r, err)
	}
	return *r.Transaction
}

// period is a period of the plan of the principal, falling due on the day.
func period(on time.Time, principal string) model.InstalmentPeriod {
	return model.InstalmentPeriod{ID: uuid.Must(uuid.NewV7()), DueOn: on, Principal: dec(principal)}
}

// confirm adds the transactions to what the repo holds, confirmed.
func (e *env) confirm(txns ...model.Transaction) {
	for _, txn := range txns {
		txn.Status = model.StatusConfirmed
		e.budgets.transactions = append(e.budgets.transactions, txn)
	}
}

func TestWhatCountsAsSpentIsTheBaseAmountOfTheExpensePostingsOfAConfirmedTransactionOfAnyKind(t *testing.T) {
	tests := []struct {
		name  string
		month time.Time
		build func(w *txnWorld, c map[string]model.Category) []model.Transaction
		want  []string
	}{
		{
			name: "an expense", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.expense(day(2026, 11, 3), c["food"], "120")}
			},
			want: []string{"food 120"},
		},
		{
			name: "an expense of several categories", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 3)), Account: &w.cash, Instrument: w.twd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("300")}, {Category: c["household"], Amount: dec("200")}},
				}))}
			},
			want: []string{"food 300", "household 200"},
		},
		{
			name: "an expense in a foreign currency counts by its amount in the base currency", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 3)), Account: &w.card, Instrument: w.usd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("100"), BaseAmount: new(dec("3200"))}},
				}))}
			},
			want: []string{"food 3200"},
		},
		{
			name: "a discount does not reduce what was paid", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 3)), Account: &w.cash, Instrument: w.twd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("450"), Discount: new(dec("50"))}},
				}))}
			},
			want: []string{"food 450"},
		},
		{
			name: "of a split only my share", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 3)), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
					Lines:  []model.CategoryLine{{Category: c["food"], Amount: dec("300")}},
					Shares: []model.Share{{PartyID: w.friend, Amount: dec("200")}},
				}))}
			},
			want: []string{"food 300"},
		},
		{
			name: "an expense that a party paid for me is mine", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 3)), PaidBy: &w.friend, Instrument: w.twd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("300")}},
				}))}
			},
			want: []string{"food 300"},
		},
		{
			name: "the fee of a transfer, and not the transfer", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.ok(model.NewTransfer(model.TransferParams{
					Header: w.header(day(2026, 11, 3)), From: w.cash, To: w.card, Instrument: w.twd, BaseCurrency: w.twd, Amount: dec("1000"),
					Fees: []model.Fee{{Account: w.cash, Instrument: w.twd, Category: c["fees"], Amount: dec("15")}},
				}))}
			},
			want: []string{"fees 15"},
		},
		{
			name: "an income and a reward are not spent, and take nothing from what is", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{
					w.expense(day(2026, 11, 3), c["food"], "100"),
					w.ok(model.NewIncome(model.IncomeParams{
						Header: w.header(day(2026, 11, 3)), Account: w.cash, Instrument: w.twd, BaseCurrency: w.twd,
						Lines: []model.CategoryLine{{Category: c["salary"], Amount: dec("5000")}},
					})),
					w.ok(model.NewReward(model.RewardParams{Header: w.header(day(2026, 11, 3)), Account: w.card, Instrument: w.twd, BaseCurrency: w.twd, Amount: dec("50")})),
				}
			},
			want: []string{"food 100"},
		},
		{
			name: "what a reconciliation found missing is spent on the system category", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.reconciliation(day(2026, 11, 10), "1000", "880")}
			},
			want: []string{"未記錄 120"},
		},
		{
			name: "what a reconciliation found too much is no spending", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return []model.Transaction{w.reconciliation(day(2026, 11, 10), "1000", "1080")}
			},
			want: nil,
		},
		{
			name: "a purchase in instalments is spent whole on the day of the purchase", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return w.plan(model.NewInstalmentExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 10)), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("12000")}},
				}, model.InstalmentSchedule{Periods: []model.InstalmentPeriod{
					period(day(2026, 12, 5), "6000"), period(day(2027, 1, 5), "6000"),
				}}))
			},
			want: []string{"food 12000"},
		},
		{
			name: "the principal of a period is not spent again, and its interest is, on its day", month: day(2026, 12, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				first := period(day(2026, 12, 5), "6000")
				first.Interest = new(dec("30"))
				return w.plan(model.NewInstalmentExpense(model.ExpenseParams{
					Header: w.header(day(2026, 11, 10)), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
					Lines: []model.CategoryLine{{Category: c["food"], Amount: dec("12000")}},
				}, model.InstalmentSchedule{
					Periods:          []model.InstalmentPeriod{first, period(day(2027, 1, 5), "6000")},
					InterestCategory: new(c["interest"]),
				}))
			},
			want: []string{"interest 30"},
		},
		{
			name: "the conversion of a bill into instalments is no expense, and the interest of its periods is", month: day(2026, 12, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				first := period(day(2026, 12, 20), "1000")
				first.Interest = new(dec("20"))
				return w.plan(model.NewBillInstalment(model.BillInstalmentParams{
					Header: w.header(day(2026, 11, 16)), Account: w.card, Instrument: w.twd, BaseCurrency: w.twd, Principal: dec("3000"),
					Schedule: model.InstalmentSchedule{
						Periods:          []model.InstalmentPeriod{first, period(day(2027, 1, 20), "2000")},
						InterestCategory: new(c["interest"]),
					},
				}))
			},
			want: []string{"interest 20"},
		},
		{
			name: "the conversion of a bill into instalments, in the month it is made", month: day(2026, 11, 1),
			build: func(w *txnWorld, c map[string]model.Category) []model.Transaction {
				return w.plan(model.NewBillInstalment(model.BillInstalmentParams{
					Header: w.header(day(2026, 11, 16)), Account: w.card, Instrument: w.twd, BaseCurrency: w.twd, Principal: dec("3000"),
					Schedule: model.InstalmentSchedule{Periods: []model.InstalmentPeriod{period(day(2026, 12, 20), "3000")}},
				}))
			},
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.clock.setDay(2027, time.March, 1) // every month of the tests is past
			e.addCategory(model.Category{ID: model.UnrecordedExpenseCategoryID, Direction: model.DirectionExpense, Name: "未記錄"})
			c := map[string]model.Category{
				"food": e.category("food", nil), "household": e.category("household", nil), "fees": e.category("fees", nil),
				"interest": e.category("interest", nil),
				"salary":   e.addCategory(model.Category{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionIncome, Name: "salary"}),
			}
			e.confirm(tt.build(newTxnWorld(t), c)...)

			got := e.spending(tt.month)

			wantLines(t, "categories", e.spendingLines(got), tt.want...)
		})
	}
}

func TestOnlyAConfirmedTransactionIsSpentNotADraftAndNotAVoidOne(t *testing.T) {
	e := newEnv(t)
	food := e.category("food", nil)
	w := newTxnWorld(t)
	e.budgets.transactions = []model.Transaction{
		w.expense(day(2026, 11, 3), food, "100"),
		w.expense(day(2026, 11, 4), food, "10"),
		w.expense(day(2026, 11, 5), food, "1"),
	}
	setStatus := func(i int, s model.Status) { e.budgets.transactions[i].Status = s }

	// A transaction is a draft until it is confirmed: only the confirmed one is spent.
	setStatus(0, model.StatusDraft)
	setStatus(1, model.StatusConfirmed)
	setStatus(2, model.StatusVoid)
	wantMoney(t, "with a draft, a confirmed and a void transaction", e.spending(day(2026, 11, 1)).Total, "10")

	// Confirming the draft makes it count, and voiding the one that was confirmed takes it out.
	setStatus(0, model.StatusConfirmed)
	wantMoney(t, "once the draft is confirmed", e.spending(day(2026, 11, 1)).Total, "110")
	setStatus(1, model.StatusVoid)
	wantMoney(t, "once the one that was confirmed is void", e.spending(day(2026, 11, 1)).Total, "100")
}

// ---- GetSpendingTrend ----

func TestTheTrendHasAMonthForEachMonthOfTheRangeOldestFirstWithZeroForThoseWithNothingAcrossAYear(t *testing.T) {
	e := newEnv(t)
	e.clock.setDay(2027, time.February, 10)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	e.spend(day(2026, 9, 30), nil, nil, food, "7")     // before the range
	e.spend(day(2026, 10, 1), nil, nil, food, "100")   // the first day of the first month
	e.spend(day(2026, 12, 31), nil, nil, lunch, "200") // the last day of the year
	e.spend(day(2026, 12, 5), nil, nil, food, "50")
	e.spend(day(2027, 1, 1), nil, nil, rent, "5000")
	e.spend(day(2027, 2, 1), nil, nil, rent, "9") // after the range

	// Given by any day of the first and the last month.
	out := e.trend(day(2026, 10, 20), day(2027, 1, 3))

	wantLines(t, "months", trendLines(out), "2026-10 100", "2026-11 0", "2026-12 250", "2027-01 5000")
	for i, m := range out.Months {
		if m.Month.Day() != 1 || m.Month.Location() != time.UTC || (i > 0 && !m.Month.After(out.Months[i-1].Month)) {
			t.Errorf("month %d is %s, want the first day of a month at midnight UTC, each after the one before", i+1, m.Month)
		}
	}
}

func TestTheTrendAsksTheRepoOnceForTheWholeRangeToTheLastDayOfItsLastMonthOrToday(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 3, 3), nil, nil, food, "1")

	tests := []struct {
		name       string
		today      time.Time
		from, to   time.Time
		wantRanges []string
		wantMonths int
	}{
		{"months that are over", day(2026, 11, 14), day(2026, 3, 9), day(2026, 6, 20), []string{"2026-03-01..2026-06-30"}, 4},
		{"up to the current month, which is partial", day(2026, 11, 14), day(2026, 9, 1), day(2026, 11, 30), []string{"2026-09-01..2026-11-14"}, 3},
		{"a range that goes on past today", day(2026, 11, 14), day(2026, 10, 1), day(2027, 2, 1), []string{"2026-10-01..2026-11-14"}, 5},
		{"across a year, in the past", day(2027, 5, 1), day(2026, 11, 1), day(2027, 2, 1), []string{"2026-11-01..2027-02-28"}, 4},
		{"across a leap February", day(2028, 6, 1), day(2028, 1, 15), day(2028, 2, 15), []string{"2028-01-01..2028-02-29"}, 2},
		{"one month", day(2026, 11, 14), day(2026, 11, 30), day(2026, 11, 1), []string{"2026-11-01..2026-11-14"}, 1},
		{"a range that has not begun", day(2026, 11, 14), day(2026, 12, 1), day(2027, 3, 1), nil, 4},
		{"a range that begins tomorrow", day(2026, 11, 30), day(2026, 12, 1), day(2027, 3, 1), nil, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.setDay(tt.today.Year(), tt.today.Month(), tt.today.Day())
			e.budgets.spendingAsked = nil

			out := e.trend(tt.from, tt.to)

			wantRanges(t, e.budgets.spendingAsked, tt.wantRanges...)
			if len(out.Months) != tt.wantMonths {
				t.Errorf("%d months = %v, want %d: every month of the range, asked for or not", len(out.Months), trendLines(out), tt.wantMonths)
			}
		})
	}
}

func TestTheTrendOfAPartialCurrentMonthCountsWhatHasHappenedByTodayAndTheMonthsToComeAreZero(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 9, 10), nil, nil, food, "100")
	e.spend(day(2026, 11, 14), nil, nil, food, "10")  // today
	e.spend(day(2026, 11, 20), nil, nil, food, "1")   // confirmed, and not yet
	e.spend(day(2026, 12, 3), nil, nil, food, "1000") // in a month that has not begun

	out := e.trend(day(2026, 9, 1), day(2026, 12, 1))

	wantLines(t, "months", trendLines(out), "2026-09 100", "2026-10 0", "2026-11 10", "2026-12 0")

	// Once the days have come, they count.
	e.clock.setDay(2026, time.December, 3)
	out = e.trend(day(2026, 9, 1), day(2026, 12, 1))
	wantLines(t, "months on the 3rd of December", trendLines(out), "2026-09 100", "2026-10 0", "2026-11 11", "2026-12 1000")
}

func TestTheTrendOfARangeThatHasNotBegunHasEveryMonthWithZeroAndAsksNothing(t *testing.T) {
	e := newEnv(t)
	food := e.category("飲食", nil)
	e.spend(day(2026, 12, 3), nil, nil, food, "1000") // confirmed already

	out := e.trend(day(2026, 12, 1), day(2027, 2, 28))

	wantLines(t, "months", trendLines(out), "2026-12 0", "2027-01 0", "2027-02 0")
	if len(e.budgets.spendingAsked) != 0 {
		t.Errorf("the repo was asked %v, want nothing asked for a range that has not begun", e.budgets.spendingAsked)
	}
}

func TestTheTrendRefusesARangeThatIsNotOneAndAsksNothing(t *testing.T) {
	tooWide := func(months int) usecase.GetSpendingTrendInput {
		from := day(2016, 11, 1)
		return usecase.GetSpendingTrendInput{From: from, To: from.AddDate(0, months-1, 0)}
	}
	tests := []struct {
		name string
		in   usecase.GetSpendingTrendInput
		want error
	}{
		{"no first month", usecase.GetSpendingTrendInput{To: day(2026, 11, 1)}, model.ErrMonthRequired},
		{"no last month", usecase.GetSpendingTrendInput{From: day(2026, 11, 1)}, model.ErrMonthRequired},
		{"neither", usecase.GetSpendingTrendInput{}, model.ErrMonthRequired},
		{"a last month before the first", usecase.GetSpendingTrendInput{From: day(2026, 11, 1), To: day(2026, 10, 31)}, model.ErrDateRangeReversed},
		{"a year before", usecase.GetSpendingTrendInput{From: day(2026, 11, 1), To: day(2025, 11, 1)}, model.ErrDateRangeReversed},
		{"one month more than the most", tooWide(usecase.MaxSpendingTrendMonths + 1), model.ErrSpendingRangeTooWide},
		{"as many months as a date can name", usecase.GetSpendingTrendInput{From: day(2, 1, 1), To: day(9999, 12, 31)}, model.ErrSpendingRangeTooWide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)

			_, err := e.svc.GetSpendingTrend(context.Background(), tt.in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(e.budgets.spendingAsked) != 0 {
				t.Errorf("the repo was asked %v, want nothing asked for a range that is refused", e.budgets.spendingAsked)
			}
		})
	}
}

func TestTheTrendTakesTheMostMonthsAndOneMonthAndTheDaysOfOneMonthInEitherOrder(t *testing.T) {
	e := newEnv(t)
	most := usecase.MaxSpendingTrendMonths

	from := day(2016, 12, 1)
	out := e.trend(from, from.AddDate(0, most-1, 0)) // November 2026 is the last of them
	if lines := trendLines(out); len(lines) != most || lines[0] != "2016-12 0" || lines[most-1] != "2026-11 0" {
		t.Errorf("trend of the most months = %d months, want %d, from 2016-12 to 2026-11", len(lines), most)
	}
	wantRanges(t, e.budgets.spendingAsked, "2016-12-01..2026-11-14")

	// The two ends in the same month are one month, whichever day comes first.
	for _, ends := range [][2]time.Time{{day(2026, 11, 1), day(2026, 11, 30)}, {day(2026, 11, 30), day(2026, 11, 1)}} {
		wantLines(t, "trend of "+ends[0].Format(time.DateOnly)+" to "+ends[1].Format(time.DateOnly), trendLines(e.trend(ends[0], ends[1])), "2026-11 0")
	}
}

func TestTheTrendAgreesWithTheSpendingOfEachMonthItHas(t *testing.T) {
	e := newEnv(t)
	e.clock.setDay(2027, time.January, 9)
	work := e.area("工作", false)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	for _, expense := range []struct {
		on       time.Time
		area     *uuid.UUID
		project  *uuid.UUID
		category model.Category
		amount   string
	}{
		{day(2026, 10, 3), &work, nil, food, "120.5"},
		{day(2026, 10, 30), nil, nil, lunch, "80"},
		{day(2026, 11, 1), &work, &trip, rent, "5000"},
		{day(2026, 11, 30), nil, &trip, food, "7.25"},
		{day(2026, 12, 15), nil, nil, food, "300"},
		{day(2027, 1, 9), &work, nil, lunch, "40"}, // today
		{day(2027, 1, 10), &work, nil, lunch, "9999"},
	} {
		e.spend(expense.on, expense.area, expense.project, expense.category, expense.amount)
	}

	out := e.trend(day(2026, 9, 1), day(2027, 2, 1))

	wantLines(t, "months", trendLines(out), "2026-09 0", "2026-10 200.5", "2026-11 5007.25", "2026-12 300", "2027-01 40", "2027-02 0")
	for _, m := range out.Months {
		if spending := e.spending(m.Month); !spending.Total.Equal(m.Total) {
			t.Errorf("%s: the trend says %s, the spending of the month says %s, want them the same", m.Month.Format("2006-01"), m.Total, spending.Total)
		}
	}
}

// ---- One definition of how far spending is read ----

// The range of days of a month was worked out inside GetAreaBudgets, and is now the
// one definition the reads of spending share: the budgets of the areas ask for what
// they always did, and the spending of the same month asks for the same days.
func TestTheBudgetsOfAreasAndTheSpendingOfTheSameMonthAskForTheSameDays(t *testing.T) {
	tests := []struct {
		name  string
		today time.Time
		month time.Time
		want  []string // none for a month that has not begun
	}{
		{"the current month", day(2026, 11, 14), day(2026, 11, 3), []string{"2026-11-01..2026-11-14"}},
		{"a month that is over", day(2026, 11, 14), day(2026, 10, 3), []string{"2026-10-01..2026-10-31"}},
		{"February of a leap year", day(2028, 6, 1), day(2028, 2, 3), []string{"2028-02-01..2028-02-29"}},
		{"the first day of the month is today", day(2026, 11, 1), day(2026, 11, 20), []string{"2026-11-01..2026-11-01"}},
		{"a month that has not begun", day(2026, 11, 14), day(2026, 12, 1), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.clock.setDay(tt.today.Year(), tt.today.Month(), tt.today.Day())

			e.areas(tt.month)
			e.spending(tt.month)

			wantRanges(t, e.budgets.areaSpendingAsked, tt.want...)
			wantRanges(t, e.budgets.spendingAsked, tt.want...)
		})
	}
}
