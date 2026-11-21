package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// budgetWorld is the ledger as the app runs it (the real use cases on a real
// PostgreSQL, through the Fx modules of cmd), with what the budget tests refer to:
// two areas, a project of the area Work, categories (Food with a subcategory Lunch,
// Rent, Fun, Fees and Interest), accounts, and a clock that the test moves.
type budgetWorld struct {
	ledgerApp
	t   *testing.T
	clk *testClock

	home, work, trip                       uuid.UUID
	food, lunch, rent, fun, fees, interest uuid.UUID
	cash, bank, wallet, card               uuid.UUID

	names map[uuid.UUID]string
	keys  int
}

func newBudgetWorld(t *testing.T) *budgetWorld {
	t.Helper()
	ctx := context.Background()
	clk := newTestClock(day) // the 14th of November 2026
	w := &budgetWorld{ledgerApp: newLedgerAppOn(t, testpg.New(t), clk), t: t, clk: clk, names: map[uuid.UUID]string{}}

	area := func(name string) uuid.UUID {
		a, err := w.CreateArea.CreateArea(ctx, catalogusecase.CreateAreaInput{Name: name})
		if err != nil {
			t.Fatalf("CreateArea(%q): %v", name, err)
		}
		w.names[a.ID] = name
		return a.ID
	}
	category := func(name string, parent *uuid.UUID) uuid.UUID {
		c, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", ParentID: parent, Name: name})
		if err != nil {
			t.Fatalf("CreateCategory(%q): %v", name, err)
		}
		w.names[c.ID] = name
		return c.ID
	}
	account := func(name, kind string) uuid.UUID {
		a, err := w.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: name, Kind: kind})
		if err != nil {
			t.Fatalf("CreateAccount(%q): %v", name, err)
		}
		return a.ID
	}

	w.home, w.work = area("Home"), area("Work")
	w.trip = w.project("Trip", &w.work)
	w.food = category("Food", nil)
	w.lunch = category("Lunch", &w.food)
	w.rent, w.fun, w.fees, w.interest = category("Rent", nil), category("Fun", nil), category("Fees", nil), category("Interest", nil)
	w.cash, w.bank, w.wallet, w.card = account("cash", "cash"), account("bank", "bank"), account("wallet", "wallet"), account("card", "card")

	// Money to spend: each account is opened with 100,000 on the first of October,
	// before anything below happens, and confirmed.
	for _, a := range []uuid.UUID{w.cash, w.bank, w.wallet} {
		opening := w.details(w.key())
		opening.OccurredOn = dateOn(2026, time.October, 1)
		out, err := w.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
			RecordDetails: opening, AccountID: a, InstrumentID: model.BaseCurrencyID, Amount: dec("100000"),
		})
		if err != nil {
			t.Fatalf("SetOpeningBalance: %v", err)
		}
		w.confirm(t, out.Transaction)
	}
	return w
}

// project makes a project whose default area is the area given, and names it.
func (w *budgetWorld) project(name string, defaultArea *uuid.UUID) uuid.UUID {
	w.t.Helper()
	p, err := w.CreateProject.CreateProject(context.Background(), catalogusecase.CreateProjectInput{Name: name, DefaultAreaID: defaultArea})
	if err != nil {
		w.t.Fatalf("CreateProject(%q): %v", name, err)
	}
	w.names[p.ID] = name
	return p.ID
}

// key makes an idempotency key that has not been used.
func (w *budgetWorld) key() string {
	w.keys++
	return fmt.Sprintf("budget-e2e-%04d", w.keys)
}

// expense records an expense of the amount of TWD on the category, paid from the
// account, on the day, as a draft.
func (w *budgetWorld) expense(on time.Time, area, project *uuid.UUID, account, category uuid.UUID, amount string) usecase.Transaction {
	w.t.Helper()
	d := w.details(w.key())
	d.OccurredOn, d.AreaID, d.ProjectID = on, area, project
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &account, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: category, Amount: dec(amount)}},
	})
	if err != nil {
		w.t.Fatalf("RecordExpense: %v", err)
	}
	return out.Transaction
}

