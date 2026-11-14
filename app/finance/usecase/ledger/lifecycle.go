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
//
// The origin of an instalment plan stands for the whole plan: it is confirmed
// with each of its periods, in the same call (see lockActed). A period given beside
// its origin is covered by it, and confirmed once, with the rest; the id of a
// period whose origin is not given is a problem, ErrPartOfPlan. The output has a
// transaction for each id given, once and in the order given, as it was confirmed:
// a period that was given is in it, and a period of a plan that was given by its
// origin alone is not.
func (s *Service) ConfirmTransactions(ctx context.Context, in usecase.ConfirmTransactionsInput) (usecase.ConfirmTransactionsOutput, error) {
	ids := uniqueIDs(in.IDs)
	if len(ids) == 0 {
		return usecase.ConfirmTransactionsOutput{}, model.ErrNoTransactions
	}

	var out usecase.ConfirmTransactionsOutput
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acted, problems, err := s.lockActed(ctx, ids)
		if err != nil {
			return err
		}
		confirmed := make([]model.Transaction, 0, len(acted))
		for _, txn := range acted {
			next, err := txn.Confirm()
			if err != nil {
				problems = append(problems, err)
				continue
			}
			confirmed = append(confirmed, next)
		}
		if len(problems) > 0 {
			return errors.Join(problems...)
		}

		stored := make(map[uuid.UUID]model.Transaction, len(confirmed))
		for _, txn := range confirmed {
			next, err := s.transactions.SetStatus(ctx, txn.ID, txn.Status)
			if err != nil {
				return fmt.Errorf("confirm transaction %s: %w", txn.ID, err)
			}
			stored[txn.ID] = next
		}
		out.Transactions = make([]usecase.Transaction, 0, len(ids))
		for _, id := range ids {
			out.Transactions = append(out.Transactions, toTransaction(stored[id]))
		}
		return nil
	})
	if err != nil {
		return usecase.ConfirmTransactionsOutput{}, err
	}
	return out, nil
}

// DiscardDraft deletes a draft with its postings. Only a draft: a confirmed
// transaction has counted, and a void one is kept as a record. The origin of an
// instalment plan is deleted with each of its periods, or none of them is; the
// periods go first, as they refer to the origin. It is given one id, so the id of
// a period, whose origin cannot be given with it, is ErrPartOfPlan.
func (s *Service) DiscardDraft(ctx context.Context, id uuid.UUID) error {
	return s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acted, problems, err := s.lockActed(ctx, []uuid.UUID{id})
		if err != nil {
			return err
		}
		for _, txn := range acted {
			if err := txn.CheckDiscardable(); err != nil {
				problems = append(problems, err)
			}
		}
		if len(problems) > 0 {
			return errors.Join(problems...)
		}

		for _, periods := range []bool{true, false} {
			for _, txn := range acted {
				if txn.IsPlanPeriod() != periods {
					continue
				}
				if err := s.transactions.Delete(ctx, txn.ID); err != nil {
					return fmt.Errorf("discard transaction %s: %w", txn.ID, err)
				}
			}
		}
		return nil
	})
}

// VoidTransaction voids a confirmed transaction. The origin of an instalment plan
// voids the whole plan, the periods that have fallen due already too, and what is
// returned is the origin. It is given one id, so the id of a period, whose origin
// cannot be given with it, is ErrPartOfPlan.
func (s *Service) VoidTransaction(ctx context.Context, id uuid.UUID) (usecase.Transaction, error) {
	var out usecase.Transaction
	err := s.tx.WithinTx(ctx, func(ctx context.Context) error {
		acted, problems, err := s.lockActed(ctx, []uuid.UUID{id})
		if err != nil {
			return err
		}
		voided := make([]model.Transaction, 0, len(acted))
		for _, txn := range acted {
			next, err := txn.Void()
			if err != nil {
				problems = append(problems, err)
				continue
			}
			voided = append(voided, next)
		}
		if len(problems) > 0 {
			return errors.Join(problems...)
		}

		for _, txn := range voided {
			stored, err := s.transactions.SetStatus(ctx, txn.ID, txn.Status)
			if err != nil {
				return fmt.Errorf("void transaction %s: %w", txn.ID, err)
			}
			if stored.ID == id {
				out = toTransaction(stored)
			}
		}
		return nil
	})
	if err != nil {
		return usecase.Transaction{}, err
	}
	return out, nil
}

