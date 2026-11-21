package budget_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

func TestSetAreaBudgetIsFromTheFirstDayOfTheMonthTheRequestFallsInAndStoresItsAllocations(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	rent := e.category("房租", nil)

	// Any day of November names it, whatever the time of day and the zone it is given in.
	for _, month := range []time.Time{day(2026, 11, 1), day(2026, 11, 17), time.Date(2026, 11, 30, 23, 0, 0, 0, taipei)} {
		got, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{
			AreaID: work, Month: month, Amount: dec("30000"),
			Allocations: []usecase.Allocation{allocation(food, "8000"), allocation(rent, "12000.50")},
		})
		if err != nil {
			t.Fatalf("SetAreaBudget(%s): %v", month, err)
		}

		if got.AreaID != work || !got.From.Equal(day(2026, 11, 1)) {
			t.Errorf("budget = %+v, want the budget of the work area from 2026-11-01", got)
		}
		wantMoney(t, "amount", got.Amount, "30000")
		if len(got.Allocations) != 2 || got.Allocations[0].CategoryID != food.ID || got.Allocations[1].CategoryID != rent.ID {
			t.Fatalf("allocations = %+v, want food and then rent", got.Allocations)
		}
		wantMoney(t, "the allocation of food", got.Allocations[0].Amount, "8000")
		wantMoney(t, "the allocation of rent", got.Allocations[1].Amount, "12000.50")
	}
	if len(e.budgets.areaBudgets) != 1 {
		t.Errorf("%d budgets stored, want the one month's budget replaced each time", len(e.budgets.areaBudgets))
	}

	// 00:30 on the 1st of December in Taipei is December, though it is still November
	// in UTC.
	e.setArea(work, time.Date(2026, 12, 1, 0, 30, 0, 0, taipei), "1000")
	if len(e.budgets.areaBudgets) != 2 || !e.budgets.areaBudgets[1].EffectiveFrom.Equal(day(2026, 12, 1)) {
		t.Errorf("budgets = %+v, want a second one from 2026-12-01", e.budgets.areaBudgets)
	}
}

func TestSetAreaBudgetChecksTheAreaWithTheCatalogAndWritesInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)

	// The fake repo refuses a write made outside a database transaction, so that the
	// budget is stored says it was made in one.
	e.setArea(work, day(2026, 11, 1), "30000", allocation(food, "8000"))

	if len(e.catalog.verifies) != 1 || len(e.catalog.verifies[0].AreaIDs) != 1 || e.catalog.verifies[0].AreaIDs[0] != work ||
		len(e.catalog.verifies[0].ProjectIDs) != 0 {
		t.Errorf("the catalog was asked %+v, want it to be asked about the one area", e.catalog.verifies)
	}
	if e.txs.calls != 1 || len(e.budgets.areaBudgets) != 1 {
		t.Errorf("%d database transactions and %d budgets stored, want 1 and 1", e.txs.calls, len(e.budgets.areaBudgets))
	}
}

func TestSettingTheBudgetOfAMonthAgainReplacesThatMonthAndLeavesTheOtherMonthsAlone(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	home := e.area("家庭", false)
	food := e.category("飲食", nil)
	e.setArea(work, day(2026, 10, 1), "5000", allocation(food, "2000"))
	e.setArea(work, day(2026, 12, 1), "9000")
	e.setArea(home, day(2026, 10, 1), "3000")
	original := e.budgets.areaBudgets[0]

	// October again, any day of it: a new amount and no allocation.
	e.setArea(work, day(2026, 10, 20), "6000")

	if len(e.budgets.areaBudgets) != 3 {
		t.Fatalf("%d budgets, want 3: the October one of work replaced, none added", len(e.budgets.areaBudgets))
	}
	october := e.budgets.areaBudgets[0]
	if october.ID != original.ID || !october.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("the budget of October is %s made at %s, want the one that was there: %s made at %s",
			october.ID, october.CreatedAt, original.ID, original.CreatedAt)
	}
	wantMoney(t, "the amount of October", october.Amount, "6000")
	if len(october.Allocations) != 0 {
		t.Errorf("allocations of October = %+v, want them replaced by none", october.Allocations)
	}
	wantMoney(t, "the amount of December", e.budgets.areaBudgets[1].Amount, "9000")
	wantMoney(t, "the amount of the other area", e.budgets.areaBudgets[2].Amount, "3000")
}

