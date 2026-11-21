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
	// setErr, when set, is what SetStatementDays returns, whatever the other methods
	// do. setCalls counts how often it was called.
	setErr   error
	setCalls int
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

// SetStatementDays stores the days as the repo does. The table's CHECK constraints
// are not the fake's to keep: the use case has to have checked them.
func (f *fakeAccounts) SetStatementDays(_ context.Context, id uuid.UUID, closingDay, paymentDueDay *int) (model.Account, error) {
	f.setCalls++
	if f.setErr != nil {
		return model.Account{}, f.setErr
	}
	a, ok := f.accounts[id]
	if !ok {
		return model.Account{}, model.ErrNotFound
	}
	a.ClosingDay, a.PaymentDueDay = closingDay, paymentDueDay
	f.accounts[id] = a
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
		"SetAccountStatementDays": func() error {
			_, err := svc.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id})
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, boom) {
			t.Errorf("%s: err = %v, want the store failure", name, err)
		}
	}
}

// seedCard stores a card with the days, as an earlier call would have.
func (f *fakeAccounts) seedCard(t *testing.T, name string, closingDay, paymentDueDay *int) uuid.UUID {
	t.Helper()
	a, err := model.NewAccount(model.NewAccountParams{
		ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCard, ClosingDay: closingDay, PaymentDueDay: paymentDueDay,
	})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	f.accounts[a.ID] = a
	return a.ID
}

func sameDay(got, want *int) bool {
	return (got == nil) == (want == nil) && (got == nil || *got == *want)
}

func TestCreateAccountOfACardCarriesItsClosingDayAndPaymentDueDay(t *testing.T) {
	accounts := newFakeAccounts()

	got, err := account.New(accounts, newFakeInstitutions()).CreateAccount(context.Background(), usecase.CreateAccountInput{
		Name: "卡", Kind: "card", ClosingDay: new(5), PaymentDueDay: new(20),
	})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}

	if !sameDay(got.ClosingDay, new(5)) || !sameDay(got.PaymentDueDay, new(20)) {
		t.Fatalf("account = %+v, want closing day 5 and payment due day 20", got)
	}
	if stored := accounts.accounts[got.ID]; !sameDay(stored.ClosingDay, new(5)) || !sameDay(stored.PaymentDueDay, new(20)) {
		t.Errorf("stored = %+v, want the days", stored)
	}
	listed, err := account.New(accounts, newFakeInstitutions()).ListAccounts(context.Background(), usecase.ListAccountsInput{})
	if err != nil || len(listed.Accounts) != 1 || !sameDay(listed.Accounts[0].ClosingDay, new(5)) || !sameDay(listed.Accounts[0].PaymentDueDay, new(20)) {
		t.Errorf("ListAccounts = %+v, %v, want the days", listed, err)
	}
}

func TestCreateAccountRefusesDaysThatCannotBeWithoutStoring(t *testing.T) {
	tests := []struct {
		name string
		in   usecase.CreateAccountInput
		want error
	}{
		{"a closing day on a bank account", usecase.CreateAccountInput{Name: "x", Kind: "bank", ClosingDay: new(5)}, model.ErrNotACard},
		{"a payment due day on cash", usecase.CreateAccountInput{Name: "x", Kind: "cash", PaymentDueDay: new(5)}, model.ErrNotACard},
		{"a payment due day without a closing day", usecase.CreateAccountInput{Name: "x", Kind: "card", PaymentDueDay: new(20)}, model.ErrDueDayWithoutClosingDay},
		{"a closing day of 0", usecase.CreateAccountInput{Name: "x", Kind: "card", ClosingDay: new(0)}, model.ErrDayOutOfRange},
		{"a closing day of 32", usecase.CreateAccountInput{Name: "x", Kind: "card", ClosingDay: new(32)}, model.ErrDayOutOfRange},
		{"a payment due day of 32", usecase.CreateAccountInput{Name: "x", Kind: "card", ClosingDay: new(5), PaymentDueDay: new(32)}, model.ErrDayOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			accounts := newFakeAccounts()

			_, err := account.New(accounts, newFakeInstitutions()).CreateAccount(context.Background(), tt.in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(accounts.accounts) != 0 {
				t.Fatalf("stored %d accounts, want 0", len(accounts.accounts))
			}
		})
	}
}