// spend records an expense and confirms it.
func (w *budgetWorld) spend(on time.Time, area, project *uuid.UUID, account, category uuid.UUID, amount string) usecase.Transaction {
	w.t.Helper()
	txn := w.expense(on, area, project, account, category, amount)
	w.confirm(w.t, txn)
	return txn
}

func allocation(category uuid.UUID, amount string) usecase.Allocation {
	return usecase.Allocation{CategoryID: category, Amount: dec(amount)}
}

// setArea sets the budget of an area from the month on, and fails the test when it
// cannot.
func (w *budgetWorld) setArea(area uuid.UUID, month time.Time, amount string, allocations ...usecase.Allocation) {
	w.t.Helper()
	_, err := w.SetAreaBudget.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{
		AreaID: area, Month: month, Amount: dec(amount), Allocations: allocations,
	})
	if err != nil {
		w.t.Fatalf("SetAreaBudget(%s): %v", month.Format("2006-01"), err)
	}
}

// setProject sets the budget of a project, and fails the test when it cannot.
func (w *budgetWorld) setProject(project uuid.UUID, amount string, allocations ...usecase.Allocation) {
	w.t.Helper()
	_, err := w.SetProjectBudget.SetProjectBudget(context.Background(), usecase.SetProjectBudgetInput{
		ProjectID: project, Amount: dec(amount), Allocations: allocations,
	})
	if err != nil {
		w.t.Fatalf("SetProjectBudget: %v", err)
	}
}

// allocationLines writes where each allocation stands as "Food 12500 of 20000".
func (w *budgetWorld) allocationLines(statuses []usecase.AllocationStatus) string {
	parts := make([]string, len(statuses))
	for i, a := range statuses {
		parts[i] = fmt.Sprintf("%s %s of %s", w.names[a.CategoryID], a.Spent, a.Amount)
	}
	return strings.Join(parts, "; ")
}

// areas returns where the areas stand in the month, one to a line, in the order they
// come: "Work: budget 30000, spent 12785 (18785 with projects), allocations [Food 12500
// of 20000], unallocated 285", with " OVER" at the end when the area is over its
// budget.
func (w *budgetWorld) areas(month time.Time) []string {
	w.t.Helper()
	out, err := w.GetAreaBudgets.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: month})
	if err != nil {
		w.t.Fatalf("GetAreaBudgets(%s): %v", month.Format("2006-01"), err)
	}
	lines := make([]string, len(out.Areas))
	for i, s := range out.Areas {
		budget := "none"
		if s.Budget != nil {
			budget = s.Budget.String()
		}
		lines[i] = fmt.Sprintf("%s: budget %s, spent %s (%s with projects), allocations [%s], unallocated %s",
			w.names[s.AreaID], budget, s.Spent, s.SpentWithProjects, w.allocationLines(s.Allocations), s.Unallocated)
		if s.Over {
			lines[i] += " OVER"
		}
	}
	return lines
}

// projects returns where the projects stand, one to a line, in the order they come:
// "Trip: budget 20000, spent 6100, allocations [Food 6100 of 8000], unallocated 0".
func (w *budgetWorld) projects(only *uuid.UUID) []string {
	w.t.Helper()
	out, err := w.GetProjectBudgets.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{ProjectID: only})
	if err != nil {
		w.t.Fatalf("GetProjectBudgets: %v", err)
	}
	lines := make([]string, len(out.Projects))
	for i, s := range out.Projects {
		budget := "none"
		if s.Budget != nil {
			budget = s.Budget.String()
		}
		lines[i] = fmt.Sprintf("%s: budget %s, spent %s, allocations [%s], unallocated %s",
			w.names[s.ProjectID], budget, s.Spent, w.allocationLines(s.Allocations), s.Unallocated)
		if s.Over {
			lines[i] += " OVER"
		}
	}
	return lines
}

