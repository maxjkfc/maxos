package ledger_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

func ids(txns ...usecase.Transaction) []uuid.UUID {
	out := make([]uuid.UUID, len(txns))
	for i, txn := range txns {
		out[i] = txn.ID
	}
	return out
}

func TestConfirmTransactionsConfirmsTheDraftsInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)
	a := e.recordLunch("lunch-0000001a")
	b := e.recordLunch("lunch-0000001b")
	opened := e.tx.calls

	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{
		IDs: []uuid.UUID{b.ID, a.ID, b.ID},
	})
	if err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}

	if got := ids(out.Transactions...); !slices.Equal(got, []uuid.UUID{b.ID, a.ID}) {
		t.Fatalf("confirmed %v, want %v: in the order given, an id given twice once", got, []uuid.UUID{b.ID, a.ID})
	}
	for _, txn := range out.Transactions {
		if txn.Status != "confirmed" || txn.ConfirmedAt == nil {
			t.Errorf("transaction %s = %s, confirmed at %v, want it confirmed", txn.ID, txn.Status, txn.ConfirmedAt)
		}
		if e.txns.byID[txn.ID].Status != model.StatusConfirmed {
			t.Errorf("stored transaction %s is %s, want confirmed", txn.ID, e.txns.byID[txn.ID].Status)
		}
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1 for all of them", got)
	}
}

func TestConfirmTransactionsIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	draft := e.recordLunch("lunch-0000001a")
	confirmed := e.confirmedLunch("lunch-0000001b")
	unknown := uuid.Must(uuid.NewV7())
	changes := len(e.txns.statusSet)

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{
		IDs: []uuid.UUID{draft.ID, confirmed.ID, unknown},
	})

	// Every transaction that cannot be confirmed is reported, so that the caller
	// can put them all right at once.
	if !errors.Is(err, model.ErrNotDraft) || !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("err = %v, want both ErrNotDraft and ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), confirmed.ID.String()) || !strings.Contains(err.Error(), unknown.String()) {
		t.Errorf("err = %q, want it to name %s and %s", err, confirmed.ID, unknown)
	}
	if got := e.txns.byID[draft.ID]; got.Status != model.StatusDraft || len(e.txns.statusSet) != changes {
		t.Errorf("the draft is %s, %d changes written, want it left a draft and nothing written", got.Status, len(e.txns.statusSet)-changes)
	}
}

func TestConfirmTransactionsLocksInTheOrderOfTheIds(t *testing.T) {
	e := newEnv(t)
	var recorded []usecase.Transaction
	for _, key := range []string{"lunch-0000001a", "lunch-0000001b", "lunch-0000001c"} {
		recorded = append(recorded, e.recordLunch(key))
	}
	byBytes := func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }
	ascending := ids(recorded...)
	slices.SortFunc(ascending, byBytes)
	descending := slices.Clone(ascending)
	slices.Reverse(descending)

	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: descending})
	if err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}

	if !slices.Equal(e.txns.locked, ascending) {
		t.Errorf("locked %v, want them locked in id order, %v, so that two calls cannot wait for each other", e.txns.locked, ascending)
	}
	if got := ids(out.Transactions...); !slices.Equal(got, descending) {
		t.Errorf("returned %v, want the order the ids were given in, %v", got, descending)
	}
}

func TestConfirmTransactionsNeedsAtLeastOneId(t *testing.T) {
	e := newEnv(t)

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{})

	if !errors.Is(err, model.ErrNoTransactions) {
		t.Fatalf("err = %v, want ErrNoTransactions", err)
	}
	if e.tx.calls != 0 {
		t.Errorf("%d database transactions opened, want none", e.tx.calls)
	}
}

func TestConfirmTransactionsDoesNotConfirmWhatIsVoid(t *testing.T) {
	e := newEnv(t)
	voided := e.confirmedLunch("lunch-0000001a")
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{voided.ID}})

	if !errors.Is(err, model.ErrNotDraft) {
		t.Fatalf("err = %v, want ErrNotDraft", err)
	}
	if got := e.txns.byID[voided.ID].Status; got != model.StatusVoid {
		t.Errorf("status = %s, want it left void", got)
	}
}

func TestDiscardDraftDeletesTheDraftInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)
	draft := e.recordLunch("lunch-0000001a")
	kept := e.recordLunch("lunch-0000001b")
	opened := e.tx.calls

	if err := e.svc.DiscardDraft(context.Background(), draft.ID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}

	if _, found := e.txns.byID[draft.ID]; found || !slices.Equal(e.txns.deleted, []uuid.UUID{draft.ID}) {
		t.Errorf("deleted %v, want only the draft deleted", e.txns.deleted)
	}
	if _, found := e.txns.byID[kept.ID]; !found {
		t.Errorf("the other draft is gone, want it left alone")
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1", got)
	}
}

