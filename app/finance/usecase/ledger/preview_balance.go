package ledger

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// GetPreviewBalances returns what the accounts would hold today if every draft were
// confirmed: the confirmed balance of each account and instrument, as GetBalances reads
// it, and what the drafts would add to it. Adding up the postings of both, which is the
// same rule as GetDraftSummary's for the drafts (the account postings of what happened
// on or before today, each in its own account), is the repo's to do, in one read so that
// the two parts are of one moment; the use case asks it for today, the day the clock
// gives, and says the sum of the two parts, which is the model's (PreviewBalance.Quantity).
func (s *Service) GetPreviewBalances(ctx context.Context, _ usecase.GetPreviewBalancesInput) (usecase.GetPreviewBalancesOutput, error) {
	balances, err := s.transactions.PreviewBalances(ctx, s.today())
	if err != nil {
		return usecase.GetPreviewBalancesOutput{}, fmt.Errorf("get the preview balances: %w", err)
	}
	out := usecase.GetPreviewBalancesOutput{Balances: make([]usecase.PreviewBalance, len(balances))}
	for i, b := range balances {
		out.Balances[i] = usecase.PreviewBalance{
			AccountID:          b.AccountID,
			InstrumentID:       b.InstrumentID,
			Quantity:           b.Quantity(),
			Confirmed:          b.Confirmed,
			Drafts:             b.Drafts,
			UnbilledInstalment: b.UnbilledInstalment,
		}
	}
	return out, nil
}
