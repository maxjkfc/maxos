package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// budgetEnv is the ledger environment with the repo of the budgets, two areas and two
// projects, which are only ids (a budget and a transaction hold the id of a catalog
// entry, and the tables have no foreign key to it), and the expense categories food
// (with the subcategories lunch, dinner and other) and rent (with another one called
// other, so that a name is not enough to tell a category).
type budgetEnv struct {
	*ledgerEnv
	budgets                *postgres.BudgetRepo
	work, home, trip, move uuid.UUID
	rent                   model.Category
	lunch, dinner          model.Category
	foodOther, rentOther   model.Category
	keys                   int
}

func newBudgetEnv(t *testing.T) *budgetEnv {
	t.Helper()
	e := newLedgerEnv(t)
	categories := postgres.NewCategoryRepo(e.pool)
	rent := insertCategory(t, categories, model.DirectionExpense, nil, "rent")
	return &budgetEnv{
		ledgerEnv: e,
		budgets:   postgres.NewBudgetRepo(e.pool),
		work:      uuid.Must(uuid.NewV7()),
		home:      uuid.Must(uuid.NewV7()),
		trip:      uuid.Must(uuid.NewV7()),
		move:      uuid.Must(uuid.NewV7()),
		rent:      rent,
		lunch:     insertCategory(t, categories, model.DirectionExpense, &e.food.ID, "lunch"),
		dinner:    insertCategory(t, categories, model.DirectionExpense, &e.food.ID, "dinner"),
		foodOther: insertCategory(t, categories, model.DirectionExpense, &e.food.ID, "other"),
		rentOther: insertCategory(t, categories, model.DirectionExpense, &rent.ID, "other"),
	}
}

// label says what to call each id the tests print.
func (e *budgetEnv) label() map[uuid.UUID]string {
	names := e.names()
	names[e.work], names[e.home], names[e.trip], names[e.move] = "work", "home", "trip", "move"
	names[e.rent.ID], names[e.lunch.ID], names[e.dinner.ID] = "rent", "lunch", "dinner"
	names[e.foodOther.ID], names[e.rentOther.ID] = "food/other", "rent/other"
	return names
}

func alloc(c model.Category, amount string) model.Allocation {
	return model.Allocation{CategoryID: c.ID, Amount: dec(amount)}
}

// saveArea saves the budget of the area from the month as a use case does, in a
// database transaction of its own, and returns it as stored.
func (e *budgetEnv) saveArea(area uuid.UUID, from time.Time, amount string, allocations ...model.Allocation) model.AreaBudget {
	e.t.Helper()
	var stored model.AreaBudget
	err := e.inTx(func(ctx context.Context) error {
		var err error
		stored, err = e.budgets.SaveAreaBudget(ctx, model.AreaBudget{
			ID: uuid.Must(uuid.NewV7()), AreaID: area, EffectiveFrom: from, Amount: dec(amount), Allocations: allocations,
		})
		return err
	})
	if err != nil {
		e.t.Fatalf("SaveAreaBudget: %v", err)
	}
	return stored
}

// saveProject saves the budget of the project in a database transaction of its own.
func (e *budgetEnv) saveProject(project uuid.UUID, amount string, allocations ...model.Allocation) model.ProjectBudget {
	e.t.Helper()
	var stored model.ProjectBudget
	err := e.inTx(func(ctx context.Context) error {
		var err error
		stored, err = e.budgets.SaveProjectBudget(ctx, model.ProjectBudget{
			ID: uuid.Must(uuid.NewV7()), ProjectID: project, Amount: dec(amount), Allocations: allocations,
		})
		return err
	})
	if err != nil {
		e.t.Fatalf("SaveProjectBudget: %v", err)
	}
	return stored
}

