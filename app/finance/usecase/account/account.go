// Package account implements the account use cases: create, archive, list and
// get.
package account

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// Service implements usecase.CreateAccount, ArchiveAccount, ListAccounts and
// GetAccount.
type Service struct {
	accounts     repo.AccountRepo
	institutions repo.InstitutionRepo
}

var (
	_ usecase.CreateAccount  = (*Service)(nil)
	_ usecase.ArchiveAccount = (*Service)(nil)
	_ usecase.ListAccounts   = (*Service)(nil)
	_ usecase.GetAccount     = (*Service)(nil)
)

// CreateAccount validates the account, checks that its institution exists and is
// in use, and stores it.
func (s *Service) CreateAccount(ctx context.Context, in usecase.CreateAccountInput) (usecase.Account, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return usecase.Account{}, fmt.Errorf("generate account id: %w", err)
	}
	account, err := model.NewAccount(model.NewAccountParams{
		ID:            id,
		Name:          in.Name,
		InstitutionID: in.InstitutionID,
		Kind:          model.AccountKind(in.Kind),
	})
	if err != nil {
		return usecase.Account{}, err
	}
	if account.InstitutionID != nil {
		institution, err := s.institutions.Get(ctx, *account.InstitutionID)
		if err != nil {
			return usecase.Account{}, fmt.Errorf("institution %s: %w", *account.InstitutionID, err)
		}
		if institution.IsArchived() {
			return usecase.Account{}, fmt.Errorf("institution %s: %w", institution.ID, model.ErrArchived)
		}
	}
	stored, err := s.accounts.Insert(ctx, account)
	if err != nil {
		return usecase.Account{}, fmt.Errorf("create account %q: %w", account.Name, err)
	}
	return toAccount(stored), nil
}

// ArchiveAccount archives the account. An archived account stays archived:
// asking again returns it as it is.
func (s *Service) ArchiveAccount(ctx context.Context, id uuid.UUID) (usecase.Account, error) {
	stored, err := s.accounts.Archive(ctx, id)
	if err != nil {
		return usecase.Account{}, fmt.Errorf("archive account %s: %w", id, err)
	}
	return toAccount(stored), nil
}

// ListAccounts returns the accounts by name.
func (s *Service) ListAccounts(ctx context.Context, in usecase.ListAccountsInput) (usecase.ListAccountsOutput, error) {
	accounts, err := s.accounts.List(ctx, in.IncludeArchived)
	if err != nil {
		return usecase.ListAccountsOutput{}, fmt.Errorf("list accounts: %w", err)
	}
	out := usecase.ListAccountsOutput{Accounts: make([]usecase.Account, 0, len(accounts))}
	for _, account := range accounts {
		out.Accounts = append(out.Accounts, toAccount(account))
	}
	return out, nil
}

// GetAccount returns the account, archived or not.
func (s *Service) GetAccount(ctx context.Context, id uuid.UUID) (usecase.Account, error) {
	account, err := s.accounts.Get(ctx, id)
	if err != nil {
		return usecase.Account{}, fmt.Errorf("get account %s: %w", id, err)
	}
	return toAccount(account), nil
}

func toAccount(a model.Account) usecase.Account {
	return usecase.Account{
		ID:            a.ID,
		Name:          a.Name,
		InstitutionID: a.InstitutionID,
		Kind:          string(a.Kind),
		CreatedAt:     a.CreatedAt,
		ArchivedAt:    a.ArchivedAt,
	}
}
