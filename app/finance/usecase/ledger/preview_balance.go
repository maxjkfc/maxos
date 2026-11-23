package ledger

import (
	"context"
	"fmt"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// GetPreviewBalances returns what the accounts would hold today if every draft were
// confirmed: for each account and instrument, the two figures of its balance as
// GetBalances reads them, what is confirmed of each, and what the drafts would add to it.
// Adding up the postings of both, which for what the account holds is the same rule as
// GetDraftSummary's for the drafts (the account postings of what happened on or before
// today, each in its own account), is the repo's to do, in one read, together with the
// count of the drafts, so that they are of one moment; the use case asks it for today,
// the day the clock gives. What a figure is made of, and whether anything is confirmed,
// are the model's to say (model.PreviewFigure, model.BalancePreview), so that a page
// writes them and does not decide them.
func (s *Service) GetPreviewBalances(ctx context.Context, _ usecase.GetPreviewBalancesInput) (usecase.GetPreviewBalancesOutput, error) {
	preview, err := s.transactions.PreviewBalances(ctx, s.today())
	if err != nil {
		return usecase.GetPreviewBalancesOutput{}, fmt.Errorf("get the preview balances: %w", err)
	}
	out := usecase.GetPreviewBalancesOutput{
		Balances:     make([]usecase.PreviewBalance, len(preview.Balances)),
		DraftCount:   preview.Drafts,
		HasConfirmed: preview.HasConfirmed(),
	}
	for i, b := range preview.Balances {
		out.Balances[i] = usecase.PreviewBalance{
			AccountID:    b.AccountID,
			InstrumentID: b.InstrumentID,
			Held:         toPreviewFigure(b.Held),
			Unbilled:     toPreviewFigure(b.Unbilled),
		}
	}
	return out, nil
}

// toPreviewFigure returns the figure as the use case gives it: with its total, and with
// what it is made of.
func toPreviewFigure(f model.PreviewFigure) usecase.PreviewFigure {
	return usecase.PreviewFigure{Total: f.Total(), Confirmed: f.Confirmed, Drafts: f.Drafts, Basis: toPreviewBasis(f.Basis())}
}

// toPreviewBasis says what the model says a figure is made of in the words of the port. A
// basis that it does not know is what no draft changes: nothing is said of it.
func toPreviewBasis(b model.PreviewBasis) usecase.PreviewBasis {
	switch b {
	case model.BasisDrafts:
		return usecase.PreviewBasisDrafts
	case model.BasisBoth:
		return usecase.PreviewBasisBoth
	default:
		return usecase.PreviewBasisConfirmed
	}
}