func wantStatus(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestAnAreaStandsAgainstItsBudgetOfTheMonthThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	november, december, october := dateOn(2026, time.November, 1), dateOn(2026, time.December, 1), dateOn(2026, time.October, 1)

	// The budgets: Work has 9,000 in October and 30,000 from November on, of which
	// 20,000 is for food (which takes lunch too) and 5,000 for rent; Home has 3,000
	// from November.
	w.setArea(w.work, october, "9000")
	w.setArea(w.work, november, "30000", allocation(w.rent, "5000"), allocation(w.food, "20000"))
	w.setArea(w.home, november, "3000")

	// The days on either side of November, neither of which is November's: the last day
	// of October, and the first of December, which has not come yet.
	w.spend(dateOn(2026, time.October, 31), &w.work, nil, w.cash, w.fun, "11")
	w.spend(dateOn(2026, time.December, 1), &w.work, nil, w.cash, w.fun, "22")

	// What is spent. The expenses of Work: lunch 200 (a subcategory of food), food 300,
	// fun 150, a fee of 15 that comes with a transfer, 120 that a reconciliation found
	// missing, and a television of 12,000 bought on the card in 12 periods, whose first
	// has 30 of interest.
	w.spend(dateOn(2026, time.November, 2), &w.work, nil, w.cash, w.lunch, "200")
	w.spend(dateOn(2026, time.November, 3), &w.work, nil, w.cash, w.food, "300")
	w.spend(dateOn(2026, time.November, 4), &w.work, nil, w.cash, w.fun, "150")
	// The trip's, whose default area is Work: in the area, but in a project.
	w.spend(dateOn(2026, time.November, 5), nil, &w.trip, w.cash, w.food, "6000")
	w.spend(dateOn(2026, time.November, 5), &w.home, nil, w.cash, w.food, "90")
	// A draft is not spent, and neither is what was confirmed and then voided.
	w.expense(dateOn(2026, time.November, 6), &w.work, nil, w.wallet, w.food, "5000")
	voided := w.spend(dateOn(2026, time.November, 7), &w.work, nil, w.cash, w.fun, "7000")
	if _, err := w.Void.VoidTransaction(ctx, voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	d := w.details(w.key())
	d.OccurredOn, d.AreaID = dateOn(2026, time.November, 8), &w.work
	transfer, err := w.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.bank, ToAccountID: w.wallet, InstrumentID: model.BaseCurrencyID, Amount: dec("1000"),
		Fees: []usecase.Fee{{AccountID: w.bank, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	w.confirm(t, transfer.Transaction)

	// The cash is 120 less than the ledger says on the 9th.
	ninth := dateOn(2026, time.November, 9)
	held, err := w.GetBalances.GetBalances(ctx, usecase.GetBalancesInput{AccountID: &w.cash, AsOf: &ninth})
	if err != nil || len(held.Balances) != 1 {
		t.Fatalf("GetBalances = %+v, %v, want the balance of the cash", held, err)
	}
	d = w.details(w.key())
	d.OccurredOn, d.AreaID = ninth, &w.work
	reconciled, err := w.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
		RecordDetails: d, AccountID: w.cash, InstrumentID: model.BaseCurrencyID, ActualBalance: held.Balances[0].Quantity.Sub(dec("120")),
	})
	if err != nil || reconciled.Transaction == nil {
		t.Fatalf("Reconcile = %+v, %v, want a draft of the 120 that is missing", reconciled, err)
	}
	w.confirm(t, *reconciled.Transaction)

	d = w.details(w.key())
	d.OccurredOn, d.AreaID = dateOn(2026, time.November, 10), &w.work
	television, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("12000")}},
		Instalments: instalmentsOf(w.interest),
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	w.confirm(t, television.Transaction)

	// A rent of 5,000 for the 20th, which is confirmed now and has not happened yet.
	w.spend(dateOn(2026, time.November, 20), &w.work, nil, w.bank, w.rent, "5000")

	// Today is the 14th. Spent is what is not a project's: the 6,000 of the trip is in
	// what Work spent altogether (18,785), not in what its budget is compared with
	// (12,785). Food has the lunch and the television, rent has nothing yet, and what
	// is in no allocation is the fun, the fee and what was missing.
	wantStatus(t, "November, on the 14th", w.areas(november),
		"Home: budget 3000, spent 90 (90 with projects), allocations [], unallocated 90",
		"Work: budget 30000, spent 12785 (18785 with projects), allocations [Food 12500 of 20000; Rent 0 of 5000], unallocated 285",
	)
	// October has the budget of October, and the 11 of its last day. September had none,
	// and no expense either, so it has no area at all; December has the budget in force,
	// which is the one of November, and nothing has happened yet: its first day has not
	// come.
	wantStatus(t, "October", w.areas(october), "Work: budget 9000, spent 11 (11 with projects), allocations [], unallocated 11")
	wantStatus(t, "September", w.areas(dateOn(2026, time.September, 15)))
	wantStatus(t, "December, on the 14th of November", w.areas(december),
		"Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0",
		"Work: budget 30000, spent 0 (0 with projects), allocations [Food 0 of 20000; Rent 0 of 5000], unallocated 0",
	)

	// The 20th: the rent has happened, and counts.
	w.clk.setDay(dateOn(2026, time.November, 20))
	wantStatus(t, "November, on the 20th", w.areas(november),
		"Home: budget 3000, spent 90 (90 with projects), allocations [], unallocated 90",
		"Work: budget 30000, spent 17785 (23785 with projects), allocations [Food 12500 of 20000; Rent 5000 of 5000], unallocated 285",
	)

	// Spending more than it has is reported and nothing more: the expense is accepted.
	w.spend(dateOn(2026, time.November, 20), &w.work, nil, w.cash, w.fun, "13000")
	wantStatus(t, "November, over", w.areas(november),
		"Home: budget 3000, spent 90 (90 with projects), allocations [], unallocated 90",
		"Work: budget 30000, spent 30785 (36785 with projects), allocations [Food 12500 of 20000; Rent 5000 of 5000], unallocated 13285 OVER",
	)

	// December: its first day has come on the 1st, and the interest of the first period
	// falls due on the 5th. The day before it is not spent, and on the day it is, once,
	// in the month it falls due in.
	w.clk.setDay(dateOn(2026, time.December, 4))
	wantStatus(t, "December, the 4th", w.areas(december),
		"Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0",
		"Work: budget 30000, spent 22 (22 with projects), allocations [Food 0 of 20000; Rent 0 of 5000], unallocated 22",
	)
	w.clk.setDay(dateOn(2026, time.December, 5))
	wantStatus(t, "December, the 5th", w.areas(december),
		"Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0",
		"Work: budget 30000, spent 52 (52 with projects), allocations [Food 0 of 20000; Rent 0 of 5000], unallocated 52",
	)
	// November is over, and is what it was.
	wantStatus(t, "November, in December", w.areas(november),
		"Home: budget 3000, spent 90 (90 with projects), allocations [], unallocated 90",
		"Work: budget 30000, spent 30785 (36785 with projects), allocations [Food 12500 of 20000; Rent 5000 of 5000], unallocated 13285 OVER",
	)

	// A budget of nothing from January on ends the budget there, and January has none.
	w.setArea(w.work, dateOn(2027, time.January, 1), "0")
	wantStatus(t, "January", w.areas(dateOn(2027, time.January, 1)), "Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0")
	wantStatus(t, "December still has it", w.areas(december),
		"Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0",
		"Work: budget 30000, spent 52 (52 with projects), allocations [Food 0 of 20000; Rent 0 of 5000], unallocated 52",
	)

	// Voiding the television takes it out of November and takes the interest out of
	// December: the plan is void, periods and all.
	if _, err := w.Void.VoidTransaction(ctx, television.Transaction.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	wantStatus(t, "November without the television", w.areas(november),
		"Home: budget 3000, spent 90 (90 with projects), allocations [], unallocated 90",
		"Work: budget 30000, spent 18785 (24785 with projects), allocations [Food 500 of 20000; Rent 5000 of 5000], unallocated 13285",
	)
	wantStatus(t, "December without the interest", w.areas(december),
		"Home: budget 3000, spent 0 (0 with projects), allocations [], unallocated 0",
		"Work: budget 30000, spent 22 (22 with projects), allocations [Food 0 of 20000; Rent 0 of 5000], unallocated 22",
	)
}

