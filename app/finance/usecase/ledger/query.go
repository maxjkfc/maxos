package ledger

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// GetTransaction returns the transaction, whatever its status.
func (s *Service) GetTransaction(ctx context.Context, id uuid.UUID) (usecase.Transaction, error) {
	txn, err := s.transactions.Get(ctx, id)
	if err != nil {
		return usecase.Transaction{}, fmt.Errorf("transaction %s: %w", id, err)
	}
	return toTransaction(txn), nil
}

// ListTransactions checks the filter and the page, and returns the page the repo
// finds. It asks the repo for one transaction more than the page holds, which is
// how it knows whether another page follows.
func (s *Service) ListTransactions(ctx context.Context, in usecase.ListTransactionsInput) (usecase.ListTransactionsOutput, error) {
	filter, limit, err := listFilter(in)
	if err != nil {
		return usecase.ListTransactionsOutput{}, err
	}
	txns, err := s.transactions.List(ctx, filter)
	if err != nil {
		return usecase.ListTransactionsOutput{}, fmt.Errorf("list transactions: %w", err)
	}

	out := usecase.ListTransactionsOutput{HasMore: len(txns) > limit}
	if out.HasMore {
		txns = txns[:limit]
	}
	out.Transactions = make([]usecase.Transaction, len(txns))
	for i, txn := range txns {
		out.Transactions[i] = toTransaction(txn)
	}
	return out, nil
}

// listFilter turns the input into the filter of the repo, which asks for one more
// than the page size, and returns the page size too.
func listFilter(in usecase.ListTransactionsInput) (repo.TransactionFilter, int, error) {
	filter := repo.TransactionFilter{
		AccountID: in.AccountID,
		PartyID:   in.PartyID,
		ProjectID: in.ProjectID,
		AreaID:    in.AreaID,
		Offset:    in.Offset,
	}
	if in.Status != "" {
		filter.Status = model.Status(in.Status)
		if !filter.Status.Valid() {
			return repo.TransactionFilter{}, 0, fmt.Errorf("%w %q: want draft, confirmed or void", model.ErrInvalidStatus, in.Status)
		}
	}
	if in.From != nil {
		from := model.DateOf(*in.From)
		filter.From = &from
	}
	if in.To != nil {
		to := model.DateOf(*in.To)
		filter.To = &to
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return repo.TransactionFilter{}, 0, fmt.Errorf("%w: from %s, to %s",
			model.ErrDateRangeReversed, filter.From.Format(time.DateOnly), filter.To.Format(time.DateOnly))
	}

	limit := in.Limit
	switch {
	case limit < 0 || in.Offset < 0:
		return repo.TransactionFilter{}, 0, fmt.Errorf("%w: limit %d, offset %d", model.ErrInvalidPage, in.Limit, in.Offset)
	case limit == 0:
		limit = usecase.DefaultListLimit
	case limit > usecase.MaxListLimit:
		limit = usecase.MaxListLimit
	}
	filter.Limit = limit + 1
	return filter, limit, nil
}

// GetBalances returns what the accounts hold. It counts the confirmed
// transactions only, which is the repo's to do.
func (s *Service) GetBalances(ctx context.Context, in usecase.GetBalancesInput) (usecase.GetBalancesOutput, error) {
	balances, err := s.transactions.Balances(ctx, in.AccountID)
	if err != nil {
		return usecase.GetBalancesOutput{}, fmt.Errorf("get balances: %w", err)
	}
	out := usecase.GetBalancesOutput{Balances: make([]usecase.Balance, len(balances))}
	for i, b := range balances {
		out.Balances[i] = usecase.Balance{AccountID: b.AccountID, InstrumentID: b.InstrumentID, Quantity: b.Quantity}
	}
	return out, nil
}

// GetDealings returns what the parties owe me. Adding up the confirmed dealing
// postings is the repo's to do, and it lists them by party id and then
// instrument code. The names of the parties are the catalog's, so for all the
// parties the use case puts them in the order the catalog lists them, by name,
// and keeps the instruments of each in the order of the repo.
//
// For one party (PartyID) there is nothing to put in order, so the catalog is
// asked about that party and not for the list of all of them. A party it does
// not know is model.ErrNotFound, whatever the repo holds, and the repo is not
// asked; an archived party is not one: what it owes still counts.
func (s *Service) GetDealings(ctx context.Context, in usecase.GetDealingsInput) (usecase.GetDealingsOutput, error) {
	if in.PartyID != nil {
		if err := s.requireParty(ctx, *in.PartyID); err != nil {
			return usecase.GetDealingsOutput{}, err
		}
	}
	dealings, err := s.transactions.Dealings(ctx, in.PartyID)
	if err != nil {
		return usecase.GetDealingsOutput{}, fmt.Errorf("get dealings: %w", err)
	}
	if in.PartyID == nil && len(dealings) > 0 {
		if err := s.sortByPartyName(ctx, dealings); err != nil {
			return usecase.GetDealingsOutput{}, err
		}
	}

	out := usecase.GetDealingsOutput{Dealings: make([]usecase.Dealing, len(dealings))}
	for i, d := range dealings {
		out.Dealings[i] = usecase.Dealing{PartyID: d.PartyID, InstrumentID: d.InstrumentID, Quantity: d.Quantity}
	}
	return out, nil
}

// sortByPartyName puts the dealings in the order the catalog lists the parties,
// by name, and keeps the instruments of each party in the order they are in. It
// fails for a dealing of a party the list does not have.
func (s *Service) sortByPartyName(ctx context.Context, dealings []model.Dealing) error {
	// Archived parties are listed too: what they owe me still counts.
	listed, err := s.listParties.ListParties(ctx, catalogusecase.ListPartiesInput{IncludeArchived: true})
	if err != nil {
		return fmt.Errorf("list parties: %w", err)
	}
	rank := make(map[uuid.UUID]int, len(listed.Parties))
	for i, party := range listed.Parties {
		rank[party.ID] = i
	}
	for _, d := range dealings {
		if _, known := rank[d.PartyID]; !known {
			return fmt.Errorf("party %s: %w", d.PartyID, model.ErrNotFound)
		}
	}
	slices.SortStableFunc(dealings, func(a, b model.Dealing) int { return cmp.Compare(rank[a.PartyID], rank[b.PartyID]) })
	return nil
}
