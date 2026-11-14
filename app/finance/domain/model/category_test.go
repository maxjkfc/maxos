package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// topLevel returns a top-level category of the given direction.
func topLevel(t *testing.T, direction model.Direction, name string) model.Category {
	t.Helper()
	c, err := model.NewCategory(model.NewCategoryParams{ID: uuid.Must(uuid.NewV7()), Direction: direction, Name: name})
	if err != nil {
		t.Fatalf("NewCategory(%q): %v", name, err)
	}
	return c
}

func TestNewCategoryTrimsTheNameAndOnlyTakesTheTwoDirections(t *testing.T) {
	for _, direction := range []model.Direction{model.DirectionExpense, model.DirectionIncome} {
		got := topLevel(t, direction, "  餐飲 \n")
		if got.Name != "餐飲" || got.Direction != direction || got.ParentID != nil {
			t.Errorf("category = %+v, want a top-level %s category called 餐飲", got, direction)
		}
	}

	for _, direction := range []model.Direction{"", "transfer", "Expense"} {
		_, err := model.NewCategory(model.NewCategoryParams{ID: uuid.Must(uuid.NewV7()), Direction: direction, Name: "x"})
		if !errors.Is(err, model.ErrInvalidDirection) {
			t.Errorf("direction %q: err = %v, want ErrInvalidDirection", direction, err)
		}
	}

	_, err := model.NewCategory(model.NewCategoryParams{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: " "})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Errorf("blank name: err = %v, want ErrNameEmpty", err)
	}
}

func TestASubcategoryGoesUnderATopLevelCategoryOfTheSameDirection(t *testing.T) {
	parent := topLevel(t, model.DirectionExpense, "餐飲")

	got, err := model.NewCategory(model.NewCategoryParams{
		ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: "午餐", Parent: &parent,
	})
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != parent.ID || got.Direction != model.DirectionExpense {
		t.Fatalf("category = %+v, want a subcategory of %s", got, parent.ID)
	}
}

func TestACategoryMayNotGoUnderASubcategory(t *testing.T) {
	parent := topLevel(t, model.DirectionExpense, "餐飲")
	child, err := model.NewCategory(model.NewCategoryParams{
		ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: "午餐", Parent: &parent,
	})
	if err != nil {
		t.Fatalf("NewCategory: %v", err)
	}

	_, err = model.NewCategory(model.NewCategoryParams{
		ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: "便當", Parent: &child,
	})
	if !errors.Is(err, model.ErrCategoryTooDeep) {
		t.Fatalf("err = %v, want ErrCategoryTooDeep", err)
	}
}

func TestASubcategoryHasTheDirectionOfItsParent(t *testing.T) {
	parent := topLevel(t, model.DirectionExpense, "餐飲")

	_, err := model.NewCategory(model.NewCategoryParams{
		ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionIncome, Name: "獎金", Parent: &parent,
	})
	if !errors.Is(err, model.ErrCategoryDirectionMismatch) {
		t.Fatalf("err = %v, want ErrCategoryDirectionMismatch", err)
	}
}
