package ledger

import (
	"context"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// RecordExpense loads the account, or checks the party that paid, the parties the
// expense was paid for, the instrument and the categories the request names, and
// records the expense the domain makes of them as a draft.
func (s *Service) RecordExpense(ctx context.Context, in usecase.RecordExpenseInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		var account *model.Account
		if in.AccountID != nil {
			loaded, err := s.account(ctx, *in.AccountID)
			if err != nil {
				return model.Transaction{}, err
			}
			account = &loaded
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}
		lines, err := s.lines(ctx, in.Lines)
		if err != nil {
			return model.Transaction{}, err
		}

		shares := make([]model.Share, len(in.Shares))
		parties := make([]uuid.UUID, 0, len(in.Shares)+1)
		for i, share := range in.Shares {
			shares[i] = model.Share{PartyID: share.PartyID, Amount: share.Amount, Memo: share.Memo}
			parties = append(parties, share.PartyID)
		}
		if in.PaidByPartyID != nil {
			parties = append(parties, *in.PaidByPartyID)
		}
		if err := s.verifyParties(ctx, parties...); err != nil {
			return model.Transaction{}, err
		}

		return model.NewExpense(model.ExpenseParams{
			Header:       h,
			Account:      account,
			PaidBy:       in.PaidByPartyID,
			Instrument:   instrument,
			BaseCurrency: base,
			Lines:        lines,
			Shares:       shares,
		})
	})
}

// RecordIncome is RecordExpense for money that comes in, and has fees instead of
// shares.
func (s *Service) RecordIncome(ctx context.Context, in usecase.RecordIncomeInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}
		lines, err := s.lines(ctx, in.Lines)
		if err != nil {
			return model.Transaction{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
		if err != nil {
			return model.Transaction{}, err
		}

		return model.NewIncome(model.IncomeParams{
			Header:       h,
			Account:      account,
			Instrument:   instrument,
			BaseCurrency: base,
			Lines:        lines,
			Fees:         fees,
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
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewTransfer(model.TransferParams{
			Header:       h,
			From:         from,
			To:           to,
			Instrument:   instrument,
			Amount:       in.Amount,
			BaseCurrency: base,
			Fees:         fees,
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
		base, err := s.baseCurrency(ctx, fromInstrument)
		if err != nil {
			return model.Transaction{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
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
			BaseCurrency:   base,
			Fees:           fees,
		})
	})
}

// RecordDealing records money paid out to a party, or received from one, and what
// the party owes me changing with it. The party is checked with the catalog.
func (s *Service) RecordDealing(ctx context.Context, in usecase.RecordDealingInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}
		if err := s.verifyParties(ctx, in.PartyID); err != nil {
			return model.Transaction{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewDealing(model.DealingParams{
			Header:       h,
			Account:      account,
			PartyID:      in.PartyID,
			Instrument:   instrument,
			BaseCurrency: base,
			Amount:       in.Amount,
			Direction:    model.DealingDirection(in.Direction),
			Fees:         fees,
		})
	})
}

// RecordReward records money or credit that a payment instrument gave back into
// an account.
func (s *Service) RecordReward(ctx context.Context, in usecase.RecordRewardInput) (usecase.RecordOutput, error) {
	return s.record(ctx, in.RecordDetails, func(ctx context.Context, h model.Header) (model.Transaction, error) {
		account, err := s.account(ctx, in.AccountID)
		if err != nil {
			return model.Transaction{}, err
		}
		instrument, err := s.instrument(ctx, in.InstrumentID)
		if err != nil {
			return model.Transaction{}, err
		}
		base, err := s.baseCurrency(ctx, instrument)
		if err != nil {
			return model.Transaction{}, err
		}
		fees, err := s.fees(ctx, in.Fees)
		if err != nil {
			return model.Transaction{}, err
		}
		return model.NewReward(model.RewardParams{
			Header:       h,
			Account:      account,
			Instrument:   instrument,
			BaseCurrency: base,
			Amount:       in.Amount,
			BaseAmount:   in.BaseAmount,
			Fees:         fees,
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
