package model

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// AccountKind is what sort of place an account is.
type AccountKind string

// The kinds of account.
const (
	AccountKindCash      AccountKind = "cash"
	AccountKindBank      AccountKind = "bank"
	AccountKindCard      AccountKind = "card"
	AccountKindExchange  AccountKind = "exchange"
	AccountKindBrokerage AccountKind = "brokerage"
	AccountKindWallet    AccountKind = "wallet"
	AccountKindLoan      AccountKind = "loan"
)

// Valid reports whether k is one of the kinds above.
func (k AccountKind) Valid() bool {
	switch k {
	case AccountKindCash, AccountKindBank, AccountKindCard, AccountKindExchange,
		AccountKindBrokerage, AccountKindWallet, AccountKindLoan:
		return true
	}
	return false
}

// Account is a place that holds instruments: cash, a bank account, a card, an
// exchange or brokerage account, a wallet, a loan. It holds any number of
// instruments, so it is not tied to one; its balance in each is what its
// transactions add up to.
type Account struct {
	ID   uuid.UUID
	Name string
	// InstitutionID is the institution that keeps the account; nil for an account
	// with none, such as cash or a wallet I keep myself. Whether the institution
	// exists and is in use is for the use case to check: the account only holds its
	// id.
	InstitutionID *uuid.UUID
	Kind          AccountKind
	// CreatedAt is assigned when the account is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the account is in use. An archived account stays
	// for the transactions that already use it, but new ones may not.
	ArchivedAt *time.Time
}

// NewAccountParams is the raw input of an account.
type NewAccountParams struct {
	ID            uuid.UUID
	Name          string
	InstitutionID *uuid.UUID
	Kind          AccountKind
}

// NewAccount validates an account and returns it. The whitespace around the
// name is trimmed.
func NewAccount(p NewAccountParams) (Account, error) {
	name, err := normalizeName(p.Name)
	if err != nil {
		return Account{}, err
	}
	if !p.Kind.Valid() {
		return Account{}, fmt.Errorf("%w %q: want one of cash, bank, card, exchange, brokerage, wallet, loan", ErrInvalidAccountKind, p.Kind)
	}
	account := Account{ID: p.ID, Name: name, Kind: p.Kind}
	if p.InstitutionID != nil {
		institutionID := *p.InstitutionID
		account.InstitutionID = &institutionID
	}
	return account, nil
}

// IsArchived reports whether the account is archived.
func (a Account) IsArchived() bool {
	return a.ArchivedAt != nil
}
