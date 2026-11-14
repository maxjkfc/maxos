// Package ledger implements the ledger use cases: recording expenses (with the
// parts paid for others), incomes, transfers, exchanges, dealings, rewards and
// opening balances as drafts, and reconciling accounts; confirming, discarding
// and voiding the drafts; and reading transactions, balances and dealings.
package ledger

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/tx"
)

// Service implements usecase.RecordExpense, RecordIncome, RecordTransfer,
// RecordExchange, RecordDealing, RecordReward, SetOpeningBalance, Reconcile,
// ConfirmTransactions, DiscardDraft, VoidTransaction, GetTransaction,
// ListTransactions, GetBalances and GetDealings.
//
// Every use case that writes does all of it, the reads that check the request
// included, in one database transaction.
type Service struct {
	tx           tx.Transactor
	transactions repo.TransactionRepo
	accounts     repo.AccountRepo
	instruments  repo.InstrumentRepo
	categories   repo.CategoryRepo
	references   catalogusecase.VerifyReferences
	defaults     catalogusecase.ResolveProjectDefaults
	projectAreas catalogusecase.ResolveProjectArea
	parties      catalogusecase.ListParties
}

var (
	_ usecase.RecordExpense       = (*Service)(nil)
	_ usecase.RecordIncome        = (*Service)(nil)
	_ usecase.RecordTransfer      = (*Service)(nil)
	_ usecase.RecordExchange      = (*Service)(nil)
	_ usecase.RecordDealing       = (*Service)(nil)
	_ usecase.RecordReward        = (*Service)(nil)
	_ usecase.SetOpeningBalance   = (*Service)(nil)
	_ usecase.Reconcile           = (*Service)(nil)
	_ usecase.ConfirmTransactions = (*Service)(nil)
	_ usecase.DiscardDraft        = (*Service)(nil)
	_ usecase.VoidTransaction     = (*Service)(nil)
	_ usecase.GetTransaction      = (*Service)(nil)
	_ usecase.ListTransactions    = (*Service)(nil)
	_ usecase.GetBalances         = (*Service)(nil)
	_ usecase.GetDealings         = (*Service)(nil)
)

// record is what the use cases that record a transaction have in common. build
// makes the transaction from the header it is given: it loads what the request
// names and lets the domain turn it into postings.
//
// It makes the call safe to repeat. A key that was used before returns the
// transaction recorded under it, and is answered before the request is checked,
// since it asks for nothing new: the account may have been archived since. Two
// calls with the same key at the same time cannot both record it: the key is
// unique in the store, and the insert of the second waits for the first to commit
// and then finds the key taken, so it returns what the first recorded.
func (s *Service) record(
	ctx context.Context,
	d usecase.RecordDetails,
	build func(ctx context.Context, h model.Header) (model.Transaction, error),
) (usecase.RecordOutput, error) {
	key, id, err := identify(d)
	if err != nil {
		return usecase.RecordOutput{}, err
	}

	var out usecase.RecordOutput
	err = s.tx.WithinTx(ctx, func(ctx context.Context) error {
		existing, found, err := s.recordedUnder(ctx, key)
		if err != nil {
			return err
		}
		if found {
			out = usecase.RecordOutput{Transaction: toTransaction(existing), Duplicate: true}
			return nil
		}

		h, err := s.header(ctx, id, key, d)
		if err != nil {
			return err
		}
		txn, err := build(ctx, h)
		if err != nil {
			return err
		}
		out, err = s.store(ctx, key, txn)
		return err
	})
	if err != nil {
		return usecase.RecordOutput{}, err
	}
	return out, nil
}

// identify checks the idempotency key of a request, returning it as the
// transaction keeps it, and makes the id of the transaction the request records.
func identify(d usecase.RecordDetails) (key string, id uuid.UUID, err error) {
	key, err = model.NormalizeIdempotencyKey(d.IdempotencyKey)
	if err != nil {
		return "", uuid.Nil, err
	}
	id, err = uuid.NewV7()
	if err != nil {
		return "", uuid.Nil, fmt.Errorf("generate transaction id: %w", err)
	}
	return key, id, nil
}