// allocationLines writes the allocations as "category amount", in the order they come.
func (e *budgetEnv) allocationLines(allocations []model.Allocation) string {
	names := e.label()
	parts := make([]string, len(allocations))
	for i, a := range allocations {
		parts[i] = fmt.Sprintf("%s %s", names[a.CategoryID], a.Amount)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// areaBudgets returns the budgets of the areas, one to a line, as AreaBudgets lists
// them: "work 2026-10 5000 [food 2000, rent 1000]".
func (e *budgetEnv) areaBudgets() []string {
	e.t.Helper()
	got, err := e.budgets.AreaBudgets(context.Background())
	if err != nil {
		e.t.Fatalf("AreaBudgets: %v", err)
	}
	names := e.label()
	lines := make([]string, len(got))
	for i, b := range got {
		lines[i] = fmt.Sprintf("%s %s %s %s", names[b.AreaID], b.EffectiveFrom.Format("2006-01"), b.Amount, e.allocationLines(b.Allocations))
	}
	return lines
}

// projectBudgets returns the budgets of the projects, or of one, one to a line: "trip
// 80000 [food 20000]".
func (e *budgetEnv) projectBudgets(project *uuid.UUID) []string {
	e.t.Helper()
	got, err := e.budgets.ProjectBudgets(context.Background(), project)
	if err != nil {
		e.t.Fatalf("ProjectBudgets: %v", err)
	}
	names := e.label()
	lines := make([]string, len(got))
	for i, b := range got {
		lines[i] = fmt.Sprintf("%s %s %s", names[b.ProjectID], b.Amount, e.allocationLines(b.Allocations))
	}
	return lines
}

// dated is the header of a transaction on the day, of the area and the project when
// there are any, with a key of its own.
func (e *budgetEnv) dated(day time.Time, area, project *uuid.UUID) model.Header {
	e.keys++
	h := e.header(fmt.Sprintf("budget-test-%04d", e.keys), day)
	h.AreaID, h.ProjectID = area, project
	return h
}

// expenseOn makes the draft of an expense of the amount of TWD on the category, paid
// from cash.
func (e *budgetEnv) expenseOn(day time.Time, area, project *uuid.UUID, category model.Category, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(day, area, project), Account: &e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: category, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("NewExpense: %v", err)
	}
	return txn
}

// spend stores an expense of the amount of TWD, confirmed.
func (e *budgetEnv) spend(day time.Time, area, project *uuid.UUID, category model.Category, amount string) model.Transaction {
	e.t.Helper()
	return e.storeConfirmed(e.expenseOn(day, area, project, category, amount))
}

// spendingLine writes what was spent on a category as "owner category amount", with
// where the category is when it is a subcategory.
func (e *budgetEnv) spendingLine(owner uuid.UUID, s model.Spending) string {
	names := e.label()
	line := fmt.Sprintf("%s %s %s", names[owner], names[s.CategoryID], s.Amount)
	if s.ParentID != nil {
		line += " (under " + names[*s.ParentID] + ")"
	}
	return line
}

func (e *budgetEnv) areaSpending(from, to time.Time) []model.AreaSpending {
	e.t.Helper()
	got, err := e.budgets.AreaSpending(context.Background(), from, to)
	if err != nil {
		e.t.Fatalf("AreaSpending(%s, %s): %v", from.Format(time.DateOnly), to.Format(time.DateOnly), err)
	}
	return got
}

// areaLines is what was spent in the areas from the day from to the day to, one to a
// line, sorted: "work food 150.75", with " in a project" when it belongs to one.
func (e *budgetEnv) areaLines(from, to time.Time) []string {
	e.t.Helper()
	rows := e.areaSpending(from, to)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = e.spendingLine(r.AreaID, r.Spending)
		if r.InProject {
			lines[i] += " in a project"
		}
	}
	slices.Sort(lines)
	return lines
}

func (e *budgetEnv) projectSpending(project *uuid.UUID, to time.Time) []model.ProjectSpending {
	e.t.Helper()
	got, err := e.budgets.ProjectSpending(context.Background(), project, to)
	if err != nil {
		e.t.Fatalf("ProjectSpending(%s): %v", to.Format(time.DateOnly), err)
	}
	return got
}

// projectLines is what was spent in the projects up to the day, one to a line,
// sorted: "trip food 700".
func (e *budgetEnv) projectLines(project *uuid.UUID, to time.Time) []string {
	e.t.Helper()
	rows := e.projectSpending(project, to)
	lines := make([]string, len(rows))
	for i, r := range rows {
		lines[i] = e.spendingLine(r.ProjectID, r.Spending)
	}
	slices.Sort(lines)
	return lines
}

// wantLines fails the test unless the lines are the ones wanted, in any order: what
// is listed is compared as a set of lines, and the order is a test of its own where
// it matters.
func wantLines(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	got, want = slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// ---- The tables ----

func TestTheBudgetTableChecksWhatEachScopeNeeds(t *testing.T) {
	e := newBudgetEnv(t)
	area, project := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	first := nov(1)

	// A budget of each scope that the table takes, changed in one column at a time.
	type row struct {
		scope         string
		area, project *uuid.UUID
		effectiveFrom *time.Time
		amount        string
	}
	areaRow := row{scope: "area", area: &area, effectiveFrom: &first, amount: "100"}
	projectRow := row{scope: "project", project: &project, amount: "100"}
	with := func(r row, change func(r *row)) row {
		change(&r)
		return r
	}
	insert := func(r row) error {
		_, err := e.pool.Exec(context.Background(),
			`INSERT INTO fin_budget (id, scope, area_id, project_id, effective_from, amount) VALUES ($1, $2, $3, $4, $5, $6::numeric)`,
			uuid.Must(uuid.NewV7()), r.scope, r.area, r.project, r.effectiveFrom, r.amount)
		return err
	}
	midMonth := nov(15)

	tests := []struct {
		name       string
		row        row
		constraint string
	}{
		{"a scope that is not one", with(areaRow, func(r *row) { r.scope = "team" }), "fin_budget_scope_check"},
		{"an area budget without an area", with(areaRow, func(r *row) { r.area = nil }), "fin_budget_scope_columns_check"},
		{"an area budget without a month", with(areaRow, func(r *row) { r.effectiveFrom = nil }), "fin_budget_scope_columns_check"},
		{"an area budget with a project", with(areaRow, func(r *row) { r.project = &project }), "fin_budget_scope_columns_check"},
		{"a project budget without a project", with(projectRow, func(r *row) { r.project = nil }), "fin_budget_scope_columns_check"},
		{"a project budget with an area", with(projectRow, func(r *row) { r.area = &area }), "fin_budget_scope_columns_check"},
		{"a project budget with a month", with(projectRow, func(r *row) { r.effectiveFrom = &first }), "fin_budget_scope_columns_check"},
		{"a month that does not begin on the first day", with(areaRow, func(r *row) { r.effectiveFrom = &midMonth }), "fin_budget_effective_from_check"},
		{"an area budget of less than nothing", with(areaRow, func(r *row) { r.amount = "-0.01" }), "fin_budget_amount_check"},
		{"a project budget of less than nothing", with(projectRow, func(r *row) { r.amount = "-1" }), "fin_budget_amount_check"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantConstraint(t, insert(tt.row), checkViolation, tt.constraint)
		})
	}
	if got := e.count("SELECT count(*) FROM fin_budget"); got != 0 {
		t.Errorf("%d budgets stored, want none", got)
	}

	// What is not wrong: both scopes, and a budget of nothing, which is what ends the
	// budget of an area.
	for name, r := range map[string]row{
		"an area budget":              areaRow,
		"an area budget of nothing":   with(areaRow, func(r *row) { r.amount = "0"; r.effectiveFrom = new(nov(1).AddDate(0, 1, 0)) }),
		"a project budget":            projectRow,
		"a project budget of nothing": with(projectRow, func(r *row) { r.amount = "0"; r.project = new(uuid.Must(uuid.NewV7())) }),
	} {
		if err := insert(r); err != nil {
			t.Errorf("%s: %v, want it stored", name, err)
		}
	}
}