func TestAProjectStandsAgainstAllItsExpensesAndNotAgainstTheBudgetOfItsAreaThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	move := w.project("Move", nil)
	october, november := dateOn(2026, time.October, 1), dateOn(2026, time.November, 1)

	w.setProject(w.trip, "20000", allocation(w.food, "8000"))
	w.setArea(w.work, october, "9000")
	w.setArea(w.work, november, "30000")

	// The trip's expenses, whatever their month, with no area given: they go to the
	// default area of the trip, Work. Lunch is a subcategory of food, which has the
	// allocation.
	w.spend(dateOn(2026, time.October, 15), nil, &w.trip, w.cash, w.food, "100")
	w.spend(dateOn(2026, time.November, 5), nil, &w.trip, w.cash, w.lunch, "6000")
	w.spend(dateOn(2026, time.November, 12), nil, &w.trip, w.cash, w.fun, "15000")
	// Not the trip's: nobody's project, and another project's.
	w.spend(dateOn(2026, time.November, 6), &w.work, nil, w.cash, w.food, "77")
	w.spend(dateOn(2026, time.November, 6), nil, &move, w.cash, w.food, "5")
	// Dated after today: counts from its day on.
	w.spend(dateOn(2026, time.November, 20), nil, &w.trip, w.cash, w.food, "500")

	wantStatus(t, "the projects on the 14th", w.projects(nil),
		"Move: budget none, spent 5, allocations [], unallocated 5",
		"Trip: budget 20000, spent 21100, allocations [Food 6100 of 8000], unallocated 15000 OVER",
	)
	// What the trip spent in Work is in the statistic of Work, and in no budget of it.
	wantStatus(t, "Work in October", w.areas(october), "Work: budget 9000, spent 0 (100 with projects), allocations [], unallocated 0")
	wantStatus(t, "Work in November", w.areas(november),
		"Work: budget 30000, spent 77 (21077 with projects), allocations [], unallocated 77",
	)

	w.clk.setDay(dateOn(2026, time.November, 20))
	wantStatus(t, "the projects on the 20th", w.projects(nil),
		"Move: budget none, spent 5, allocations [], unallocated 5",
		"Trip: budget 20000, spent 21600, allocations [Food 6600 of 8000], unallocated 15000 OVER",
	)

	// One project, whose budget is taken away: it is listed still, by what it spent.
	trip := w.trip
	wantStatus(t, "the trip alone", w.projects(&trip), "Trip: budget 20000, spent 21600, allocations [Food 6600 of 8000], unallocated 15000 OVER")
	if err := w.RemoveProjectBudget.RemoveProjectBudget(ctx, usecase.RemoveProjectBudgetInput{ProjectID: trip}); err != nil {
		t.Fatalf("RemoveProjectBudget: %v", err)
	}
	wantStatus(t, "the trip with no budget", w.projects(&trip), "Trip: budget none, spent 21600, allocations [], unallocated 21600")
	err := w.RemoveProjectBudget.RemoveProjectBudget(ctx, usecase.RemoveProjectBudgetInput{ProjectID: trip})
	if !errors.Is(err, model.ErrNotFound) {
		t.Errorf("removing it again: err = %v, want ErrNotFound", err)
	}

	// A project the catalog does not have is an error, and an archived one is still
	// reported: what it spent counts.
	unknown := uuid.Must(uuid.NewV7())
	if _, err := w.GetProjectBudgets.GetProjectBudgets(ctx, usecase.GetProjectBudgetsInput{ProjectID: &unknown}); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("an unknown project: err = %v, want ErrNotFound", err)
	}
	if _, err := w.ArchiveProject.ArchiveProject(ctx, trip); err != nil {
		t.Fatalf("ArchiveProject: %v", err)
	}
	wantStatus(t, "the archived trip", w.projects(&trip), "Trip: budget none, spent 21600, allocations [], unallocated 21600")
	// An archived project can no longer be given a budget.
	_, err = w.SetProjectBudget.SetProjectBudget(ctx, usecase.SetProjectBudgetInput{ProjectID: trip, Amount: dec("1")})
	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || !refErr.Problems[0].Archived {
		t.Errorf("setting the budget of an archived project: err = %v, want the catalog's *ReferenceError, archived", err)
	}
}

