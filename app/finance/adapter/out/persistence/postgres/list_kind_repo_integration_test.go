package postgres_test

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// storedOfEveryKind stores a transaction of each of the ten kinds, and the plans, whose
// origin and periods are not all of one kind, and returns everything stored.
func (e *ledgerEnv) storedOfEveryKind() []model.Transaction {
	e.t.Helper()
	var all []model.Transaction
	store := func(txn model.Transaction) model.Transaction {
		e.t.Helper()
		stored := e.store(txn)
		all = append(all, stored)
		return stored
	}
	must := func(txn model.Transaction, err error) model.Transaction {
		e.t.Helper()
		if err != nil {
			e.t.Fatalf("the domain refused the transaction: %v", err)
		}
		return txn
	}

	store(e.expenseOf(e.header("kind-expense-0001", nov(1)), "10"))
	store(must(model.NewIncome(model.IncomeParams{
		Header: e.header("kind-income-00001", nov(2)), Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("100")}},
	})))
	store(must(model.NewTransfer(model.TransferParams{
		Header: e.header("kind-transfer-001", nov(3)), From: e.bank, To: e.cash, Instrument: e.twd, Amount: dec("5"),
	})))
	store(must(model.NewExchange(model.ExchangeParams{
		Header: e.header("kind-exchange-001", nov(4)), FromAccount: e.bank, ToAccount: e.bank,
		FromInstrument: e.twd, ToInstrument: e.usd, FromAmount: dec("3200"), ToAmount: dec("100"),
	})))
	store(e.opening("kind-opening-0001", e.cash, "100"))
	store(must(model.NewReward(model.RewardParams{
		Header: e.header("kind-reward-00001", nov(5)), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("5"),
	})))
	store(e.dealingOf("kind-dealing-0001", nov(6), e.partyA, e.twd, "5000", model.DealingPaidOut))
	reconciliation, err := model.Reconcile(model.ReconcileParams{
		Header: e.header("kind-reconcile-01", nov(7)), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		RecordedBalance: dec("1000"), ActualBalance: dec("880"),
	})
	if err != nil || reconciliation.Transaction == nil {
		e.t.Fatalf("Reconcile = %+v, %v", reconciliation, err)
	}
	store(*reconciliation.Transaction)
	store(must(model.NewTrade(model.TradeParams{
		Header: e.header("kind-trade-000001", nov(8)), Direction: model.TradeBuy, Account: e.cash, Instrument: e.btc, Quantity: dec("0.1"),
		CashAccount: e.bank, Currency: e.twd, Amount: dec("300000"), BaseCurrency: e.twd,
	})))

	// A purchase in instalments: its origin is an expense, and each period is an instalment. The
	// conversion of a bill: its origin and each period are instalments.
	for _, plan := range []model.InstalmentPlan{e.planOf("kind-plan-0000001"), e.billInstalmentOf("kind-bill-0000001")} {
		e.storePlan(plan)
		for _, txn := range plan.Transactions() {
			all = append(all, e.get(txn.ID))
		}
	}
	return all
}

