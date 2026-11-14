package usecase

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Reconcile confirms what an account holds of an instrument on a day and makes
// the ledger say it. It compares the actual balance with the recorded one, which
// is the confirmed postings of the account and the instrument up to and including
// that day, and records the difference as a draft:
//
//   - for a currency, an expense of the category 未記錄 (unrecorded) when the
//     account holds less than the ledger says, and an income of the category of the
//     same name when it holds more;
//   - for an instrument that is not a currency, an adjustment of the quantity,
//     which is neither an expense nor an income.
//
// When there is no difference nothing is recorded. As with everything else, the
// draft counts toward nothing until ConfirmTransactions confirms it.
//
// What the ledger says is not known while drafts that touch the account in the
// instrument, dated on or before the day, are waiting: Reconcile refuses with a
// *model.PendingDraftsError (an ErrPendingDrafts) until they are confirmed or
// discarded. A transaction dated on or before the day that is recorded and
// confirmed after the draft of a reconciliation is made, but before the draft is
// confirmed, makes the adjustment stale, and Reconcile does not detect that.
type Reconcile interface {
	Reconcile(ctx context.Context, in ReconcileInput) (ReconcileOutput, error)
}

// ReconcileInput is an account to reconcile. RecordDetails.OccurredOn is the day
// the balance was observed, and is required. The account and the instrument must
// exist and not be archived.
type ReconcileInput struct {
	RecordDetails
	AccountID    uuid.UUID
	InstrumentID uuid.UUID
	// ActualBalance is what the account really holds of the instrument on the day,
	// with no more decimal places than the instrument. It may be negative, for an
	// account that owes.
	ActualBalance decimal.Decimal
	// DifferenceBaseAmount is the difference in the base currency (TWD), as of the
	// day, as a positive amount whichever way the difference goes. It is required
	// when the instrument is a currency other than the base currency and there is a
	// difference: without it, Reconcile returns a *model.DifferenceBaseAmountRequiredError
	// that carries the difference, to ask again with. For the base currency it may
	// be left out, and must equal the difference if given. For an instrument that is
	// not a currency it must be left out.
	DifferenceBaseAmount *decimal.Decimal
}

// ReconcileOutput is the result of Reconcile.
type ReconcileOutput struct {
	// RecordedBalance is what the ledger said the account held of the instrument on
	// the day, before the difference.
	RecordedBalance decimal.Decimal
	// Difference is the actual balance minus the recorded one: negative when the
	// account holds less than the ledger says, positive when it holds more.
	Difference decimal.Decimal
	// Transaction is the draft that makes up for the difference, and is nil when
	// there is none. A reconciliation without a difference is not stored.
	Transaction *Transaction
	// Duplicate is true when the idempotency key was already used: nothing was
	// written, and Transaction is the one stored under the key, which may differ
	// from what was asked this time. RecordedBalance and Difference are not worked
	// out then.
	Duplicate bool
}
