package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// newCategory returns a category ready to be stored, under parent when it is set.
// It skips model.NewCategory on purpose, so that the tests can store what the
// model would refuse: the table is the last guard of the category rules.
func newCategory(direction model.Direction, parent *uuid.UUID, name string) model.Category {
	return model.Category{ID: uuid.Must(uuid.NewV7()), Direction: direction, ParentID: parent, Name: name}
}

// insertCategory stores a new category and returns it as stored.
func insertCategory(t *testing.T, r *postgres.CategoryRepo, direction model.Direction, parent *uuid.UUID, name string) model.Category {
	t.Helper()
	stored, err := r.Insert(context.Background(), newCategory(direction, parent, name))
	if err != nil {
		t.Fatalf("Insert %s category %q: %v", direction, name, err)
	}
	return stored
}

// categoryNames returns the names of the categories, without the system
// categories that migration 0005 seeds in every database: the tests that list
// what they inserted are about those.
func categoryNames(categories []model.Category) []string {
	names := make([]string, 0, len(categories))
	for _, category := range categories {
		if !category.IsSystem() {
			names = append(names, category.Name)
		}
	}
	return names
}

func sameNames(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestCategoryRoundTripsItsDirectionAndParent(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	top := insertCategory(t, r, model.DirectionExpense, nil, "餐飲")
	if top.Direction != model.DirectionExpense || top.ParentID != nil || top.ArchivedAt != nil || top.CreatedAt.IsZero() {
		t.Fatalf("top-level = %+v, want an expense, with no parent, in use, with the time the database assigned", top)
	}

	sub := newCategory(model.DirectionExpense, &top.ID, "早餐")
	stored, err := r.Insert(ctx, sub)
	if err != nil {
		t.Fatalf("Insert subcategory: %v", err)
	}
	got, err := r.Get(ctx, sub.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for name, c := range map[string]model.Category{"Insert": stored, "Get": got} {
		if c.ID != sub.ID || c.Name != "早餐" || c.Direction != model.DirectionExpense ||
			c.ParentID == nil || *c.ParentID != top.ID || c.ArchivedAt != nil || c.CreatedAt.IsZero() {
			t.Errorf("%s = %+v, want the subcategory of %s", name, c, top.ID)
		}
	}

	income := insertCategory(t, r, model.DirectionIncome, nil, "薪資")
	if income.Direction != model.DirectionIncome {
		t.Fatalf("income = %+v, want direction income", income)
	}
}

func TestCategoryTopLevelNamesAreUniquePerDirection(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	first := insertCategory(t, r, model.DirectionExpense, nil, "其他")

	// parent_id is NULL for both, and the constraint still sees them as equal.
	if _, err := r.Insert(ctx, newCategory(model.DirectionExpense, nil, "其他")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second top-level insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newCategory(model.DirectionExpense, nil, "其他")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived category: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Insert(ctx, newCategory(model.DirectionIncome, nil, "其他")); err != nil {
		t.Fatalf("same name in the other direction: %v, want it stored", err)
	}
}

func TestCategorySubcategoryNamesAreUniqueUnderTheirParent(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	food := insertCategory(t, r, model.DirectionExpense, nil, "餐飲")
	travel := insertCategory(t, r, model.DirectionExpense, nil, "旅遊")
	insertCategory(t, r, model.DirectionExpense, &food.ID, "其他")

	if _, err := r.Insert(ctx, newCategory(model.DirectionExpense, &food.ID, "其他")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("same name under the same parent: err = %v, want ErrAlreadyExists", err)
	}
	if _, err := r.Insert(ctx, newCategory(model.DirectionExpense, &travel.ID, "其他")); err != nil {
		t.Fatalf("same name under another parent: %v, want it stored", err)
	}
	if _, err := r.Insert(ctx, newCategory(model.DirectionExpense, nil, "其他")); err != nil {
		t.Fatalf("same name as a top-level category: %v, want it stored", err)
	}
}

func TestCategoryParentRulesAreKeptByTheDatabase(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	top := insertCategory(t, r, model.DirectionExpense, nil, "餐飲")
	sub := insertCategory(t, r, model.DirectionExpense, &top.ID, "早餐")
	unknown := uuid.Must(uuid.NewV7())

	tests := []struct {
		name     string
		category model.Category
	}{
		{"a category under a subcategory", newCategory(model.DirectionExpense, &sub.ID, "加蛋")},
		{"a subcategory with the other direction than its parent", newCategory(model.DirectionIncome, &top.ID, "獎金")},
		{"a subcategory of an unknown parent", newCategory(model.DirectionExpense, &unknown, "x")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := r.Insert(ctx, tt.category)
			wantConstraint(t, err, foreignKeyViolation, "fin_category_parent_fkey")

			if _, err := r.Get(ctx, tt.category.ID); !errors.Is(err, model.ErrNotFound) {
				t.Fatalf("Get after the refused insert: err = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestCategoryDirectionIsCheckedByTheDatabase(t *testing.T) {
	r := postgres.NewCategoryRepo(testpg.New(t))

	_, err := r.Insert(context.Background(), newCategory("transfer", nil, "x"))
	wantConstraint(t, err, checkViolation, "fin_category_direction_check")
}

func TestCategoryMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	if _, err := r.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Archive(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Archive: err = %v, want ErrNotFound", err)
	}
}

func TestCategoryArchiveTakesTheSubcategoriesAlong(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	food := insertCategory(t, r, model.DirectionExpense, nil, "餐飲")
	breakfast := insertCategory(t, r, model.DirectionExpense, &food.ID, "早餐")
	lunch := insertCategory(t, r, model.DirectionExpense, &food.ID, "午餐")
	travel := insertCategory(t, r, model.DirectionExpense, nil, "旅遊")
	trip := insertCategory(t, r, model.DirectionExpense, &travel.ID, "機票")

	// A subcategory archived earlier keeps the time it was archived.
	earlier, err := r.Archive(ctx, lunch.ID)
	if err != nil {
		t.Fatalf("Archive subcategory: %v", err)
	}
	if earlier.ID != lunch.ID || earlier.ArchivedAt == nil {
		t.Fatalf("Archive(lunch) = %+v, want the subcategory itself, archived", earlier)
	}

	archived, err := r.Archive(ctx, food.ID)
	if err != nil {
		t.Fatalf("Archive parent: %v", err)
	}
	if archived.ID != food.ID || archived.ArchivedAt == nil {
		t.Fatalf("Archive(food) = %+v, want the parent itself, archived", archived)
	}

	for _, id := range []uuid.UUID{food.ID, breakfast.ID, lunch.ID} {
		got, err := r.Get(ctx, id)
		if err != nil || got.ArchivedAt == nil {
			t.Fatalf("Get(%s) = %+v, %v, want it archived", id, got, err)
		}
	}
	if got, _ := r.Get(ctx, lunch.ID); !got.ArchivedAt.Equal(*earlier.ArchivedAt) {
		t.Fatalf("lunch archived at %v, want the first time %v", got.ArchivedAt, earlier.ArchivedAt)
	}
	for _, id := range []uuid.UUID{travel.ID, trip.ID} {
		if got, err := r.Get(ctx, id); err != nil || got.ArchivedAt != nil {
			t.Fatalf("Get(%s) = %+v, %v, want it untouched, in use", id, got, err)
		}
	}

	again, err := r.Archive(ctx, food.ID)
	if err != nil {
		t.Fatalf("second Archive of the parent: %v", err)
	}
	if again.ArchivedAt == nil || !again.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", again.ArchivedAt, archived.ArchivedAt)
	}
}

func TestCategoryArchiveOfASubcategoryLeavesItsSiblingsAndParent(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	food := insertCategory(t, r, model.DirectionExpense, nil, "餐飲")
	breakfast := insertCategory(t, r, model.DirectionExpense, &food.ID, "早餐")
	lunch := insertCategory(t, r, model.DirectionExpense, &food.ID, "午餐")

	if _, err := r.Archive(ctx, breakfast.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	for id, wantArchived := range map[uuid.UUID]bool{food.ID: false, breakfast.ID: true, lunch.ID: false} {
		got, err := r.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		if got.IsArchived() != wantArchived {
			t.Errorf("category %q archived = %v, want %v", got.Name, got.IsArchived(), wantArchived)
		}
	}
}

func TestCategoryListFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	insertCategory(t, r, model.DirectionExpense, nil, "Transport")
	food := insertCategory(t, r, model.DirectionExpense, nil, "Food")
	insertCategory(t, r, model.DirectionExpense, &food.ID, "Breakfast")
	insertCategory(t, r, model.DirectionIncome, nil, "Salary")
	old := insertCategory(t, r, model.DirectionIncome, nil, "Old job")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	tests := []struct {
		name            string
		direction       model.Direction
		includeArchived bool
		want            []string
	}{
		{"both directions in use", "", false, []string{"Breakfast", "Food", "Transport", "Salary"}},
		{"both directions with the archived", "", true, []string{"Breakfast", "Food", "Transport", "Old job", "Salary"}},
		{"expenses", model.DirectionExpense, false, []string{"Breakfast", "Food", "Transport"}},
		{"expenses with the archived", model.DirectionExpense, true, []string{"Breakfast", "Food", "Transport"}},
		{"incomes in use", model.DirectionIncome, false, []string{"Salary"}},
		{"incomes with the archived", model.DirectionIncome, true, []string{"Old job", "Salary"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.List(ctx, tt.direction, tt.includeArchived)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if names := categoryNames(got); !sameNames(names, tt.want...) {
				t.Fatalf("List(%q, %v) = %v, want %v", tt.direction, tt.includeArchived, names, tt.want)
			}
		})
	}
}

func TestTheSystemCategoriesAreSeededUnderTheIdsOfTheModelAndNeverArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))
	system := map[uuid.UUID]model.Direction{
		model.UnrecordedExpenseCategoryID: model.DirectionExpense,
		model.UnrecordedIncomeCategoryID:  model.DirectionIncome,
	}

	for id, direction := range system {
		got, err := r.Get(ctx, id)
		if err != nil {
			t.Fatalf("Get(%s): %v, want the system category of %s seeded under the id of the model", id, err, direction)
		}
		if got.Name != "未記錄" || got.Direction != direction || got.ParentID != nil || got.IsArchived() || !got.IsSystem() {
			t.Errorf("category = %+v, want the top-level category 未記錄 of %s, in use", got, direction)
		}

		// Another top-level category of that name cannot be added next to it.
		if _, err := r.Insert(ctx, newCategory(direction, nil, "未記錄")); !errors.Is(err, model.ErrAlreadyExists) {
			t.Errorf("Insert of a second 未記錄 of %s: err = %v, want ErrAlreadyExists", direction, err)
		}

		// The table keeps it in use whatever asks for it to be archived.
		_, err = r.Archive(ctx, id)
		wantConstraint(t, err, checkViolation, "fin_category_system_not_archived_check")
		if got, err := r.Get(ctx, id); err != nil || got.IsArchived() {
			t.Errorf("Get(%s) = %+v, %v, want it still in use", id, got, err)
		}
	}

	// They are listed with every other category of their direction.
	listed, err := r.List(ctx, "", false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := 0
	for _, c := range listed {
		if c.IsSystem() {
			found++
		}
	}
	if found != len(system) {
		t.Errorf("List has %d system categories, want %d", found, len(system))
	}
}

func TestTheSystemCategoriesHaveNoSubcategories(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewCategoryRepo(testpg.New(t))

	for id, direction := range map[uuid.UUID]model.Direction{
		model.UnrecordedExpenseCategoryID: model.DirectionExpense,
		model.UnrecordedIncomeCategoryID:  model.DirectionIncome,
	} {
		// The model refuses it too. The table is what holds when something goes
		// round the model, and the parent key alone would let it through: the
		// parent is top-level and of the same direction.
		child := newCategory(direction, &id, "子類別")

		_, err := r.Insert(ctx, child)

		wantConstraint(t, err, checkViolation, "fin_category_system_parent_check")
		if _, err := r.Get(ctx, child.ID); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("Get after the refused insert under the %s system category: err = %v, want ErrNotFound", direction, err)
		}
	}
}
