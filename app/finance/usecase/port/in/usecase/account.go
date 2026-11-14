package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Account is an account as the use cases return it.
type Account struct {
	ID   uuid.UUID
	Name string
	// InstitutionID is the institution that keeps the account; nil for an account
	// with none.
	InstitutionID *uuid.UUID
	// Kind is "cash", "bank", "card", "exchange", "brokerage", "wallet" or "loan".
	Kind      string
	CreatedAt time.Time
	// ArchivedAt is nil while the account is in use.
	ArchivedAt *time.Time
}

// CreateAccount adds an account.
type CreateAccount interface {
	CreateAccount(ctx context.Context, in CreateAccountInput) (Account, error)
}

// CreateAccountInput is an account to add.
type CreateAccountInput struct {
	// Name is trimmed of the whitespace around it and must not be empty. No other
	// account, archived or not, may have the same name.
	Name string
	// Kind is "cash", "bank", "card", "exchange", "brokerage", "wallet" or "loan".
	Kind string
	// InstitutionID, when set, must be an institution that exists and is not
	// archived. Cash and wallets I keep myself have none.
	InstitutionID *uuid.UUID
}

// ArchiveAccount retires an account: it stays for the transactions that already
// use it, but new ones may no longer use it. Archiving an archived account
// changes nothing and returns it as it is.
type ArchiveAccount interface {
	ArchiveAccount(ctx context.Context, id uuid.UUID) (Account, error)
}

// ListAccounts lists the accounts.
type ListAccounts interface {
	ListAccounts(ctx context.Context, in ListAccountsInput) (ListAccountsOutput, error)
}

// ListAccountsInput filters the listing.
type ListAccountsInput struct {
	// IncludeArchived lists the archived accounts too; by default only those in
	// use.
	IncludeArchived bool
}

// ListAccountsOutput holds the accounts, by name.
type ListAccountsOutput struct {
	Accounts []Account
}

// GetAccount returns one account, archived or not.
type GetAccount interface {
	GetAccount(ctx context.Context, id uuid.UUID) (Account, error)
}
