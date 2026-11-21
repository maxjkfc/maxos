package model

import (
	"cmp"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DraftSummary is the drafts that are waiting to be confirmed, grouped by the month
// they happened in and then by the account they move. It is worked out when asked
// for and never stored: only drafts are in it, not confirmed or void transactions,
// whatever day they happened on.
//
// A draft is counted once in its month, and once in each account it has an account
// posting to, so a transfer between two accounts is in both. The postings that make
// up the amounts are the account postings: an instalment posting is not one, so a
// draft that has none, such as an expense a party paid or the origin of a purchase
// paid in instalments, belongs to no account.
type DraftSummary struct {
	// Months are the months drafts happened in, the newest first. A month with no
	// draft is not in it.
	Months []DraftMonth
}

// Count returns how many drafts there are, each counted once: the drafts of the
// months added up.
func (s DraftSummary) Count() int {
	n := 0
	for _, m := range s.Months {
		n += m.Count
	}
	return n
}

// DraftMonth is the drafts that happened in one month.
type DraftMonth struct {
	// Month is the first day of the month, as a date at midnight UTC (see MonthOf).
	Month time.Time
	// Count is how many drafts happened in the month, each counted once, whatever
	// number of accounts it moves.
	Count int
	// Accounts are the accounts the drafts of the month move, by account name and
	// then id, and last the drafts that move none.
	Accounts []DraftAccount
}

// DraftAccount is the drafts of one month that move one account, or that move none.
type DraftAccount struct {
	// AccountID is the account, or nil for the drafts of the month that have no
	// account posting. Those have no Amounts.
	AccountID *uuid.UUID
	// Count is how many drafts of the month have an account posting to the account.
	Count int
	// Amounts are what they move of each instrument, by instrument code, an
	// instrument that none moves left out.
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

// AddDraftAmounts adds up the amounts of the groups, one amount for each
// instrument that any of them has, by instrument code. It is how the amounts of
// the accounts of a month become those of the month, and those of the months
// become the amounts of all the drafts.
func AddDraftAmounts(groups ...[]DraftAmount) []DraftAmount {
	var (
		sums  []DraftAmount
		index = make(map[uuid.UUID]int) // instrument id to its place in sums
	)
	for _, group := range groups {
		for _, amount := range group {
			i, seen := index[amount.InstrumentID]
			if !seen {
				i = len(sums)
				index[amount.InstrumentID] = i
				sums = append(sums, DraftAmount{InstrumentID: amount.InstrumentID, InstrumentCode: amount.InstrumentCode})
			}
			sums[i].Outflow = sums[i].Outflow.Add(amount.Outflow)
			sums[i].Inflow = sums[i].Inflow.Add(amount.Inflow)
		}
	}
	slices.SortFunc(sums, func(a, b DraftAmount) int { return cmp.Compare(a.InstrumentCode, b.InstrumentCode) })
	return sums
}
