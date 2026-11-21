package budget_test

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/budget"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// taipei is the timezone of the application in the tests: ahead of UTC, so that the
// first hours of a day there are still the day before in UTC.
var taipei = time.FixedZone("UTC+8", 8*60*60)

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

// day is a date, as midnight UTC, the way the callers of the use cases give one.
func day(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC)
}

// fakeClock is a clock.Clock that tells the time a test sets.
type fakeClock struct{ now time.Time }

func (f *fakeClock) Now() time.Time { return f.now }

// setDay makes it noon in Taipei on the day.
func (f *fakeClock) setDay(year int, month time.Month, d int) {
	f.now = time.Date(year, month, d, 12, 0, 0, 0, taipei)
}

// txKey marks a context that belongs to a database transaction.
type txKey struct{}

// errNotInTx is what the fake repo returns for a write that was not made in a
// database transaction: the use cases must make all their writes in one.
var errNotInTx = errors.New("the write was made outside a database transaction")

// fakeTx is a tx.Transactor that runs the function at once and marks the context
// it passes, so that the fake repo can tell a write made inside the database
// transaction from one made outside. It does not undo anything on an error: the
// use cases must not rely on a rollback for what they can check before they write.
type fakeTx struct{ calls int }

func (f *fakeTx) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	f.calls++
	return fn(context.WithValue(ctx, txKey{}, true))
}

func inTx(ctx context.Context) bool { return ctx.Value(txKey{}) != nil }

// spendRow is an expense posting of a confirmed transaction that happened on a day,
// which the fake repo adds up as the real one does.
type spendRow struct {
	on       time.Time
	area     *uuid.UUID
	project  *uuid.UUID
	category model.Category
	amount   decimal.Decimal
}

// fakeBudgets is an in-memory repo.BudgetRepo. Its budgets are kept as they are
// saved; what was spent is the rows a test adds, which it filters by day and by
// area and project as the statements of the real repo do.
type fakeBudgets struct {
	areaBudgets    []model.AreaBudget
	projectBudgets []model.ProjectBudget
	spending       []spendRow

	// saves counts the budgets stored, new or replaced.
	saves int
	// areaSpendingAsked is each range of days AreaSpending was asked for, and
	// projectSpendingAsked the last day each call of ProjectSpending was given, with
	// the project it was limited to, or nil, in projectFilters.
	areaSpendingAsked    [][2]time.Time
	projectSpendingAsked []time.Time
	projectFilters       []*uuid.UUID
	// projectBudgetReads counts the calls of ProjectBudgets.
	projectBudgetReads int
}

func (f *fakeBudgets) SaveAreaBudget(ctx context.Context, b model.AreaBudget) (model.AreaBudget, error) {
	if !inTx(ctx) {
		return model.AreaBudget{}, errNotInTx
	}
	f.saves++
	for i, old := range f.areaBudgets {
		if old.AreaID == b.AreaID && old.EffectiveFrom.Equal(b.EffectiveFrom) {
			b.ID, b.CreatedAt = old.ID, old.CreatedAt
			f.areaBudgets[i] = b
			return b, nil
		}
	}
	b.CreatedAt = time.Date(2026, 1, 1, 0, 0, len(f.areaBudgets)+1, 0, time.UTC)
	f.areaBudgets = append(f.areaBudgets, b)
	return b, nil
}

func (f *fakeBudgets) SaveProjectBudget(ctx context.Context, b model.ProjectBudget) (model.ProjectBudget, error) {
	if !inTx(ctx) {
		return model.ProjectBudget{}, errNotInTx
	}
	f.saves++
	for i, old := range f.projectBudgets {
		if old.ProjectID == b.ProjectID {
			b.ID, b.CreatedAt = old.ID, old.CreatedAt
			f.projectBudgets[i] = b
			return b, nil
		}
	}
	b.CreatedAt = time.Date(2026, 1, 1, 0, 0, len(f.projectBudgets)+1, 0, time.UTC)
	f.projectBudgets = append(f.projectBudgets, b)
	return b, nil
}

func (f *fakeBudgets) DeleteProjectBudget(_ context.Context, projectID uuid.UUID) error {
	for i, b := range f.projectBudgets {
		if b.ProjectID == projectID {
			f.projectBudgets = slices.Delete(f.projectBudgets, i, i+1)
			return nil
		}
	}
	return model.ErrNotFound
}

