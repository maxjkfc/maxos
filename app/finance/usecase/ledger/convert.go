package ledger

import (
	"slices"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// toTransaction returns the transaction as the use cases return it. Nothing in it
// is shared with t, so what the caller does with it cannot reach the store's
// copy.
func toTransaction(t model.Transaction) usecase.Transaction {
	out := usecase.Transaction{
		ID:             t.ID,
		Kind:           string(t.Kind),
		Status:         string(t.Status),
		OccurredOn:     t.OccurredOn,
		OccurredAt:     clone(t.OccurredAt),
		Description:    t.Description,
		Merchant:       t.Merchant,
		ProjectID:      clone(t.ProjectID),
		AreaID:         clone(t.AreaID),
		IdempotencyKey: t.IdempotencyKey,
		TagIDs:         slices.Clone(t.TagIDs),
		Postings:       make([]usecase.Posting, len(t.Postings)),
		CreatedAt:      t.CreatedAt,
		ConfirmedAt:    clone(t.ConfirmedAt),
		VoidedAt:       clone(t.VoidedAt),
	}
	for i, p := range t.Postings {
		out.Postings[i] = usecase.Posting{
			Seq:          p.Seq,
			Book:         string(p.Book),
			AccountID:    clone(p.AccountID),
			CategoryID:   clone(p.CategoryID),
			PartyID:      clone(p.PartyID),
			InstrumentID: p.InstrumentID,
			Quantity:     p.Quantity,
			BaseAmount:   clone(p.BaseAmount),
			Discount:     clone(p.Discount),
			Memo:         p.Memo,
		}
	}
	return out
}

// clone returns a pointer to a copy of what p points at, nil for nil.
func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
