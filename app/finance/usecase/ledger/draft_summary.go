package ledger

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// GetDraftSummary returns the drafts that are waiting to be confirmed, by the month
// they happened in and the account they are of, with what confirming them would
// change in the balances today. The repo reads the facts about every draft in one
// statement (TransactionRepo.DraftTransactions); which of them are drafts to be
// counted, the months and accounts, and what is moved are the model's to say
// (model.SummarizeDrafts), as of the day the clock gives. No draft is no month and a
// count of zero, and no error.
func (s *Service) GetDraftSummary(ctx context.Context, _ usecase.GetDraftSummaryInput) (usecase.GetDraftSummaryOutput, error) {
	today := s.today()
	txns, err := s.transactions.DraftTransactions(ctx)
	if err != nil {
		return usecase.GetDraftSummaryOutput{}, fmt.Errorf("get the draft summary: %w", err)
	}
	summary, err := model.SummarizeDrafts(txns, today)
	if err != nil {
		return usecase.GetDraftSummaryOutput{}, fmt.Errorf("get the draft summary: %w", err)
	}

	out := usecase.GetDraftSummaryOutput{
		Count:              summary.Count,
		OwnTransfers:       summary.OwnTransfers,
		OwnTransferAmounts: toDraftAmounts(summary.OwnTransferAmounts),
		Amounts:            toDraftAmounts(summary.Amounts),
		Months:             make([]usecase.DraftMonth, len(summary.Months)),
	}
	for i, month := range summary.Months {
		accounts := make([]usecase.DraftAccount, len(month.Accounts))
		for j, account := range month.Accounts {
			accounts[j] = usecase.DraftAccount{AccountID: account.AccountID, Count: account.Count, Amounts: toDraftAmounts(account.Amounts)}
		}
		out.Months[i] = usecase.DraftMonth{
			Month:              month.Month,
			Count:              month.Count,
			OwnTransfers:       month.OwnTransfers,
			OwnTransferAmounts: toDraftAmounts(month.OwnTransferAmounts),
			Amounts:            toDraftAmounts(month.Amounts),
			Accounts:           accounts,
		}
	}
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
