package model

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Balance is how much of an instrument an account holds on a day: the quantities
// of its confirmed postings up to that day added up. It is worked out when asked
// for and never stored; drafts and void transactions are not in it, and neither
// is a transaction that happens after the day. A negative balance is what the
// account owes.
type Balance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Quantity is what the account holds: its account postings added up.
	Quantity decimal.Decimal
	// UnbilledInstalment is the principal of instalment plans that is not yet due
	// on the day (未到期分期): the instalment postings of the account added up. It
	// is zero or less, with the sign of what an account owes: -11000 is 11,000 still
	// to be charged to the account in the periods to come. It is not in Quantity: a
	// period reaches the balance on the day it falls due, and until then it is shown
	// here. InstalmentPlanSummary.Remaining is the same amount of one plan, as a
	// size that is zero or more.
	UnbilledInstalment decimal.Decimal
}

// PreviewBalance is how much of an instrument an account would hold on a day, and how
// much it would owe in instalments that are not yet due, if every draft that has
// happened by then were confirmed: what its Balance says and, added to it, what
// confirming the drafts would change. It is worked out when asked for and never
// stored. A draft changes no balance, no budget and no report until it is confirmed,
// so this is not a balance but the balance there would be, and it is only ever shown
// as that. It is what GetBalances says of the account once every draft is confirmed,
// both of its figures.
type PreviewBalance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Held is what the account holds: the account book. What is confirmed of it is
	// the Quantity of the Balance, and what the drafts add is the postings of the
	// account book of the transactions in the status draft that happened on or before
	// the day, added up: the postings that SummarizeDrafts adds up into the amounts of
	// an account, of all months, each in its own account. So a transfer between two
	// of my accounts is in both of them, and a period of a draft plan is in from the
	// day it falls due, not before. A draft dated after the day is not in it, nor is a
	// void transaction.
	Held PreviewFigure
	// Unbilled is the principal of instalment plans that is not yet due: the
	// instalment book, zero or less, as in Balance. What is confirmed of it is the
	// UnbilledInstalment of the Balance, and what the drafts add is the postings of the
	// instalment book of the same drafts: the origin of a draft plan owes the whole
	// principal from its day, and each of its periods gives back what it charges from
	// the day it falls due, which then is in Held. A bill converted into instalments
	// puts into Held what it pays off and into Unbilled what is owed from then on, so a
	// preview that left Unbilled out would show the debt of the card less than it will
	// be once the plan is confirmed.
	Unbilled PreviewFigure
}

// HasConfirmed is whether anything of the balance is confirmed, in either figure.
func (b PreviewBalance) HasConfirmed() bool {
	return !b.Held.Confirmed.IsZero() || !b.Unbilled.Confirmed.IsZero()
}

// PreviewFigure is one figure of a PreviewBalance: what is confirmed of it, and what
// the drafts would add to it, negative when they would take away.
type PreviewFigure struct {
	Confirmed decimal.Decimal
	Drafts    decimal.Decimal
}

// Total is what the figure would be with every draft confirmed: Confirmed and Drafts
// added up.
func (f PreviewFigure) Total() decimal.Decimal {
	return f.Confirmed.Add(f.Drafts)
}

// Basis says what the figure is made of.
func (f PreviewFigure) Basis() PreviewBasis {
	switch {
	case f.Drafts.IsZero():
		return BasisConfirmed
	case f.Confirmed.IsZero():
		return BasisDrafts
	default:
		return BasisBoth
	}
}

// PreviewBasis is what a PreviewFigure is made of.
type PreviewBasis string

const (
	// BasisConfirmed is a figure that no draft changes: it is what it is, or nothing.
	BasisConfirmed PreviewBasis = "confirmed"
	// BasisBoth is a figure that is confirmed in part, and that drafts change.
	BasisBoth PreviewBasis = "both"
	// BasisDrafts is a figure none of which is confirmed: drafts make up the whole of it.
	BasisDrafts PreviewBasis = "drafts"
)

// BalancePreview is the preview balances of every account and instrument on a day, and
// how many drafts wait: what a page that says "these are what the balances would be with
// the drafts confirmed" has to say, from one read.
type BalancePreview struct {
	// Balances are the accounts and instruments with a confirmed posting of either book
	// or a draft's posting to an account of either book, by account name and then
	// instrument code.
	Balances []PreviewBalance
	// Drafts is how many drafts wait, each counted once whatever day it happened on and
	// whatever it moves: the number that SummarizeDrafts counts, a plan being the one
	// draft of its origin.
	Drafts int
}

// HasConfirmed is whether anything of any balance is confirmed.
func (p BalancePreview) HasConfirmed() bool {
	for _, b := range p.Balances {
		if b.HasConfirmed() {
			return true
		}
	}
	return false
}