// AreaBudgets returns every budget by area id and then by the month it takes effect
// in, as the real repo does, each with its allocations in the order they were saved
// (the real repo lists them by the name of the category).
func (f *fakeBudgets) AreaBudgets(context.Context) ([]model.AreaBudget, error) {
	out := slices.Clone(f.areaBudgets)
	slices.SortFunc(out, func(a, b model.AreaBudget) int {
		return cmp.Or(slices.Compare(a.AreaID[:], b.AreaID[:]), a.EffectiveFrom.Compare(b.EffectiveFrom))
	})
	return out, nil
}

func (f *fakeBudgets) ProjectBudgets(_ context.Context, projectID *uuid.UUID) ([]model.ProjectBudget, error) {
	f.projectBudgetReads++
	var out []model.ProjectBudget
	for _, b := range f.projectBudgets {
		if projectID == nil || b.ProjectID == *projectID {
			out = append(out, b)
		}
	}
	return out, nil
}

// AreaSpending returns the rows that have an area and happened from the day from to
// the day to, both days included. It does not add them up: the use case does not
// depend on that, and a test can give two expenses of one category.
func (f *fakeBudgets) AreaSpending(_ context.Context, from, to time.Time) ([]model.AreaSpending, error) {
	f.areaSpendingAsked = append(f.areaSpendingAsked, [2]time.Time{from, to})
	var out []model.AreaSpending
	for _, row := range f.spending {
		if row.area == nil || row.on.Before(from) || row.on.After(to) {
			continue
		}
		out = append(out, model.AreaSpending{
			AreaID:    *row.area,
			InProject: row.project != nil,
			Spending:  model.Spending{CategoryID: row.category.ID, ParentID: row.category.ParentID, Amount: row.amount},
		})
	}
	return out, nil
}

// ProjectSpending returns the rows that belong to a project, of the project asked
// for when there is one, that happened up to the day to, that day included.
func (f *fakeBudgets) ProjectSpending(_ context.Context, projectID *uuid.UUID, to time.Time) ([]model.ProjectSpending, error) {
	f.projectSpendingAsked = append(f.projectSpendingAsked, to)
	f.projectFilters = append(f.projectFilters, projectID)
	var out []model.ProjectSpending
	for _, row := range f.spending {
		if row.project == nil || row.on.After(to) || (projectID != nil && *row.project != *projectID) {
			continue
		}
		out = append(out, model.ProjectSpending{
			ProjectID: *row.project,
			Spending:  model.Spending{CategoryID: row.category.ID, ParentID: row.category.ParentID, Amount: row.amount},
		})
	}
	return out, nil
}

// fakeCategories is an in-memory repo.CategoryRepo of which only Get is used.
type fakeCategories struct {
	repo.CategoryRepo

	categories map[uuid.UUID]model.Category
}

func (f *fakeCategories) Get(_ context.Context, id uuid.UUID) (model.Category, error) {
	c, ok := f.categories[id]
	if !ok {
		return model.Category{}, model.ErrNotFound
	}
	return c, nil
}

// fakeInstruments is a repo.InstrumentRepo of which only Get is used: it has the
// base currency, TWD, with two decimal places.
type fakeInstruments struct{ repo.InstrumentRepo }

func (fakeInstruments) Get(_ context.Context, id uuid.UUID) (model.Instrument, error) {
	if id != model.BaseCurrencyID {
		return model.Instrument{}, model.ErrNotFound
	}
	return model.Instrument{ID: id, Code: model.BaseCurrencyCode, Name: "新台幣", Kind: model.InstrumentKindFiat, IsCurrency: true, Decimals: 2}, nil
}

// fakeCatalog is the catalog as the budgets see it, with some areas and projects.
// As a catalogusecase.VerifyReferences, an id it does not know is not found and one
// it knows as archived is archived. As a catalogusecase.ListAreas and ListProjects
// it lists them by name, as the catalog does, those in use or, with
// IncludeArchived, the archived ones too.
type fakeCatalog struct {
	areas    map[uuid.UUID]catalogusecase.Area
	projects map[uuid.UUID]catalogusecase.Project

	// verifies is every VerifyReferencesInput it was asked about, and listAreasCalls
	// and listProjectsCalls count the calls of the listings; includeArchived is
	// what each listing was asked for.
	verifies          []catalogusecase.VerifyReferencesInput
	listAreasCalls    int
	listProjectsCalls int
	includeArchived   []bool
	// verifyErr and listErr, when set, are what VerifyReferences and the two
	// listings return.
	verifyErr error
	listErr   error
}

