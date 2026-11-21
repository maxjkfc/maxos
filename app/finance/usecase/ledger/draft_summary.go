package ledger

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// GetDraftSummary returns the drafts that are waiting to be confirmed, by the month
// they happened in and the account they move, with what they would move. Which
// transactions are drafts, which account an account posting moves, and the
// grouping are the repo's to read, in one snapshot (TransactionRepo.DraftSummary);
// the use case only works out what the repo does not say: the net of each amount,
// and the amounts of a month and of all the drafts, which are those of its accounts
// and of its months added up (model.AddDraftAmounts). No draft is no month and a
// count of zero, and no error.
//
// It asks nothing of the clock: today does not limit the drafts, a draft dated after
// today is waiting like any other.
func (s *Service) GetDraftSummary(ctx context.Context, _ usecase.GetDraftSummaryInput) (usecase.GetDraftSummaryOutput, error) {
	summary, err := s.transactions.DraftSummary(ctx)
	if err != nil {
		return usecase.GetDraftSummaryOutput{}, fmt.Errorf("get the draft summary: %w", err)
	}

	out := usecase.GetDraftSummaryOutput{Count: summary.Count(), Months: make([]usecase.DraftMonth, len(summary.Months))}
	monthAmounts := make([][]model.DraftAmount, len(summary.Months))
	for i, month := range summary.Months {
		accounts := make([]usecase.DraftAccount, len(month.Accounts))
		accountAmounts := make([][]model.DraftAmount, len(month.Accounts))
		for j, account := range month.Accounts {
			accounts[j] = usecase.DraftAccount{AccountID: account.AccountID, Count: account.Count, Amounts: toDraftAmounts(account.Amounts)}
			accountAmounts[j] = account.Amounts
		}
		monthAmounts[i] = model.AddDraftAmounts(accountAmounts...)
		out.Months[i] = usecase.DraftMonth{
			Month:    month.Month,
			Count:    month.Count,
			Amounts:  toDraftAmounts(monthAmounts[i]),
			Accounts: accounts,
		}
	}
	out.Amounts = toDraftAmounts(model.AddDraftAmounts(monthAmounts...))
	return out, nil
}

// toDraftAmounts returns the amounts as the use case gives them: each with its net,
// which is not in the model's.
func toDraftAmounts(amounts []model.DraftAmount) []usecase.DraftAmount {
	out := make([]usecase.DraftAmount, len(amounts))
	for i, a := range amounts {
		out[i] = usecase.DraftAmount{InstrumentID: a.InstrumentID, Outflow: a.Outflow, Inflow: a.Inflow, Net: a.Net()}
	}
	return out
}
