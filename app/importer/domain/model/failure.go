package model

import (
	"fmt"
	"strings"
)

// FailureCode says what stopped a command, as a problem or a warning does: without
// the words, which the command line has.
type FailureCode string

// Failure is what stops a command and that the user can read in terms of the import:
// a file that cannot be read, reference data that the ledger has otherwise than the
// mapping says, a step that the ledger would not take. It is a code, where it is and
// the values the text of the code fills in; the words are the command line's.
type Failure struct {
	Code FailureCode
	// File is the file it is about, and Line the line of that file, from 1; a failure
	// that is not about a file or a line has none.
	File string
	Line int
	// Args are the values the text of the code fills in; each code says which.
	Args []string
	// Err is what lies beneath, in its own words: the reply of the ledger or the
	// system's reason. The command line says it as it is.
	Err error
}

// Error is for logs and tests: the code and what it is about. What the user reads is
// the command line's text of the code.
func (f *Failure) Error() string {
	var b strings.Builder
	b.WriteString(string(f.Code))
	if f.File != "" {
		fmt.Fprintf(&b, " file=%s", f.File)
	}
	if f.Line > 0 {
		fmt.Fprintf(&b, " line=%d", f.Line)
	}
	if len(f.Args) > 0 {
		fmt.Fprintf(&b, " args=%q", f.Args)
	}
	if f.Err != nil {
		fmt.Fprintf(&b, ": %v", f.Err)
	}
	return b.String()
}

// Unwrap gives the error beneath.
func (f *Failure) Unwrap() error { return f.Err }

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

// The codes of the failures that are about the ledger.
const (
	// FailureLedger is a step that the ledger would not take, or could not do. Args:
	// the verb, the thing, and its name, which is empty when the step is not about one
	// thing but about all of them (a list) or about a day (a confirmation). Err is the
	// ledger's own reply.
	FailureLedger FailureCode = "ledger"
	// FailureArchived is reference data that the mapping names and the ledger has
	// archived. Args: the thing and its name.
	FailureArchived FailureCode = "archived"
	// FailureInstrumentDiffers is an instrument that the ledger has otherwise than the
	// mapping says. Args: its code, then what the ledger has and what the mapping says,
	// each as the kind, the number of decimals and "currency" or "not_currency".
	FailureInstrumentDiffers FailureCode = "instrument_differs"
	// FailureAccountKindDiffers is an account that the ledger has as another kind than
	// the mapping says. Args: its name, the kind the ledger has, the kind the mapping
	// says.
	FailureAccountKindDiffers FailureCode = "account_kind_differs"
)

// LedgerFailure is a step of the ledger that failed: see FailureLedger.
func LedgerFailure(verb Verb, thing Thing, name string, err error) *Failure {
	return &Failure{Code: FailureLedger, Args: []string{string(verb), string(thing), name}, Err: err}
}