// idsOf returns the ids of the transactions, in the order of the ids.
func idsOf(txns []model.Transaction) []uuid.UUID {
	ids := make([]uuid.UUID, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return ids
}

func TestListFiltersByTheKindOfTheTransactionForEachOfTheTenKinds(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	all := e.storedOfEveryKind()
	byKind := map[model.Kind][]model.Transaction{}
	for _, txn := range all {
		byKind[txn.Kind] = append(byKind[txn.Kind], txn)
	}
	kinds := []model.Kind{
		model.KindExpense, model.KindIncome, model.KindTransfer, model.KindExchange, model.KindOpening,
		model.KindReward, model.KindDealing, model.KindReconciliation, model.KindInstalment, model.KindTrade,
	}
	for _, kind := range kinds {
		if !kind.Valid() || len(byKind[kind]) == 0 {
			t.Fatalf("kind %q: %d stored, want at least one of each of the ten kinds", kind, len(byKind[kind]))
		}
	}
	// The plans are made of more than one kind: the origin of a purchase in instalments is an
	// expense, and every period of it, and the conversion of a bill with its periods, are
	// instalments.
	if got, want := len(byKind[model.KindExpense]), 2; got != want {
		t.Fatalf("%d expenses stored, want %d: one, and the origin of a purchase in instalments", got, want)
	}
	if got, want := len(byKind[model.KindInstalment]), 12+1+3; got != want {
		t.Fatalf("%d instalments stored, want %d: the 12 periods of a purchase, the conversion of a bill and its 3 periods", got, want)
	}

	for _, kind := range kinds {
		t.Run(string(kind), func(t *testing.T) {
			got, err := e.txns.List(ctx, repo.TransactionFilter{Kind: kind, Limit: 100})
			if err != nil {
				t.Fatalf("List: %v", err)
			}

			if !slices.Equal(idsOf(got), idsOf(byKind[kind])) {
				t.Errorf("List of kind %s = %d transactions, want the %d of that kind and no other", kind, len(got), len(byKind[kind]))
			}
			for _, txn := range got {
				if txn.Kind != kind {
					t.Errorf("transaction %s is a %s in the list of %s", txn.ID, txn.Kind, kind)
				}
			}
		})
	}

	// No kind lists them all.
	got, err := e.txns.List(ctx, repo.TransactionFilter{Limit: 100})
	if err != nil || len(got) != len(all) {
		t.Errorf("List with no kind = %d transactions, %v, want all %d", len(got), err, len(all))
	}
}

func TestListFiltersByTheKindWithTheOtherFiltersAndPagesWithinIt(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	all := e.storedOfEveryKind()
	var transfer, instalments []model.Transaction
	for _, txn := range all {
		switch txn.Kind {
		case model.KindTransfer:
			transfer = append(transfer, txn)
		case model.KindInstalment:
			instalments = append(instalments, txn)
		}
	}

	list := func(f repo.TransactionFilter) []model.Transaction {
		t.Helper()
		if f.Limit == 0 {
			f.Limit = 100
		}
		got, err := e.txns.List(ctx, f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		return got
	}

	// A kind and a status: the transfer is a draft until it is confirmed.
	if got := list(repo.TransactionFilter{Kind: model.KindTransfer, Status: model.StatusConfirmed}); len(got) != 0 {
		t.Errorf("confirmed transfers = %d, want none while it is a draft", len(got))
	}
	e.setStatus(transfer[0].ID, model.StatusConfirmed)
	if got := list(repo.TransactionFilter{Kind: model.KindTransfer, Status: model.StatusConfirmed}); !slices.Equal(idsOf(got), idsOf(transfer)) {
		t.Errorf("confirmed transfers = %v, want the transfer", idsOf(got))
	}
	if got := list(repo.TransactionFilter{Kind: model.KindTransfer, Status: model.StatusDraft}); len(got) != 0 {
		t.Errorf("draft transfers = %d, want none once it is confirmed", len(got))
	}
	if got := list(repo.TransactionFilter{Kind: model.KindExpense, Status: model.StatusConfirmed}); len(got) != 0 {
		t.Errorf("confirmed expenses = %d, want none: the transfer that is confirmed is not one", len(got))
	}

	// A kind and an account: the expense paid from cash, and not the origin of the purchase in
	// instalments, which is charged to the card.
	got := list(repo.TransactionFilter{Kind: model.KindExpense, AccountID: &e.cash.ID})
	if len(got) != 1 || got[0].IdempotencyKey != "kind-expense-0001" {
		t.Errorf("expenses with a posting to the cash = %d, want the one expense paid from it", len(got))
	}
	// A kind and a plan: the instalments of the plan, which for a purchase in instalments are its
	// periods and not its origin, which is an expense.
	planID := instalments[0].PlanID
	if planID == nil {
		t.Fatal("an instalment that is part of no plan")
	}
	var ofPlan []model.Transaction
	for _, txn := range instalments {
		if txn.PlanID != nil && *txn.PlanID == *planID {
			ofPlan = append(ofPlan, txn)
		}
	}
	if got := list(repo.TransactionFilter{Kind: model.KindInstalment, PlanID: planID}); !slices.Equal(idsOf(got), idsOf(ofPlan)) {
		t.Errorf("instalments of the plan %s = %d, want the %d of its transactions that are instalments", planID, len(got), len(ofPlan))
	}
	// A kind and a range of days: the periods of the purchase fall due on the 5th of each month
	// from December, those of the bill on the 20th of November, December and January.
	from, to := on(2026, time.December, 1), on(2027, time.February, 28)
	inRange := list(repo.TransactionFilter{Kind: model.KindInstalment, From: &from, To: &to})
	for _, txn := range inRange {
		if txn.Kind != model.KindInstalment || txn.OccurredOn.Before(from) || txn.OccurredOn.After(to) {
			t.Errorf("transaction %s: a %s of %s, want an instalment from December to February", txn.ID, txn.Kind, txn.OccurredOn.Format(time.DateOnly))
		}
	}
	if want := 3 + 2; len(inRange) != want {
		t.Errorf("%d instalments from December to February, want %d: three periods of the purchase and two of the bill", len(inRange), want)
	}

	// Pages are of the kind: the first of five, the second, the last, and one past the end.
	var paged []model.Transaction
	for offset := 0; offset < len(instalments); offset += 5 {
		page := list(repo.TransactionFilter{Kind: model.KindInstalment, Limit: 5, Offset: offset})
		if want := min(5, len(instalments)-offset); len(page) != want {
			t.Errorf("page at %d = %d transactions, want %d", offset, len(page), want)
		}
		paged = append(paged, page...)
	}
	if !slices.Equal(idsOf(paged), idsOf(instalments)) {
		t.Errorf("the pages of instalments hold %d transactions, want the %d there are, each once", len(paged), len(instalments))
	}
	if page := list(repo.TransactionFilter{Kind: model.KindInstalment, Limit: 5, Offset: len(instalments)}); len(page) != 0 {
		t.Errorf("a page past the end = %d transactions, want none", len(page))
	}
}
