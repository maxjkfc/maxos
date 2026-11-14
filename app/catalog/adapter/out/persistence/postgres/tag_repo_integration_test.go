package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/catalog/domain/model"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// newTag returns a tag ready to be stored.
func newTag(t *testing.T, name string) model.Tag {
	t.Helper()
	g, err := model.NewTag(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewTag: %v", err)
	}
	return g
}

// insertTag stores a new tag and returns it as stored.
func insertTag(t *testing.T, r *postgres.TagRepo, name string) model.Tag {
	t.Helper()
	stored, err := r.Insert(context.Background(), newTag(t, name))
	if err != nil {
		t.Fatalf("Insert tag %q: %v", name, err)
	}
	return stored
}

func TestTagInsertReturnsTheStoredTag(t *testing.T) {
	r := postgres.NewTagRepo(testpg.New(t))
	g := newTag(t, "b旅遊")

	stored, err := r.Insert(context.Background(), g)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.ID != g.ID || stored.Name != "b旅遊" || stored.ArchivedAt != nil {
		t.Fatalf("stored = %+v, want %+v not archived", stored, g)
	}
	if stored.CreatedAt.IsZero() {
		t.Fatal("created_at was not assigned by the database")
	}

	got, err := r.Get(context.Background(), g.ID)
	if err != nil || got.ID != g.ID || got.Name != "b旅遊" {
		t.Fatalf("Get = %+v, %v, want the stored tag", got, err)
	}
}

func TestTagNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewTagRepo(testpg.New(t))
	first := insertTag(t, r, "b旅遊")

	if _, err := r.Insert(ctx, newTag(t, "b旅遊")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newTag(t, "b旅遊")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived tag: err = %v, want ErrAlreadyExists", err)
	}

	other := insertTag(t, r, "a報銷")
	if _, err := r.Rename(ctx, other.ID, "b旅遊"); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrAlreadyExists", err)
	}
	if got, _ := r.Get(ctx, other.ID); got.Name != "a報銷" {
		t.Fatalf("name = %q after the refused rename, want it unchanged", got.Name)
	}
}

func TestTagMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewTagRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	if _, err := r.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Rename(ctx, unknown, "x"); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Rename: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Archive(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Archive: err = %v, want ErrNotFound", err)
	}
}

func TestTagArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewTagRepo(testpg.New(t))
	g := insertTag(t, r, "b旅遊")

	first, err := r.Archive(ctx, g.ID)
	if err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	if first.ArchivedAt == nil {
		t.Fatal("ArchivedAt is nil after Archive")
	}

	second, err := r.Archive(ctx, g.ID)
	if err != nil {
		t.Fatalf("second Archive: %v", err)
	}
	if second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", second.ArchivedAt, first.ArchivedAt)
	}
}

func TestTagListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewTagRepo(testpg.New(t))
	insertTag(t, r, "b旅遊")
	insertTag(t, r, "a報銷")
	old := insertTag(t, r, "舊的")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names := tagNames(inUse); len(names) != 2 || names[0] != "a報銷" || names[1] != "b旅遊" {
		t.Fatalf("List(false) = %v, want [a報銷 b旅遊]", names)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if names := tagNames(all); len(names) != 3 || names[2] != "舊的" {
		t.Fatalf("List(true) = %v, want [a報銷 b旅遊 舊的]", names)
	}
}

func TestTagGetByIDsReturnsTheOnesThatExist(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewTagRepo(testpg.New(t))
	a := insertTag(t, r, "b旅遊")
	b := insertTag(t, r, "舊的")
	insertTag(t, r, "a報銷")
	if _, err := r.Archive(ctx, b.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := r.GetByIDs(ctx, []uuid.UUID{a.ID, b.ID, uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetByIDs = %+v, want the 2 tags that exist, the archived one included", got)
	}
	found := map[uuid.UUID]bool{}
	for _, g := range got {
		found[g.ID] = true
	}
	if !found[a.ID] || !found[b.ID] {
		t.Fatalf("GetByIDs = %+v, want %s and %s", got, a.ID, b.ID)
	}

	if none, err := r.GetByIDs(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("GetByIDs(nil) = %+v, %v, want nothing", none, err)
	}
}

func tagNames(tags []model.Tag) []string {
	names := make([]string, len(tags))
	for i, g := range tags {
		names[i] = g.Name
	}
	return names
}