func TestTheBudgetTableHoldsOneBudgetForAnAreaAndMonthAndOneForAProject(t *testing.T) {
	e := newBudgetEnv(t)
	insertArea := func(area uuid.UUID, from time.Time) error {
		_, err := e.pool.Exec(context.Background(),
			`INSERT INTO fin_budget (id, scope, area_id, effective_from, amount) VALUES ($1, 'area', $2, $3, 100)`,
			uuid.Must(uuid.NewV7()), area, from)
		return err
	}
	insertProject := func(project uuid.UUID) error {
		_, err := e.pool.Exec(context.Background(),
			`INSERT INTO fin_budget (id, scope, project_id, amount) VALUES ($1, 'project', $2, 100)`,
			uuid.Must(uuid.NewV7()), project)
		return err
	}

	for _, err := range []error{
		insertArea(e.work, nov(1)),
		insertArea(e.work, nov(1).AddDate(0, 1, 0)), // the same area in another month
		insertArea(e.home, nov(1)),                  // another area in the same month
		insertProject(e.trip),
		insertProject(e.move),
	} {
		if err != nil {
			t.Fatalf("insert: %v, want budgets that do not clash to be stored", err)
		}
	}

	wantConstraint(t, insertArea(e.work, nov(1)), uniqueViolation, "fin_budget_area_id_effective_from_key")
	wantConstraint(t, insertProject(e.trip), uniqueViolation, "fin_budget_project_id_key")
}