func newFakeCatalog() *fakeCatalog {
	return &fakeCatalog{
		areas:    map[uuid.UUID]catalogusecase.Area{},
		projects: map[uuid.UUID]catalogusecase.Project{},
	}
}

func (f *fakeCatalog) VerifyReferences(_ context.Context, in catalogusecase.VerifyReferencesInput) error {
	f.verifies = append(f.verifies, in)
	if f.verifyErr != nil {
		return f.verifyErr
	}
	var problems []catalogusecase.ReferenceProblem
	for _, id := range in.ProjectIDs {
		p, exists := f.projects[id]
		if !exists || p.ArchivedAt != nil {
			problems = append(problems, catalogusecase.ReferenceProblem{Kind: catalogusecase.ReferenceProject, ID: id, Archived: exists})
		}
	}
	for _, id := range in.AreaIDs {
		a, exists := f.areas[id]
		if !exists || a.ArchivedAt != nil {
			problems = append(problems, catalogusecase.ReferenceProblem{Kind: catalogusecase.ReferenceArea, ID: id, Archived: exists})
		}
	}
	if len(problems) > 0 {
		return &catalogusecase.ReferenceError{Problems: problems}
	}
	return nil
}

func (f *fakeCatalog) ListAreas(_ context.Context, in catalogusecase.ListAreasInput) (catalogusecase.ListAreasOutput, error) {
	f.listAreasCalls++
	f.includeArchived = append(f.includeArchived, in.IncludeArchived)
	if f.listErr != nil {
		return catalogusecase.ListAreasOutput{}, f.listErr
	}
	var out catalogusecase.ListAreasOutput
	for _, a := range f.areas {
		if a.ArchivedAt == nil || in.IncludeArchived {
			out.Areas = append(out.Areas, a)
		}
	}
	slices.SortFunc(out.Areas, func(a, b catalogusecase.Area) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	return out, nil
}

func (f *fakeCatalog) ListProjects(_ context.Context, in catalogusecase.ListProjectsInput) (catalogusecase.ListProjectsOutput, error) {
	f.listProjectsCalls++
	f.includeArchived = append(f.includeArchived, in.IncludeArchived)
	if f.listErr != nil {
		return catalogusecase.ListProjectsOutput{}, f.listErr
	}
	var out catalogusecase.ListProjectsOutput
	for _, p := range f.projects {
		if p.ArchivedAt == nil || in.IncludeArchived {
			out.Projects = append(out.Projects, p)
		}
	}
	slices.SortFunc(out.Projects, func(a, b catalogusecase.Project) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID.String(), b.ID.String()))
	})
	return out, nil
}

// env is the budget use cases on fakes, with the helpers the tests build their
// world with.
type env struct {
	t          *testing.T
	svc        *budget.Service
	clock      *fakeClock
	txs        *fakeTx
	budgets    *fakeBudgets
	categories *fakeCategories
	catalog    *fakeCatalog
}

// newEnv makes the use cases on empty fakes. It is noon on the 14th of November
// 2026 in Taipei.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{
		t:          t,
		clock:      &fakeClock{},
		txs:        &fakeTx{},
		budgets:    &fakeBudgets{},
		categories: &fakeCategories{categories: map[uuid.UUID]model.Category{}},
		catalog:    newFakeCatalog(),
	}
	e.clock.setDay(2026, time.November, 14)
	e.svc = budget.New(e.txs, e.budgets, e.categories, fakeInstruments{}, e.catalog, e.catalog, e.catalog, e.clock)
	return e
}

// area adds an area to the catalog, archived or not.
func (e *env) area(name string, archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	a := catalogusecase.Area{ID: id, Name: name}
	if archived {
		a.ArchivedAt = &time.Time{}
	}
	e.catalog.areas[id] = a
	return id
}

// archiveArea archives an area of the catalog.
func (e *env) archiveArea(id uuid.UUID) {
	a := e.catalog.areas[id]
	a.ArchivedAt = &time.Time{}
	e.catalog.areas[id] = a
}

