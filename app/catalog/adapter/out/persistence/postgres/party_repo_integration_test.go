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

// newParty returns a party ready to be stored.
func newParty(t *testing.T, name string) model.Party {
	t.Helper()
	p, err := model.NewParty(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewParty: %v", err)
	}
	return p
}

// insertParty stores a new party and returns it as stored.
func insertParty(t *testing.T, r *postgres.PartyRepo, name string) model.Party {
	t.Helper()
	stored, err := r.Insert(context.Background(), newParty(t, name))
	if err != nil {
		t.Fatalf("Insert party %q: %v", name, err)
	}
	return stored
}

func TestPartyInsertReturnsTheStoredParty(t *testing.T) {
	r := postgres.NewPartyRepo(testpg.New(t))
	p := newParty(t, "B 朋友")

	stored, err := r.Insert(context.Background(), p)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.ID != p.ID || stored.Name != "B 朋友" || stored.ArchivedAt != nil {
		t.Fatalf("stored = %+v, want %+v not archived", stored, p)
	}
	if stored.CreatedAt.IsZero() {
		t.Fatal("created_at was not assigned by the database")
	}

	got, err := r.Get(context.Background(), p.ID)
	if err != nil || got.ID != p.ID || got.Name != "B 朋友" {
		t.Fatalf("Get = %+v, %v, want the stored party", got, err)
	}
}

func TestPartyNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewPartyRepo(testpg.New(t))
	first := insertParty(t, r, "B 朋友")

	if _, err := r.Insert(ctx, newParty(t, "B 朋友")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newParty(t, "B 朋友")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived party: err = %v, want ErrAlreadyExists", err)
	}

	other := insertParty(t, r, "A 公司")
	if _, err := r.Rename(ctx, other.ID, "B 朋友"); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("rename onto a taken name: err = %v, want ErrAlreadyExists", err)
	}
	if got, _ := r.Get(ctx, other.ID); got.Name != "A 公司" {
		t.Fatalf("name = %q after the refused rename, want it unchanged", got.Name)
	}
}

func TestPartyMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewPartyRepo(testpg.New(t))
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

func TestPartyArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewPartyRepo(testpg.New(t))
	p := insertParty(t, r, "B 朋友")

	first, err := r.Archive(ctx, p.ID)
	if err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	if first.ArchivedAt == nil {
		t.Fatal("ArchivedAt is nil after Archive")
	}

	second, err := r.Archive(ctx, p.ID)
	if err != nil {
		t.Fatalf("second Archive: %v", err)
	}
	if second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", second.ArchivedAt, first.ArchivedAt)
	}
}

func TestPartyListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewPartyRepo(testpg.New(t))
	insertParty(t, r, "B 朋友")
	insertParty(t, r, "A 公司")
	old := insertParty(t, r, "舊的")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names := partyNames(inUse); len(names) != 2 || names[0] != "A 公司" || names[1] != "B 朋友" {
		t.Fatalf("List(false) = %v, want [A 公司 B 朋友]", names)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if names := partyNames(all); len(names) != 3 || names[2] != "舊的" {
		t.Fatalf("List(true) = %v, want [A 公司 B 朋友 舊的]", names)
	}
}

func TestPartyGetByIDsReturnsTheOnesThatExist(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewPartyRepo(testpg.New(t))
	a := insertParty(t, r, "B 朋友")
	b := insertParty(t, r, "舊的")
	insertParty(t, r, "A 公司")
	if _, err := r.Archive(ctx, b.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := r.GetByIDs(ctx, []uuid.UUID{a.ID, b.ID, uuid.Must(uuid.NewV7())})
	if err != nil {
		t.Fatalf("GetByIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("GetByIDs = %+v, want the 2 parties that exist, the archived one included", got)
	}
	found := map[uuid.UUID]bool{}
	for _, p := range got {
		found[p.ID] = true
	}
	if !found[a.ID] || !found[b.ID] {
		t.Fatalf("GetByIDs = %+v, want %s and %s", got, a.ID, b.ID)
	}

	if none, err := r.GetByIDs(ctx, nil); err != nil || len(none) != 0 {
		t.Fatalf("GetByIDs(nil) = %+v, %v, want nothing", none, err)
	}
}

func partyNames(parties []model.Party) []string {
	names := make([]string, len(parties))
	for i, p := range parties {
		names[i] = p.Name
	}
	return names
}
