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

// newInstitution returns an institution ready to be stored.
func newInstitution(t *testing.T, name string) model.Institution {
	t.Helper()
	i, err := model.NewInstitution(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewInstitution: %v", err)
	}
	return i
}

// insertInstitution stores a new institution and returns it as stored.
func insertInstitution(t *testing.T, r *postgres.InstitutionRepo, name string) model.Institution {
	t.Helper()
	stored, err := r.Insert(context.Background(), newInstitution(t, name))
	if err != nil {
		t.Fatalf("Insert institution %q: %v", name, err)
	}
	return stored
}

func institutionNames(institutions []model.Institution) []string {
	names := make([]string, len(institutions))
	for i, institution := range institutions {
		names[i] = institution.Name
	}
	return names
}

func TestInstitutionInsertReturnsTheStoredInstitution(t *testing.T) {
	r := postgres.NewInstitutionRepo(testpg.New(t))
	i := newInstitution(t, "國泰世華")

	stored, err := r.Insert(context.Background(), i)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if stored.ID != i.ID || stored.Name != "國泰世華" || stored.ArchivedAt != nil {
		t.Fatalf("stored = %+v, want %+v not archived", stored, i)
	}
	if stored.CreatedAt.IsZero() {
		t.Fatal("created_at was not assigned by the database")
	}

	got, err := r.Get(context.Background(), i.ID)
	if err != nil || got.ID != i.ID || got.Name != "國泰世華" {
		t.Fatalf("Get = %+v, %v, want the stored institution", got, err)
	}
}

func TestInstitutionNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstitutionRepo(testpg.New(t))
	first := insertInstitution(t, r, "國泰世華")

	if _, err := r.Insert(ctx, newInstitution(t, "國泰世華")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newInstitution(t, "國泰世華")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived institution: err = %v, want ErrAlreadyExists", err)
	}
}

func TestInstitutionMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstitutionRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	if _, err := r.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Archive(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Archive: err = %v, want ErrNotFound", err)
	}
}

func TestInstitutionArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstitutionRepo(testpg.New(t))
	i := insertInstitution(t, r, "國泰世華")

	first, err := r.Archive(ctx, i.ID)
	if err != nil {
		t.Fatalf("first Archive: %v", err)
	}
	if first.ArchivedAt == nil {
		t.Fatal("ArchivedAt is nil after Archive")
	}

	second, err := r.Archive(ctx, i.ID)
	if err != nil {
		t.Fatalf("second Archive: %v", err)
	}
	if second.ArchivedAt == nil || !second.ArchivedAt.Equal(*first.ArchivedAt) {
		t.Fatalf("ArchivedAt = %v after archiving again, want the first time %v", second.ArchivedAt, first.ArchivedAt)
	}
}

func TestInstitutionListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewInstitutionRepo(testpg.New(t))
	insertInstitution(t, r, "Binance")
	insertInstitution(t, r, "Cathay")
	old := insertInstitution(t, r, "Zed Bank")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names := institutionNames(inUse); len(names) != 2 || names[0] != "Binance" || names[1] != "Cathay" {
		t.Fatalf("List(false) = %v, want [Binance Cathay]", names)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if names := institutionNames(all); len(names) != 3 || names[2] != "Zed Bank" {
		t.Fatalf("List(true) = %v, want [Binance Cathay Zed Bank]", names)
	}
}