func TestTheAllocationTableChecksAmountsKeysAndForeignKeysAndFollowsItsBudget(t *testing.T) {
	e := newBudgetEnv(t)
	budget := e.saveArea(e.work, nov(1), "1000")
	other := e.saveProject(e.trip, "1000")
	insert := func(budgetID, categoryID uuid.UUID, amount string) error {
		_, err := e.pool.Exec(context.Background(),
			`INSERT INTO fin_budget_allocation (budget_id, category_id, amount) VALUES ($1, $2, $3::numeric)`, budgetID, categoryID, amount)
		return err
	}
	unknown := uuid.Must(uuid.NewV7())

	wantConstraint(t, insert(budget.ID, e.food.ID, "0"), checkViolation, "fin_budget_allocation_amount_check")
	wantConstraint(t, insert(budget.ID, e.food.ID, "-1"), checkViolation, "fin_budget_allocation_amount_check")
	wantConstraint(t, insert(unknown, e.food.ID, "100"), foreignKeyViolation, "fin_budget_allocation_budget_id_fkey")
	wantConstraint(t, insert(budget.ID, unknown, "100"), foreignKeyViolation, "fin_budget_allocation_category_id_fkey")
	if err := insert(budget.ID, e.food.ID, "100"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	wantConstraint(t, insert(budget.ID, e.food.ID, "200"), uniqueViolation, "fin_budget_allocation_pkey")
	// The same category in another budget is another allocation.
	if err := insert(other.ID, e.food.ID, "100"); err != nil {
		t.Fatalf("insert into the other budget: %v", err)
	}

	// A budget takes its allocations with it, and only its own.
	if _, err := e.pool.Exec(context.Background(), `DELETE FROM fin_budget WHERE id = $1`, budget.ID); err != nil {
		t.Fatalf("delete the budget: %v", err)
	}
	if got := e.count("SELECT count(*) FROM fin_budget_allocation"); got != 1 {
		t.Errorf("%d allocations left, want only that of the other budget", got)
	}
}

// ---- Storing budgets ----

func TestSavingTheBudgetOfAnAreaStoresItWithItsAllocationsByCategoryNameAndReturnsItAsStored(t *testing.T) {
	e := newBudgetEnv(t)

	// Given in the opposite order of their names. The two categories called "other" are
	// told apart by their ids, which go by the order they were made in.
	stored := e.saveArea(e.work, nov(1), "30000", alloc(e.rent, "12000"), alloc(e.rentOther, "100"), alloc(e.food, "8000.5"), alloc(e.foodOther, "50"))

	if stored.ID == uuid.Nil || stored.CreatedAt.IsZero() {
		t.Errorf("stored = %+v, want the id and the time it was made", stored)
	}
	wantLines(t, "the budgets", e.areaBudgets(), "work 2026-11 30000 [food 8000.5, food/other 50, rent/other 100, rent 12000]")
	if got, _ := e.budgets.AreaBudgets(context.Background()); len(got) != 1 || !got[0].EffectiveFrom.Equal(nov(1)) || got[0].ID != stored.ID ||
		got[0].EffectiveFrom.Location() != time.UTC {
		t.Errorf("budgets = %+v, want the one stored, from 2026-11-01 as a date at midnight UTC", got)
	}
}

func TestSavingTheBudgetOfAMonthAgainReplacesItsAmountAndAllocationsAndLeavesTheOtherMonthsAlone(t *testing.T) {
	e := newBudgetEnv(t)
	october := e.saveArea(e.work, on(2026, time.October, 1), "5000", alloc(e.food, "2000"), alloc(e.rent, "1000"))
	e.saveArea(e.work, on(2026, time.December, 1), "9000", alloc(e.rent, "3000"))
	e.saveArea(e.home, on(2026, time.October, 1), "700")

	// October of work again: another amount, one allocation instead of two. The budget
	// given has an id of its own, which the row that is there does not take.
	again := e.saveArea(e.work, on(2026, time.October, 1), "6000", alloc(e.lunch, "300"))

	if again.ID != october.ID || !again.CreatedAt.Equal(october.CreatedAt) {
		t.Errorf("stored %s made at %s, want the row that was there: %s made at %s", again.ID, again.CreatedAt, october.ID, october.CreatedAt)
	}
	got := e.areaBudgets()
	wantLines(t, "the budgets", got,
		"work 2026-10 6000 [lunch 300]",
		"work 2026-12 9000 [rent 3000]",
		"home 2026-10 700 []",
	)
	if n := e.count("SELECT count(*) FROM fin_budget"); n != 3 {
		t.Errorf("%d budgets, want 3: replaced, not added", n)
	}
	if n := e.count("SELECT count(*) FROM fin_budget_allocation"); n != 2 {
		t.Errorf("%d allocations, want 2: the old ones of October are gone", n)
	}
}

func TestSavingTheBudgetOfAProjectReplacesTheOneItHas(t *testing.T) {
	e := newBudgetEnv(t)
	first := e.saveProject(e.trip, "80000", alloc(e.food, "20000"), alloc(e.rent, "30000"))
	e.saveProject(e.move, "5000")

	again := e.saveProject(e.trip, "90000", alloc(e.lunch, "1000"))

	if again.ID != first.ID || !again.CreatedAt.Equal(first.CreatedAt) {
		t.Errorf("stored %s made at %s, want the row that was there: %s made at %s", again.ID, again.CreatedAt, first.ID, first.CreatedAt)
	}
	wantLines(t, "the budgets of the projects", e.projectBudgets(nil), "trip 90000 [lunch 1000]", "move 5000 []")
	if n := e.count("SELECT count(*) FROM fin_budget WHERE scope = 'project'"); n != 2 {
		t.Errorf("%d budgets of projects, want 2", n)
	}
}

// A budget is written in several statements: the row, then its old allocations go and
// the new ones come in. They belong in one database transaction, so that a save that
// fails half way is not a budget of the new amount with no allocations.
func TestASaveThatFailsHalfWayLeavesTheBudgetAsItWas(t *testing.T) {
	e := newBudgetEnv(t)
	e.saveArea(e.work, nov(1), "1000", alloc(e.food, "300"))
	e.saveProject(e.trip, "2000", alloc(e.rent, "100"))
	missing := model.Allocation{CategoryID: uuid.Must(uuid.NewV7()), Amount: dec("1")} // the foreign key refuses it last

	err := e.inTx(func(ctx context.Context) error {
		_, err := e.budgets.SaveAreaBudget(ctx, model.AreaBudget{
			ID: uuid.Must(uuid.NewV7()), AreaID: e.work, EffectiveFrom: nov(1), Amount: dec("5000"),
			Allocations: []model.Allocation{alloc(e.rent, "100"), missing},
		})
		return err
	})
	wantConstraint(t, err, foreignKeyViolation, "fin_budget_allocation_category_id_fkey")
	err = e.inTx(func(ctx context.Context) error {
		_, err := e.budgets.SaveProjectBudget(ctx, model.ProjectBudget{
			ID: uuid.Must(uuid.NewV7()), ProjectID: e.trip, Amount: dec("9000"),
			Allocations: []model.Allocation{alloc(e.food, "100"), missing},
		})
		return err
	})
	wantConstraint(t, err, foreignKeyViolation, "fin_budget_allocation_category_id_fkey")

	wantLines(t, "the budgets of the areas", e.areaBudgets(), "work 2026-11 1000 [food 300]")
	wantLines(t, "the budgets of the projects", e.projectBudgets(nil), "trip 2000 [rent 100]")
}

func TestARemovedProjectBudgetGoesWithItsAllocationsAndRemovingWhatIsNotThereIsNotFound(t *testing.T) {
	e := newBudgetEnv(t)
	e.saveProject(e.trip, "80000", alloc(e.food, "20000"))
	e.saveProject(e.move, "5000", alloc(e.rent, "100"))
	e.saveArea(e.work, nov(1), "1000", alloc(e.food, "300"))

	if err := e.budgets.DeleteProjectBudget(context.Background(), e.trip); err != nil {
		t.Fatalf("DeleteProjectBudget: %v", err)
	}

	wantLines(t, "the budgets of the projects", e.projectBudgets(nil), "move 5000 [rent 100]")
	if n := e.count("SELECT count(*) FROM fin_budget_allocation"); n != 2 {
		t.Errorf("%d allocations, want 2: that of the removed budget gone, the others there", n)
	}
	if err := e.budgets.DeleteProjectBudget(context.Background(), e.trip); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("removing it again: err = %v, want ErrNotFound", err)
	}
	// An area is not a project, whatever the id: the budget of the area stays.
	if err := e.budgets.DeleteProjectBudget(context.Background(), e.work); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("removing the budget of an id that is an area's: err = %v, want ErrNotFound", err)
	}
	wantLines(t, "the budgets of the areas", e.areaBudgets(), "work 2026-11 1000 [food 300]")
}