func TestSetAreaBudgetRefusesAnAreaThatIsNotThereOrIsArchivedAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	archived := e.area("舊的", true)
	tests := []struct {
		name         string
		area         uuid.UUID
		wantArchived bool
	}{
		{"an area the catalog does not have", uuid.Must(uuid.NewV7()), false},
		{"an archived area", archived, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{AreaID: tt.area, Month: day(2026, 11, 1), Amount: dec("100")})

			var refErr *catalogusecase.ReferenceError
			if !errors.As(err, &refErr) || len(refErr.Problems) != 1 ||
				refErr.Problems[0].Kind != catalogusecase.ReferenceArea || refErr.Problems[0].ID != tt.area || refErr.Problems[0].Archived != tt.wantArchived {
				t.Fatalf("err = %v, want the catalog's *ReferenceError for the area, archived %v", err, tt.wantArchived)
			}
			if e.budgets.saves != 0 {
				t.Errorf("%d budgets stored, want none", e.budgets.saves)
			}
		})
	}
}

// A budget is in force until a later one replaces it, and an area that is archived is
// listed with the one it had: what it spent still counts. If the budget could not be
// ended it would be there for ever. What ends it is a budget of nothing, which is not a
// new use of the area, so an archived area is given that, and nothing else; the catalog
// is still asked, for the area has to be there.
func TestTheBudgetOfAnAreaThatIsArchivedSinceCanBeEndedAndNothingElseCanBeSetForIt(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	work := e.area("工作", false)
	food := e.category("飲食", nil)
	e.setArea(work, day(2026, 10, 1), "1000", allocation(food, "300"))
	e.archiveArea(work)

	// It is in force still, and the area is listed with it, in any month after.
	for _, month := range []time.Time{day(2026, 12, 1), day(2030, 6, 1)} {
		got := e.areaOf(work, month)
		if got.Budget == nil {
			t.Fatalf("%s: no budget, want the one set for 2026-10: it is in force until another replaces it", month.Format("2006-01"))
		}
		wantMoney(t, "the budget in "+month.Format("2006-01"), *got.Budget, "1000")
	}
	stored := e.budgets.saves

	// What is set for it other than the end is refused, as for any archived area, and
	// nothing is stored: more than nothing, less than nothing, and nothing with an
	// allocation, which is no end of the budget but a share of one that is not there.
	refused := []struct {
		name        string
		amount      string
		allocations []usecase.Allocation
	}{
		{"a budget of more than nothing", "500", nil},
		{"a budget of less than nothing", "-1", nil},
		{"a budget of nothing with an allocation", "0", []usecase.Allocation{allocation(food, "1")}},
		{"a budget with its allocations", "500", []usecase.Allocation{allocation(food, "100")}},
	}
	for _, tt := range refused {
		_, err := e.svc.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{
			AreaID: work, Month: day(2026, 12, 1), Amount: dec(tt.amount), Allocations: tt.allocations,
		})

		var refErr *catalogusecase.ReferenceError
		if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || refErr.Problems[0].ID != work || !refErr.Problems[0].Archived {
			t.Errorf("%s: err = %v, want the catalog's *ReferenceError for the area, archived", tt.name, err)
		}
	}
	if e.budgets.saves != stored {
		t.Fatalf("%d budgets stored by what was refused, want none", e.budgets.saves-stored)
	}

	// The end of it is not refused, and the catalog is asked about the area all the same.
	asked := len(e.catalog.verifies)
	ended, err := e.svc.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: work, Month: day(2026, 12, 17), Amount: dec("0")})
	if err != nil {
		t.Fatalf("SetAreaBudget of nothing for the archived area: %v, want the budget ended: that is no new use of the area", err)
	}
	if ended.AreaID != work || !ended.From.Equal(day(2026, 12, 1)) || !ended.Amount.IsZero() || len(ended.Allocations) != 0 {
		t.Errorf("budget = %+v, want the budget of nothing of the work area from 2026-12-01", ended)
	}
	if got := e.catalog.verifies[asked:]; len(got) != 1 || len(got[0].AreaIDs) != 1 || got[0].AreaIDs[0] != work {
		t.Errorf("the catalog was asked %+v, want it asked once, about the area", got)
	}

	// From December on the area has no budget; the months before have what they had,
	// and long after there is nothing in force and nothing spent, so no area at all.
	e.spend(day(2026, 11, 5), &work, nil, food, "90")
	e.spend(day(2026, 12, 3), &work, nil, food, "22")
	e.clock.setDay(2026, time.December, 10)
	october := e.areaOf(work, day(2026, 10, 1))
	if october.Budget == nil {
		t.Fatal("October has no budget, want the one it had: the end does not reach back")
	}
	wantMoney(t, "October: the budget", *october.Budget, "1000")
	november := e.areaOf(work, day(2026, 11, 1))
	if november.Budget == nil {
		t.Fatal("November has no budget, want the one it had: the end does not reach back")
	}
	wantMoney(t, "November: the budget", *november.Budget, "1000")
	wantMoney(t, "November: spent", november.Spent, "90")
	december := e.areaOf(work, day(2026, 12, 1))
	if december.Budget != nil {
		t.Errorf("December: budget = %s, want none: it was ended from then", december.Budget)
	}
	wantMoney(t, "December: spent", december.Spent, "22")
	if got := e.areas(day(2030, 6, 1)); len(got) != 0 {
		t.Errorf("June 2030: %+v, want no area: no budget is in force and nothing was spent", got)
	}

	// Once it is ended, a budget of more than nothing is as refused as it was.
	_, err = e.svc.SetAreaBudget(ctx, usecase.SetAreaBudgetInput{AreaID: work, Month: day(2026, 12, 1), Amount: dec("500")})
	var refErr *catalogusecase.ReferenceError
	if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || !refErr.Problems[0].Archived {
		t.Errorf("err = %v, want the catalog's *ReferenceError for the area, archived", err)
	}
}

