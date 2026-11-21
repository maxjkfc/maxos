package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// The summary of the drafts is made by model.SummarizeDrafts, whose tests show what it makes
// of the rows. What these tests show is that the statement of DraftTransactions returns the
// right rows, and that the list says the same of the drafts as the summary counts.

// draftRows reads the drafts the summary is made from, and writes each as a line: the day it
// happened on, its kind, its place in a plan when it is in one, and the postings that have an
// account, in the order they have in their transaction. The repo does not say in what order
// the drafts come, so the lines are in the order of the days, and then of the text. It fails
// the test when the name of an account or the code of an instrument is not that of its id.
func (e *ledgerEnv) draftRows() []string {
	e.t.Helper()
	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		e.t.Fatalf("DraftTransactions: %v", err)
	}
	names := e.names()
	lines := make([]string, len(rows))
	for i, txn := range rows {
		postings := make([]string, len(txn.Postings))
		for j, p := range txn.Postings {
			if p.AccountName != names[p.AccountID] || p.InstrumentCode != names[p.InstrumentID] {
				e.t.Errorf("a posting of %s is to account %s, %q, in instrument %s, %q, want %q and %q",
					txn.ID, p.AccountID, p.AccountName, p.InstrumentID, p.InstrumentCode, names[p.AccountID], names[p.InstrumentID])
			}
			postings[j] = fmt.Sprintf("%s(%s) %s %s", p.Book, p.AccountName, p.InstrumentCode, p.Quantity)
		}
		plan := ""
		if txn.PlanSeq != nil {
			plan = fmt.Sprintf(" plan %d", *txn.PlanSeq)
		}
		lines[i] = fmt.Sprintf("%s %s%s [%s]", txn.OccurredOn.Format(time.DateOnly), txn.Kind, plan, strings.Join(postings, ", "))
	}
	slices.Sort(lines)
	return lines
}

// wantRows fails the test unless the lines are the ones wanted.
func wantRows(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// draftExpense stores the draft of an expense of the amount of TWD on food, paid from
// the account, on the day.
func (e *ledgerEnv) draftExpense(key string, day time.Time, account model.Account, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.header(key, day), Account: &account, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("NewExpense: %v", err)
	}
	return e.store(txn)
}

// draftTransfer stores the draft of a transfer of the amount of the instrument between two
// accounts, with the fees.
func (e *ledgerEnv) draftTransfer(key string, day time.Time, from, to model.Account, instrument model.Instrument, amount string, fees ...model.Fee) model.Transaction {
	e.t.Helper()
	txn, err := model.NewTransfer(model.TransferParams{
		Header: e.header(key, day), From: from, To: to, Instrument: instrument, BaseCurrency: e.twd, Amount: dec(amount), Fees: fees,
	})
	if err != nil {
		e.t.Fatalf("NewTransfer: %v", err)
	}
	return e.store(txn)
}

// draftPaidByParty stores the draft of an expense of the amount of TWD on food that a party
// paid for me, which has no posting to an account.
func (e *ledgerEnv) draftPaidByParty(key string, day time.Time, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.header(key, day), PaidBy: &e.partyA, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("NewExpense paid by a party: %v", err)
	}
	return e.store(txn)
}

func TestDraftTransactionsAreTheDraftsOnlyWhateverDayTheyHappenedOnAndALaterStatusTakesOneOut(t *testing.T) {
	e := newLedgerEnv(t)
	e.draftExpense("rows-oct-00000001", on(2026, time.October, 30), e.cash, "80")
	nov14 := e.draftExpense("rows-nov-00000001", on(2026, time.November, 14), e.cash, "120")
	dec20 := e.draftExpense("rows-dec-00000001", on(2026, time.December, 20), e.card, "60") // dated in the future: still waiting
	// A confirmed transaction and a void one are not drafts, whatever day they are of.
	e.storeConfirmed(e.expenseOf(e.header("rows-conf-00000001", nov(5)), "1000"))
	voided := e.storeConfirmed(e.expenseOf(e.header("rows-void-00000001", nov(6)), "2000"))
	e.setStatus(voided.ID, model.StatusVoid)

	wantRows(t, "the drafts", e.draftRows(),
		"2026-10-30 expense [account(cash) TWD -80]",
		"2026-11-14 expense [account(cash) TWD -120]",
		"2026-12-20 expense [account(card) TWD -60]",
	)

	// Confirming a draft takes it out.
	e.setStatus(nov14.ID, model.StatusConfirmed)
	wantRows(t, "with one confirmed", e.draftRows(),
		"2026-10-30 expense [account(cash) TWD -80]",
		"2026-12-20 expense [account(card) TWD -60]",
	)
	// So does discarding it, which deletes it.
	if err := e.inTx(func(ctx context.Context) error { return e.txns.Delete(ctx, dec20.ID) }); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantRows(t, "with another discarded", e.draftRows(), "2026-10-30 expense [account(cash) TWD -80]")
}

