package ledger

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/google/uuid"

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
//
// It adds the balances up, for all the accounts and for those of each kind, an instrument
// at a time (model.TotalPreviewBalances): the sums are of the balances it was given, so
// they are of the same moment. The order of the instruments is that of the catalog's list
// (by code) and the kind of an account is the catalog's too: they are read apart from the
// balances, and only when there is a balance, since a balance has no code and no kind of
// its own. Neither changes what a balance is. A balance cannot have an account or an
// instrument that the lists do not have; if one did, its instrument would come last and
// its account would be of the kind "", and nothing would be left out of a sum.
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
	if len(preview.Balances) == 0 {
		return out, nil
	}

	accounts, err := s.accounts.List(ctx, true)
	if err != nil {
		return usecase.GetPreviewBalancesOutput{}, fmt.Errorf("list the accounts to add up the preview balances: %w", err)
	}
	instruments, err := s.instruments.List(ctx, true)
	if err != nil {
		return usecase.GetPreviewBalancesOutput{}, fmt.Errorf("list the instruments to add up the preview balances: %w", err)
	}
	rank := make(map[uuid.UUID]int, len(instruments))
	for i, instrument := range instruments {
		rank[instrument.ID] = i
	}
	kindOf := make(map[uuid.UUID]string, len(accounts))
	for _, a := range accounts {
		kindOf[a.ID] = string(a.Kind)
	}

	out.Totals = toPreviewTotals(preview.Balances, rank)
	byKind := make(map[string][]model.PreviewBalance)
	for _, b := range preview.Balances {
		kind := kindOf[b.AccountID]
		byKind[kind] = append(byKind[kind], b)
	}
	for _, kind := range slices.Sorted(maps.Keys(byKind)) {
		out.Kinds = append(out.Kinds, usecase.PreviewKindTotals{Kind: kind, Totals: toPreviewTotals(byKind[kind], rank)})
	}
	return out, nil
}

// toPreviewTotals adds the balances up and puts the instruments in the order of rank, the
// instruments it has not got last, by id.
func toPreviewTotals(balances []model.PreviewBalance, rank map[uuid.UUID]int) []usecase.PreviewTotal {
	totals := model.TotalPreviewBalances(balances)
	position := func(id uuid.UUID) int {
		if i, found := rank[id]; found {
			return i
		}
		return len(rank)
	}
	slices.SortFunc(totals, func(a, b model.PreviewTotal) int {
		return cmp.Or(cmp.Compare(position(a.InstrumentID), position(b.InstrumentID)), bytes.Compare(a.InstrumentID[:], b.InstrumentID[:]))
	})
	out := make([]usecase.PreviewTotal, len(totals))
	for i, t := range totals {
		out[i] = usecase.PreviewTotal{InstrumentID: t.InstrumentID, Held: toPreviewFigure(t.Held), Unbilled: toPreviewFigure(t.Unbilled)}
	}
	return out
}

// toPreviewFigure returns the figure as the use case gives it: with its total, and with
// what it is made of, in the words of the port (usecase.PreviewBasisOf, the one mapping
// from the names of the model's basis, which the tests of this use case hold to the
// three words of the port).
func toPreviewFigure(f model.PreviewFigure) usecase.PreviewFigure {
	return usecase.PreviewFigure{Total: f.Total(), Confirmed: f.Confirmed, Drafts: f.Drafts, Basis: usecase.PreviewBasisOf(string(f.Basis()))}
}
