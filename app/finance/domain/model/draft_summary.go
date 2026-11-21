package model

import (
	"bytes"
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DraftSummary is the drafts that are waiting to be confirmed, grouped by the month
// they happened in and then by the account they are of, and what confirming them
// would change in the balances. SummarizeDrafts works it out; it is never stored.
type DraftSummary struct {
	// Count is how many drafts there are, each counted once.
	Count int
	// OwnTransfers is how many of them move money between my own accounts, which the
	// Amounts leave out.
	OwnTransfers int
	// Amounts are what all the drafts would change in the balances of all the
	// accounts together, by instrument code: the Amounts of Months added up.
	Amounts []DraftAmount
	// Months are the months drafts happened in, the newest first. A month with no
	// draft is not in it.
	Months []DraftMonth
}

// DraftMonth is the drafts that happened in one month.
type DraftMonth struct {
	// Month is the first day of the month, as a date at midnight UTC (see MonthOf).
	Month time.Time
	// Count is how many drafts happened in the month, each counted once, whatever
	// number of accounts it is of.
	Count int
	// OwnTransfers is how many of them move money between my own accounts.
	OwnTransfers int
	// Amounts are what the drafts of the month would change in the balances of all
	// the accounts together, by instrument code: what moves between my own accounts
	// is not in them, so they are not the Amounts of Accounts added up.
	Amounts []DraftAmount
	// Accounts are the accounts the drafts of the month are of, by account name and
	// then id, and last the drafts that are of none.
	Accounts []DraftAccount
}

// DraftAccount is the drafts of one month that are of one account, or that are of
// none.
type DraftAccount struct {
	// AccountID is the account, or nil for the drafts of the month that have no
	// posting to any account. Those have no Amounts.
	AccountID *uuid.UUID
	// Count is how many drafts of the month have a posting to the account.
	Count int
	// Amounts are what they move of each instrument in the account, by instrument
	// code, an instrument that none moves left out.
	Amounts []DraftAmount
}

// DraftAmount is what drafts would move of one instrument in an account, or in some
// accounts together: the account postings of the drafts added up.
type DraftAmount struct {
	InstrumentID uuid.UUID
	// InstrumentCode is the code of the instrument, which amounts are put in the
	// order of.
	InstrumentCode string
	// Outflow is the negative account postings added up, as a positive number.
	Outflow decimal.Decimal
	// Inflow is the positive account postings added up.
	Inflow decimal.Decimal
}

// Net returns Inflow less Outflow, with the sign of a balance: negative is a
// decrease.
func (a DraftAmount) Net() decimal.Decimal {
	return a.Inflow.Sub(a.Outflow)
}

// DraftTransaction is a transaction whose status is draft, as SummarizeDrafts
// reads it: an ordinary one, the origin of an instalment plan, or a period of one.
type DraftTransaction struct {
	ID   uuid.UUID
	Kind Kind
	// OccurredOn is the day it happened, as a date at midnight UTC (see DateOf).
	OccurredOn time.Time
	// PlanID and PlanSeq say where it is in an instalment plan, as those of
	// Transaction do; both are nil for one that is in none.
	PlanID  *uuid.UUID
	PlanSeq *int
	// Postings are its postings that have an account, of the account book or of the
	// instalment book, and no others.
	Postings []DraftPosting
}

// DraftPosting is a posting of a draft to an account.
type DraftPosting struct {
	AccountID uuid.UUID
	// AccountName is the name of the account, which the accounts of a month are put
	// in the order of.
	AccountName string
	// Book is the account book or the instalment book.
	Book         Book
	InstrumentID uuid.UUID
	// InstrumentCode is the code of the instrument.
	InstrumentCode string
	Quantity       decimal.Decimal
}

// isPlanOrigin and isPlanPeriod say where the transaction is in an instalment plan, as
// the methods of Transaction do.
func (t DraftTransaction) isPlanOrigin() bool { return t.PlanSeq != nil && *t.PlanSeq == 0 }
func (t DraftTransaction) isPlanPeriod() bool { return t.PlanSeq != nil && *t.PlanSeq > 0 }

// SummarizeDrafts works the summary of the drafts out from every transaction whose
// status is draft, as of today, a date (see DateOf).
//
// A draft is a transaction that is not a period of an instalment plan. The origin
// of a plan stands for the whole plan, which is confirmed, discarded and voided as
// one, so a plan is one draft whatever number of periods it has, and it is of the
// month of its origin; any other draft is of the month of the day it happened on. A
// draft is of each account it has a posting to, of any book, and of none when it
// has no posting to an account. It is counted once wherever the drafts are added up,
// and once in each account it is of.
//
// The amounts say what confirming the drafts would change in the balances today: the
// postings of the account book of the transactions that happened on or before today,
// each in its account. A period of a plan is one of them: it counts from the day it
// falls due, in the month and the account of its plan. A draft that is dated after
// today is counted, and moves nothing yet.
//
// A transfer moves money between my own accounts. Of each instrument, the smaller of
// what leaves accounts and what arrives in them is money that changes no total: it is
// left out of the amounts of the month and of all the drafts, so that what is left of
// a fee that the transfer has stays in them, and it is in the amounts of each account
// it moves, whose balance does change. A draft that has any is one of the
// OwnTransfers. Only a transfer is: an exchange or a trade moves two instruments,
// each one way, and the fee of an income, a reward, a dealing or a bill instalment,
// even one in the instrument that comes in, is not money moving from one of my
// accounts to another.
//
// The order of the transactions is of no account. SummarizeDrafts returns
// ErrMalformedPlan for a period whose plan has no origin among them.
func SummarizeDrafts(txns []DraftTransaction, today time.Time) (DraftSummary, error) {
	originDays := make(map[uuid.UUID]time.Time) // the day of the origin of each plan
	for _, t := range txns {
		if t.isPlanOrigin() {
			originDays[t.ID] = t.OccurredOn
		}
	}

	months := make(map[time.Time]*draftMonthSums)
	for _, t := range txns {
		day := t.OccurredOn
		if t.isPlanPeriod() {
			if t.PlanID == nil {
				return DraftSummary{}, fmt.Errorf("%w: period %d of transaction %s is part of no plan", ErrMalformedPlan, *t.PlanSeq, t.ID)
			}
			origin, found := originDays[*t.PlanID]
			if !found {
				return DraftSummary{}, fmt.Errorf("%w: transaction %s is a period of plan %s, whose origin is not a draft", ErrMalformedPlan, t.ID, *t.PlanID)
			}
			day = origin
		}
		first := MonthOf(day)
		month := months[first]
		if month == nil {
			month = &draftMonthSums{moves: make(draftMoves), accounts: make(map[uuid.UUID]*draftAccountSums)}
			months[first] = month
		}
		if !t.isPlanPeriod() {
			month.count(t)
		}
		if !t.OccurredOn.After(today) {
			month.move(t)
		}
	}

	out := DraftSummary{Months: make([]DraftMonth, 0, len(months))}
	overall := make(draftMoves)
	for first, sums := range months {
		month := DraftMonth{Month: first, Count: sums.drafts, OwnTransfers: sums.ownTransfers, Amounts: sums.moves.amounts()}
		accounts := make([]*draftAccountSums, 0, len(sums.accounts))
		for _, a := range sums.accounts {
			accounts = append(accounts, a)
		}
		slices.SortFunc(accounts, func(a, b *draftAccountSums) int {
			return cmp.Or(cmp.Compare(a.name, b.name), bytes.Compare(a.id[:], b.id[:]))
		})
		for _, a := range accounts {
			month.Accounts = append(month.Accounts, DraftAccount{AccountID: new(a.id), Count: a.drafts, Amounts: a.moves.amounts()})
		}
		if sums.ofNone > 0 {
			month.Accounts = append(month.Accounts, DraftAccount{Count: sums.ofNone})
		}
		for _, a := range sums.moves {
			overall.add(a.InstrumentID, a.InstrumentCode, a.Outflow, a.Inflow)
		}
		out.Count += month.Count
		out.OwnTransfers += month.OwnTransfers
		out.Months = append(out.Months, month)
	}
	out.Amounts = overall.amounts()
	slices.SortFunc(out.Months, func(a, b DraftMonth) int { return b.Month.Compare(a.Month) })
	return out, nil
}

// draftMonthSums is what is known of the drafts of one month while the summary is
// worked out.
type draftMonthSums struct {
	// drafts and ownTransfers are how many drafts there are in the month, and how many
	// of them move money between my own accounts; ofNone is how many have no account.
	drafts, ownTransfers, ofNone int
	// moves is what the drafts move in all the accounts together, without what moves
	// between my own accounts.
	moves    draftMoves
	accounts map[uuid.UUID]*draftAccountSums
}

// draftAccountSums is what is known of the drafts of one month that are of one
// account.
type draftAccountSums struct {
	id     uuid.UUID
	name   string
	drafts int
	moves  draftMoves
}

// account returns the sums of the account in the month, which are made the first
// time they are asked for.
func (m *draftMonthSums) account(id uuid.UUID, name string) *draftAccountSums {
	a := m.accounts[id]
	if a == nil {
		a = &draftAccountSums{id: id, name: name, moves: make(draftMoves)}
		m.accounts[id] = a
	}
	return a
}

// count counts the draft: in the month, and in each account that it has a posting to,
// of any book, once, or in none.
func (m *draftMonthSums) count(t DraftTransaction) {
	m.drafts++
	counted := make(map[uuid.UUID]bool, len(t.Postings))
	for _, p := range t.Postings {
		if !counted[p.AccountID] {
			counted[p.AccountID] = true
			m.account(p.AccountID, p.AccountName).drafts++
		}
	}
	if len(counted) == 0 {
		m.ofNone++
	}
}

// move adds up what the transaction moves: its postings of the account book, each in
// its account, and in all the accounts together without the part of a transfer that
// goes from one of them to another.
func (m *draftMonthSums) move(t DraftTransaction) {
	moved := make(draftMoves)
	for _, p := range t.Postings {
		if p.Book != BookAccount {
			continue
		}
		outflow, inflow := draftFlow(p.Quantity)
		m.account(p.AccountID, p.AccountName).moves.add(p.InstrumentID, p.InstrumentCode, outflow, inflow)
		moved.add(p.InstrumentID, p.InstrumentCode, outflow, inflow)
	}

	own := false
	if t.Kind == KindTransfer {
		for _, a := range moved {
			if internal := decimal.Min(a.Outflow, a.Inflow); internal.IsPositive() {
				a.Outflow, a.Inflow = a.Outflow.Sub(internal), a.Inflow.Sub(internal)
				own = true
			}
		}
	}
	for _, a := range moved {
		m.moves.add(a.InstrumentID, a.InstrumentCode, a.Outflow, a.Inflow)
	}
	if own {
		m.ownTransfers++
	}
}

// draftFlow returns what a posting of the quantity moves: a negative quantity leaves
// as an outflow, as a positive number, and a positive one arrives as an inflow.
func draftFlow(quantity decimal.Decimal) (outflow, inflow decimal.Decimal) {
	if quantity.IsNegative() {
		return quantity.Neg(), decimal.Zero
	}
	return decimal.Zero, quantity
}

// draftMoves adds up what is moved of each instrument, by the id of the instrument.
type draftMoves map[uuid.UUID]*DraftAmount

func (m draftMoves) add(instrumentID uuid.UUID, code string, outflow, inflow decimal.Decimal) {
	a := m[instrumentID]
	if a == nil {
		a = &DraftAmount{InstrumentID: instrumentID, InstrumentCode: code}
		m[instrumentID] = a
	}
	a.Outflow = a.Outflow.Add(outflow)
	a.Inflow = a.Inflow.Add(inflow)
}

// amounts returns the sums by the code of the instrument, which is compared as bytes,
// and then by its id. An instrument that nothing is moved of is left out.
func (m draftMoves) amounts() []DraftAmount {
	out := make([]DraftAmount, 0, len(m))
	for _, a := range m {
		if !a.Outflow.IsZero() || !a.Inflow.IsZero() {
			out = append(out, *a)
		}
	}
	slices.SortFunc(out, func(a, b DraftAmount) int {
		return cmp.Or(cmp.Compare(a.InstrumentCode, b.InstrumentCode), bytes.Compare(a.InstrumentID[:], b.InstrumentID[:]))
	})
	return out
}