func TestNoDraftIsNoRowsAndNotAnError(t *testing.T) {
	e := newLedgerEnv(t)

	if got := e.draftRows(); len(got) != 0 {
		t.Errorf("rows of a ledger with no transaction = %q, want none", got)
	}

	// Nor does a ledger that has confirmed transactions only.
	e.storeConfirmed(e.expenseOf(e.header("rows-conf-00000001", nov(5)), "10"))
	if got := e.draftRows(); len(got) != 0 {
		t.Errorf("rows of a ledger with a confirmed transaction only = %q, want none", got)
	}
}

func TestDraftTransactionsCarryThePostingsThatHaveAnAccountOfAnyBookAndNoOthers(t *testing.T) {
	e := newLedgerEnv(t)
	e.draftExpense("rows-expense-0001", nov(1), e.cash, "120") // an expense posting to food as well
	e.draftPaidByParty("rows-paid-000001", nov(2), "90")       // a dealing posting to the party and no account
	e.draftTransfer("rows-transfer-001", nov(3), e.bank, e.cash, e.twd, "1000",
		model.Fee{Account: e.card, Instrument: e.twd, Category: e.fees, Amount: dec("15")})
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.header("rows-income-00001", nov(4)), Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("50000")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.store(income)
	e.store(e.dealingOf("rows-dealing-0001", nov(5), e.partyA, e.twd, "5000", model.DealingPaidOut))
	exchange, err := model.NewExchange(model.ExchangeParams{
		Header: e.header("rows-exchange-001", nov(6)), FromAccount: e.bank, ToAccount: e.cash,
		FromInstrument: e.twd, ToInstrument: e.usd, FromAmount: dec("3200"), ToAmount: dec("100"),
	})
	if err != nil {
		t.Fatalf("NewExchange: %v", err)
	}
	e.store(exchange)

	wantRows(t, "the drafts", e.draftRows(),
		"2026-11-01 expense [account(cash) TWD -120]",
		"2026-11-02 expense []",
		"2026-11-03 transfer [account(bank) TWD -1000, account(cash) TWD 1000, account(card) TWD -15]",
		"2026-11-04 income [account(bank) TWD 50000]",
		"2026-11-05 dealing [account(bank) TWD -5000]",
		"2026-11-06 exchange [account(bank) TWD -3200, account(cash) USD 100]",
	)
}

func TestDraftTransactionsOfAPlanAreItsOriginAndEveryPeriodWithTheirPlaceInIt(t *testing.T) {
	e := newLedgerEnv(t)
	purchase := e.storePlan(e.planOf("rows-plan-00000001"))
	bill := e.storePlan(e.billInstalmentOf("rows-bill-00000001"))
	e.confirmedPlan("rows-plan-confirmed") // its transactions are not drafts

	// The origin of a purchase paid in instalments has a posting to the card of the
	// instalment book alone. That of a bill pays it off with one of the account book, and
	// owes it again with one of the instalment book. Each period charges the account: the
	// principal stops being unbilled, and the account pays it, with the interest.
	want := []string{
		"2026-11-14 expense plan 0 [instalment(card) TWD -12000]",
		"2026-11-16 instalment plan 0 [account(bank) TWD 3000, instalment(bank) TWD -3000]",
		"2026-11-20 instalment plan 1 [instalment(bank) TWD 1000, account(bank) TWD -1000]",
		"2026-12-20 instalment plan 2 [instalment(bank) TWD 1000, account(bank) TWD -1000]",
		"2027-01-20 instalment plan 3 [instalment(bank) TWD 1000, account(bank) TWD -1000]",
		"2026-12-05 instalment plan 1 [instalment(card) TWD 1000, account(card) TWD -1030]",
	}
	for i := 1; i < 12; i++ {
		want = append(want, fmt.Sprintf("%s instalment plan %d [instalment(card) TWD 1000, account(card) TWD -1000]",
			on(2026, time.Month(12+i), 5).Format(time.DateOnly), i+1))
	}
	slices.Sort(want)
	wantRows(t, "the drafts", e.draftRows(), want...)

	// Each transaction says which plan it is part of, and where: the origin is the first, the
	// periods follow, each of the plan of its origin.
	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		t.Fatalf("DraftTransactions: %v", err)
	}
	byID := make(map[uuid.UUID]model.DraftTransaction, len(rows))
	for _, txn := range rows {
		byID[txn.ID] = txn
	}
	for _, plan := range []model.InstalmentPlan{purchase, bill} {
		for _, txn := range plan.Transactions() {
			got, found := byID[txn.ID]
			switch {
			case !found:
				t.Errorf("transaction %s of plan %s, position %d, is not among the drafts", txn.ID, plan.Origin.ID, *txn.PlanSeq)
			case got.PlanID == nil || *got.PlanID != plan.Origin.ID || got.PlanSeq == nil || *got.PlanSeq != *txn.PlanSeq:
				t.Errorf("transaction %s is of plan %v, position %v, want plan %s, position %d", txn.ID, got.PlanID, got.PlanSeq, plan.Origin.ID, *txn.PlanSeq)
			}
		}
	}

	// Confirmed with its origin, the whole of a plan leaves the drafts.
	e.setPlanStatus(purchase, model.StatusConfirmed)
	if got := e.draftRows(); len(got) != 4 {
		t.Errorf("%d rows once the purchase is confirmed, want the 4 of the bill: %q", len(got), got)
	}
}