// Ending a budget is no way round the check that the area is there: the catalog is
// asked, and an area it does not have is refused for a budget of nothing too, and a
// catalog that fails does not make the area archived.
func TestTheBudgetOfAnAreaIsEndedOnlyOnceTheCatalogHasSaidTheAreaIsThere(t *testing.T) {
	t.Run("an area the catalog does not have", func(t *testing.T) {
		e := newEnv(t)
		unknown := uuid.Must(uuid.NewV7())

		_, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{AreaID: unknown, Month: day(2026, 12, 1), Amount: dec("0")})

		var refErr *catalogusecase.ReferenceError
		if !errors.As(err, &refErr) || len(refErr.Problems) != 1 || refErr.Problems[0].ID != unknown || refErr.Problems[0].Archived {
			t.Fatalf("err = %v, want the catalog's *ReferenceError for the area, not archived: it is not there", err)
		}
		if e.budgets.saves != 0 {
			t.Errorf("%d budgets stored, want none", e.budgets.saves)
		}
	})
	t.Run("a catalog that fails", func(t *testing.T) {
		e := newEnv(t)
		work := e.area("工作", false)
		e.archiveArea(work)
		boom := errors.New("the catalog is down")
		e.catalog.verifyErr = boom

		_, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{AreaID: work, Month: day(2026, 12, 1), Amount: dec("0")})

		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want what the catalog returned", err)
		}
		if e.budgets.saves != 0 {
			t.Errorf("%d budgets stored, want none", e.budgets.saves)
		}
	})
}

// refusal is a budget that is refused whichever kind it is: an amount and the
// allocations, of categories that refusals makes.
type refusal struct {
	name        string
	amount      string
	allocations []usecase.Allocation
	want        error
	mention     string // what the message must name; empty for nothing in particular
}

