package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GetDraftSummary says how many drafts are waiting to be confirmed and what they
// would change, grouped by the month they happened in and then by the account they
// move: the page that reviews an imported month before it is confirmed reads it, so
// that the review does not have to load every draft. A draft changes no balance, no
// budget and no report until ConfirmTransactions confirms it; the amounts here are
// what it would move once it is.
//
// Only drafts are in it, not confirmed or void transactions, whatever day they
// happened on, and today does not limit them: a draft dated after today is waiting
// like any other. The month of a draft is the month of the day it happened on. Only
// account postings are amounts of an account, and an instalment posting is not one:
// the origin of a purchase paid in instalments has that and its expense and no
// account posting, so it moves no account, and each period of the plan, which has an
// account posting, is a draft of the month it falls due in. The origin of the
// conversion of a bill into instalments does have an account posting, the one that
// pays the bill off, and is a draft of that account.
//
// A draft is counted once wherever the total of drafts is meant (Count of the
// output and of a month), and once for each account it moves in an account's own
// Count: a transfer between two accounts is a draft of each of them.
type GetDraftSummary interface {
	GetDraftSummary(ctx context.Context, in GetDraftSummaryInput) (GetDraftSummaryOutput, error)
}

// GetDraftSummaryInput asks for every draft there is; it has nothing to choose yet.
type GetDraftSummaryInput struct{}

// DraftAmount is what drafts would move of one instrument in an account or in
// some accounts together: the account postings of the drafts added up, so what an
// account would hold more or less once they were confirmed.
type DraftAmount struct {
	InstrumentID uuid.UUID
	// Outflow is the negative account postings added up, as a positive number: what
	// would leave the accounts.
	Outflow decimal.Decimal
	// Inflow is the positive account postings added up: what would arrive.
	Inflow decimal.Decimal
	// Net is Inflow less Outflow, with the sign of a balance: negative is a
	// decrease.
	Net decimal.Decimal
}

// DraftAccount is the drafts of one month that move one account.
type DraftAccount struct {
	// AccountID is the account, or nil for the drafts of the month that have no
	// account posting: an expense paid by a party on my behalf, the origin of a
	// purchase paid in instalments. Those have no Amounts.
	AccountID *uuid.UUID
	// Count is how many drafts of the month move the account.
	Count int
	// Amounts are what they move of each instrument, by instrument code, an
	// instrument that none moves left out.
	Amounts []DraftAmount
}

// DraftMonth is the drafts that happened in one month.
type DraftMonth struct {
	// Month is the first day of the month, as a date at midnight UTC.
	Month time.Time
	// Count is how many drafts happened in the month, each counted once, whatever
	// number of accounts it moves.
	Count int
	// Amounts are what the drafts of the month would move of each instrument in all
	// the accounts together, by instrument code: the Amounts of Accounts added up.
	Amounts []DraftAmount
	// Accounts are the accounts the drafts of the month move, by account name and
	// then id, and last the drafts that have none (AccountID nil).
	Accounts []DraftAccount
}

// GetDraftSummaryOutput holds the drafts that are waiting.
type GetDraftSummaryOutput struct {
	// Count is how many drafts there are, each counted once.
	Count int
	// Amounts are what all the drafts would move of each instrument, by instrument
	// code: the Amounts of Months added up.
	Amounts []DraftAmount
	// Months are the months drafts happened in, the newest first. A month with no
	// draft is not in it.
	Months []DraftMonth
}
