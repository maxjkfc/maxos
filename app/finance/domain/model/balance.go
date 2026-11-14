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
	// is zero or less, as what the account owes. It is not in Quantity: a period
	// reaches the balance on the day it falls due, and until then it is shown
	// here.
	UnbilledInstalment decimal.Decimal
}