func TestABudgetThatBreaksARuleIsRefusedAndNothingIsStoredThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	november := dateOn(2026, time.November, 1)
	salary, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "income", Name: "Salary"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	old, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Old"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := w.ArchiveCat.ArchiveCategory(ctx, old.ID); err != nil {
		t.Fatalf("ArchiveCategory: %v", err)
	}
	retired, err := w.CreateArea.CreateArea(ctx, catalogusecase.CreateAreaInput{Name: "Retired"})
	if err != nil {
		t.Fatalf("CreateArea: %v", err)
	}
	if _, err := w.ArchiveArea.ArchiveArea(ctx, retired.ID); err != nil {
		t.Fatalf("ArchiveArea: %v", err)
	}
	unrecorded := model.UnrecordedExpenseCategoryID

	tests := []struct {
		name        string
		area        uuid.UUID
		amount      string
		allocations []usecase.Allocation
		want        error
	}{
		{"an income category", w.work, "1000", []usecase.Allocation{allocation(salary.ID, "100")}, model.ErrWrongCategoryDirection},
		{"the system category of what was not recorded", w.work, "1000", []usecase.Allocation{allocation(unrecorded, "100")}, model.ErrSystemCategory},
		{"an archived category", w.work, "1000", []usecase.Allocation{allocation(old.ID, "100")}, model.ErrArchived},
		{"a category that does not exist", w.work, "1000", []usecase.Allocation{allocation(uuid.Must(uuid.NewV7()), "100")}, model.ErrNotFound},
		{"a category and its parent", w.work, "1000", []usecase.Allocation{allocation(w.lunch, "100"), allocation(w.food, "100")}, model.ErrParentAndChildAllocated},
		{"a category twice", w.work, "1000", []usecase.Allocation{allocation(w.food, "100"), allocation(w.food, "100")}, model.ErrDuplicateAllocation},
		{"allocations of more than the budget", w.work, "1000", []usecase.Allocation{allocation(w.food, "600"), allocation(w.rent, "400.01")}, model.ErrAllocationsExceedBudget},
		{"a budget of less than nothing", w.work, "-1", nil, model.ErrBudgetAmountNegative},
		{"a budget finer than the base currency", w.work, "100.001", nil, model.ErrTooManyDecimals},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: tt.area, Month: november, Amount: dec(tt.amount), Allocations: tt.allocations})
			if !errors.Is(err, tt.want) {
				t.Errorf("area: err = %v, want %v", err, tt.want)
			}
			_, err = w.SetProjectBudget.SetProjectBudget(ctx, usecase.SetProjectBudgetInput{ProjectID: w.trip, Amount: dec(tt.amount), Allocations: tt.allocations})
			if !errors.Is(err, tt.want) {
				t.Errorf("project: err = %v, want %v", err, tt.want)
			}
		})
	}

	// An area that is archived cannot be given a budget either.
	_, err = w.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: retired.ID, Month: november, Amount: dec("1")})
	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || !refErr.Problems[0].Archived {
		t.Errorf("an archived area: err = %v, want the catalog's *ReferenceError, archived", err)
	}
	if _, err := w.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: w.work, Amount: dec("1")}); !errors.Is(err, model.ErrMonthRequired) {
		t.Errorf("no month: err = %v, want ErrMonthRequired", err)
	}

	wantStatus(t, "the areas", w.areas(november))
	wantStatus(t, "the projects", w.projects(nil))

	// What is allowed is stored, and set again for the same month it is replaced,
	// allocations and all, and a month that has no allocation has none.
	w.setArea(w.work, november, "1000", allocation(w.food, "600"), allocation(w.rent, "400"))
	wantStatus(t, "the budget set", w.areas(november),
		"Work: budget 1000, spent 0 (0 with projects), allocations [Food 0 of 600; Rent 0 of 400], unallocated 0")
	w.setArea(w.work, dateOn(2026, time.November, 17), "2000", allocation(w.lunch, "50"))
	wantStatus(t, "the budget set again", w.areas(november),
		"Work: budget 2000, spent 0 (0 with projects), allocations [Lunch 0 of 50], unallocated 0")
}