func TestAreaBudgetsListsEveryBudgetOfEveryAreaByAreaThenMonthWithAndWithoutAllocations(t *testing.T) {
	e := newBudgetEnv(t)
	e.saveArea(e.home, on(2026, time.December, 1), "300")
	e.saveArea(e.work, on(2027, time.February, 1), "0")
	e.saveArea(e.work, on(2026, time.October, 1), "5000", alloc(e.food, "2000"))
	e.saveArea(e.home, on(2026, time.October, 1), "200", alloc(e.rent, "50"), alloc(e.food, "60"))
	e.saveProject(e.trip, "80000", alloc(e.food, "20000")) // a project's budget is not an area's

	got, err := e.budgets.AreaBudgets(context.Background())
	if err != nil {
		t.Fatalf("AreaBudgets: %v", err)
	}

	if len(got) != 4 {
		t.Fatalf("%d budgets, want the 4 of the areas, and not the project's", len(got))
	}
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if c := bytes.Compare(prev.AreaID[:], cur.AreaID[:]); c > 0 || (c == 0 && !prev.EffectiveFrom.Before(cur.EffectiveFrom)) {
			t.Errorf("budget %d (%s from %s) comes after budget %d (%s from %s), want them by area id and then by month",
				i, e.label()[prev.AreaID], prev.EffectiveFrom.Format("2006-01"), i+1, e.label()[cur.AreaID], cur.EffectiveFrom.Format("2006-01"))
		}
	}
	wantLines(t, "the budgets", e.areaBudgets(),
		"work 2026-10 5000 [food 2000]",
		"work 2027-02 0 []",
		"home 2026-10 200 [food 60, rent 50]",
		"home 2026-12 300 []",
	)
}

func TestProjectBudgetsCanBeLimitedToOneProject(t *testing.T) {
	e := newBudgetEnv(t)
	e.saveProject(e.trip, "80000", alloc(e.food, "20000"))
	e.saveProject(e.move, "5000")
	e.saveArea(e.work, nov(1), "1000")

	wantLines(t, "every project", e.projectBudgets(nil), "trip 80000 [food 20000]", "move 5000 []")
	only := e.trip
	wantLines(t, "one project", e.projectBudgets(&only), "trip 80000 [food 20000]")
	nothing := uuid.Must(uuid.NewV7())
	wantLines(t, "a project with no budget", e.projectBudgets(&nothing))
}

// ---- What was spent ----

func TestAreaSpendingAddsUpTheExpensesOfEachAreaAndCategoryAndSeparatesThoseOfAProject(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "120.5")
	e.spend(nov(3), &e.work, nil, e.food, "30.25")
	e.spend(nov(3), &e.work, nil, e.lunch, "80")
	e.spend(nov(4), &e.work, &e.trip, e.food, "700") // in the area and in a project
	e.spend(nov(4), &e.work, &e.move, e.food, "20")  // in another project, which is no matter
	e.spend(nov(4), &e.home, nil, e.food, "55")
	e.spend(nov(5), nil, nil, e.food, "999")     // in no area
	e.spend(nov(5), nil, &e.trip, e.food, "998") // in a project that has no area

	wantLines(t, "what the areas spent", e.areaLines(nov(1), nov(30)),
		"home food 55",
		"work food 150.75",
		"work food 720 in a project",
		"work lunch 80 (under food)",
	)

	// By area id, then category id, the expenses outside projects before those in one.
	rows := e.areaSpending(nov(1), nov(30))
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		c := cmpIDs(prev.AreaID, cur.AreaID)
		if c == 0 {
			c = cmpIDs(prev.CategoryID, cur.CategoryID)
		}
		if c == 0 && prev.InProject && !cur.InProject {
			c = 1
		}
		if c > 0 {
			t.Errorf("row %d (%s) comes after row %d (%s), want them by area, category, and outside projects first",
				i, e.spendingLine(prev.AreaID, prev.Spending), i+1, e.spendingLine(cur.AreaID, cur.Spending))
		}
	}
}

func cmpIDs(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }

func TestAreaSpendingTakesTheFirstAndTheLastDayOfTheRangeAndNeitherNeighbour(t *testing.T) {
	e := newBudgetEnv(t)
	// An amount for each day, so that what is counted is what the total adds up.
	e.spend(on(2026, time.October, 31), &e.work, nil, e.food, "1")
	e.spend(nov(1), &e.work, nil, e.food, "10")
	e.spend(nov(30), &e.work, nil, e.food, "100")
	e.spend(on(2026, time.December, 1), &e.work, nil, e.food, "1000")

	tests := []struct {
		name     string
		from, to time.Time
		want     string // "" is no spending
	}{
		{"the month: its first and last day, and not the days around it", nov(1), nov(30), "110"},
		{"to the day before the last", nov(1), nov(29), "10"},
		{"from the day after the first", nov(2), nov(30), "100"},
		{"the first day alone", nov(1), nov(1), "10"},
		{"the last day alone", nov(30), nov(30), "100"},
		{"the day before the month alone", on(2026, time.October, 31), on(2026, time.October, 31), "1"},
		{"the day after the month alone", on(2026, time.December, 1), on(2026, time.December, 1), "1000"},
		{"a day with nothing", nov(15), nov(15), ""},
		{"all of them", on(2026, time.October, 1), on(2026, time.December, 31), "1111"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.want == "" {
				wantLines(t, "spending", e.areaLines(tt.from, tt.to))
				return
			}
			wantLines(t, "spending", e.areaLines(tt.from, tt.to), "work food "+tt.want)
		})
	}
}

