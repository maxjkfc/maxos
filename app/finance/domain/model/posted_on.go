package model

import (
	"fmt"
	"time"
)

// The posting date (入帳日) of a transaction is the day the institution put it on a
// statement, when that is not the day it happened. It decides which statement of a
// card the transaction is on, and nothing else: balances, budgets and dealings go
// by OccurredOn, as they always did. So it is not part of what a confirmed
// transaction is that must not change: no posting, amount, date or base amount is
// touched by setting or clearing it, and the transaction can be given one, have it
// changed or taken away after it is confirmed, which a statement needs, as the day
// the institution posted it is learnt when the statement comes.

// StatementDate returns the day that puts the transaction on a statement: its
// posting date when it has one, and otherwise the day it happened.
func (t Transaction) StatementDate() time.Time {
	if t.PostedOn != nil {
		return *t.PostedOn
	}
	return t.OccurredOn
}

// CheckPostedOnAllowed returns an error when the transaction cannot have a posting
// date, or cannot have it changed, whatever the date: ErrPartOfPlan for one that is
// part of an instalment plan, the origin and the periods, whose days are the days
// the institution bills them already and which a reschedule replaces, and
// ErrTransactionVoid for a void one. A draft and a confirmed transaction pass.
// What it needs of nothing but the transaction, so that a call can refuse a plan
// without taking a lock on it; WithPostedOn is the rest of the rules.
func (t Transaction) CheckPostedOnAllowed() error {
	if t.PlanID != nil {
		return fmt.Errorf("%w: transaction %s is in plan %s, and a plan has no posting date: its periods fall due on the days the institution bills them",
			ErrPartOfPlan, t.ID, *t.PlanID)
	}
	if t.Status == StatusVoid {
		return fmt.Errorf("%w: transaction %s is void, and counts on no statement", ErrTransactionVoid, t.ID)
	}
	return nil
}

// WithPostedOn returns the transaction with the posting date postedOn, and with
// none when it is nil. Only the day postedOn falls on counts, as its own location
// reads it. accounts are the accounts of the transaction's account postings, which
// the caller loads: it is what the rule that the transaction must post to a card
// is checked on. A draft and a confirmed transaction may be given one, change it
// and have it taken away, as often as wanted, and giving the date it has returns
// the transaction as it is. A transaction is refused when it:
//
//   - is part of an instalment plan (ErrPartOfPlan), or is void
//     (ErrTransactionVoid); see CheckPostedOnAllowed;
//   - has no account posting to an account that is a card (ErrNoCardPosting);
//   - is given a date before the day it happened (ErrPostedBeforeOccurred).
//
// The rules apply to taking the date away as much as to setting it: a transaction
// that cannot have a posting date is not asked about one.
func (t Transaction) WithPostedOn(postedOn *time.Time, accounts []Account) (Transaction, error) {
	if err := t.CheckPostedOnAllowed(); err != nil {
		return Transaction{}, err
	}
	if !t.postsToCard(accounts) {
		return Transaction{}, fmt.Errorf("%w: transaction %s has no posting to an account that is a card", ErrNoCardPosting, t.ID)
	}
	t.PostedOn = nil
	if postedOn != nil {
		on := DateOf(*postedOn)
		t.PostedOn = &on
	}
	if err := t.checkPostedOn(); err != nil {
		return Transaction{}, err
	}
	return t, nil
}

// postsToCard reports whether the transaction has an account posting to one of the
// accounts that is a card.
func (t Transaction) postsToCard(accounts []Account) bool {
	for _, p := range t.Postings {
		if p.Book != BookAccount || p.AccountID == nil {
			continue
		}
		for _, a := range accounts {
			if a.ID == *p.AccountID && a.Kind == AccountKindCard {
				return true
			}
		}
	}
	return false
}

// checkPostedOn checks the posting date the transaction has, if it has one: that
// it is not in a plan, and that it is not before the day the transaction happened.
// What needs the accounts is not checked here (see WithPostedOn): the transaction
// does not know what kind its accounts are.
func (t Transaction) checkPostedOn() error {
	if t.PostedOn == nil {
		return nil
	}
	if t.PlanID != nil {
		return fmt.Errorf("%w: transaction %s is in plan %s, and has a posting date", ErrPartOfPlan, t.ID, *t.PlanID)
	}
	if t.PostedOn.Before(t.OccurredOn) {
		return fmt.Errorf("%w: it is %s, and the transaction happened on %s",
			ErrPostedBeforeOccurred, t.PostedOn.Format(time.DateOnly), t.OccurredOn.Format(time.DateOnly))
	}
	return nil
}