func TestDiscardDraftRefusesWhatIsNotADraft(t *testing.T) {
	e := newEnv(t)
	confirmed := e.confirmedLunch("lunch-0000001a")
	voided := e.confirmedLunch("lunch-0000001b")
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	for name, id := range map[string]uuid.UUID{"confirmed": confirmed.ID, "void": voided.ID} {
		err := e.svc.DiscardDraft(context.Background(), id)
		if !errors.Is(err, model.ErrNotDraft) {
			t.Errorf("%s: err = %v, want ErrNotDraft", name, err)
		}
		if _, found := e.txns.byID[id]; !found {
			t.Errorf("%s: the transaction is gone, want it kept", name)
		}
	}
	if len(e.txns.deleted) != 0 {
		t.Errorf("deleted %v, want nothing deleted", e.txns.deleted)
	}
}

func TestDiscardDraftReportsAnUnknownTransaction(t *testing.T) {
	e := newEnv(t)
	unknown := uuid.Must(uuid.NewV7())

	err := e.svc.DiscardDraft(context.Background(), unknown)

	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), unknown.String()) {
		t.Fatalf("err = %v, want ErrNotFound naming the id", err)
	}
}

func TestVoidTransactionVoidsAConfirmedTransactionInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)
	confirmed := e.confirmedLunch("lunch-0000001a")
	opened := e.tx.calls

	got, err := e.svc.VoidTransaction(context.Background(), confirmed.ID)
	if err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	if got.ID != confirmed.ID || got.Status != "void" || got.VoidedAt == nil || got.ConfirmedAt == nil {
		t.Fatalf("transaction = %+v, want it void, with the time it was confirmed and the time it was voided", got)
	}
	if e.txns.byID[confirmed.ID].Status != model.StatusVoid || len(e.txns.byID[confirmed.ID].Postings) != 2 {
		t.Errorf("stored = %+v, want it void, with its postings kept", e.txns.byID[confirmed.ID])
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1", got)
	}
}

func TestVoidTransactionRefusesWhatIsNotConfirmed(t *testing.T) {
	e := newEnv(t)
	draft := e.recordLunch("lunch-0000001a")
	voided := e.confirmedLunch("lunch-0000001b")
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	changes := len(e.txns.statusSet)

	for name, id := range map[string]uuid.UUID{"a draft": draft.ID, "a void transaction": voided.ID} {
		if _, err := e.svc.VoidTransaction(context.Background(), id); !errors.Is(err, model.ErrNotConfirmed) {
			t.Errorf("%s: err = %v, want ErrNotConfirmed", name, err)
		}
	}
	unknown := uuid.Must(uuid.NewV7())
	if _, err := e.svc.VoidTransaction(context.Background(), unknown); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("unknown: err = %v, want ErrNotFound", err)
	}
	if len(e.txns.statusSet) != changes {
		t.Errorf("%d changes written, want none", len(e.txns.statusSet)-changes)
	}
}

func TestGetTransaction(t *testing.T) {
	e := newEnv(t)
	recorded := e.recordLunch("lunch-0000001a")

	got, err := e.svc.GetTransaction(context.Background(), recorded.ID)
	if err != nil || got.ID != recorded.ID || got.Status != "draft" || len(got.Postings) != 2 {
		t.Fatalf("GetTransaction = %+v, %v, want the draft with its postings", got, err)
	}

	unknown := uuid.Must(uuid.NewV7())
	_, err = e.svc.GetTransaction(context.Background(), unknown)
	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), unknown.String()) {
		t.Fatalf("err = %v, want ErrNotFound naming the id", err)
	}
}

func TestListTransactionsAsksOneMoreThanThePageToKnowIfAnotherFollows(t *testing.T) {
	e := newEnv(t)
	for range 3 {
		e.txns.listResult = append(e.txns.listResult, model.Transaction{
			ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft,
		})
	}

	tests := []struct {
		limit       int
		wantAsked   int
		wantPage    int
		wantHasMore bool
	}{
		{limit: 2, wantAsked: 3, wantPage: 2, wantHasMore: true},
		{limit: 3, wantAsked: 4, wantPage: 3, wantHasMore: false},
		{limit: 10, wantAsked: 11, wantPage: 3, wantHasMore: false},
		{limit: 0, wantAsked: usecase.DefaultListLimit + 1, wantPage: 3, wantHasMore: false},
		{limit: 100000, wantAsked: usecase.MaxListLimit + 1, wantPage: 3, wantHasMore: false},
	}
	for _, tt := range tests {
		out, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{Limit: tt.limit})
		if err != nil {
			t.Fatalf("limit %d: ListTransactions: %v", tt.limit, err)
		}
		if e.txns.listed.Limit != tt.wantAsked || len(out.Transactions) != tt.wantPage || out.HasMore != tt.wantHasMore {
			t.Errorf("limit %d: asked the repo for %d, got a page of %d, has more %v; want %d, %d, %v",
				tt.limit, e.txns.listed.Limit, len(out.Transactions), out.HasMore, tt.wantAsked, tt.wantPage, tt.wantHasMore)
		}
	}
}