func TestSpendingCountsConfirmedTransactionsOnlyNotDraftsAndNotVoidOnes(t *testing.T) {
	e := newBudgetEnv(t)
	draft := e.store(e.expenseOn(nov(3), &e.work, &e.trip, e.food, "1"))
	e.spend(nov(4), &e.work, &e.trip, e.food, "10")
	voided := e.spend(nov(5), &e.work, &e.trip, e.food, "100")

	check := func(when string, area, project string) {
		t.Helper()
		wantLines(t, "area spending "+when, e.areaLines(nov(1), nov(30)), strings.Split(area, ",")...)
		wantLines(t, "project spending "+when, e.projectLines(nil, nov(30)), strings.Split(project, ",")...)
	}

	// The draft has not been confirmed, and counts nowhere.
	check("with a draft", "work food 110 in a project", "trip food 110")
	// Confirming it makes it count.
	e.setStatus(draft.ID, model.StatusConfirmed)
	check("once it is confirmed", "work food 111 in a project", "trip food 111")
	// Voiding takes it out of the figures again.
	e.setStatus(voided.ID, model.StatusVoid)
	check("with a void one", "work food 11 in a project", "trip food 11")
}

func TestSpendingDatedLaterIsNotCountedUntilThatDayHasBeenReached(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(14), &e.work, &e.trip, e.food, "10")
	e.spend(nov(20), &e.work, &e.trip, e.food, "100") // dated in the future, confirmed already

	tests := []struct {
		name string
		to   time.Time
		want string
	}{
		{"the day before it", nov(19), "10"},
		{"its own day", nov(20), "110"},
		{"the day after", nov(21), "110"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, "area spending", e.areaLines(nov(1), tt.to), "work food "+tt.want+" in a project")
			wantLines(t, "project spending", e.projectLines(nil, tt.to), "trip food "+tt.want)
		})
	}
}

func TestSpendingOfASubcategoryIsListedWithItsParentAndSoFallsIntoTheAllocationOfIt(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(nov(2), &e.work, nil, e.food, "1")
	e.spend(nov(2), &e.work, nil, e.lunch, "10")
	e.spend(nov(2), &e.work, nil, e.foodOther, "100")
	e.spend(nov(2), &e.work, &e.trip, e.rentOther, "1000")

	wantLines(t, "area spending", e.areaLines(nov(1), nov(30)),
		"work food 1",
		"work lunch 10 (under food)",
		"work food/other 100 (under food)",
		"work rent/other 1000 (under rent) in a project",
	)
	wantLines(t, "project spending", e.projectLines(nil, nov(30)), "trip rent/other 1000 (under rent)")

	// What the rows come with is what the domain needs to put them in an allocation: the
	// rows as the database returns them, of the expenses that are not a project's, fall
	// into the allocation of food, which takes lunch and the other of food with it, and
	// leave the allocation of rent, which the project's expense is not in, with nothing.
	var outsideProjects []model.Spending
	for _, row := range e.areaSpending(nov(1), nov(30)) {
		if !row.InProject {
			outsideProjects = append(outsideProjects, row.Spending)
		}
	}
	budget := dec("5000")
	area := model.NewBudgetStatus(&budget, []model.Allocation{alloc(e.food, "2000"), alloc(e.rent, "500")}, outsideProjects)
	if !area.Allocations[0].Spent.Equal(dec("111")) || !area.Allocations[1].Spent.IsZero() || !area.Unallocated.IsZero() {
		t.Errorf("area: spent into food %s, into rent %s, unallocated %s, want 111, 0 and 0", area.Allocations[0].Spent, area.Allocations[1].Spent, area.Unallocated)
	}
	// The project's own expense, of a subcategory of rent, falls into the allocation of rent.
	var ofTheTrip []model.Spending
	for _, row := range e.projectSpending(nil, nov(30)) {
		ofTheTrip = append(ofTheTrip, row.Spending)
	}
	project := model.NewBudgetStatus(&budget, []model.Allocation{alloc(e.rent, "1500")}, ofTheTrip)
	if !project.Allocations[0].Spent.Equal(dec("1000")) || !project.Unallocated.IsZero() {
		t.Errorf("project: spent into rent %s, unallocated %s, want 1000 and 0", project.Allocations[0].Spent, project.Unallocated)
	}
}

