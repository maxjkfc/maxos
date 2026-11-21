package model

import "time"

// WarningCode says what is odd in the mapping or the rows, without stopping anything.
// Like a problem, a warning is a code, where it is, and the values its text fills in.
type WarningCode string

// The warnings a plan has.
const (
	// WarningOverrideUnused is an override of a row that the import takes no override
	// for, since the row is part of an instalment plan.
	WarningOverrideUnused WarningCode = "override_unused"
	// WarningRowUnknown is an override of a row that the export does not have.
	WarningRowUnknown WarningCode = "row_unknown"
)

// Warning is something odd that does not stop anything.
type Warning struct {
	Code WarningCode
	// Subject is where it is: the entry of the mapping, as the file names it.
	Subject string
	Args    []string
}

// Where the day of an opening balance comes from, when it is not the day before the
// first row of its own account: the arguments of a CaveatOpeningDate.
const (
	OpeningFromLedgerAccount = "ledger_account"
	OpeningFromFile          = "file"
)

// DifferenceReason says why a unit does not do to the accounts what its rows say.
type DifferenceReason string

// The reasons a unit differs from its rows.
const (
	// DifferenceFee is a fee (手續費) that the ledger has no place for in this unit.
	DifferenceFee DifferenceReason = "fee"
	// DifferenceDiscount is a discount (折扣) that the ledger has no place for in this
	// unit: only an expense has one.
	DifferenceDiscount DifferenceReason = "discount"
	// DifferenceOther is an effect that differs for a reason the plan does not know.
	DifferenceOther DifferenceReason = "other"
)

// Difference is a unit whose effect on the accounts, as the ledger will have it, is
// not what its rows say.
type Difference struct {
	// Unit is the ID of the unit, and Rows the keys of its rows.
	Unit string
	Rows []string
	// Reasons say why: at least one.
	Reasons []DifferenceReason
	// Effects are what the ledger lacks of the rows, by day and by account and
	// instrument: what the rows do less what the unit does.
	Effects []Effect
}

// ProjectEvidence is what the rows that have a value of 專案 say of it: how many
// there are and which days they are on, and when the project would seem to have been
// under way. The period is for the report alone: the import never sets the active
// period of a project, because the ledger puts every transaction that has no project
// of its own into the project that is active on its day, which a guess would do to
// transactions that are not part of it.
type ProjectEvidence struct {
	// Value is the value of 專案.
	Value string
	// Rows is how many rows with a day have it, First and Last the first and the last
	// of those days.
	Rows        int
	First, Last time.Time
	// From and To are the period the rows suggest, both given or neither: the longest
	// run of consecutive days, of two days or more, that holds 80% of the rows.
	From, To time.Time
}
