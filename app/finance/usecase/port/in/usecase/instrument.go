// Package usecase declares what the finance context offers its callers: the use
// cases on the reference data every transaction points at (instruments,
// institutions, accounts and categories) and the ledger (recording, confirming,
// voiding and listing transactions, reconciling accounts, and balances and
// dealings). It is the only part of the finance context that other contexts may
// depend on.
//
// The use cases return the errors of the finance domain (ErrNameEmpty,
// ErrNotFound, ErrAlreadyExists, ErrArchived and the rest, all in domain/model),
// wrapped with what was being done; the ledger adds the ones about transactions
// (ErrAmountNotPositive, ErrBaseAmountRequired, ErrNotDraft and the rest), and
// Reconcile the two that carry something with them: *PendingDraftsError, the
// number of drafts that stand in its way, and *DifferenceBaseAmountRequiredError,
// the difference to ask again with. A project, area, party or tag that cannot be
// used comes back as the *ReferenceError of the catalog's in-port, wrapped the
// same way.
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Instrument is an instrument as the use cases return it.
type Instrument struct {
	ID uuid.UUID
	// Code is upper case, e.g. "TWD", "USDT", "2330.TW".
	Code string
	Name string
	// Kind is "fiat", "crypto", "stock" or "fund".
	Kind string
	// IsCurrency is whether the instrument is used to price and to pay; it does
	// not follow from Kind.
	IsCurrency bool
	// Decimals is how many decimal places a quantity of the instrument has.
	Decimals  int
	CreatedAt time.Time
	// ArchivedAt is nil while the instrument is in use.
	ArchivedAt *time.Time
}

// CreateInstrument adds an instrument.
type CreateInstrument interface {
	CreateInstrument(ctx context.Context, in CreateInstrumentInput) (Instrument, error)
}

// CreateInstrumentInput is an instrument to add.
type CreateInstrumentInput struct {
	// Code is trimmed of the whitespace around it and made upper case; it must not
	// be empty or have whitespace inside it. No other instrument, archived or not,
	// may have the same code.
	Code string
	// Name is trimmed of the whitespace around it and must not be empty.
	Name string
	// Kind is "fiat", "crypto", "stock" or "fund".
	Kind string
	// IsCurrency is independent of Kind: USDT is crypto and a currency.
	IsCurrency bool
	// Decimals is from 0 to 18.
	Decimals int
}

// ArchiveInstrument retires an instrument: it stays for what already holds it,
// but new transactions may no longer use it. Archiving an archived instrument
// changes nothing and returns it as it is. The base currency, TWD, cannot be
// archived.
type ArchiveInstrument interface {
	ArchiveInstrument(ctx context.Context, id uuid.UUID) (Instrument, error)
}

// ListInstruments lists the instruments.
type ListInstruments interface {
	ListInstruments(ctx context.Context, in ListInstrumentsInput) (ListInstrumentsOutput, error)
}

// ListInstrumentsInput filters the listing.
type ListInstrumentsInput struct {
	// IncludeArchived lists the archived instruments too; by default only those
	// in use.
	IncludeArchived bool
}

// ListInstrumentsOutput holds the instruments, by code.
type ListInstrumentsOutput struct {
	Instruments []Instrument
}

// GetInstrument returns one instrument, archived or not.
type GetInstrument interface {
	GetInstrument(ctx context.Context, id uuid.UUID) (Instrument, error)
}