func TestFeesAndInterestAreSpentLikeAnyOtherExpenseOnTheDayTheyAreCharged(t *testing.T) {
	e := newBudgetEnv(t)

	// A fee of 15 that comes with a transfer, which is not an expense itself.
	transfer, err := model.NewTransfer(model.TransferParams{
		Header: e.dated(nov(3), &e.work, nil), From: e.bank, To: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("1000"),
		Fees: []model.Fee{{Account: e.bank, Instrument: e.twd, Category: e.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	e.storeConfirmed(transfer)

	// An expense of 12,000 paid in 12 periods from December: all of it is spent on the
	// day of the purchase, and each period's interest on the day it falls due. The
	// first period has 30 of it.
	periods := make([]model.InstalmentPeriod, 12)
	for i := range periods {
		periods[i] = model.InstalmentPeriod{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.Month(12+i), 5), Principal: dec("1000")}
	}
	periods[0].Interest = decPtr("30")
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: e.dated(nov(14), &e.work, nil), Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("12000")}},
	}, model.InstalmentSchedule{Periods: periods, InterestCategory: &e.interest})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	e.storePlan(plan)
	e.setPlanStatus(plan, model.StatusConfirmed)

	// A conversion of a debt into a plan is not an expense: it has no expense posting.
	bill, err := model.NewBillInstalment(model.BillInstalmentParams{
		Header: e.dated(nov(16), &e.work, nil), Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd, Principal: dec("3000"),
		Schedule: model.InstalmentSchedule{Periods: []model.InstalmentPeriod{
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 20), Principal: dec("1000")},
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 20), Principal: dec("2000")},
		}},
	})
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}
	e.storePlan(bill)
	e.setPlanStatus(bill, model.StatusConfirmed)

	wantLines(t, "November", e.areaLines(nov(1), nov(30)), "work fees 15", "work food 12000")
	wantLines(t, "December, the day before the interest falls due", e.areaLines(on(2026, time.December, 1), on(2026, time.December, 4)))
	wantLines(t, "December, on the day", e.areaLines(on(2026, time.December, 1), on(2026, time.December, 5)), "work interest 30")
	wantLines(t, "the months after", e.areaLines(on(2027, time.January, 1), on(2027, time.December, 31)))
}

