package account_test

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/account"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// fakeAccounts is an in-memory repo.AccountRepo.
type fakeAccounts struct {
	repo.AccountRepo

	accounts map[uuid.UUID]model.Account
	// err, when set, is what every method returns.
	err error
	// gotIncludeArchived is the flag List was last asked with.
	gotIncludeArchived bool
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{accounts: map[uuid.UUID]model.Account{}}
}

func (f *fakeAccounts) nameTaken(name string) bool {
	for _, a := range f.accounts {
		if a.Name == name {
			return true
		}
	}
	return false
}

func (f *fakeAccounts) Insert(_ context.Context, a model.Account) (model.Account, error) {
	if f.err != nil {
		return model.Account{}, f.err
	}
	if f.nameTaken(a.Name) {
		return model.Account{}, model.ErrAlreadyExists
	}
	a.CreatedAt = epoch.Add(time.Duration(len(f.accounts)) * time.Second)
	f.accounts[a.ID] = a
	return a, nil
}

func (f *fakeAccounts) Get(_ context.Context, id uuid.UUID) (model.Account, error) {
	if f.err != nil {
		return model.Account{}, f.err
	}
	a, ok := f.accounts[id]
	if !ok {
		return model.Account{}, model.ErrNotFound
	}
	return a, nil
}

func (f *fakeAccounts) List(_ context.Context, includeArchived bool) ([]model.Account, error) {
	f.gotIncludeArchived = includeArchived
	if f.err != nil {
		return nil, f.err
	}
	var out []model.Account
	for _, a := range f.accounts {
		if includeArchived || !a.IsArchived() {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *fakeAccounts) Archive(_ context.Context, id uuid.UUID) (model.Account, error) {
	if f.err != nil {
		return model.Account{}, f.err
	}
	a, ok := f.accounts[id]
	if !ok {
		return model.Account{}, model.ErrNotFound
	}
	if a.ArchivedAt == nil {
		at := epoch.Add(time.Hour)
		a.ArchivedAt = &at
		f.accounts[id] = a
	}
	return a, nil
}

// seed stores an account directly, as an earlier call would have.
func (f *fakeAccounts) seed(t *testing.T, name string) uuid.UUID {
	t.Helper()
	a, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCash})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	f.accounts[a.ID] = a
	return a.ID
}

// fakeInstitutions is an in-memory repo.InstitutionRepo. Only Get is used by the
// account use cases; the others are left to the embedded nil interface.
type fakeInstitutions struct {
	repo.InstitutionRepo

	institutions map[uuid.UUID]model.Institution
}

func newFakeInstitutions() *fakeInstitutions {
	return &fakeInstitutions{institutions: map[uuid.UUID]model.Institution{}}
}

func (f *fakeInstitutions) Get(_ context.Context, id uuid.UUID) (model.Institution, error) {
	i, ok := f.institutions[id]
	if !ok {
		return model.Institution{}, model.ErrNotFound
	}
	return i, nil
}

// seed stores an institution directly, archived or not.
func (f *fakeInstitutions) seed(t *testing.T, name string, archived bool) uuid.UUID {
	t.Helper()
	i, err := model.NewInstitution(uuid.Must(uuid.NewV7()), name)
	if err != nil {
		t.Fatalf("NewInstitution: %v", err)
	}
	if archived {
		at := epoch
		i.ArchivedAt = &at
	}
	f.institutions[i.ID] = i
	return i.ID
}