func refusals(e *env) []refusal {
	food := e.category("飲食", nil)
	lunch := e.category("午餐", &food)
	rent := e.category("房租", nil)
	archived := e.addCategory(model.Category{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: "舊的", ArchivedAt: &time.Time{}})
	income := e.addCategory(model.Category{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionIncome, Name: "薪水"})
	system := e.addCategory(model.Category{ID: model.UnrecordedExpenseCategoryID, Direction: model.DirectionExpense, Name: "未記錄"})
	missing := model.Category{ID: uuid.Must(uuid.NewV7())} // not in the repo

	return []refusal{
		{"less than nothing", "-1", nil, model.ErrBudgetAmountNegative, ""},
		{"finer than the base currency", "100.001", nil, model.ErrTooManyDecimals, ""},
		{"a category that does not exist", "10000", []usecase.Allocation{allocation(food, "100"), allocation(missing, "100")}, model.ErrNotFound, "allocation 2"},
		{"an archived category", "10000", []usecase.Allocation{allocation(archived, "100")}, model.ErrArchived, "allocation 1"},
		{"an income category", "10000", []usecase.Allocation{allocation(income, "100")}, model.ErrWrongCategoryDirection, "allocation 1"},
		{"a system category", "10000", []usecase.Allocation{allocation(system, "100")}, model.ErrSystemCategory, "allocation 1"},
		{"the same category twice", "10000", []usecase.Allocation{allocation(food, "100"), allocation(rent, "100"), allocation(food, "100")}, model.ErrDuplicateAllocation, "allocation 3"},
		{"a category and its parent", "10000", []usecase.Allocation{allocation(lunch, "100"), allocation(food, "100")}, model.ErrParentAndChildAllocated, "allocation 1"},
		{"allocations that add up to more than the budget", "10000", []usecase.Allocation{allocation(food, "6000"), allocation(rent, "4000.01")}, model.ErrAllocationsExceedBudget, "10000.01"},
		{"an allocation of nothing", "10000", []usecase.Allocation{allocation(food, "0")}, model.ErrAmountNotPositive, "allocation 1"},
		{"an allocation finer than the base currency", "10000", []usecase.Allocation{allocation(food, "1.005")}, model.ErrTooManyDecimals, "allocation 1"},
	}
}

func TestSetAreaBudgetRefusesWhatTheRulesRefuseAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)
	for _, tt := range refusals(e) {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{
				AreaID: work, Month: day(2026, 11, 1), Amount: dec(tt.amount), Allocations: tt.allocations,
			})

			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("err = %v, want %v naming %q", err, tt.want, tt.mention)
			}
			if e.budgets.saves != 0 {
				t.Errorf("%d budgets stored, want none", e.budgets.saves)
			}
		})
	}
}

func TestSetAreaBudgetNeedsAMonth(t *testing.T) {
	e := newEnv(t)
	work := e.area("工作", false)

	_, err := e.svc.SetAreaBudget(context.Background(), usecase.SetAreaBudgetInput{AreaID: work, Amount: dec("100")})

	if !errors.Is(err, model.ErrMonthRequired) {
		t.Fatalf("err = %v, want ErrMonthRequired", err)
	}
	if e.budgets.saves != 0 || len(e.catalog.verifies) != 0 {
		t.Errorf("%d budgets stored and the catalog asked %d times, want neither: there is nothing to check without a month", e.budgets.saves, len(e.catalog.verifies))
	}
}

func TestSetProjectBudgetStoresOneBudgetForTheProjectAndReplacesItWhenSetAgain(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	food := e.category("飲食", nil)
	stay := e.category("住宿", nil)

	first := e.setProject(trip, "80000", allocation(food, "20000"), allocation(stay, "30000"))

	if first.ProjectID != trip || len(first.Allocations) != 2 {
		t.Fatalf("budget = %+v, want the budget of the trip with two allocations", first)
	}
	wantMoney(t, "amount", first.Amount, "80000")
	if len(e.catalog.verifies) != 1 || len(e.catalog.verifies[0].ProjectIDs) != 1 || e.catalog.verifies[0].ProjectIDs[0] != trip ||
		len(e.catalog.verifies[0].AreaIDs) != 0 {
		t.Errorf("the catalog was asked %+v, want it to be asked about the one project", e.catalog.verifies)
	}
	original := e.budgets.projectBudgets[0]

	// Set again: a new amount and one allocation, replacing all that was.
	second := e.setProject(trip, "90000", allocation(food, "25000"))

	if len(e.budgets.projectBudgets) != 1 {
		t.Fatalf("%d budgets of projects, want the one replaced", len(e.budgets.projectBudgets))
	}
	stored := e.budgets.projectBudgets[0]
	if stored.ID != original.ID {
		t.Errorf("the budget is %s, want the one that was there: %s", stored.ID, original.ID)
	}
	wantMoney(t, "amount", second.Amount, "90000")
	if len(second.Allocations) != 1 || len(stored.Allocations) != 1 || stored.Allocations[0].CategoryID != food.ID {
		t.Errorf("allocations = %+v, want only that of food", stored.Allocations)
	}
	if e.txs.calls != 2 {
		t.Errorf("%d database transactions, want one for each call", e.txs.calls)
	}
}

