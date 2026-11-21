package usecase

import (
	"fmt"
	"strings"
)

// LedgerFailureCode says what stopped a command in its dealings with the ledger, as a
// problem or a warning of a plan does: without the words, which the command line has.
type LedgerFailureCode string

// LedgerFailure is what stops a command that is working on the ledger, and that the
// user can read in terms of the import: reference data that the ledger has otherwise
// than the mapping says, or a step that the ledger would not take. It is a code and
// the values the text of the code fills in; the words are the command line's.
type LedgerFailure struct {
	Code LedgerFailureCode
	// Args are the values the text of the code fills in; each code says which.
	Args []string
	// Err is what lies beneath, in its own words: the reply of the ledger. The command
	// line says it as it is.
	Err error
}

// Error is for logs and tests: the code and what it is about. What the user reads is
// the command line's text of the code.
func (f *LedgerFailure) Error() string {
	var b strings.Builder
	b.WriteString(string(f.Code))
	if len(f.Args) > 0 {
		fmt.Fprintf(&b, " args=%q", f.Args)
	}
	if f.Err != nil {
		fmt.Fprintf(&b, ": %v", f.Err)
	}
	return b.String()
}

// Unwrap gives the error beneath.
func (f *LedgerFailure) Unwrap() error { return f.Err }

// The codes of a failure of the ledger.
const (
	// LedgerStepFailed is a step that the ledger would not take, or could not do. Args:
	// the verb, the thing, and its name, which is empty when the step is not about one
	// thing but about all of them (a list) or about a day (a confirmation). Err is the
	// ledger's own reply.
	LedgerStepFailed LedgerFailureCode = "ledger"
	// LedgerArchived is reference data that the mapping names and the ledger has
	// archived. Args: the thing and its name.
	LedgerArchived LedgerFailureCode = "archived"
	// LedgerInstrumentDiffers is an instrument that the ledger has otherwise than the
	// mapping says. Args: its code, then what the ledger has and what the mapping says,
	// each as the kind, the number of decimals and "currency" or "not_currency".
	LedgerInstrumentDiffers LedgerFailureCode = "instrument_differs"
	// LedgerAccountKindDiffers is an account that the ledger has as another kind than
	// the mapping says. Args: its name, the kind the ledger has, the kind the mapping
	// says.
	LedgerAccountKindDiffers LedgerFailureCode = "account_kind_differs"
)

// Verb is what the import was doing to the ledger when a step failed.
type Verb string

// The verbs of a step of the ledger.
const (
	VerbList    Verb = "list"
	VerbGet     Verb = "get"
	VerbCreate  Verb = "create"
	VerbConfirm Verb = "confirm"
	VerbDiscard Verb = "discard"
)

// Thing is what a step of the ledger was about.
type Thing string

// The things of a step of the ledger.
const (
	ThingInstrument  Thing = "instrument"
	ThingInstitution Thing = "institution"
	ThingAccount     Thing = "account"
	ThingCategory    Thing = "category"
	ThingArea        Thing = "area"
	ThingProject     Thing = "project"
	ThingTag         Thing = "tag"
	ThingParty       Thing = "party"
	ThingTransaction Thing = "transaction"
	ThingBalance     Thing = "balance"
)

// StepFailure is a step of the ledger that failed: see LedgerStepFailed.
func StepFailure(verb Verb, thing Thing, name string, err error) *LedgerFailure {
	return &LedgerFailure{Code: LedgerStepFailed, Args: []string{string(verb), string(thing), name}, Err: err}
}
