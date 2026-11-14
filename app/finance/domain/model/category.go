package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Direction says which way a category faces: what an expense bought, or where an
// income came from. Only expenses and incomes have categories.
type Direction string

// The two directions.
const (
	DirectionExpense Direction = "expense"
	DirectionIncome  Direction = "income"
)

// Valid reports whether d is one of the directions above.
func (d Direction) Valid() bool {
	return d == DirectionExpense || d == DirectionIncome
}

// Category is what an expense bought or where an income came from, in at most two
// levels: a top-level category, and subcategories under it.
type Category struct {
	ID        uuid.UUID
	Direction Direction
	// ParentID is the top-level category this one goes under; nil for a top-level
	// category.
	ParentID *uuid.UUID
	Name     string
	// CreatedAt is assigned when the category is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the category is in use. An archived category stays
	// for the transactions that already use it, but new ones may not.
	ArchivedAt *time.Time
}

// NewCategoryParams is the raw input of a category.
type NewCategoryParams struct {
	ID        uuid.UUID
	Direction Direction
	Name      string
	// Parent is the category the new one goes under, nil for a top-level
	// category. It must itself be top-level, have the same Direction and not be a
	// system category. Whether it is archived is for the use case to check.
	Parent *Category
}

// NewCategory validates a category and returns it. The whitespace around the name
// is trimmed. It returns ErrSystemCategory when the parent is a system category,
// ErrCategoryTooDeep when the parent is a subcategory itself, and
// ErrCategoryDirectionMismatch when the parent has the other direction.
func NewCategory(p NewCategoryParams) (Category, error) {
	name, err := normalizeName(p.Name)
	if err != nil {
		return Category{}, err
	}
	if !p.Direction.Valid() {
		return Category{}, fmt.Errorf("%w %q: want expense or income", ErrInvalidDirection, p.Direction)
	}
	category := Category{ID: p.ID, Direction: p.Direction, Name: name}
	if p.Parent != nil {
		if p.Parent.IsSystem() {
			return Category{}, fmt.Errorf("%w: %q cannot have subcategories", ErrSystemCategory, p.Parent.Name)
		}
		if p.Parent.ParentID != nil {
			return Category{}, fmt.Errorf("%w: %q is already a subcategory", ErrCategoryTooDeep, p.Parent.Name)
		}
		if p.Parent.Direction != p.Direction {
			return Category{}, fmt.Errorf("%w: %q is %s, not %s", ErrCategoryDirectionMismatch, p.Parent.Name, p.Parent.Direction, p.Direction)
		}
		parentID := p.Parent.ID
		category.ParentID = &parentID
	}
	return category, nil
}

// IsArchived reports whether the category is archived.
func (c Category) IsArchived() bool {
	return c.ArchivedAt != nil
}

// The ids of the two system categories, both called 未記錄 (unrecorded): what a
// reconciliation books when an account holds less of a currency than the ledger
// says (an expense) or more (an income). They are the ids migration 0005 gives
// the two rows it seeds, and the two must stay equal. A system category is never
// archived, and only a reconciliation records to it.
var (
	UnrecordedExpenseCategoryID = uuid.MustParse("00000000-0000-7000-8000-000000000101")
	UnrecordedIncomeCategoryID  = uuid.MustParse("00000000-0000-7000-8000-000000000102")
)

// IsSystemCategory reports whether id is the id of a system category.
func IsSystemCategory(id uuid.UUID) bool {
	return id == UnrecordedExpenseCategoryID || id == UnrecordedIncomeCategoryID
}

// IsSystem reports whether the category is a system category.
func (c Category) IsSystem() bool {
	return IsSystemCategory(c.ID)
}

// CheckArchivable returns ErrSystemCategory for a system category, which is never
// archived, and nil for every other category.
func (c Category) CheckArchivable() error {
	if c.IsSystem() {
		return fmt.Errorf("%w: %q cannot be archived", ErrSystemCategory, c.Name)
	}
	return nil
}
