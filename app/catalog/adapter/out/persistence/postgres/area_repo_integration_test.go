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

// newArea returns an area ready to be stored.
func newArea(t *testing.T, name string) model.Area {
	t.Helper()
	a, err := model.NewArea(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewArea: %v", err)
	}
	return a
}

// insertArea stores a new area and returns it as stored.
func insertArea(t *testing.T, r *postgres.AreaRepo, name string) model.Area {
	t.Helper()
	stored, err := r.Insert(context.Background(), newArea(t, name))
	if err != nil {
		t.Fatalf("Insert area %q: %v", name, err)
	}
	return stored
}

func TestAreaInsertReturnsTheStoredArea(t *testing.T) {
	r := postgres.NewAreaRepo(testpg.New(t))
	a := newArea(t, "玩樂")

	stored, err := r.Insert(context.Background(), a)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.ID != a.ID || stored.Name != "玩樂" || stored.ArchivedAt != nil {
		t.Fatalf("stored = %+v, want %+v not archived", stored, a)
	}
	if stored.CreatedAt.IsZero() {
		t.Fatal("created_at was not assigned by the database")
	}

	got, err := r.Get(context.Background(), a.ID)
	if err != nil || got.ID != a.ID || got.Name != "玩樂" {
		t.Fatalf("Get = %+v, %v, want the stored area", got, err)
	}
}

func TestAreaNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAreaRepo(testpg.New(t))
	first := insertArea(t, r, "玩樂")

	if _, err := r.Insert(ctx, newArea(t, "玩樂")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newArea(t, "玩樂")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived area: err = %v, want ErrAlreadyExists", err)
	}

	other := insertArea(t, r, "工作")
	if _, err := r.Rename(ctx, other.ID, "玩樂"); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrAlreadyExists", err)
	}
	if got, _ := r.Get(ctx, other.ID); got.Name != "工作" {
		t.Fatalf("name = %q after the refused rename, want it unchanged", got.Name)
	}
}

func TestAreaMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAreaRepo(testpg.New(t))
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

func TestAreaArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAreaRepo(testpg.New(t))
	a := insertArea(t, r, "玩樂")

	first, err := r.Archive(ctx, a.ID)
	if err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	if first.ArchivedAt == nil {
		t.Fatal("ArchivedAt is nil after Archive")
	}

	second, err := r.Archive(ctx, a.ID)
	if err != nil {
		t.Fatalf("second Archive: %v", err)
	}
	if second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", second.ArchivedAt, first.ArchivedAt)
	}
}

func TestAreaListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAreaRepo(testpg.New(t))
	insertArea(t, r, "玩樂")
	insertArea(t, r, "工作")
	old := insertArea(t, r, "舊的")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names := areaNames(inUse); len(names) != 2 || names[0] != "工作" || names[1] != "玩樂" {
		t.Fatalf("List(false) = %v, want [工作 玩樂]", names)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if names := areaNames(all); len(names) != 3 || names[2] != "舊的" {
		t.Fatalf("List(true) = %v, want [工作 玩樂 舊的]", names)
	}
}

func TestAreaGetByIDsReturnsTheOnesThatExist(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAreaRepo(testpg.New(t))
	a := insertArea(t, r, "玩樂")
	b := insertArea(t, r, "舊的")
	insertArea(t, r, "工作")
	if _, err := r.Archive(ctx, b.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := r.GetByIDs(ctx, []uuid.UUID{a.ID, b.ID, uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetByIDs = %+v, want the 2 areas that exist, the archived one included", got)
	}
	found := map[uuid.UUID]bool{}
	for _, area := range got {
		found[area.ID] = true
	}
	if !found[a.ID] || !found[b.ID] {
		t.Fatalf("GetByIDs = %+v, want %s and %s", got, a.ID, b.ID)
	}

	if none, err := r.GetByIDs(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("GetByIDs(nil) = %+v, %v, want nothing", none, err)
	}
}

func areaNames(areas []model.Area) []string {
	names := make([]string, len(areas))
	for i, a := range areas {
		names[i] = a.Name
	}
	return names
}
