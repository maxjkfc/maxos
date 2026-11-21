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
	Kind string
	// ClosingDay is the day of the month the statement of a card closes, from 1 to
	// 31, and nil for an account that has none: only a card has one. In a month with
	// fewer days it is the last day of the month, so 31 is always the last day.
	ClosingDay *int
	// PaymentDueDay is the day of the month a statement is to be paid by, from 1 to
	// 31, counted like ClosingDay; nil for an account that has none. The statement
	// is due on the first such day after it closes. Only an account with a closing
	// day has one.
	PaymentDueDay *int
	CreatedAt     time.Time
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
	// ClosingDay and PaymentDueDay are the days a card closes its statement and has
	// it paid by, from 1 to 31 (ErrDayOutOfRange), and may be left out. Only a card
	// has them (ErrNotACard), and a payment due day needs a closing day
	// (ErrDueDayWithoutClosingDay).
	ClosingDay    *int
	PaymentDueDay *int
}

// SetAccountStatementDays sets, changes or clears the closing day and the payment
// due day of a card. Both days are replaced by what the input has: a nil clears
// the day it stands for, so a call that gives a closing day and no payment due day
// leaves the card with the first only. An archived card can have them changed too:
// it keeps the transactions it has, and its statements are still read.
//
// The days are not historised: the statements are cut from the days the card has
// now, so changing them cuts the past statements again, and no statement keeps the
// days it was once cut by.
type SetAccountStatementDays interface {
	SetAccountStatementDays(ctx context.Context, in SetAccountStatementDaysInput) (Account, error)
}

// SetAccountStatementDaysInput names the account and its new days.
type SetAccountStatementDaysInput struct {
	// ID is the account, which must be a card (ErrNotACard) when a day is given.
	ID uuid.UUID
	// ClosingDay is the new closing day, from 1 to 31 (ErrDayOutOfRange); nil clears
	// it, and the payment due day has to be cleared with it, as a due day needs a
	// closing day (ErrDueDayWithoutClosingDay).
	ClosingDay *int
	// PaymentDueDay is the new payment due day, from 1 to 31 (ErrDayOutOfRange); nil
	// clears it.
	PaymentDueDay *int
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