// A budget is in force until a later one replaces it, and an area that is archived is
// listed with the one it had: what it spent still counts. The way to end it is a budget
// of nothing, which an archived area is given, and nothing else.
func TestTheBudgetOfAnAreaThatIsArchivedSinceCanBeEndedAndNothingElseThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	october, november, december := dateOn(2026, time.October, 1), dateOn(2026, time.November, 1), dateOn(2026, time.December, 1)
	june2030 := dateOn(2030, time.June, 1)

	// Home has 1,000 a month from October on, 300 of it for food. It spent 90 on food in
	// November; 22 on fun is dated in December, and confirmed already: it counts from its
	// day. Then the area is archived.
	w.setArea(w.home, october, "1000", allocation(w.food, "300"))
	w.spend(dateOn(2026, time.November, 5), &w.home, nil, w.cash, w.food, "90")
	w.spend(dateOn(2026, time.December, 3), &w.home, nil, w.cash, w.fun, "22")
	if _, err := w.ArchiveArea.ArchiveArea(ctx, w.home); err != nil {
		t.Fatalf("ArchiveArea: %v", err)
	}

	// The budget is in force still, and the area is listed with it, in every month after.
	wantStatus(t, "November", w.areas(november),
		"Home: budget 1000, spent 90 (90 with projects), allocations [Food 90 of 300], unallocated 0")
	wantStatus(t, "December, on the 14th of November", w.areas(december),
		"Home: budget 1000, spent 0 (0 with projects), allocations [Food 0 of 300], unallocated 0")
	wantStatus(t, "June 2030", w.areas(june2030),
		"Home: budget 1000, spent 0 (0 with projects), allocations [Food 0 of 300], unallocated 0")

	// What is set for it other than the end is refused as for any archived area, and
	// leaves the budget as it is: more than nothing, and nothing with an allocation.
	isArchived := func(err error) bool {
		var refErr *catalogusecase.ReferenceError
		return errors.As(err, &refErr) && len(refErr.Problems) == 1 && refErr.Problems[0].ID == w.home && refErr.Problems[0].Archived
	}
	for name, request := range map[string]usecase.SetAreaBudgetInput{
		"a budget of more than nothing":          {AreaID: w.home, Month: december, Amount: dec("500")},
		"a budget of nothing with an allocation": {AreaID: w.home, Month: december, Amount: dec("0"), Allocations: []usecase.Allocation{allocation(w.food, "100")}},
	} {
		if _, err := w.SetAreaBudget.SetAreaBudget(ctx, request); !isArchived(err) {
			t.Errorf("%s: err = %v, want the catalog's *ReferenceError for the area, archived", name, err)
		}
	}
	wantStatus(t, "December after what was refused", w.areas(december),
		"Home: budget 1000, spent 0 (0 with projects), allocations [Food 0 of 300], unallocated 0")

	// The end of it is not refused: from December on there is no budget.
	if _, err := w.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: w.home, Month: december, Amount: dec("0")}); err != nil {
		t.Fatalf("ending the budget of the archived area: %v", err)
	}
	w.clk.setDay(dateOn(2026, time.December, 10))
	wantStatus(t, "December", w.areas(december),
		"Home: budget none, spent 22 (22 with projects), allocations [], unallocated 22")
	wantStatus(t, "November still has it", w.areas(november),
		"Home: budget 1000, spent 90 (90 with projects), allocations [Food 90 of 300], unallocated 0")
	wantStatus(t, "June 2030: nothing is in force, and nothing was spent", w.areas(june2030))

	// And a budget of more than nothing is as refused as it was.
	if _, err := w.SetAreaBudget.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: w.home, Month: december, Amount: dec("500")}); !isArchived(err) {
		t.Errorf("a budget of 500 after the end: err = %v, want the catalog's *ReferenceError for the area, archived", err)
	}
	wantStatus(t, "December after it", w.areas(december),
		"Home: budget none, spent 22 (22 with projects), allocations [], unallocated 22")
}