// listed lists the transactions the filter matches, in the order they come, and fails the
// test when it cannot.
func (e *ledgerEnv) listed(f repo.TransactionFilter) []model.Transaction {
	e.t.Helper()
	if f.Limit == 0 {
		f.Limit = 500
	}
	got, err := e.txns.List(context.Background(), f)
	if err != nil {
		e.t.Fatalf("List(%+v): %v", f, err)
	}
	return got
}

func TestListWithoutAccountListsTheTransactionsThatHaveNoPostingToAnAccountOfAnyBook(t *testing.T) {
	e := newLedgerEnv(t)
	paid := e.draftPaidByParty("list-paid-0000001", nov(2), "90")
	paidAndConfirmed := e.setStatus(e.draftPaidByParty("list-paid-0000002", nov(3), "40").ID, model.StatusConfirmed)
	e.draftExpense("list-lunch-000001", nov(4), e.cash, "120")
	e.storePlan(e.planOf("list-plan-0000001")) // its origin posts to the card, of the instalment book
	e.store(e.dealingOf("list-dealing-0001", nov(5), e.partyA, e.twd, "5000", model.DealingPaidOut))

	// What has no posting to an account, a draft or not, and nothing else: not what has one
	// of the instalment book only, such as the origin of a plan.
	got := e.listed(repo.TransactionFilter{WithoutAccount: true})
	if want := []uuid.UUID{paidAndConfirmed.ID, paid.ID}; !slices.Equal(idsInOrder(got), want) {
		t.Errorf("no account = %v, want the two expenses a party paid, newest first: %v", idsInOrder(got), want)
	}
	got = e.listed(repo.TransactionFilter{WithoutAccount: true, Status: model.StatusDraft})
	if want := []uuid.UUID{paid.ID}; !slices.Equal(idsInOrder(got), want) {
		t.Errorf("no account, of the drafts = %v, want the draft one: %v", idsInOrder(got), want)
	}
	from, to := nov(3), nov(3)
	got = e.listed(repo.TransactionFilter{WithoutAccount: true, From: &from, To: &to})
	if want := []uuid.UUID{paidAndConfirmed.ID}; !slices.Equal(idsInOrder(got), want) {
		t.Errorf("no account, on the 3rd = %v, want the one of that day: %v", idsInOrder(got), want)
	}
}

// idsInOrder returns the ids of the transactions, in the order they are in.
func idsInOrder(txns []model.Transaction) []uuid.UUID {
	ids := make([]uuid.UUID, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
	}
	return ids
}

func TestListByAnAccountMatchesAPostingOfAnyBookAndSoDoesTheOriginOfAPlanOfTheCard(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.storePlan(e.planOf("list-plan-0000001"))
	bill := e.storePlan(e.billInstalmentOf("list-bill-0000001"))
	e.draftExpense("list-lunch-000001", nov(4), e.cash, "120")

	// The origin of the purchase paid in instalments, which has a posting to the card of the
	// instalment book, is listed for the card, and so are its periods; and the conversion of a
	// bill is of the account that owes the bill, as are its periods.
	forCard := e.listed(repo.TransactionFilter{AccountID: &e.card.ID})
	if want := idsOf(plan.Transactions()); !slices.Equal(idsOf(forCard), want) {
		t.Errorf("the card = %d transactions, want the origin and the 12 periods of the plan, %d", len(forCard), len(want))
	}
	forBank := e.listed(repo.TransactionFilter{AccountID: &e.bank.ID})
	if want := idsOf(bill.Transactions()); !slices.Equal(idsOf(forBank), want) {
		t.Errorf("the bank = %d transactions, want the origin and the 3 periods of the bill, %d", len(forBank), len(want))
	}
}

