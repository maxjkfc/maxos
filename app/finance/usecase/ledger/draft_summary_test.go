package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// draftSummary reads the summary of the drafts, and fails the test when it cannot.
func (e *env) draftSummary() usecase.GetDraftSummaryOutput {
	e.t.Helper()
	out, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil {
		e.t.Fatalf("GetDraftSummary: %v", err)
	}
	return out
}

// amountsText writes amounts as "TWD out 120 in 0 net -120", one for each instrument,
// in the order they come.
func (e *env) amountsText(amounts []usecase.DraftAmount) string {
	names := e.names()
	parts := make([]string, len(amounts))
	for i, a := range amounts {
		parts[i] = fmt.Sprintf("%s out %s in %s net %s", names[a.InstrumentID], a.Outflow, a.Inflow, a.Net)
	}
	return strings.Join(parts, ", ")
}

// monthLines writes a month of the summary: a line for the month, with how many drafts
// happened in it and what they would move, and under it a line for each account in the
// order they come, the drafts that have no account as "(none)".
func (e *env) monthLines(m usecase.DraftMonth) []string {
	names := e.names()
	lines := []string{fmt.Sprintf("%s: %d [%s]", m.Month.Format("2006-01"), m.Count, e.amountsText(m.Amounts))}
	for _, a := range m.Accounts {
		name := "(none)"
		if a.AccountID != nil {
			name = names[*a.AccountID]
		}
		lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, e.amountsText(a.Amounts)))
	}
	return lines
}

// summaryLines writes the whole summary: a line for all the drafts, and then the lines of
// each month, the newest first.
func (e *env) summaryLines(out usecase.GetDraftSummaryOutput) []string {
	lines := []string{fmt.Sprintf("all: %d [%s]", out.Count, e.amountsText(out.Amounts))}
	for _, m := range out.Months {
		lines = append(lines, e.monthLines(m)...)
	}
	return lines
}

// detailsOn is the details of a record of the key, of a day other than that of the tests.
func (e *env) detailsOn(key string, d time.Time) usecase.RecordDetails {
	return usecase.RecordDetails{IdempotencyKey: key, OccurredOn: d}
}

// expenseDraft records an expense of the amount of TWD on food, paid from the account.
func (e *env) expenseDraft(key string, d time.Time, account model.Account, amount string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: e.detailsOn(key, d), AccountID: &account.ID, InstrumentID: e.twd.ID,
		Lines: []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return out.Transaction
}

// dollarExpenseDraft records an expense of the amount of USD on food, paid from the
// account, that was the amount given in TWD that day.
func (e *env) dollarExpenseDraft(key string, d time.Time, account model.Account, amount, base string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: e.detailsOn(key, d), AccountID: &account.ID, InstrumentID: e.usd.ID,
		Lines: []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec(amount), BaseAmount: decPtr(base)}},
	})
	if err != nil {
		e.t.Fatalf("RecordExpense(%q) in dollars: %v", key, err)
	}
	return out.Transaction
}

// paidByPartyDraft records an expense of the amount of TWD on food that a party paid
// for me, which has no posting to an account.
func (e *env) paidByPartyDraft(key string, d time.Time, amount string) usecase.Transaction {
	e.t.Helper()
	friend := e.refs.addParty("Amy "+key, false)
	out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: e.detailsOn(key, d), PaidByPartyID: &friend, InstrumentID: e.twd.ID,
		Lines: []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("RecordExpense(%q) paid by a party: %v", key, err)
	}
	return out.Transaction
}

// transferDraft records a transfer of the amount of the instrument between two accounts,
// with the fees given.
func (e *env) transferDraft(key string, d time.Time, from, to model.Account, instrument model.Instrument, amount string, fees ...usecase.Fee) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: e.detailsOn(key, d), FromAccountID: from.ID, ToAccountID: to.ID, InstrumentID: instrument.ID,
		Amount: dec(amount), Fees: fees,
	})
	if err != nil {
		e.t.Fatalf("RecordTransfer(%q): %v", key, err)
	}
	return out.Transaction
}

