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

// newAccount returns a cash account, with no institution, ready to be stored.
func newAccount(t *testing.T, name string) model.Account {
	t.Helper()
	a, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCash})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	return a
}

// insertAccount stores a new cash account and returns it as stored.
func insertAccount(t *testing.T, r *postgres.AccountRepo, name string) model.Account {
	t.Helper()
	stored, err := r.Insert(context.Background(), newAccount(t, name))
	if err != nil {
		t.Fatalf("Insert account %q: %v", name, err)
	}
	return stored
}

func accountNames(accounts []model.Account) []string {
	names := make([]string, len(accounts))
	for i, account := range accounts {
		names[i] = account.Name
	}
	return names
}

func TestAccountRoundTripsItsKindAndInstitution(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	institutions, accounts := postgres.NewInstitutionRepo(pool), postgres.NewAccountRepo(pool)
	institution := insertInstitution(t, institutions, "國泰世華")

	withInstitution := newAccount(t, "薪轉戶")
	withInstitution.Kind = model.AccountKindBank
	withInstitution.InstitutionID = &institution.ID
	stored, err := accounts.Insert(ctx, withInstitution)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := accounts.Get(ctx, withInstitution.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for name, a := range map[string]model.Account{"Insert": stored, "Get": got} {
		if a.ID != withInstitution.ID || a.Name != "薪轉戶" || a.Kind != model.AccountKindBank ||
			a.InstitutionID == nil || *a.InstitutionID != institution.ID || a.ArchivedAt != nil || a.CreatedAt.IsZero() {
			t.Errorf("%s = %+v, want a bank account at %s, in use, with the time the database assigned", name, a, institution.ID)
		}
	}

	alone := insertAccount(t, accounts, "錢包")
	if alone.Kind != model.AccountKindCash || alone.InstitutionID != nil {
		t.Fatalf("stored = %+v, want a cash account with no institution", alone)
	}
	if got, err := accounts.Get(ctx, alone.ID); err != nil || got.InstitutionID != nil {
		t.Fatalf("Get = %+v, %v, want no institution", got, err)
	}
}

func TestAccountNamesAreUniqueEvenWhenArchived(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	first := insertAccount(t, r, "錢包")

	if _, err := r.Insert(ctx, newAccount(t, "錢包")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("second insert: err = %v, want ErrAlreadyExists", err)
	}

	if _, err := r.Archive(ctx, first.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	if _, err := r.Insert(ctx, newAccount(t, "錢包")); !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("insert next to an archived account: err = %v, want ErrAlreadyExists", err)
	}
}

func TestAccountKindIsCheckedByTheDatabase(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))

	// The literal skips model.NewAccount on purpose: the table is the last guard.
	_, err := r.Insert(ctx, model.Account{ID: uuid.Must(uuid.NewV7()), Name: "x", Kind: "piggy"})
	wantConstraint(t, err, checkViolation, "fin_account_kind_check")

	for _, kind := range []model.AccountKind{
		model.AccountKindCash, model.AccountKindBank, model.AccountKindCard, model.AccountKindExchange,
		model.AccountKindBrokerage, model.AccountKindWallet, model.AccountKindLoan,
	} {
		if _, err := r.Insert(ctx, model.Account{ID: uuid.Must(uuid.NewV7()), Name: string(kind), Kind: kind}); err != nil {
			t.Errorf("Insert a %s account: %v, want it stored", kind, err)
		}
	}
}

func TestAccountInstitutionMustExist(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	a := newAccount(t, "薪轉戶")
	a.InstitutionID = &unknown
	_, err := r.Insert(ctx, a)
	wantConstraint(t, err, foreignKeyViolation, "fin_account_institution_id_fkey")

	if _, err := r.Get(ctx, a.ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("Get after the refused insert: err = %v, want ErrNotFound", err)
	}
}

func TestAccountMethodsReportAnUnknownID(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	unknown := uuid.Must(uuid.NewV7())

	if _, err := r.Get(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Get: err = %v, want ErrNotFound", err)
	}
	if _, err := r.Archive(ctx, unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("Archive: err = %v, want ErrNotFound", err)
	}
}

func TestAccountArchiveKeepsTheTimeOfTheFirstArchive(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	a := insertAccount(t, r, "錢包")

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

func TestAccountListIsByNameAndHidesArchivedUnlessAsked(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	insertAccount(t, r, "Wallet")
	insertAccount(t, r, "Bank")
	old := insertAccount(t, r, "Zed")
	if _, err := r.Archive(ctx, old.ID); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	inUse, err := r.List(ctx, false)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if names := accountNames(inUse); len(names) != 2 || names[0] != "Bank" || names[1] != "Wallet" {
		t.Fatalf("List(false) = %v, want [Bank Wallet]", names)
	}

	all, err := r.List(ctx, true)
	if err != nil {
		t.Fatalf("List(true): %v", err)
	}
	if names := accountNames(all); len(names) != 3 || names[2] != "Zed" {
		t.Fatalf("List(true) = %v, want [Bank Wallet Zed]", names)
	}
}
