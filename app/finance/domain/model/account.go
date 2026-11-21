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
	// ClosingDay is the day of the month the statement of a card closes, from
	// MinDayOfMonth to MaxDayOfMonth, and nil for an account that has none. In a
	// month with fewer days it is the last day of the month, so 31 is always the last
	// day (see DateInMonth). Only a card has one.
	ClosingDay *int
	// PaymentDueDay is the day of the month a statement is to be paid by, counted
	// like ClosingDay, and nil for an account that has none. The statement is due on
	// the first such day after the day it closes. Only an account that has a closing
	// day has one.
	PaymentDueDay *int
}

// NewAccountParams is the raw input of an account.
type NewAccountParams struct {
	ID            uuid.UUID
	Name          string
	InstitutionID *uuid.UUID
	Kind          AccountKind
	// ClosingDay and PaymentDueDay follow the rules of Account.WithStatementDays.
	ClosingDay    *int
	PaymentDueDay *int
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
	if err := checkStatementDays(p.Kind, p.ClosingDay, p.PaymentDueDay); err != nil {
		return Account{}, err
	}
	account := Account{
		ID:            p.ID,
		Name:          name,
		Kind:          p.Kind,
		ClosingDay:    clonePtr(p.ClosingDay),
		PaymentDueDay: clonePtr(p.PaymentDueDay),
	}
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

// CheckHasStatements returns nil when the account has statements: it is a card, and
// has a closing day to cut them by. An account that is not a card, and a card that
// has no closing day, is ErrNoStatements, with which of the two it is.
func (a Account) CheckHasStatements() error {
	switch {
	case a.Kind != AccountKindCard:
		return fmt.Errorf("%w: account %q is a %s, not a card", ErrNoStatements, a.Name, a.Kind)
	case a.ClosingDay == nil:
		return fmt.Errorf("%w: card %q has no closing day", ErrNoStatements, a.Name)
	}
	return nil
}

// WithStatementDays returns the account with the closing day and the payment due
// day given in place of those it has: a nil clears the day it stands for, so the
// two are set together, and a call that gives only one of them clears the other.
// A day is from MinDayOfMonth to MaxDayOfMonth (ErrDayOutOfRange). Only a card
// has them (ErrNotACard), and a payment due day needs a closing day
// (ErrDueDayWithoutClosingDay). Nothing is checked of the statements that were cut
// by the old days: they are not stored, and are cut again by the new ones.
func (a Account) WithStatementDays(closingDay, paymentDueDay *int) (Account, error) {
	if err := checkStatementDays(a.Kind, closingDay, paymentDueDay); err != nil {
		return Account{}, err
	}
	a.ClosingDay = clonePtr(closingDay)
	a.PaymentDueDay = clonePtr(paymentDueDay)
	return a, nil
}

// checkStatementDays checks the closing day and the payment due day an account of
// the kind is to have.
func checkStatementDays(kind AccountKind, closingDay, paymentDueDay *int) error {
	if closingDay == nil && paymentDueDay == nil {
		return nil
	}
	if kind != AccountKindCard {
		return fmt.Errorf("%w: the account is of the kind %s", ErrNotACard, kind)
	}
	if closingDay == nil {
		return ErrDueDayWithoutClosingDay
	}
	if err := checkDayOfMonth("closing day", *closingDay); err != nil {
		return err
	}
	if paymentDueDay != nil {
		return checkDayOfMonth("payment due day", *paymentDueDay)
	}
	return nil
}

// checkDayOfMonth returns ErrDayOutOfRange, naming what the day is, for a day that
// is not from MinDayOfMonth to MaxDayOfMonth.
func checkDayOfMonth(what string, day int) error {
	if day < MinDayOfMonth || day > MaxDayOfMonth {
		return fmt.Errorf("%w: the %s is %d", ErrDayOutOfRange, what, day)
	}
	return nil
}