func TestGetDraftSummaryGroupsTheDraftsByMonthAndAccountAndWorksOutTheNetAndTheSums(t *testing.T) {
	e := newEnv(t)
	// Some of each: an expense, an expense in dollars, an income, a transfer between two
	// accounts, an expense a month earlier, and one dated after today, the 14th of
	// November, which is waiting like any other.
	e.expenseDraft("summary-lunch-01", on(2026, time.November, 14), e.cash, "120")
	e.dollarExpenseDraft("summary-usd-001", on(2026, time.November, 20), e.bank, "100", "3200")
	if _, err := e.svc.RecordIncome(context.Background(), usecase.RecordIncomeInput{
		RecordDetails: e.detailsOn("summary-pay-0001", on(2026, time.November, 3)), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
		Lines: []usecase.CategoryLine{{CategoryID: e.salary.ID, Amount: dec("50000")}},
	}); err != nil {
		t.Fatalf("RecordIncome: %v", err)
	}
	e.transferDraft("summary-move-001", on(2026, time.November, 3), e.bank, e.cash, e.twd, "1000")
	e.expenseDraft("summary-old-0001", on(2026, time.October, 30), e.cash, "80")
	e.expenseDraft("summary-late-001", on(2026, time.December, 20), e.card, "60")

	opened := e.tx.calls // by recording the drafts
	got := e.draftSummary()

	wantLines(t, e.summaryLines(got),
		"all: 6 [TWD out 1260 in 51000 net 49740, USD out 100 in 0 net -100]",
		"2026-12: 1 [TWD out 60 in 0 net -60]",
		"  card: 1 [TWD out 60 in 0 net -60]",
		"2026-11: 4 [TWD out 1120 in 51000 net 49880, USD out 100 in 0 net -100]",
		"  bank: 3 [TWD out 1000 in 50000 net 49000, USD out 100 in 0 net -100]",
		"  cash: 2 [TWD out 120 in 1000 net 880]",
		"2026-10: 1 [TWD out 80 in 0 net -80]",
		"  cash: 1 [TWD out 80 in 0 net -80]",
	)
	for _, m := range got.Months {
		if m.Month.Day() != 1 || m.Month.Location() != time.UTC {
			t.Errorf("month %s is not the first day of a month at midnight UTC", m.Month)
		}
	}
	// The use case opens no database transaction: the repo reads in a snapshot of its own.
	if e.tx.calls != opened || e.txns.draftSummaries != 1 {
		t.Errorf("%d database transactions opened and the repo asked %d times, want 0 and 1", e.tx.calls-opened, e.txns.draftSummaries)
	}
}