func TestSetAccountStatementDaysSetsChangesAndClearsTheDaysOfACard(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	id := accounts.seedCard(t, "卡", nil, nil)
	svc := account.New(accounts, newFakeInstitutions())

	set, err := svc.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id, ClosingDay: new(5), PaymentDueDay: new(20)})
	if err != nil || !sameDay(set.ClosingDay, new(5)) || !sameDay(set.PaymentDueDay, new(20)) || set.ID != id || set.Name != "卡" || set.Kind != "card" {
		t.Fatalf("set = %+v, %v, want the same card with closing day 5 and payment due day 20", set, err)
	}

	changed, err := svc.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id, ClosingDay: new(31), PaymentDueDay: new(15)})
	if err != nil || !sameDay(changed.ClosingDay, new(31)) || !sameDay(changed.PaymentDueDay, new(15)) {
		t.Fatalf("changed = %+v, %v, want closing day 31 and payment due day 15", changed, err)
	}

	// The two are replaced together: a nil clears the day it stands for.
	onlyClosing, err := svc.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id, ClosingDay: new(10)})
	if err != nil || !sameDay(onlyClosing.ClosingDay, new(10)) || onlyClosing.PaymentDueDay != nil {
		t.Fatalf("with a closing day alone = %+v, %v, want closing day 10 and no payment due day", onlyClosing, err)
	}

	cleared, err := svc.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id})
	if err != nil || cleared.ClosingDay != nil || cleared.PaymentDueDay != nil {
		t.Fatalf("cleared = %+v, %v, want no days", cleared, err)
	}
	if stored := accounts.accounts[id]; stored.ClosingDay != nil || stored.PaymentDueDay != nil {
		t.Errorf("stored = %+v, want no days", stored)
	}
	got, err := svc.GetAccount(ctx, id)
	if err != nil || got.ClosingDay != nil {
		t.Errorf("GetAccount = %+v, %v, want no days", got, err)
	}
}

func TestSetAccountStatementDaysCanChangeAnArchivedCard(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	id := accounts.seedCard(t, "舊卡", new(5), new(20))
	if _, err := accounts.Archive(ctx, id); err != nil {
		t.Fatalf("Archive: %v", err)
	}

	got, err := account.New(accounts, newFakeInstitutions()).SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: id, ClosingDay: new(8), PaymentDueDay: new(25)})

	if err != nil || !sameDay(got.ClosingDay, new(8)) || got.ArchivedAt == nil {
		t.Fatalf("got %+v, %v, want the archived card with closing day 8", got, err)
	}
}

func TestSetAccountStatementDaysRefusesWhatCannotBeAndLeavesTheAccountAlone(t *testing.T) {
	ctx := context.Background()
	accounts := newFakeAccounts()
	card := accounts.seedCard(t, "卡", new(5), new(20))
	cash := accounts.seed(t, "錢包")
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name string
		in   usecase.SetAccountStatementDaysInput
		want error
	}{
		{"a closing day on an account that is not a card", usecase.SetAccountStatementDaysInput{ID: cash, ClosingDay: new(5)}, model.ErrNotACard},
		{"a payment due day on an account that is not a card", usecase.SetAccountStatementDaysInput{ID: cash, PaymentDueDay: new(5)}, model.ErrNotACard},
		{"a payment due day without a closing day", usecase.SetAccountStatementDaysInput{ID: card, PaymentDueDay: new(20)}, model.ErrDueDayWithoutClosingDay},
		{"a closing day of 0", usecase.SetAccountStatementDaysInput{ID: card, ClosingDay: new(0)}, model.ErrDayOutOfRange},
		{"a closing day of 32", usecase.SetAccountStatementDaysInput{ID: card, ClosingDay: new(32), PaymentDueDay: new(20)}, model.ErrDayOutOfRange},
		{"a payment due day of 0", usecase.SetAccountStatementDaysInput{ID: card, ClosingDay: new(5), PaymentDueDay: new(0)}, model.ErrDayOutOfRange},
		{"an account that is not there", usecase.SetAccountStatementDaysInput{ID: unknown, ClosingDay: new(5)}, model.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writes := accounts.setCalls

			_, err := account.New(accounts, newFakeInstitutions()).SetAccountStatementDays(ctx, tt.in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if accounts.setCalls != writes {
				t.Errorf("the store was written to by a call that was refused")
			}
		})
	}
	if stored := accounts.accounts[card]; !sameDay(stored.ClosingDay, new(5)) || !sameDay(stored.PaymentDueDay, new(20)) {
		t.Errorf("stored card = %+v, want it as it was", stored)
	}

	// Nothing to refuse when there is nothing to set: clearing the days of an account
	// that has none leaves it as it is.
	if _, err := account.New(accounts, newFakeInstitutions()).SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: cash}); err != nil {
		t.Errorf("clearing the days of cash: %v, want it to succeed", err)
	}
}

func TestSetAccountStatementDaysReportsAFailureToStoreThem(t *testing.T) {
	accounts := newFakeAccounts()
	id := accounts.seedCard(t, "卡", nil, nil)
	boom := errors.New("check violation")
	accounts.setErr = boom

	_, err := account.New(accounts, newFakeInstitutions()).SetAccountStatementDays(context.Background(), usecase.SetAccountStatementDaysInput{ID: id, ClosingDay: new(5)})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "卡") {
		t.Fatalf("err = %v, want the failure of the store, with the account named", err)
	}
}
