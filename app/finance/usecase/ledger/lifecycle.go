package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// ConfirmTransactions confirms the drafts in one database transaction, so that
// it is all or nothing. It checks all of them before it changes any, and when
// some cannot be confirmed the error carries one problem for each of them, so
// that the caller can put them all right at once.
func (s *Service) ConfirmTransactions(ctx context.Context, in usecase.ConfirmTransactionsInput) (usecase.ConfirmTransactionsOutput, error) {
	ids := uniqueIDs(in.IDs)
	if len(ids) == 0 {
		return usecase.ConfirmTransactionsOutput{}, model.ErrNoTransactions
	}

	var out usecase.ConfirmTransactionsOutput
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		// Locking in the order of the ids, and not in the order they were given,
		// keeps two calls with some ids in common from waiting for each other.
		locking := slices.Clone(ids)
		slices.SortFunc(locking, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })

		confirmed := make(map[uuid.UUID]model.Transaction, len(ids))
		var problems []error
		for _, id := range locking {
			txn, err := s.transactions.GetForUpdate(ctx, id)
			if errors.Is(err, model.ErrNotFound) {
				problems = append(problems, fmt.Errorf("transaction %s: %w", id, err))
				continue
			}
			if err != nil {
				return fmt.Errorf("get transaction %s: %w", id, err)
			}
			next, err := txn.Confirm()
			if err != nil {
				problems = append(problems, err)
				continue
			}
			confirmed[id] = next
		}
		if len(problems) > 0 {
			return errors.Join(problems...)
		}

		out.Transactions = make([]usecase.Transaction, 0, len(ids))
		for _, id := range ids {
			stored, err := s.transactions.SetStatus(ctx, id, confirmed[id].Status)
			if err != nil {
				return fmt.Errorf("confirm transaction %s: %w", id, err)
			}
			out.Transactions = append(out.Transactions, toTransaction(stored))
		}
		return nil
	})
	if err != nil {
		return usecase.ConfirmTransactionsOutput{}, err
	}
	return out, nil
}

// DiscardDraft deletes a draft with its postings. Only a draft: a confirmed
// transaction has counted, and a void one is kept as a record.
func (s *Service) DiscardDraft(ctx context.Context, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		txn, err := s.transactions.GetForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("transaction %s: %w", id, err)
		}
		if err := txn.CheckDiscardable(); err != nil {
			return err
		}
		if err := s.transactions.Delete(ctx, id); err != nil {
			return fmt.Errorf("discard transaction %s: %w", id, err)
		}
		return nil
	})
}

// VoidTransaction voids a confirmed transaction.
func (s *Service) VoidTransaction(ctx context.Context, id uuid.UUID) (usecase.Transaction, error) {
	var out usecase.Transaction
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		txn, err := s.transactions.GetForUpdate(ctx, id)
		if err != nil {
			return fmt.Errorf("transaction %s: %w", id, err)
		}
		voided, err := txn.Void()
		if err != nil {
			return err
		}
		stored, err := s.transactions.SetStatus(ctx, id, voided.Status)
		if err != nil {
			return fmt.Errorf("void transaction %s: %w", id, err)
		}
		out = toTransaction(stored)
		return nil
	})
	if err != nil {
		return usecase.Transaction{}, err
	}
	return out, nil
}

// uniqueIDs returns ids without the repeats, in the order of their first
// appearance.
func uniqueIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]struct{}, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, repeat := seen[id]; repeat {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
