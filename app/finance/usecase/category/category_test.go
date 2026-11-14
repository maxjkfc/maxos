package category_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/category"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeRepo is an in-memory repo.CategoryRepo.
type fakeRepo struct {
	repo.CategoryRepo

	categories map[uuid.UUID]model.Category
	// err, when set, is what every method returns.
	err error
	// gotDirection and gotIncludeArchived are what List was last asked with.
	gotDirection       model.Direction
	gotIncludeArchived bool
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{categories: map[uuid.UUID]model.Category{}}
}

func sameParent(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func (f *fakeRepo) nameTaken(c model.Category) bool {
	for _, other := range f.categories {
		if other.Direction == c.Direction && sameParent(other.ParentID, c.ParentID) && other.Name == c.Name {
			return true
		}
	}
	return false
}

func (f *fakeRepo) Insert(_ context.Context, c model.Category) (model.Category, error) {
	if f.err != nil {
		return model.Category{}, f.err
	}
	if f.nameTaken(c) {
		return model.Category{}, model.ErrAlreadyExists
	}
	c.CreatedAt = epoch.Add(time.Duration(len(f.categories)) * time.Second)
	f.categories[c.ID] = c
	return c, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (model.Category, error) {
	if f.err != nil {
		return model.Category{}, f.err
	}
	c, ok := f.categories[id]
	if !ok {
		return model.Category{}, model.ErrNotFound
	}
	return c, nil
}

func (f *fakeRepo) List(_ context.Context, direction model.Direction, includeArchived bool) ([]model.Category, error) {
	f.gotDirection, f.gotIncludeArchived = direction, includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Category
	for _, c := range f.categories {
		if (direction == "" || c.Direction == direction) && (includeArchived || !c.IsArchived()) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Direction != out[j].Direction {
			return out[i].Direction < out[j].Direction
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Archive archives the category and its subcategories, as the real repo does.
func (f *fakeRepo) Archive(_ context.Context, id uuid.UUID) (model.Category, error) {
	if f.err != nil {
		return model.Category{}, f.err
	}
	if _, ok := f.categories[id]; !ok {
		return model.Category{}, model.ErrNotFound
	}
	for cid, c := range f.categories {
		if (cid == id || (c.ParentID != nil && *c.ParentID == id)) && c.ArchivedAt == nil {
			at := epoch.Add(time.Hour)
			c.ArchivedAt = &at
			f.categories[cid] = c
		}
	}
	return f.categories[id], nil
}

// seed stores a category directly, as an earlier call would have.
func (f *fakeRepo) seed(t *testing.T, direction model.Direction, parent *uuid.UUID, name string) uuid.UUID {
	t.Helper()
	c := model.Category{ID: uuid.Must(uuid.NewV7()), Direction: direction, ParentID: parent, Name: name}
	f.categories[c.ID] = c
	return c.ID
}

// seedArchived stores an archived category directly.
func (f *fakeRepo) seedArchived(t *testing.T, direction model.Direction, name string) uuid.UUID {
	t.Helper()
	id := f.seed(t, direction, nil, name)
	c := f.categories[id]
	at := epoch
	c.ArchivedAt = &at
	f.categories[id] = c
	return id
}

func TestCreateCategoryStoresATopLevelCategory(t *testing.T) {
	r := newFakeRepo()

	got, err := category.New(r).CreateCategory(context.Background(), usecase.CreateCategoryInput{Direction: "expense", Name: "  餐飲 "})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if got.Name != "餐飲" || got.Direction != "expense" || got.ParentID != nil || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("category = %+v, want the trimmed name, expense, no parent, an id, the time the store assigned, and not archived", got)
	}
	if stored := r.categories[got.ID]; stored.Name != "餐飲" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreateCategoryStoresASubcategoryWithItsParent(t *testing.T) {
	r := newFakeRepo()
	parent := r.seed(t, model.DirectionExpense, nil, "餐飲")

	got, err := category.New(r).CreateCategory(context.Background(), usecase.CreateCategoryInput{Direction: "expense", ParentID: &parent, Name: "早餐"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if got.ParentID == nil || *got.ParentID != parent || got.Direction != "expense" {
		t.Fatalf("category = %+v, want a subcategory of %s", got, parent)
	}
	if stored := r.categories[got.ID]; stored.ParentID == nil || *stored.ParentID != parent {
		t.Fatalf("stored = %+v, want it to carry the parent", stored)
	}
}

func TestCreateCategoryRejectsInvalidInputWithoutStoring(t *testing.T) {
	r := newFakeRepo()
	top := r.seed(t, model.DirectionExpense, nil, "餐飲")
	sub := r.seed(t, model.DirectionExpense, &top, "早餐")
	archived := r.seedArchived(t, model.DirectionExpense, "舊的")
	unknown := uuid.Must(uuid.NewV7())
	before := len(r.categories)

	tests := []struct {
		name string
		in   usecase.CreateCategoryInput
		want error
	}{
		{"blank name", usecase.CreateCategoryInput{Direction: "expense", Name: " \t "}, model.ErrNameEmpty},
		{"unknown direction", usecase.CreateCategoryInput{Direction: "transfer", Name: "x"}, model.ErrInvalidDirection},
		{"empty direction", usecase.CreateCategoryInput{Name: "x"}, model.ErrInvalidDirection},
		{"unknown parent", usecase.CreateCategoryInput{Direction: "expense", ParentID: &unknown, Name: "x"}, model.ErrNotFound},
		{"archived parent", usecase.CreateCategoryInput{Direction: "expense", ParentID: &archived, Name: "x"}, model.ErrArchived},
		{"parent is a subcategory", usecase.CreateCategoryInput{Direction: "expense", ParentID: &sub, Name: "x"}, model.ErrCategoryTooDeep},
		{"parent has the other direction", usecase.CreateCategoryInput{Direction: "income", ParentID: &top, Name: "x"}, model.ErrCategoryDirectionMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := category.New(r).CreateCategory(context.Background(), tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(r.categories) != before {
				t.Fatalf("stored %d categories, want %d", len(r.categories), before)
			}
		})
	}
}

func TestCreateCategoryNamesTheParentItRefuses(t *testing.T) {
	r := newFakeRepo()
	archived := r.seedArchived(t, model.DirectionExpense, "舊的")
	unknown := uuid.Must(uuid.NewV7())
	svc := category.New(r)

	_, err := svc.CreateCategory(context.Background(), usecase.CreateCategoryInput{Direction: "expense", ParentID: &unknown, Name: "x"})
	if err == nil || !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("unknown parent: err = %v, want it to name the id", err)
	}
	_, err = svc.CreateCategory(context.Background(), usecase.CreateCategoryInput{Direction: "expense", ParentID: &archived, Name: "x"})
	if err == nil || !strings.Contains(err.Error(), archived.String()) {
		t.Errorf("archived parent: err = %v, want it to name the id", err)
	}
}

func TestCreateCategoryReportsATakenName(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	svc := category.New(r)
	if _, err := svc.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "餐飲"}); err != nil {
		t.Fatalf("first CreateCategory: %v", err)
	}

	_, err := svc.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: " 餐飲 "})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "餐飲") {
		t.Errorf("err = %q, want it to name the category", err)
	}
	if len(r.categories) != 1 {
		t.Fatalf("stored %d categories, want 1", len(r.categories))
	}
}

func TestArchiveCategory(t *testing.T) {
	ctx := context.Background()

	t.Run("archives the category and its subcategories", func(t *testing.T) {
		r := newFakeRepo()
		top := r.seed(t, model.DirectionExpense, nil, "餐飲")
		sub := r.seed(t, model.DirectionExpense, &top, "早餐")

		got, err := category.New(r).ArchiveCategory(ctx, top)
		if err != nil {
			t.Fatalf("ArchiveCategory: %v", err)
		}
		if got.ID != top || got.ArchivedAt == nil || !r.categories[top].IsArchived() || !r.categories[sub].IsArchived() {
			t.Fatalf("category = %+v, want the parent returned archived, with its subcategory archived too", got)
		}
	})

	t.Run("reports an unknown category", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := category.New(newFakeRepo()).ArchiveCategory(ctx, unknown)
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})
}