// lockActed resolves the ids a call that confirms, discards or voids was given to
// the transactions it acts on, and locks them until the database transaction
// ends. They are the transactions the ids name, except that the origin of an
// instalment plan stands for the whole plan: it is acted on with each of its
// periods, and a period that is given beside its origin is one of them, acted on
// once. They are returned as they stand once locked, so that what is checked of
// them is what is written over, in the order of their ids, which is the order they
// were locked in.
//
// All of them are locked in one order, the order of their ids, and not one id after
// the other as they are met, nor one plan after the other: two calls that have
// some of them in common cannot then each hold what the other needs next. To know
// what to lock before it locks anything, each id is read first without a lock. What
// that read tells cannot change while the call is going on: a transaction is never
// moved into a plan or out of one, nor a period out of its plan. What can change
// is the status, and a draft that is deleted; both are looked at again, in what
// the locks return.
//
// A problem of the request is not an error of the call, and the others are still
// looked at, so that all of them are reported together: an id that is not found,
// and the id of a period whose origin is not among the ids, which is ErrPartOfPlan
// and is not locked, nor acted on: a plan is acted on through its origin.
func (s *Service) lockActed(ctx context.Context, ids []uuid.UUID) (acted []model.Transaction, problems []error, err error) {
	asked := make([]model.Transaction, 0, len(ids))
	origins := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		txn, err := s.transactions.Get(ctx, id)
		if errors.Is(err, model.ErrNotFound) {
			problems = append(problems, fmt.Errorf("transaction %s: %w", id, err))
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("get transaction %s: %w", id, err)
		}
		asked = append(asked, txn)
		if txn.IsPlanOrigin() {
			origins[txn.ID] = true
		}
	}

	needed := make([]uuid.UUID, 0, len(asked))
	for _, txn := range asked {
		switch {
		case txn.IsPlanPeriod():
			if origins[*txn.PlanID] {
				// Covered by its origin, which is among the ids: it is acted on with the
				// rest of the plan, once.
				continue
			}
			problems = append(problems, fmt.Errorf("%w: transaction %s is period %d of plan %s, whose origin is not among the ids: give the origin, which acts on all of the plan",
				model.ErrPartOfPlan, txn.ID, *txn.PlanSeq, *txn.PlanID))
		case txn.IsPlanOrigin():
			plan, err := s.transactions.GetPlan(ctx, txn.ID)
			if errors.Is(err, model.ErrNotFound) {
				// Discarded since it was read: it is not there, as it is for an id that
				// never was.
				problems = append(problems, fmt.Errorf("transaction %s: %w", txn.ID, err))
				continue
			}
			if err != nil {
				return nil, nil, fmt.Errorf("get plan %s: %w", txn.ID, err)
			}
			needed = append(needed, txn.ID)
			for _, period := range plan.Periods {
				needed = append(needed, period.ID)
			}
		default:
			needed = append(needed, txn.ID)
		}
	}

	// Every id is in needed once: the caller removes the repeats of a request, and no
	// two plans have a period in common.
	slices.SortFunc(needed, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	acted = make([]model.Transaction, 0, len(needed))
	for _, id := range needed {
		txn, err := s.transactions.GetForUpdate(ctx, id)
		if errors.Is(err, model.ErrNotFound) {
			problems = append(problems, fmt.Errorf("transaction %s: %w", id, err))
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("get transaction %s: %w", id, err)
		}
		acted = append(acted, txn)
	}
	return acted, problems, nil
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