func TestListOfDraftsWithoutPlanPeriodsLeavesTheirPeriodsToTheListOfThePlan(t *testing.T) {
	e := newLedgerEnv(t)
	lunch := e.draftExpense("list-lunch-000001", nov(4), e.cash, "120")
	plan := e.storePlan(e.planOf("list-plan-0000001"))
	bill := e.storePlan(e.billInstalmentOf("list-bill-0000001"))
	confirmed := e.confirmedPlan("list-plan-confirmed")

	// Without the periods, a plan is its origin: what the summary counts.
	got := e.listed(repo.TransactionFilter{Status: model.StatusDraft, WithoutPlanPeriods: true})
	if want := idsOf([]model.Transaction{lunch, plan.Origin, bill.Origin}); !slices.Equal(idsOf(got), want) {
		t.Errorf("the drafts without the periods of plans = %d transactions, want the lunch and the two origins: %d", len(got), len(want))
	}
	// Asked for by its plan, with the status draft too, the periods are listed with the origin.
	got = e.listed(repo.TransactionFilter{Status: model.StatusDraft, PlanID: &plan.Origin.ID})
	if want := idsOf(plan.Transactions()); !slices.Equal(idsOf(got), want) {
		t.Errorf("the drafts of the plan = %d transactions, want its origin and 12 periods: %d", len(got), len(want))
	}
	// Not asked to leave them out, the drafts are all that have the status.
	got = e.listed(repo.TransactionFilter{Status: model.StatusDraft})
	if want := 1 + 13 + 4; len(got) != want {
		t.Errorf("the drafts = %d transactions, want the lunch, the 13 of the purchase and the 4 of the bill: %d", len(got), want)
	}
	// What is left out is the periods, and no other transaction: not a transaction that is
	// confirmed with the periods of its plan, whose status is not the one asked for.
	got = e.listed(repo.TransactionFilter{Status: model.StatusConfirmed, WithoutPlanPeriods: true})
	if want := idsOf([]model.Transaction{confirmed.Origin}); !slices.Equal(idsOf(got), want) {
		t.Errorf("the confirmed without the periods = %d transactions, want the origin of the confirmed plan: %d", len(got), len(want))
	}
}

func TestEveryCountOfTheSummaryIsTheNumberOfDraftsTheListFindsForItsMonthAndAccount(t *testing.T) {
	e := newLedgerEnv(t)
	// A draft of each of the ten kinds, a purchase in instalments and the conversion of a bill, and
	// some more: an expense that a party paid, which no account is of, a transfer with a fee paid
	// from a third account, and an expense in October and one in December.
	e.storedOfEveryKind()
	e.draftPaidByParty("agree-paid-0000001", nov(9), "90")
	e.draftTransfer("agree-transfer-001", nov(10), e.bank, e.cash, e.twd, "1000",
		model.Fee{Account: e.card, Instrument: e.twd, Category: e.fees, Amount: dec("15")})
	e.draftExpense("agree-oct-00000001", on(2026, time.October, 30), e.cash, "80")
	e.draftExpense("agree-dec-00000001", on(2026, time.December, 20), e.card, "60")

	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		t.Fatalf("DraftTransactions: %v", err)
	}
	summary, err := model.SummarizeDrafts(rows, forever)
	if err != nil {
		t.Fatalf("SummarizeDrafts: %v", err)
	}

	// listedCount is how many drafts are listed for the month, of the account or of none.
	listedCount := func(month time.Time, account *uuid.UUID) int {
		first, last := month, model.LastDayOf(month)
		return len(e.listed(repo.TransactionFilter{
			Status: model.StatusDraft, WithoutPlanPeriods: true, From: &first, To: &last, AccountID: account, WithoutAccount: account == nil,
		}))
	}
	checked := 0
	for _, month := range summary.Months {
		if got := len(e.listed(repo.TransactionFilter{
			Status: model.StatusDraft, WithoutPlanPeriods: true, From: &month.Month, To: new(model.LastDayOf(month.Month)),
		})); got != month.Count {
			t.Errorf("%s: the summary counts %d drafts, and the list has %d", month.Month.Format("2006-01"), month.Count, got)
		}
		for _, account := range month.Accounts {
			name := "none"
			if account.AccountID != nil {
				name = e.names()[*account.AccountID]
			}
			if got := listedCount(month.Month, account.AccountID); got != account.Count {
				t.Errorf("%s, %s: the summary counts %d drafts, and the list has %d", month.Month.Format("2006-01"), name, account.Count, got)
			}
			checked++
		}
	}
	// The drafts are in October, November and December, and November has all that the others
	// have not: the groups of the accounts that something is paid from or into, and of none.
	if len(summary.Months) != 3 || checked != 1+4+1 {
		t.Errorf("%d months and %d groups of a month and an account checked, want 3 and 6", len(summary.Months), checked)
	}
	if all := e.listed(repo.TransactionFilter{Status: model.StatusDraft, WithoutPlanPeriods: true}); len(all) != summary.Count {
		t.Errorf("the summary counts %d drafts, and the list of the drafts has %d", summary.Count, len(all))
	}
}
