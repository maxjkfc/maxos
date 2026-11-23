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

// PreviewBalance is how much of an instrument an account would hold on a day if every
// draft that has happened by then were confirmed: what its Balance holds, and, added to
// it, what confirming the drafts would change. It is worked out when asked for and never
// stored. A draft changes no balance, no budget and no report until it is confirmed, so
// this is not a balance but the balance there would be, and it is only ever shown as
// that.
type PreviewBalance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// Confirmed is the Quantity of the Balance of the account and instrument: its
	// confirmed account postings up to the day, added up.
	Confirmed decimal.Decimal
	// Drafts is what confirming the drafts would add to Confirmed, negative when it
	// would take away: the account postings of the transactions in the status draft
	// that happened on or before the day, added up. They are the postings that
	// SummarizeDrafts adds up into the amounts of an account, of all months: those of
	// the account book only, each in its own account, so a transfer between two of my
	// accounts is in both of them, and a period of a draft plan from the day it falls
	// due, not before. A draft dated after the day is not in it, nor is a void
	// transaction.
	Drafts decimal.Decimal
	// UnbilledInstalment is that of the Balance: the principal of the confirmed
	// instalment plans that is not yet due. A draft plan has none yet: it is not in
	// the balance of an account until it is confirmed, and the periods of it that
	// have fallen due are in Drafts, the way they will be in the Quantity of the
	// Balance.
	UnbilledInstalment decimal.Decimal
}

// Quantity returns what the account would hold: Confirmed and Drafts added up.
func (b PreviewBalance) Quantity() decimal.Decimal {
	return b.Confirmed.Add(b.Drafts)
}
