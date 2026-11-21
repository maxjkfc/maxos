package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// GetDraftSummary says how many drafts are waiting to be confirmed and what
// confirming them would change in the balances, by the month they happened in and by
// the account they are of: the page that reviews an imported month before it is
// confirmed reads it, so that the review does not have to load every draft. A draft
// changes no balance, no budget and no report until ConfirmTransactions confirms it.
//
// A draft is what ConfirmTransactions confirms by itself: a transaction in the
// status draft, and, of an instalment plan, its origin and not its periods. The
// origin stands for the whole plan, which is confirmed, discarded and voided as one,
// so a plan is one draft whatever number of periods it has, and it is a draft of the
// month of its origin. These are the drafts that ListTransactions lists for the
// status draft. Confirmed and void transactions are not in the summary, whatever day
// they happened on, and today does not limit the drafts: a draft dated after today is
// waiting like any other. The month of a draft is the month of the day it happened
// on.
//
// A draft is of an account when it has a posting to it, of any book: the
// definition of ListTransactionsInput.AccountID, so the origin of a purchase paid in
// instalments, whose posting to the card is of the instalment book, is a draft of
// the card. A draft that has no posting to any account, such as an expense that a
// party paid for me, is of none (DraftAccount.AccountID is nil): the definition of
// ListTransactionsInput.WithoutAccount. A draft is counted once wherever the total of
// drafts is meant (Count of the output and of a month), and once for each account it
// is of in the Count of that account: a transfer between two accounts is a draft of
// each of them. So for a month and an account, the count here is the number of
// transactions that ListTransactions lists for that month, that account and the
// status draft; and for a month and no account, those it lists with WithoutAccount.
//
// The amounts say what confirming the drafts would change in the balances as
// GetBalances reads them today: the account postings of the drafts, each in its
// account, and only of the drafts whose day is not after today, because a transaction
// counts toward a balance from its day. A period of a draft plan that falls due
// after today is left out, then: it changes no balance until its due day, as one
// that is due already does, so it is in, in the month and the account of its
// plan. A draft dated after today is left out of the amounts and counted in Count.
//
// A draft that is a transfer between two of my own accounts in one instrument (with
// a fee or without) changes no total: what leaves one account arrives at another.
// For each such transaction and instrument, the smaller of what leaves accounts and
// what arrives in them is that part: it is in neither the Outflow nor the Inflow of
// a month or of all the drafts, and it is in the amounts of each account it moves,
// whose balance does change. A fee that the transfer has is what is left over, and
// is in the Outflow. OwnTransfers is how many drafts have such a part; each of them
// is in Count too. Only a transfer has it: what an income, a dealing, a reward or a
// trade moves does not go between my own accounts, even when it has a fee, and a
// transfer dated after today has none, because it has no amounts.
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

// DraftAccount is the drafts of one month that are of one account.
type DraftAccount struct {
	// AccountID is the account, or nil for the drafts of the month that are of none.
	// Those have no Amounts.
	AccountID *uuid.UUID
	// Count is how many drafts of the month are of the account.
	Count int
	// Amounts are what they would move of each instrument in the account, by
	// instrument code, an instrument that none moves left out. The part of a transfer
	// that arrives or leaves this account is in them: its balance does change.
	Amounts []DraftAmount
}

// DraftMonth is the drafts that happened in one month.
type DraftMonth struct {
	// Month is the first day of the month, as a date at midnight UTC.
	Month time.Time
	// Count is how many drafts happened in the month, each counted once, whatever
	// number of accounts it is of.
	Count int
	// OwnTransfers is how many of them move money between my own accounts, which the
	// Amounts do not count.
	OwnTransfers int
	// OwnTransferAmounts is the money that they move between my own accounts, by
	// instrument code: of each instrument, Outflow and Inflow are both what moves, and
	// the Net is zero. It is in the Amounts of each account that it moves (it leaves
	// one and arrives in another) and not in the Amounts of the month: of each
	// instrument, the Outflow of the Amounts of the Accounts added up, less this
	// Outflow, is the Outflow of the Amounts of the month, and so is the Inflow.
	OwnTransferAmounts []DraftAmount
	// Amounts are what the drafts of the month would change in the balances of all
	// the accounts together, by instrument code: what moves between my own accounts
	// is not in them, so they are not the Amounts of Accounts added up.
	Amounts []DraftAmount
	// Accounts are the accounts the drafts of the month are of, by account name and
	// then id, and last the drafts that are of none (AccountID nil).
	Accounts []DraftAccount
}

// GetDraftSummaryOutput holds the drafts that are waiting.
type GetDraftSummaryOutput struct {
	// Count is how many drafts there are, each counted once.
	Count int
	// OwnTransfers is how many of them move money between my own accounts.
	OwnTransfers int
	// OwnTransferAmounts is the money that they move between my own accounts, by
	// instrument code: the OwnTransferAmounts of Months added up.
	OwnTransferAmounts []DraftAmount
	// Amounts are what all the drafts would change in the balances of all the
	// accounts together, by instrument code: the Amounts of Months added up.
	Amounts []DraftAmount
	// Months are the months drafts happened in, the newest first. A month with no
	// draft is not in it.
	Months []DraftMonth
}
