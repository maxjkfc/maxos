package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Category is a category as the use cases return it.
type Category struct {
	ID uuid.UUID
	// Direction is "expense" or "income".
	Direction string
	// ParentID is the top-level category this one goes under; nil for a top-level
	// category.
	ParentID  *uuid.UUID
	Name      string
	CreatedAt time.Time
	// ArchivedAt is nil while the category is in use.
	ArchivedAt *time.Time
}

// CreateCategory adds a category.
type CreateCategory interface {
	CreateCategory(ctx context.Context, in CreateCategoryInput) (Category, error)
}

// CreateCategoryInput is a category to add.
type CreateCategoryInput struct {
	// Direction is "expense" or "income".
	Direction string
	// ParentID, when set, makes the category a subcategory of it. The parent must
	// exist, not be archived, be a top-level category itself (categories have two
	// levels at most) and have the same Direction.
	ParentID *uuid.UUID
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// category of the same direction and under the same parent (or, for a
	// top-level category, no other top-level category), archived or not, may have
	// the same name.
	Name string
}

// ArchiveCategory retires a category: it stays for the transactions that already
// use it, but new ones may no longer use it. Archiving a top-level category
// archives its subcategories with it. Archiving an archived category changes
// nothing and returns it as it is. The two system categories called 未記錄
// (unrecorded) cannot be archived: reconciliations record to them.
type ArchiveCategory interface {
	ArchiveCategory(ctx context.Context, id uuid.UUID) (Category, error)
}

// ListCategories lists the categories.
type ListCategories interface {
	ListCategories(ctx context.Context, in ListCategoriesInput) (ListCategoriesOutput, error)
}

// ListCategoriesInput filters the listing.
type ListCategoriesInput struct {
	// Direction is "expense" or "income" to list only the categories of that
	// direction; empty lists both.
	Direction string
	// IncludeArchived lists the archived categories too; by default only those in
	// use.
	IncludeArchived bool
}

// ListCategoriesOutput holds the categories, by direction and name. Top-level
// categories and subcategories are in the same list; ParentID tells them apart.
type ListCategoriesOutput struct {
	Categories []Category
}

// GetCategory returns one category, archived or not.
type GetCategory interface {
	GetCategory(ctx context.Context, id uuid.UUID) (Category, error)
}
