package model

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Balance is how much of an instrument an account holds: the quantities of its
// confirmed postings added up. It is worked out when asked for and never stored;
// drafts and void transactions are not in it. A negative balance is what the
// account owes.
type Balance struct {
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	Quantity     decimal.Decimal
}