func TestListTransactionsPassesTheFilterOn(t *testing.T) {
	e := newEnv(t)
	taipei := time.FixedZone("UTC+8", 8*60*60)
	from := time.Date(2026, 11, 12, 23, 59, 0, 0, taipei)
	to := time.Date(2026, 11, 14, 0, 1, 0, 0, taipei)
	project, area, party := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	_, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{
		Status:    "confirmed",
		From:      &from,
		To:        &to,
		AccountID: &e.cash.ID,
		PartyID:   &party,
		ProjectID: &project,
		AreaID:    &area,
		Limit:     20,
		Offset:    40,
	})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}

	f := e.txns.listed
	wantFrom, wantTo := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)
	if f.Status != model.StatusConfirmed || f.From == nil || !f.From.Equal(wantFrom) || f.To == nil || !f.To.Equal(wantTo) {
		t.Errorf("filter = %+v, want confirmed, from %v to %v: the day each time falls on", f, wantFrom, wantTo)
	}
	if f.AccountID == nil || *f.AccountID != e.cash.ID || f.PartyID == nil || *f.PartyID != party ||
		f.ProjectID == nil || *f.ProjectID != project || f.AreaID == nil || *f.AreaID != area {
		t.Errorf("filter = %+v, want the account, the party, the project and the area", f)
	}
	if f.Limit != 21 || f.Offset != 40 {
		t.Errorf("limit %d, offset %d, want 21 (the page and one more) and 40", f.Limit, f.Offset)
	}

	if _, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{}); err != nil {
		t.Fatalf("ListTransactions without a filter: %v", err)
	}
	if f := e.txns.listed; f.Status != "" || f.From != nil || f.To != nil || f.AccountID != nil || f.PartyID != nil || f.ProjectID != nil || f.AreaID != nil {
		t.Errorf("filter = %+v, want no condition set", f)
	}
}

func TestListTransactionsRefusesWhatCannotBeRight(t *testing.T) {
	e := newEnv(t)
	early, late := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		in   usecase.ListTransactionsInput
		want error
	}{
		{"a status that is not one", usecase.ListTransactionsInput{Status: "pending"}, model.ErrInvalidStatus},
		{"a range that ends before it starts", usecase.ListTransactionsInput{From: &late, To: &early}, model.ErrDateRangeReversed},
		{"a negative limit", usecase.ListTransactionsInput{Limit: -1}, model.ErrInvalidPage},
		{"a negative offset", usecase.ListTransactionsInput{Offset: -1}, model.ErrInvalidPage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := e.svc.ListTransactions(context.Background(), tt.in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if e.txns.lists != 0 {
				t.Errorf("the repo was asked %d times, want it not asked", e.txns.lists)
			}
		})
	}

	// The two ends of a range may be the same day.
	if _, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{From: &early, To: &early}); err != nil {
		t.Errorf("a one-day range: %v, want it accepted", err)
	}
}

func TestListTransactionsReturnsWholeTransactions(t *testing.T) {
	e := newEnv(t)
	recorded := e.recordLunch("lunch-0000001a")
	e.txns.listResult = []model.Transaction{e.txns.byID[recorded.ID]}

	out, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{})
	if err != nil || len(out.Transactions) != 1 {
		t.Fatalf("ListTransactions = %+v, %v, want the one transaction", out, err)
	}
	wantLines(t, e.lines(out.Transactions[0]), "account(cash) -120 TWD", "expense(food) +120 TWD, base +120")
}

func TestGetBalances(t *testing.T) {
	e := newEnv(t)
	e.txns.balanceRows = []model.Balance{
		{AccountID: e.bank.ID, InstrumentID: e.twd.ID, Quantity: dec("880")},
		{AccountID: e.bank.ID, InstrumentID: e.usd.ID, Quantity: dec("-12.5")},
	}

	all, err := e.svc.GetBalances(context.Background(), usecase.GetBalancesInput{})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if e.txns.balancesFor != nil || len(all.Balances) != 2 {
		t.Fatalf("asked for %v, got %d balances, want every account's, 2", e.txns.balancesFor, len(all.Balances))
	}
	if got := all.Balances[1]; got.AccountID != e.bank.ID || got.InstrumentID != e.usd.ID || !got.Quantity.Equal(dec("-12.5")) {
		t.Errorf("balance = %+v, want what the repo returned", got)
	}

	if _, err := e.svc.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &e.bank.ID}); err != nil {
		t.Fatalf("GetBalances for an account: %v", err)
	}
	if e.txns.balancesFor == nil || *e.txns.balancesFor != e.bank.ID {
		t.Errorf("asked for %v, want the account %s", e.txns.balancesFor, e.bank.ID)
	}
}
