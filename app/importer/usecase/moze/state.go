package moze

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// state is what the ledger has of the import, read at one time: the ids of the
// accounts and instruments by the names the plan uses, and the transactions the
// import recorded, found by their idempotency keys. The import tells its own
// transactions from the others by the key: it starts with model.KeyPrefix.
type state struct {
	accountIDs      map[string]uuid.UUID
	accountNames    map[uuid.UUID]string
	instrumentIDs   map[string]uuid.UUID
	instrumentCodes map[uuid.UUID]string
	byKey           map[string]finance.Transaction
}

// readState reads the ledger.
func (a *Importer) readState(ctx context.Context) (*state, error) {
	st := &state{
		accountIDs:      map[string]uuid.UUID{},
		accountNames:    map[uuid.UUID]string{},
		instrumentIDs:   map[string]uuid.UUID{},
		instrumentCodes: map[uuid.UUID]string{},
		byKey:           map[string]finance.Transaction{},
	}
	accounts, err := a.ledger.ListAccounts.ListAccounts(ctx, finance.ListAccountsInput{IncludeArchived: true})
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	for _, acc := range accounts.Accounts {
		st.accountIDs[acc.Name] = acc.ID
		st.accountNames[acc.ID] = acc.Name
	}
	instruments, err := a.ledger.ListInstruments.ListInstruments(ctx, finance.ListInstrumentsInput{IncludeArchived: true})
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	for _, in := range instruments.Instruments {
		st.instrumentIDs[in.Code] = in.ID
		st.instrumentCodes[in.ID] = in.Code
	}
	return st, st.readTransactions(ctx, a)
}

// readTransactions lists every transaction of the ledger, a page at a time, and keeps
// the ones the import recorded.
func (st *state) readTransactions(ctx context.Context, a *Importer) error {
	for offset := 0; ; {
		page, err := a.ledger.ListTransactions.ListTransactions(ctx, finance.ListTransactionsInput{Limit: finance.MaxListLimit, Offset: offset})
		if err != nil {
			return fmt.Errorf("list transactions: %w", err)
		}
		for _, t := range page.Transactions {
			if strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) {
				st.byKey[t.IdempotencyKey] = t
			}
		}
		offset += len(page.Transactions)
		if !page.HasMore || len(page.Transactions) == 0 {
			return nil
		}
	}
}

// unitState is where a unit stands in the ledger.
type unitState int

const (
	// unitMissing is a unit the ledger does not have, or has only some of.
	unitMissing unitState = iota
	unitDraft
	unitConfirmed
	unitVoid
)

// of returns where the unit stands, and the transactions of it that are drafts.
func (st *state) of(u model.Unit) (unitState, []uuid.UUID) {
	var drafts []uuid.UUID
	voided := false
	for _, key := range u.Keys() {
		t, ok := st.byKey[key]
		if !ok {
			return unitMissing, nil
		}
		switch t.Status {
		case "draft":
			drafts = append(drafts, t.ID)
		case "void":
			voided = true
		}
	}
	switch {
	case len(drafts) > 0:
		return unitDraft, drafts
	case voided:
		return unitVoid, nil
	default:
		return unitConfirmed, nil
	}
}

// adjustment returns the transaction that reconciles the adjustment, if the import
// recorded one.
func (st *state) adjustment(adj model.Adjustment) (finance.Transaction, bool) {
	t, ok := st.byKey[adj.Key]
	return t, ok
}

// orphans are the drafts of the import that the plan does not have any more: the
// export was changed, or is another.
func (st *state) orphans(plan model.Plan) []finance.Transaction {
	known := map[string]bool{}
	for _, u := range plan.Units {
		for _, k := range u.Keys() {
			known[k] = true
		}
	}
	for _, adj := range plan.Adjustments {
		known[adj.Key] = true
	}
	var out []finance.Transaction
	for key, t := range st.byKey {
		if !known[key] && t.Status == "draft" {
			out = append(out, t)
		}
	}
	return out
}