func TestCreateAccountStoresTheTrimmedName(t *testing.T) {
	accounts := newFakeAccounts()

	got, err := account.New(accounts, newFakeInstitutions()).CreateAccount(context.Background(), usecase.CreateAccountInput{Name: "  錢包 ", Kind: "cash"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if got.Name != "錢包" || got.Kind != "cash" || got.InstitutionID != nil || got.ID == uuid.Nil || got.CreatedAt.IsZero() || got.ArchivedAt != nil {
		t.Fatalf("account = %+v, want the trimmed name, kind cash, no institution, an id, the time the store assigned, and not archived", got)
	}
	if stored := accounts.accounts[got.ID]; stored.Name != "錢包" {
		t.Fatalf("stored = %+v, want the trimmed name", stored)
	}
}

func TestCreateAccountCarriesTheInstitution(t *testing.T) {
	institutions := newFakeInstitutions()
	institutionID := institutions.seed(t, "國泰世華", false)

	got, err := account.New(newFakeAccounts(), institutions).CreateAccount(context.Background(), usecase.CreateAccountInput{
		Name:          "薪轉戶",
		Kind:          "bank",
		InstitutionID: &institutionID,
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	if got.InstitutionID == nil || *got.InstitutionID != institutionID || got.Kind != "bank" {
		t.Fatalf("account = %+v, want kind bank at institution %s", got, institutionID)
	}
}

func TestCreateAccountRejectsInvalidInputWithoutStoring(t *testing.T) {
	institutions := newFakeInstitutions()
	archivedID := institutions.seed(t, "舊銀行", true)
	unknownID := uuid.Must(uuid.NewV7())

	tests := []struct {
		name string
		in   usecase.CreateAccountInput
		want error
	}{
		{"blank name", usecase.CreateAccountInput{Name: " \t ", Kind: "cash"}, model.ErrNameEmpty},
		{"unknown kind", usecase.CreateAccountInput{Name: "x", Kind: "piggy"}, model.ErrInvalidAccountKind},
		{"unknown institution", usecase.CreateAccountInput{Name: "x", Kind: "bank", InstitutionID: &unknownID}, model.ErrNotFound},
		{"archived institution", usecase.CreateAccountInput{Name: "x", Kind: "bank", InstitutionID: &archivedID}, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := newFakeAccounts()

			_, err := account.New(accounts, institutions).CreateAccount(context.Background(), tt.in)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(accounts.accounts) != 0 {
				t.Fatalf("stored %d accounts, want 0", len(accounts.accounts))
			}
		})
	}
}

func TestCreateAccountNamesTheInstitutionItRefuses(t *testing.T) {
	institutions := newFakeInstitutions()
	archivedID := institutions.seed(t, "舊銀行", true)
	unknownID := uuid.Must(uuid.NewV7())
	svc := account.New(newFakeAccounts(), institutions)

	_, err := svc.CreateAccount(context.Background(), usecase.CreateAccountInput{Name: "x", Kind: "bank", InstitutionID: &unknownID})
	if err == nil || !strings.Contains(err.Error(), unknownID.String()) {
		t.Errorf("unknown institution: err = %v, want it to name the id", err)
	}
	_, err = svc.CreateAccount(context.Background(), usecase.CreateAccountInput{Name: "x", Kind: "bank", InstitutionID: &archivedID})
	if err == nil || !strings.Contains(err.Error(), archivedID.String()) {
		t.Errorf("archived institution: err = %v, want it to name the id", err)
	}
}

func TestCreateAccountReportsATakenName(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	svc := account.New(accounts, newFakeInstitutions())
	if _, err := svc.CreateAccount(ctx, usecase.CreateAccountInput{Name: "錢包", Kind: "cash"}); err != nil {
		t.Fatalf("first CreateAccount: %v", err)
	}

	_, err := svc.CreateAccount(ctx, usecase.CreateAccountInput{Name: " 錢包 ", Kind: "wallet"})
	if !errors.Is(err, model.ErrAlreadyExists) {
		t.Fatalf("err = %v, want ErrAlreadyExists", err)
	}
	if !strings.Contains(err.Error(), "錢包") {
		t.Errorf("err = %q, want it to name the account", err)
	}
	if len(accounts.accounts) != 1 {
		t.Fatalf("stored %d accounts, want 1", len(accounts.accounts))
	}
}

func TestArchiveAccount(t *testing.T) {
	ctx := context.Background()

	t.Run("archives", func(t *testing.T) {
		accounts := newFakeAccounts()
		id := accounts.seed(t, "錢包")

		got, err := account.New(accounts, newFakeInstitutions()).ArchiveAccount(ctx, id)
		if err != nil {
			t.Fatalf("ArchiveAccount: %v", err)
		}
		if got.ArchivedAt == nil || !accounts.accounts[id].IsArchived() {
			t.Fatalf("account = %+v, stored = %+v, want it archived", got, accounts.accounts[id])
		}
	})

	t.Run("reports an unknown account", func(t *testing.T) {
		unknown := uuid.Must(uuid.NewV7())

		_, err := account.New(newFakeAccounts(), newFakeInstitutions()).ArchiveAccount(ctx, unknown)
		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if !strings.Contains(err.Error(), unknown.String()) {
			t.Errorf("err = %q, want it to name the id", err)
		}
	})
}

func TestListAccountsKeepsArchivedOnesOutUnlessAsked(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	accounts.seed(t, "錢包")
	accounts.seed(t, "薪轉戶")
	archived := accounts.seed(t, "舊的")
	if _, err := accounts.Archive(ctx, archived); err != nil {
		t.Fatalf("Archive: %v", err)
	}
	svc := account.New(accounts, newFakeInstitutions())

	inUse, err := svc.ListAccounts(ctx, usecase.ListAccountsInput{})
	if err != nil {
		t.Fatalf("ListAccounts: %v", err)
	}
	if accounts.gotIncludeArchived || len(inUse.Accounts) != 2 {
		t.Fatalf("asked for archived = %v, got %d accounts, want not asked and 2", accounts.gotIncludeArchived, len(inUse.Accounts))
	}

	all, err := svc.ListAccounts(ctx, usecase.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		t.Fatalf("ListAccounts(IncludeArchived): %v", err)
	}
	if !accounts.gotIncludeArchived || len(all.Accounts) != 3 {
		t.Fatalf("asked for archived = %v, got %d accounts, want asked and 3", accounts.gotIncludeArchived, len(all.Accounts))
	}
	for i := 1; i < len(all.Accounts); i++ {
		if all.Accounts[i-1].Name > all.Accounts[i].Name {
			t.Fatalf("accounts = %+v, want the order the repo gave (by name)", all.Accounts)
		}
	}
}

func TestGetAccount(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	id := accounts.seed(t, "錢包")
	svc := account.New(accounts, newFakeInstitutions())

	got, err := svc.GetAccount(ctx, id)
	if err != nil || got.ID != id || got.Name != "錢包" {
		t.Fatalf("GetAccount = %+v, %v, want the account", got, err)
	}

	unknown := uuid.Must(uuid.NewV7())
	_, err = svc.GetAccount(ctx, unknown)
	if !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("err = %q, want it to name the id", err)
	}
}

func TestAccountUseCasesReportStoreFailures(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("connection reset")
	accounts := newFakeAccounts()
	id := accounts.seed(t, "錢包")
	accounts.err = boom
	svc := account.New(accounts, newFakeInstitutions())

	calls := map[string]func() error{
		"CreateAccount": func() error {
			_, err := svc.CreateAccount(ctx, usecase.CreateAccountInput{Name: "x", Kind: "cash"})
			return err
		},
		"ArchiveAccount": func() error { _, err := svc.ArchiveAccount(ctx, id); return err },
		"ListAccounts":   func() error { _, err := svc.ListAccounts(ctx, usecase.ListAccountsInput{}); return err },
		"GetAccount":     func() error { _, err := svc.GetAccount(ctx, id); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}