func TestSetProjectBudgetRefusesAProjectThatIsNotThereOrIsArchivedAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	archived := e.project("結束了", true)
	tests := []struct {
		name         string
		project      uuid.UUID
		wantArchived bool
	}{
		{"a project the catalog does not have", uuid.Must(uuid.NewV7()), false},
		{"an archived project", archived, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.SetProjectBudget(context.Background(), usecase.SetProjectBudgetInput{ProjectID: tt.project, Amount: dec("100")})

			var refErr *catalogusecase.ReferenceError
			if !errors.As(err, &refErr) || len(refErr.Problems) != 1 ||
				refErr.Problems[0].Kind != catalogusecase.ReferenceProject || refErr.Problems[0].Archived != tt.wantArchived {
				t.Fatalf("err = %v, want the catalog's *ReferenceError for the project, archived %v", err, tt.wantArchived)
			}
			if e.budgets.saves != 0 {
				t.Errorf("%d budgets stored, want none", e.budgets.saves)
			}
		})
	}
}

func TestSetProjectBudgetRefusesWhatTheRulesRefuseAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	for _, tt := range refusals(e) {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.SetProjectBudget(context.Background(), usecase.SetProjectBudgetInput{
				ProjectID: trip, Amount: dec(tt.amount), Allocations: tt.allocations,
			})

			if !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.mention) {
				t.Errorf("err = %v, want %v naming %q", err, tt.want, tt.mention)
			}
			if e.budgets.saves != 0 {
				t.Errorf("%d budgets stored, want none", e.budgets.saves)
			}
		})
	}
}

func TestARemovedProjectBudgetIsGoneAndRemovingItAgainIsNotFound(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	other := e.project("搬家", false)
	food := e.category("飲食", nil)
	e.setProject(trip, "80000", allocation(food, "20000"))
	e.setProject(other, "5000")

	if err := e.svc.RemoveProjectBudget(context.Background(), usecase.RemoveProjectBudgetInput{ProjectID: trip}); err != nil {
		t.Fatalf("RemoveProjectBudget: %v", err)
	}

	got := e.projects()
	if len(got) != 1 || got[0].ProjectID != other {
		t.Errorf("projects = %+v, want only the other one, whose budget stays", got)
	}
	err := e.svc.RemoveProjectBudget(context.Background(), usecase.RemoveProjectBudgetInput{ProjectID: trip})
	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), trip.String()) {
		t.Errorf("removing it again: err = %v, want ErrNotFound naming the project", err)
	}
}

func TestTheBudgetOfAnArchivedProjectCanBeRemoved(t *testing.T) {
	e := newEnv(t)
	trip := e.project("日本旅行", false)
	e.setProject(trip, "80000")
	e.archiveProject(trip) // the trip is over, and archived since
	asked := len(e.catalog.verifies)

	if err := e.svc.RemoveProjectBudget(context.Background(), usecase.RemoveProjectBudgetInput{ProjectID: trip}); err != nil {
		t.Fatalf("RemoveProjectBudget: %v", err)
	}

	if len(e.budgets.projectBudgets) != 0 {
		t.Errorf("%d budgets of projects left, want none", len(e.budgets.projectBudgets))
	}
	if len(e.catalog.verifies) != asked {
		t.Error("the catalog was asked about the project, want taking a budget away to be none of its concern")
	}
}
