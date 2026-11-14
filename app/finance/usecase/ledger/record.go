package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// RecordExpense loads the account, the instrument and the categories the request
// names, and records the expense the domain makes of them: a draft, or a
// confirmed transaction when asked.
func (s *Service) RecordExpense(ctx context.Context, in usecase.RecordExpenseInput) (usecase.RecordOutput, error) {
	return s.recordCategorized(ctx, in.RecordDetails, in.AccountID, in.InstrumentID, in.Lines, model.NewExpense)
}

// RecordIncome is RecordExpense for money that comes in.
func (s *Service) RecordIncome(ctx context.Context, in usecase.RecordIncomeInput) (usecase.RecordOutput, error) {
	return s.recordCategorized(ctx, in.RecordDetails, in.AccountID, in.InstrumentID, in.Lines, model.NewIncome)
}

// recordCategorized records an expense or an income, which only differ in the
// constructor of the domain.
func (s *Service) recordCategorized(
	ctx context.Context,
	d usecase.RecordDetails,
	accountID, instrumentID uuid.UUID,
	lines []usecase.CategoryLine,
	newTransaction func(model.CategorizedParams) (model.Transaction, error),
) (usecase.RecordOutput, error) {
	return s.record(ctx, d, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		account, err := s.account(ctx, accountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, instrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}

		categories := make(map[uuid.UUID]model.Category, len(lines))
		modelLines := make([]model.CategoryLine, len(lines))
		for i, line := range lines {
			category, loaded := categories[line.CategoryID]
			if !loaded {
				if category, err = s.category(ctx, line.CategoryID); err != nil {
					return model.Transaction{}, fmt.Errorf("line %d: %w", i+1, err)
				}
				categories[line.CategoryID] = category
			}
			modelLines[i] = model.CategoryLine{
				Category:   category,
				Amount:     line.Amount,
				BaseAmount: line.BaseAmount,
				Memo:       line.Memo,
			}
		}

		return newTransaction(model.CategorizedParams{
			Header:       h,
			Account:      account,
			Instrument:   instrument,
			BaseCurrency: base,
			Lines:        modelLines,
		})
	})
}

// RecordTransfer records money moved between two accounts in one instrument.
func (s *Service) RecordTransfer(ctx context.Context, in usecase.RecordTransferInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		from, err := s.account(ctx, in.FromAccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		to, err := s.account(ctx, in.ToAccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewTransfer(model.TransferParams{
			Header:     h,
			From:       from,
			To:         to,
			Instrument: instrument,
			Amount:     in.Amount,
		})
	})
}

// RecordExchange records one currency changed into another.
func (s *Service) RecordExchange(ctx context.Context, in usecase.RecordExchangeInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		fromAccount, err := s.account(ctx, in.FromAccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		toAccount, err := s.account(ctx, in.ToAccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		fromInstrument, err := s.instrument(ctx, in.FromInstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		toInstrument, err := s.instrument(ctx, in.ToInstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewExchange(model.ExchangeParams{
			Header:         h,
			FromAccount:    fromAccount,
			ToAccount:      toAccount,
			FromInstrument: fromInstrument,
			ToInstrument:   toInstrument,
			FromAmount:     in.FromAmount,
			ToAmount:       in.ToAmount,
		})
	})
}

// SetOpeningBalance records what an account held of an instrument to begin with.
func (s *Service) SetOpeningBalance(ctx context.Context, in usecase.SetOpeningBalanceInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewOpeningBalance(model.OpeningBalanceParams{
			Header:     h,
			Account:    account,
			Instrument: instrument,
			Amount:     in.Amount,
		})
	})
}
