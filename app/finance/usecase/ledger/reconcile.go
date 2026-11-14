package ledger

import (
	"context"
	"fmt"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// Reconcile works out what the ledger says the account held of the instrument on
// the day, and lets the domain turn the difference from what it really held into
// a draft. It does all of that in one database transaction, so the balance, the
// drafts it was worked out beside, and the draft it records are read and written
// together.
//
// The recorded balance counts the confirmed postings of the account and the
// instrument up to and including the day, so it is not known while a draft is
// dated on or before the day with a posting to them: the draft may be confirmed
// and change it. Reconcile refuses with a *model.PendingDraftsError then. A
// transaction dated on or before the day that is recorded and confirmed after the
// draft of a reconciliation was made but before it is confirmed makes the draft
// stale, and is not detected.
//
// A key that was used before returns the transaction recorded under it, as it
// does for the use cases that record, and is answered before anything else is
// looked at: the draft it recorded is itself one of the pending drafts.
// RecordedBalance and Difference are not worked out then. A reconciliation with
// no difference records nothing, so it has no key to repeat.
func (s *Service) Reconcile(ctx context.Context, in usecase.ReconcileInput) (usecase.ReconcileOutput, error) {
	key, id, err := identify(in.RecordDetails)
	if err != nil {
		return usecase.ReconcileOutput{}, err
	}

	var out usecase.ReconcileOutput
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		existing, found, err := s.recordedUnder(ctx, key)
		if err != nil {
			return err
		}
		if found {
			txn := toTransaction(existing)
			out = usecase.ReconcileOutput{Transaction: &txn, Duplicate: true}
			return nil
		}

		h, err := s.header(ctx, id, key, in.RecordDetails)
		if err != nil {
			return err
		}
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return err
		}

		day := model.DateOf(in.OccurredOn)
		pending, err := s.transactions.PendingDrafts(ctx, account.ID, instrument.ID, day)
		if err != nil {
			return fmt.Errorf("count the drafts of account %s: %w", account.ID, err)
		}
		if pending > 0 {
			return fmt.Errorf("reconcile account %q in %s on %s: %w", account.Name, instrument.Code, day.Format(time.DateOnly),
				&model.PendingDraftsError{Count: pending})
		}
		recorded, err := s.transactions.BalanceAsOf(ctx, account.ID, instrument.ID, day)
		if err != nil {
			return fmt.Errorf("get the balance of account %s: %w", account.ID, err)
		}

		reconciliation, err := model.Reconcile(model.ReconcileParams{
			Header:               h,
			Account:              account,
			Instrument:           instrument,
			BaseCurrency:         base,
			RecordedBalance:      recorded,
			ActualBalance:        in.ActualBalance,
			DifferenceBaseAmount: in.DifferenceBaseAmount,
		})
		if err != nil {
			return err
		}
		out = usecase.ReconcileOutput{RecordedBalance: recorded, Difference: reconciliation.Difference}
		if reconciliation.Transaction == nil {
			return nil
		}

		stored, err := s.store(ctx, key, *reconciliation.Transaction)
		if err != nil {
			return err
		}
		if stored.Duplicate {
			// Another call recorded the key meanwhile: what it recorded is the answer,
			// and what this call worked out is not.
			out = usecase.ReconcileOutput{Transaction: &stored.Transaction, Duplicate: true}
			return nil
		}
		out.Transaction = &stored.Transaction
		return nil
	})
	if err != nil {
		return usecase.ReconcileOutput{}, err
	}
	return out, nil
}