func TestAnExpenseOfAReconciliationIsSpentOnTheSystemCategoryWithNoParent(t *testing.T) {
	e := newBudgetEnv(t)
	reconciliation, err := model.Reconcile(model.ReconcileParams{
		Header: e.dated(nov(10), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		RecordedBalance: dec("1000"), ActualBalance: dec("880"),
	})
	if err != nil || reconciliation.Transaction == nil {
		t.Fatalf("Reconcile = %+v, %v, want a draft of the 120 that is missing", reconciliation, err)
	}
	e.storeConfirmed(*reconciliation.Transaction)

	wantLines(t, "area spending", e.areaLines(nov(1), nov(30)), "work unrecorded 120")
}

func TestASplitExpenseSpendsOnlyMyShare(t *testing.T) {
	e := newBudgetEnv(t)
	// 500 is paid from the card: 300 is mine, and A owes me 200.
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), &e.work, nil), Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines:  []model.CategoryLine{{Category: e.food, Amount: dec("300")}},
		Shares: []model.Share{{PartyID: e.partyA, Amount: dec("200")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(txn)

	wantLines(t, "area spending", e.areaLines(nov(1), nov(30)), "work food 300")
}

func TestAnExpenseInAForeignCurrencyIsSpentByItsAmountInTheBaseCurrency(t *testing.T) {
	e := newBudgetEnv(t)
	// 100 USD that was 3,200 TWD on the day, and a second one, on another day, of 3,100.
	for day, base := range map[int]string{3: "3200", 4: "3100"} {
		txn, err := model.NewExpense(model.ExpenseParams{
			Header: e.dated(nov(day), &e.work, &e.trip), Account: &e.card, Instrument: e.usd, BaseCurrency: e.twd,
			Lines: []model.CategoryLine{{Category: e.food, Amount: dec("100"), BaseAmount: decPtr(base)}},
		})
		if err != nil {
			t.Fatalf("NewExpense: %v", err)
		}
		e.storeConfirmed(txn)
	}

	wantLines(t, "area spending", e.areaLines(nov(1), nov(30)), "work food 6300 in a project")
	wantLines(t, "project spending", e.projectLines(nil, nov(30)), "trip food 6300")
}

func TestARewardADiscountAndAnIncomeDoNotReduceWhatWasSpent(t *testing.T) {
	e := newBudgetEnv(t)
	// 450 was paid, after the merchant took 50 off at checkout: the expense is 450.
	discounted, err := model.NewExpense(model.ExpenseParams{
		Header: e.dated(nov(3), &e.work, nil), Account: &e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("450"), Discount: decPtr("50")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(discounted)
	wantLines(t, "area spending with the discount", e.areaLines(nov(1), nov(30)), "work food 450")

	// The same day, in the same area: a reward of 50 and an income of 5,000.
	reward, err := model.NewReward(model.RewardParams{
		Header: e.dated(nov(3), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	e.storeConfirmed(reward)
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.dated(nov(3), &e.work, nil), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("5000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.storeConfirmed(income)

	wantLines(t, "area spending with the reward and the income", e.areaLines(nov(1), nov(30)), "work food 450")
}

func TestProjectSpendingAddsUpTheExpensesOfEachProjectWhateverTheDayUpToTheDayGiven(t *testing.T) {
	e := newBudgetEnv(t)
	e.spend(on(2025, time.March, 1), nil, &e.trip, e.food, "1") // long ago, and in no area
	e.spend(nov(2), &e.work, &e.trip, e.food, "10")             // in an area too
	e.spend(nov(3), &e.home, &e.trip, e.lunch, "100")
	e.spend(nov(3), nil, &e.move, e.food, "1000")
	e.spend(nov(3), &e.work, nil, e.food, "9999") // in no project

	wantLines(t, "every project", e.projectLines(nil, nov(30)),
		"trip food 11",
		"trip lunch 100 (under food)",
		"move food 1000",
	)
	trip := e.trip
	wantLines(t, "one project", e.projectLines(&trip, nov(30)), "trip food 11", "trip lunch 100 (under food)")
	nothing := uuid.Must(uuid.NewV7())
	wantLines(t, "a project with no expense", e.projectLines(&nothing, nov(30)))
	wantLines(t, "up to a day before them all", e.projectLines(nil, on(2025, time.February, 28)))
	wantLines(t, "up to the day of the first", e.projectLines(nil, on(2025, time.March, 1)), "trip food 1")

	// By project id and then category id.
	rows := e.projectSpending(nil, nov(30))
	for i := 1; i < len(rows); i++ {
		prev, cur := rows[i-1], rows[i]
		if c := cmpIDs(prev.ProjectID, cur.ProjectID); c > 0 || (c == 0 && cmpIDs(prev.CategoryID, cur.CategoryID) > 0) {
			t.Errorf("row %d (%s) comes after row %d (%s), want them by project and then category",
				i, e.spendingLine(prev.ProjectID, prev.Spending), i+1, e.spendingLine(cur.ProjectID, cur.Spending))
		}
	}
}

// ---- Concurrency ----

// Every save begins with the statement that stores the row of the budget, which locks
// it until the database transaction ends, so that saves of the one month are one after
// the other, each in whole: what is left is what one of them set, not the amount of one
// with the allocations of another.
func TestSavesOfTheBudgetOfOneMonthAtTheSameTimeLeaveWhatOneOfThemSetWhole(t *testing.T) {
	e := newBudgetEnv(t)
	categories := []model.Category{e.food, e.rent, e.lunch, e.dinner, e.foodOther, e.rentOther}
	callers := len(categories)
	// Caller i sets 1000+i, with 100 for the category i and 200 for the next: no two
	// callers set the same allocations, and the amount says which one it was.
	allocationsOf := func(i int) []model.Allocation {
		return []model.Allocation{alloc(categories[i], "100"), alloc(categories[(i+1)%callers], "200")}
	}
	// holding runs a save in a database transaction of its own, which it keeps open a
	// moment after the save, so that the others reach theirs while the row is locked. It
	// has a limit of its own: a call that is stuck ends with an error, and the test does
	// not hang.
	holding := func(save func(ctx context.Context) error) error {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return e.db.WithinTx(ctx, func(ctx context.Context) error {
			if err := save(ctx); err != nil {
				return err
			}
			time.Sleep(5 * time.Millisecond)
			return nil
		})
	}

	for round := range 10 {
		start := make(chan struct{})
		errs := make([]error, 2*callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(2)
			go func() {
				defer wg.Done()
				<-start
				errs[i] = holding(func(ctx context.Context) error {
					_, err := e.budgets.SaveAreaBudget(ctx, model.AreaBudget{
						ID: uuid.Must(uuid.NewV7()), AreaID: e.work, EffectiveFrom: nov(1), Amount: dec(fmt.Sprint(1000 + i)), Allocations: allocationsOf(i),
					})
					return err
				})
			}()
			go func() {
				defer wg.Done()
				<-start
				errs[callers+i] = holding(func(ctx context.Context) error {
					_, err := e.budgets.SaveProjectBudget(ctx, model.ProjectBudget{
						ID: uuid.Must(uuid.NewV7()), ProjectID: e.trip, Amount: dec(fmt.Sprint(1000 + i)), Allocations: allocationsOf(i),
					})
					return err
				})
			}()
		}
		close(start)
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("round %d, call %d: %v", round, i, err)
			}
		}

		ctx := context.Background()
		areaBudgets, err := e.budgets.AreaBudgets(ctx)
		if err != nil {
			t.Fatalf("AreaBudgets: %v", err)
		}
		projectBudgets, err := e.budgets.ProjectBudgets(ctx, nil)
		if err != nil {
			t.Fatalf("ProjectBudgets: %v", err)
		}
		if len(areaBudgets) != 1 || len(projectBudgets) != 1 {
			t.Fatalf("round %d: %d budgets of the area and %d of the project, want one of each", round, len(areaBudgets), len(projectBudgets))
		}
		for what, got := range map[string]struct {
			amount      int64
			allocations []model.Allocation
		}{
			"area":    {areaBudgets[0].Amount.IntPart(), areaBudgets[0].Allocations},
			"project": {projectBudgets[0].Amount.IntPart(), projectBudgets[0].Allocations},
		} {
			i := int(got.amount) - 1000
			if i < 0 || i >= callers {
				t.Fatalf("round %d, %s: the amount is %d, want the amount one of the callers set", round, what, got.amount)
			}
			want := map[uuid.UUID]string{categories[i].ID: "100", categories[(i+1)%callers].ID: "200"}
			if len(got.allocations) != len(want) {
				t.Fatalf("round %d, %s: %d allocations, want the 2 that caller %d set with its amount", round, what, len(got.allocations), i)
			}
			for _, a := range got.allocations {
				if w, ok := want[a.CategoryID]; !ok || !a.Amount.Equal(dec(w)) {
					t.Errorf("round %d, %s: allocation of %s is %s, want the allocations of caller %d, whose amount it has", round, what, e.label()[a.CategoryID], a.Amount, i)
				}
			}
		}
		if n := e.count("SELECT count(*) FROM fin_budget_allocation"); n != 4 {
			t.Fatalf("round %d: %d allocations stored, want the 2 of each of the two budgets", round, n)
		}
	}
}