func TestListCategories(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	food := r.seed(t, model.DirectionExpense, nil, "餐飲")
	r.seed(t, model.DirectionExpense, &food, "早餐")
	r.seed(t, model.DirectionIncome, nil, "薪資")
	r.seedArchived(t, model.DirectionExpense, "舊的")
	svc := category.New(r)

	t.Run("lists both directions in use by default", func(t *testing.T) {
		got, err := svc.ListCategories(ctx, usecase.ListCategoriesInput{})
		if err != nil {
			t.Fatalf("ListCategories: %v", err)
		}
		if r.gotDirection != "" || r.gotIncludeArchived || len(got.Categories) != 3 {
			t.Fatalf("asked for direction %q and archived = %v, got %d categories, want no direction, not archived and 3", r.gotDirection, r.gotIncludeArchived, len(got.Categories))
		}
	})

	t.Run("passes the direction and the archived flag on", func(t *testing.T) {
		got, err := svc.ListCategories(ctx, usecase.ListCategoriesInput{Direction: "expense", IncludeArchived: true})
		if err != nil {
			t.Fatalf("ListCategories: %v", err)
		}
		if r.gotDirection != model.DirectionExpense || !r.gotIncludeArchived || len(got.Categories) != 3 {
			t.Fatalf("asked for direction %q and archived = %v, got %d categories, want expense, archived and 3", r.gotDirection, r.gotIncludeArchived, len(got.Categories))
		}
		for _, c := range got.Categories {
			if c.Direction != "expense" {
				t.Fatalf("categories = %+v, want only expenses", got.Categories)
			}
		}
	})

	t.Run("rejects an unknown direction without asking the repo", func(t *testing.T) {
		r.gotDirection, r.gotIncludeArchived = "untouched", true

		_, err := svc.ListCategories(ctx, usecase.ListCategoriesInput{Direction: "transfer"})
		if !errors.Is(err, model.ErrInvalidDirection) {
			t.Fatalf("err = %v, want ErrInvalidDirection", err)
		}
		if r.gotDirection != "untouched" {
			t.Fatalf("the repo was asked to list for direction %q, want it not called", r.gotDirection)
		}
	})
}

