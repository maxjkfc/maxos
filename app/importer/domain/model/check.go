package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// BalanceLine is what an account holds of an instrument at a day: by the ledger, by
// the export, and what the plan says the ledger lacks of the export.
type BalanceLine struct {
	Account    string
	Instrument string
	// Ledger is what the ledger says, Expected what the export says, and Unrecorded
	// what the rows that are not recorded add to it: the part of the export that the
	// ledger does not have.
	Ledger     decimal.Decimal
	Expected   decimal.Decimal
	Unrecorded decimal.Decimal
}

// Difference is how much more the ledger holds than the export says.
func (l BalanceLine) Difference() decimal.Decimal { return l.Ledger.Sub(l.Expected) }

// Unexplained is the difference that what the plan does not record does not account
// for: it is nothing when the ledger holds what the export says without it.
func (l BalanceLine) Unexplained() decimal.Decimal {
	return l.Ledger.Sub(l.Expected.Sub(l.Unrecorded))
}

// MonthCheck is the balances of the accounts at the end of a month, by the ledger and
// by the export.
type MonthCheck struct {
	Month string
	End   time.Time
	Lines []BalanceLine
	// Decimals are the decimal places of each instrument in Lines, by code.
	Decimals map[string]int
}

// Differences are the lines whose ledger balance is not what the export says.
func (c MonthCheck) Differences() []BalanceLine {
	var out []BalanceLine
	for _, l := range c.Lines {
		if !l.Difference().IsZero() {
			out = append(out, l)
		}
	}
	return out
}

// Unexplained is how many differences are not accounted for by what the plan does
// not record.
func (c MonthCheck) Unexplained() int {
	n := 0
	for _, l := range c.Lines {
		if !l.Unexplained().IsZero() {
			n++
		}
	}
	return n
}
