package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// Reconcile works out what the ledger says the account held of the instrument on
// the day, and lets the domain turn the difference from what it really held into
// a draft. It does all of that in one database transaction, so the balance, the
// drafts it was worked out beside, and the draft it records are read and written
// together.
//
// Reconciliations of one account are made one at a time. The first statement of
// the transaction locks the account (repo.AccountRepo.Lock) until it ends, so a
// second reconciliation of it that starts meanwhile waits there and then reads
// what the first committed: its draft, which refuses it (*model.PendingDraftsError),
// or, with the same key, its transaction, which it returns. Without the lock both
// would read the same balance and the same empty list of drafts, and each would
// draft the difference. The transaction is READ COMMITTED, as all of them are, so
// the statements after the lock do see what the other one committed. The lock is
// on the account, not on the account and the instrument: that is more than the
// check needs, and reconciling two instruments of one account at once is not
// something to make fast.
//
// The lock is taken before the key is looked up, not after it. A retry that
// overlaps its first call has to be answered with what that call recorded;
// looked up first, the key is not there yet, the retry waits for the lock, and it
// is then refused for a draft that is its own. An account that is not found does
// not fail the lock: a key that was used before is answered whatever became of
// the account, and the account is looked at further down, where the request is
// validated.
//
// The recorded balance counts the confirmed account postings of the account and
// the instrument up to and including the day, so it is not known while a draft is
// dated on or before the day with an account posting to them: the draft may be
// confirmed and change it. Reconcile refuses with a *model.PendingDraftsError
// then. A transaction dated on or before the day that is recorded and confirmed
// after the draft of a reconciliation was made but before it is confirmed makes
// the draft stale, and is not detected. What an instalment plan has not charged
// to the account yet is not what the account holds, so it is not in the balance:
// the origin of an expense paid in instalments has no account posting, and a
// period has one on the day it falls due, so the drafts of a plan keep the account
// from being reconciled only for the periods that fall due on or before the day.
//
// A key that was used before returns the transaction recorded under it, as it
// does for the use cases that record, and is answered right after the lock,
// before anything else is looked at: the draft it recorded is itself one of the
// pending drafts.
// RecordedBalance and Difference are not worked out then. A reconciliation with
// no difference records nothing, so it has no key to repeat.
func (s *Service) Reconcile(ctx context.Context, in usecase.ReconcileInput) (usecase.ReconcileOutput, error) {
	key, id, err := identify(in.RecordDetails)
	if err != nil {
		return usecase.ReconcileOutput{}, err
	}

	var out usecase.ReconcileOutput
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.accounts.Lock(ctx, in.AccountID); err != nil && !errors.Is(err, model.ErrNotFound) {
			return fmt.Errorf("lock account %s: %w", in.AccountID, err)
		}
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
		balances, err := s.transactions.Balances(ctx, repo.BalanceFilter{AccountID: &account.ID, InstrumentID: &instrument.ID, Day: day})
		if err != nil {
			return fmt.Errorf("get the balance of account %s: %w", account.ID, err)
		}
		// There is a balance for each account and instrument, so none or one: what the
		// account holds is the quantity, and not the unbilled instalment beside it.
		recorded := decimal.Zero
		for _, balance := range balances {
			recorded = recorded.Add(balance.Quantity)
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