func TestGetCategory(t *testing.T) {
	ctx := context.Background()
	r := newFakeRepo()
	id := r.seed(t, model.DirectionIncome, nil, "薪資")
	svc := category.New(r)

	got, err := svc.GetCategory(ctx, id)
	if err != nil || got.ID != id || got.Name != "薪資" || got.Direction != "income" {
		t.Fatalf("GetCategory = %+v, %v, want the category", got, err)
	}

	unknown := uuid.Must(uuid.NewV7())
	_, err = svc.GetCategory(ctx, unknown)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("err = %q, want it to name the id", err)
	}
}

func TestCategoryUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	r := newFakeRepo()
	id := r.seed(t, model.DirectionExpense, nil, "餐飲")
	r.err = boom
	svc := category.New(r)

	calls := map[string]func() error{
		"CreateCategory": func() error {
			_, err := svc.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "x"})
			return err
		},
		"CreateCategory under a parent": func() error {
			_, err := svc.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", ParentID: &id, Name: "x"})
			return err
		},
		"ArchiveCategory": func() error { _, err := svc.ArchiveCategory(ctx, id); return err },
		"ListCategories":  func() error { _, err := svc.ListCategories(ctx, usecase.ListCategoriesInput{}); return err },
		"GetCategory":     func() error { _, err := svc.GetCategory(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