func TestADraftIsCountedOnceInItsMonthAndOnceInEachAccountItMoves(t *testing.T) {
	fee := func(e *env, from model.Account) usecase.Fee {
		return usecase.Fee{AccountID: from.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")}
	}
	tests := []struct {
		name string
		make func(e *env)
		want []string
	}{
		{
			name: "a transfer is a draft of both accounts, and one of the month",
			make: func(e *env) {
				e.transferDraft("transfer-0000001", on(2026, time.November, 5), e.bank, e.cash, e.twd, "1000")
			},
			want: []string{
				"all: 1 [TWD out 1000 in 1000 net 0]",
				"2026-11: 1 [TWD out 1000 in 1000 net 0]",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "a fee paid from the account the money leaves is not a second draft of that account",
			make: func(e *env) {
				e.transferDraft("transfer-0000002", on(2026, time.November, 5), e.bank, e.cash, e.twd, "1000", fee(e, e.bank))
			},
			want: []string{
				"all: 1 [TWD out 1015 in 1000 net -15]",
				"2026-11: 1 [TWD out 1015 in 1000 net -15]",
				"  bank: 1 [TWD out 1015 in 0 net -1015]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "a fee paid from a third account makes the draft one of that one too",
			make: func(e *env) {
				e.transferDraft("transfer-0000003", on(2026, time.November, 5), e.bank, e.cash, e.twd, "1000", fee(e, e.card))
			},
			want: []string{
				"all: 1 [TWD out 1015 in 1000 net -15]",
				"2026-11: 1 [TWD out 1015 in 1000 net -15]",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  card: 1 [TWD out 15 in 0 net -15]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "two drafts of one account are two of it",
			make: func(e *env) {
				e.expenseDraft("expense-00000001", on(2026, time.November, 5), e.cash, "120")
				e.expenseDraft("expense-00000002", on(2026, time.November, 6), e.cash, "30")
			},
			want: []string{
				"all: 2 [TWD out 150 in 0 net -150]",
				"2026-11: 2 [TWD out 150 in 0 net -150]",
				"  cash: 2 [TWD out 150 in 0 net -150]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			tt.make(e)

			wantLines(t, e.summaryLines(e.draftSummary()), tt.want...)
		})
	}
}

func TestTheDraftsThatMoveNoAccountAreAGroupOfTheirMonthThatComesLastAndHasNoAmounts(t *testing.T) {
	e := newEnv(t)
	// An expense a party paid for me has a posting to that party and none to an account;
	// the origin of a purchase paid in instalments has a posting of the instalment book,
	// which is no account posting; its periods are charged to the account, on the days
	// they fall due.
	e.paidByPartyDraft("friend-paid-0001", on(2026, time.November, 8), "90")
	e.recordPlan("laptop-00000001")
	e.expenseDraft("expense-00000001", on(2026, time.November, 9), e.cash, "120")

	got := e.draftSummary()

	// The origin is dated the 14th of November, and the periods fall due on the 5th of
	// each month from December: 15 drafts in 13 months, 3 of them in November.
	if got.Count != 15 || len(got.Months) != 13 {
		t.Fatalf("count %d in %d months, want 15 in 13", got.Count, len(got.Months))
	}
	wantLines(t, e.monthLines(got.Months[12]),
		"2026-11: 3 [TWD out 120 in 0 net -120]",
		"  cash: 1 [TWD out 120 in 0 net -120]",
		"  (none): 2 []", // the expense the party paid, and the origin of the plan
	)
	if none := got.Months[12].Accounts[1]; none.AccountID != nil || len(none.Amounts) != 0 {
		t.Errorf("the group of no account = %+v, want no account and no amount", none)
	}
	// A period moves the account the plan charges: the first has the interest, and the
	// principal that is taken out of what is unbilled is no account posting.
	wantLines(t, e.monthLines(got.Months[11]), "2026-12: 1 [TWD out 1030 in 0 net -1030]", "  bank: 1 [TWD out 1030 in 0 net -1030]")
	wantLines(t, e.monthLines(got.Months[0]), "2027-11: 1 [TWD out 1000 in 0 net -1000]", "  bank: 1 [TWD out 1000 in 0 net -1000]")
	// The amounts of all of it are those of the account postings: the 12,030 the periods
	// charge and the 120 of the expense, and nothing of the 12,000 of the origin.
	wantLines(t, []string{e.amountsText(got.Amounts)}, "TWD out 12150 in 0 net -12150")
}

func TestTheConversionOfABillIntoInstalmentsIsADraftOfTheAccountThatOwesTheBill(t *testing.T) {
	e := newEnv(t)
	out, err := e.svc.RecordBillInstalment(context.Background(), usecase.RecordBillInstalmentInput{
		RecordDetails: e.details("bill-instalment-1"), AccountID: e.card.ID, InstrumentID: e.twd.ID, Principal: dec("3000"), Schedule: e.schedule(3, "1000"),
	})
	if err != nil {
		t.Fatalf("RecordBillInstalment: %v", err)
	}
	if origin := e.txns.byID[out.Transaction.ID]; origin.Kind != model.KindInstalment {
		t.Fatalf("the origin is a %s, want a bill instalment", origin.Kind)
	}

	got := e.draftSummary()

	// Unlike the origin of a purchase paid in instalments, this origin has an account
	// posting: it pays off what the card owed, and each period charges it again, in the
	// month it falls due.
	if len(got.Months) != 4 || got.Count != 4 {
		t.Fatalf("%d drafts in %d months, want 4 in 4: the origin and 3 periods", got.Count, len(got.Months))
	}
	wantLines(t, e.monthLines(got.Months[3]), "2026-11: 1 [TWD out 0 in 3000 net 3000]", "  card: 1 [TWD out 0 in 3000 net 3000]")
	wantLines(t, e.monthLines(got.Months[2]), "2026-12: 1 [TWD out 1000 in 0 net -1000]", "  card: 1 [TWD out 1000 in 0 net -1000]")
}

func TestAConfirmedTransactionIsNoDraftAndConfirmingOrDiscardingOrVoidingTakesADraftOut(t *testing.T) {
	e := newEnv(t)
	keep := e.expenseDraft("expense-00000001", on(2026, time.November, 5), e.cash, "1")
	confirm := e.expenseDraft("expense-00000002", on(2026, time.November, 6), e.cash, "10")
	discard := e.expenseDraft("expense-00000003", on(2026, time.November, 7), e.cash, "100")
	void := e.expenseDraft("expense-00000004", on(2026, time.November, 8), e.cash, "1000")
	wantLines(t, e.summaryLines(e.draftSummary()),
		"all: 4 [TWD out 1111 in 0 net -1111]",
		"2026-11: 4 [TWD out 1111 in 0 net -1111]",
		"  cash: 4 [TWD out 1111 in 0 net -1111]",
	)

	e.confirm(confirm)
	wantLines(t, e.summaryLines(e.draftSummary()),
		"all: 3 [TWD out 1101 in 0 net -1101]",
		"2026-11: 3 [TWD out 1101 in 0 net -1101]",
		"  cash: 3 [TWD out 1101 in 0 net -1101]",
	)

	if err := e.svc.DiscardDraft(context.Background(), discard.ID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}
	e.confirm(void)
	if _, err := e.svc.VoidTransaction(context.Background(), void.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	wantLines(t, e.summaryLines(e.draftSummary()),
		"all: 1 [TWD out 1 in 0 net -1]",
		"2026-11: 1 [TWD out 1 in 0 net -1]",
		"  cash: 1 [TWD out 1 in 0 net -1]",
	)

	// The last one confirmed: nothing is waiting, and a month that has no draft is not listed.
	e.confirm(keep)
	if got := e.draftSummary(); got.Count != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("summary = %+v, want nothing: no draft is waiting", got)
	}
}

func TestGetDraftSummaryOfALedgerWithNoDraftIsEmptyAndNotAnError(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil || got.Count != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("GetDraftSummary = %+v, %v, want a count of 0 and no month, with no error", got, err)
	}

	// Nor does a ledger that has only confirmed transactions have anything waiting.
	e.confirmedLunch("lunch-00000001")
	e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")
	if got, err = e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{}); err != nil || got.Count != 0 || len(got.Months) != 0 {
		t.Errorf("GetDraftSummary of confirmed transactions only = %+v, %v, want nothing", got, err)
	}
}

func TestTheDraftSummaryIsInTheOrderOfMonthsAccountNamesAndInstrumentCodes(t *testing.T) {
	e := newEnv(t)
	// The ids of the accounts are in the order they were made, cash first, and those of the
	// instruments are the base currency's, which is the least, and then USD and BTC: neither
	// is the order of the names and the codes.
	e.transferDraft("move-btc-000001", on(2026, time.November, 5), e.cash, e.bank, e.btc, "0.1")
	e.dollarExpenseDraft("card-usd-000001", on(2026, time.November, 6), e.card, "20", "640")
	e.expenseDraft("cash-twd-000001", on(2026, time.November, 7), e.cash, "120")
	e.paidByPartyDraft("friend-paid-0001", on(2026, time.November, 8), "90")
	e.expenseDraft("bank-twd-000001", on(2027, time.January, 2), e.bank, "5")
	e.expenseDraft("bank-twd-000002", on(2025, time.December, 31), e.bank, "6")

	got := e.draftSummary()

	wantLines(t, e.summaryLines(got),
		"all: 6 [BTC out 0.1 in 0.1 net 0, TWD out 131 in 0 net -131, USD out 20 in 0 net -20]",
		"2027-01: 1 [TWD out 5 in 0 net -5]",
		"  bank: 1 [TWD out 5 in 0 net -5]",
		"2026-11: 4 [BTC out 0.1 in 0.1 net 0, TWD out 120 in 0 net -120, USD out 20 in 0 net -20]",
		"  bank: 1 [BTC out 0 in 0.1 net 0.1]",
		"  card: 1 [USD out 20 in 0 net -20]",
		"  cash: 2 [BTC out 0.1 in 0 net -0.1, TWD out 120 in 0 net -120]",
		"  (none): 1 []",
		"2025-12: 1 [TWD out 6 in 0 net -6]",
		"  bank: 1 [TWD out 6 in 0 net -6]",
	)
	// By name and not by id: the bank was made after the cash.
	if first := got.Months[1].Accounts[0].AccountID; first == nil || *first != e.bank.ID {
		t.Errorf("the first account of November is %v, want the bank, which is first by name", first)
	}
}

func TestWhenTheRepoCannotReadTheDraftsTheErrorGoesOn(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("the database is down")
	e.txns.readErr = boom

	_, err := e.svc.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "draft summary") {
		t.Errorf("err = %v, want what the repo returned, with what was being done", err)
	}
}

func TestTheSumsOfAMonthAndOfAllTheDraftsAreWhatTheAccountsAndTheMonthsAddUpTo(t *testing.T) {
	e := newEnv(t)
	e.transferDraft("move-twd-000001", on(2026, time.November, 5), e.bank, e.cash, e.twd, "700")
	e.transferDraft("move-twd-000002", on(2026, time.December, 5), e.cash, e.card, e.twd, "300")
	e.expenseDraft("card-twd-000001", on(2026, time.December, 6), e.card, "50")

	got := e.draftSummary()

	wantLines(t, e.summaryLines(got),
		"all: 3 [TWD out 1050 in 1000 net -50]",
		"2026-12: 2 [TWD out 350 in 300 net -50]",
		"  card: 2 [TWD out 50 in 300 net 250]",
		"  cash: 1 [TWD out 300 in 0 net -300]",
		"2026-11: 1 [TWD out 700 in 700 net 0]",
		"  bank: 1 [TWD out 700 in 0 net -700]",
		"  cash: 1 [TWD out 0 in 700 net 700]",
	)
	// The net of every amount at every level is what came in less what went out.
	check := func(where string, amounts []usecase.DraftAmount) {
		for _, a := range amounts {
			if want := a.Inflow.Sub(a.Outflow); !a.Net.Equal(want) {
				t.Errorf("%s: net %s, want inflow less outflow, %s", where, a.Net, want)
			}
		}
	}
	check("all", got.Amounts)
	for _, m := range got.Months {
		check(m.Month.Format("2006-01"), m.Amounts)
		for _, a := range m.Accounts {
			check(m.Month.Format("2006-01")+" account", a.Amounts)
		}
	}
}