// recordedUnder returns the transaction recorded under the key, and whether there
// is one.
func (s *Service) recordedUnder(ctx context.Context, key string) (model.Transaction, bool, error) {
	existing, err := s.transactions.GetByIdempotencyKey(ctx, key)
	switch {
	case err == nil:
		return existing, true, nil
	case errors.Is(err, model.ErrNotFound):
		return model.Transaction{}, false, nil
	default:
		return model.Transaction{}, false, fmt.Errorf("look up idempotency key: %w", err)
	}
}

// store inserts the draft. When another call recorded its key after the look-up
// of recordedUnder, whose commit is what the insert waited for, it returns what
// that call recorded, as a duplicate.
func (s *Service) store(ctx context.Context, key string, txn model.Transaction) (usecase.RecordOutput, error) {
	stored, err := s.transactions.Insert(ctx, txn)
	if errors.Is(err, repo.ErrDuplicateKey) {
		existing, err := s.transactions.GetByIdempotencyKey(ctx, key)
		if err != nil {
			return usecase.RecordOutput{}, fmt.Errorf("get the transaction recorded under the idempotency key: %w", err)
		}
		return usecase.RecordOutput{Transaction: toTransaction(existing), Duplicate: true}, nil
	}
	if err != nil {
		return usecase.RecordOutput{}, fmt.Errorf("store transaction: %w", err)
	}
	return usecase.RecordOutput{Transaction: toTransaction(stored)}, nil
}

// header makes the header of a new transaction from what the caller gave. It
// checks with the catalog that the project, area and tags can be used, and fills
// in what the caller left open. With no project given, that is the project the
// day belongs to; with no area given, it is the default area of the project,
// whether the caller named the project or the day did. An area the caller gave
// is never replaced.
func (s *Service) header(ctx context.Context, id uuid.UUID, key string, d usecase.RecordDetails) (model.Header, error) {
	// The day is needed to find the project, so it is checked here and not left
	// to the model.
	if d.OccurredOn.IsZero() {
		return model.Header{}, model.ErrDateRequired
	}
	h := model.Header{
		ID:             id,
		IdempotencyKey: key,
		OccurredOn:     d.OccurredOn,
		OccurredAt:     d.OccurredAt,
		Description:    d.Description,
		Merchant:       d.Merchant,
		ProjectID:      d.ProjectID,
		AreaID:         d.AreaID,
		TagIDs:         d.TagIDs,
	}

	refs := catalogusecase.VerifyReferencesInput{TagIDs: d.TagIDs}
	if d.ProjectID != nil {
		refs.ProjectIDs = []uuid.UUID{*d.ProjectID}
	}
	if d.AreaID != nil {
		refs.AreaIDs = []uuid.UUID{*d.AreaID}
	}
	if len(refs.ProjectIDs)+len(refs.AreaIDs)+len(refs.TagIDs) > 0 {
		if err := s.references.VerifyReferences(ctx, refs); err != nil {
			return model.Header{}, fmt.Errorf("check the project, area and tags: %w", err)
		}
	}

	switch {
	case d.ProjectID == nil:
		projectID, areaID, err := s.projectOf(ctx, d.OccurredOn)
		if err != nil {
			return model.Header{}, err
		}
		h.ProjectID = projectID
		if h.AreaID == nil {
			h.AreaID = areaID
		}
	case h.AreaID == nil:
		area, err := s.projectAreas.ResolveProjectArea(ctx, *d.ProjectID)
		if err != nil {
			return model.Header{}, fmt.Errorf("get the default area of project %s: %w", *d.ProjectID, err)
		}
		if area != nil {
			id := area.ID
			h.AreaID = &id
		}
	}
	return h, nil
}

// projectOf returns the project the day belongs to and the default area of it,
// either of which may be nil. When more than one project is active on the day it
// returns neither, instead of failing: the transaction is recorded without a
// project, and the caller can still name one.
func (s *Service) projectOf(ctx context.Context, day time.Time) (projectID, areaID *uuid.UUID, err error) {
	resolved, err := s.defaults.ResolveProjectDefaults(ctx, catalogusecase.ResolveProjectDefaultsInput{Date: day})
	var ambiguous *catalogusecase.AmbiguousProjectsError
	switch {
	case errors.As(err, &ambiguous):
		return nil, nil, nil
	case err != nil:
		return nil, nil, fmt.Errorf("find the project of %s: %w", day.Format(time.DateOnly), err)
	case resolved.Project == nil:
		return nil, nil, nil
	}
	project := resolved.Project.ID
	projectID = &project
	if resolved.DefaultArea != nil {
		area := resolved.DefaultArea.ID
		areaID = &area
	}
	return projectID, areaID, nil
}