// project adds a project to the catalog, archived or not.
func (e *env) project(name string, archived bool) uuid.UUID {
	id := uuid.Must(uuid.NewV7())
	p := catalogusecase.Project{ID: id, Name: name}
	if archived {
		p.ArchivedAt = &time.Time{}
	}
	e.catalog.projects[id] = p
	return id
}

// archiveProject archives a project of the catalog.
func (e *env) archiveProject(id uuid.UUID) {
	p := e.catalog.projects[id]
	p.ArchivedAt = &time.Time{}
	e.catalog.projects[id] = p
}

// category adds an expense category, a subcategory of the parent when there is one.
func (e *env) category(name string, parent *model.Category) model.Category {
	c := model.Category{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: name}
	if parent != nil {
		c.ParentID = &parent.ID
	}
	e.categories.categories[c.ID] = c
	return c
}

// addCategory adds the category, as it is, which is how a test gets an archived one,
// an income one or a system one.
func (e *env) addCategory(c model.Category) model.Category {
	e.categories.categories[c.ID] = c
	return c
}

// spend adds an expense of the amount to the category on the day: in the area, when
// there is one, and in the project, when there is one.
func (e *env) spend(on time.Time, area, project *uuid.UUID, category model.Category, amount string) {
	e.budgets.spending = append(e.budgets.spending, spendRow{on: on, area: area, project: project, category: category, amount: dec(amount)})
}

func allocation(c model.Category, amount string) usecase.Allocation {
	return usecase.Allocation{CategoryID: c.ID, Amount: dec(amount)}
}

// setArea sets the budget of the area for the month, and fails the test when it
// cannot.
func (e *env) setArea(area uuid.UUID, month time.Time, amount string, allocations ...usecase.Allocation) usecase.AreaBudget {
	e.t.Helper()
	got, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{
		AreaID: area, Month: month, Amount: dec(amount), Allocations: allocations,
	})
	if err != nil {
		e.t.Fatalf("SetAreaBudget(%s): %v", month.Format("2006-01"), err)
	}
	return got
}

// setProject sets the budget of the project, and fails the test when it cannot.
func (e *env) setProject(project uuid.UUID, amount string, allocations ...usecase.Allocation) usecase.ProjectBudget {
	e.t.Helper()
	got, err := e.svc.SetProjectBudget(context.Background(), usecase.SetProjectBudgetInput{
		ProjectID: project, Amount: dec(amount), Allocations: allocations,
	})
	if err != nil {
		e.t.Fatalf("SetProjectBudget: %v", err)
	}
	return got
}

// areas returns where the areas stand in the month, and fails the test when it
// cannot.
func (e *env) areas(month time.Time) []usecase.AreaBudgetStatus {
	e.t.Helper()
	out, err := e.svc.GetAreaBudgets(context.Background(), usecase.GetAreaBudgetsInput{Month: month})
	if err != nil {
		e.t.Fatalf("GetAreaBudgets(%s): %v", month.Format("2006-01"), err)
	}
	return out.Areas
}

// areaOf returns where the area stands in the month; it fails the test when the
// area is not among those listed.
func (e *env) areaOf(area uuid.UUID, month time.Time) usecase.AreaBudgetStatus {
	e.t.Helper()
	for _, status := range e.areas(month) {
		if status.AreaID == area {
			return status
		}
	}
	e.t.Fatalf("area %s is not listed for %s", area, month.Format("2006-01"))
	return usecase.AreaBudgetStatus{}
}

// projects returns where the projects stand, and fails the test when it cannot.
func (e *env) projects() []usecase.ProjectBudgetStatus {
	e.t.Helper()
	out, err := e.svc.GetProjectBudgets(context.Background(), usecase.GetProjectBudgetsInput{})
	if err != nil {
		e.t.Fatalf("GetProjectBudgets: %v", err)
	}
	return out.Projects
}

// projectOf returns where the project stands; it fails the test when the project is
// not among those listed.
func (e *env) projectOf(project uuid.UUID) usecase.ProjectBudgetStatus {
	e.t.Helper()
	for _, status := range e.projects() {
		if status.ProjectID == project {
			return status
		}
	}
	e.t.Fatalf("project %s is not listed", project)
	return usecase.ProjectBudgetStatus{}
}

// wantMoney fails the test unless the amount is the one wanted.
func wantMoney(t *testing.T, what string, got decimal.Decimal, want string) {
	t.Helper()
	if !got.Equal(dec(want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}