// account loads an account, archived or not; the model refuses an archived one.
func (s *Service) account(ctx context.Context, id uuid.UUID) (model.Account, error) {
	account, err := s.accounts.Get(ctx, id)
	if err != nil {
		return model.Account{}, fmt.Errorf("account %s: %w", id, err)
	}
	return account, nil
}

// instrument loads an instrument, archived or not; the model refuses an archived
// one.
func (s *Service) instrument(ctx context.Context, id uuid.UUID) (model.Instrument, error) {
	instrument, err := s.instruments.Get(ctx, id)
	if err != nil {
		return model.Instrument{}, fmt.Errorf("instrument %s: %w", id, err)
	}
	return instrument, nil
}

// baseCurrency returns the base currency: the instrument itself when it is that,
// otherwise the one the store has.
func (s *Service) baseCurrency(ctx context.Context, instrument model.Instrument) (model.Instrument, error) {
	if instrument.ID == model.BaseCurrencyID {
		return instrument, nil
	}
	return s.instrument(ctx, model.BaseCurrencyID)
}

// category loads a category, archived or not; the model refuses an archived one.
func (s *Service) category(ctx context.Context, id uuid.UUID) (model.Category, error) {
	category, err := s.categories.Get(ctx, id)
	if err != nil {
		return model.Category{}, fmt.Errorf("category %s: %w", id, err)
	}
	return category, nil
}

// verifyParties checks with the catalog that the parties can be used: that they
// exist and are not archived. It does nothing for no party.
func (s *Service) verifyParties(ctx context.Context, ids ...uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := s.references.VerifyReferences(ctx, catalogusecase.VerifyReferencesInput{PartyIDs: ids}); err != nil {
		return fmt.Errorf("check the parties: %w", err)
	}
	return nil
}

// lines loads the categories the lines of an expense or an income name, each
// once, and returns the lines as the domain takes them. A category that does not
// exist is an error that names the line; whether it can be used is for the model.
func (s *Service) lines(ctx context.Context, lines []usecase.CategoryLine) ([]model.CategoryLine, error) {
	categories := make(map[uuid.UUID]model.Category, len(lines))
	out := make([]model.CategoryLine, len(lines))
	for i, line := range lines {
		category, loaded := categories[line.CategoryID]
		if !loaded {
			var err error
			if category, err = s.category(ctx, line.CategoryID); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
			categories[line.CategoryID] = category
		}
		out[i] = model.CategoryLine{
			Category:   category,
			Amount:     line.Amount,
			BaseAmount: line.BaseAmount,
			Discount:   line.Discount,
			Memo:       line.Memo,
		}
	}
	return out, nil
}

// fees loads the account, the instrument and the category each fee names, and
// returns the fees as the domain takes them. Whether they can be used is for the
// model; one that does not exist is an error that names the fee.
func (s *Service) fees(ctx context.Context, fees []usecase.Fee) ([]model.Fee, error) {
	out := make([]model.Fee, len(fees))
	for i, fee := range fees {
		account, err := s.account(ctx, fee.AccountID)
		if err != nil {
			return nil, fmt.Errorf("fee %d: %w", i+1, err)
		}
		instrument, err := s.instrument(ctx, fee.InstrumentID)
		if err != nil {
			return nil, fmt.Errorf("fee %d: %w", i+1, err)
		}
		category, err := s.category(ctx, fee.CategoryID)
		if err != nil {
			return nil, fmt.Errorf("fee %d: %w", i+1, err)
		}
		out[i] = model.Fee{
			Account:    account,
			Instrument: instrument,
			Category:   category,
			Amount:     fee.Amount,
			BaseAmount: fee.BaseAmount,
			Memo:       fee.Memo,
		}
	}
	return out, nil
}
