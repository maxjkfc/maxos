package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// draftSummary reads the summary of the drafts, and fails the test when it cannot.
func (e *ledgerEnv) draftSummary() model.DraftSummary {
	e.t.Helper()
	got, err := e.txns.DraftSummary(context.Background())
	if err != nil {
		e.t.Fatalf("DraftSummary: %v", err)
	}
	return got
}

// draftAmounts writes amounts as "TWD out 120 in 0 net -120", one for each instrument, in
// the order they come. The code of each is checked to be that of its instrument.
func (e *ledgerEnv) draftAmounts(amounts []model.DraftAmount) string {
	names := e.names()
	parts := make([]string, len(amounts))
	for i, a := range amounts {
		if a.InstrumentCode != names[a.InstrumentID] {
			e.t.Errorf("an amount of the instrument %s has the code %q, want %q", a.InstrumentID, a.InstrumentCode, names[a.InstrumentID])
		}
		parts[i] = fmt.Sprintf("%s out %s in %s net %s", a.InstrumentCode, a.Outflow, a.Inflow, a.Net())
	}
	return strings.Join(parts, ", ")
}

// draftLines writes the summary: a line for how many drafts there are, and then for each
// month a line with how many happened in it, and under it a line for each account in the
// order they come, with how many of the month's drafts move it and what they move, the
// drafts that move none as "(none)".
func (e *ledgerEnv) draftLines(s model.DraftSummary) []string {
	names := e.names()
	lines := []string{fmt.Sprintf("all: %d", s.Count())}
	for _, m := range s.Months {
		lines = append(lines, fmt.Sprintf("%s: %d", m.Month.Format("2006-01"), m.Count))
		for _, a := range m.Accounts {
			name := "(none)"
			if a.AccountID != nil {
				name = names[*a.AccountID]
			}
			lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, e.draftAmounts(a.Amounts)))
		}
	}
	return lines
}

// wantSummary fails the test unless the lines are the ones wanted, in that order: the
// order of the summary is part of what it promises.
func wantSummary(t *testing.T, what string, got []string, want ...string) {
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

func TestDraftSummaryHasTheDraftsOnlyByMonthNewestFirstAndALaterStatusTakesADraftOut(t *testing.T) {
	e := newLedgerEnv(t)
	e.draftExpense("summary-oct-0001", on(2026, time.October, 30), e.cash, "80")
	nov14 := e.draftExpense("summary-nov-0001", on(2026, time.November, 14), e.cash, "120")
	nov20 := e.draftExpense("summary-nov-0002", on(2026, time.November, 20), e.cash, "30")
	dec20 := e.draftExpense("summary-dec-0001", on(2026, time.December, 20), e.card, "60") // dated in the future: still waiting
	// A confirmed transaction and a void one are not drafts, whatever day they are of.
	e.storeConfirmed(e.expenseOf(e.header("summary-conf-001", nov(5)), "1000"))
	voided := e.storeConfirmed(e.expenseOf(e.header("summary-void-001", nov(6)), "2000"))
	e.setStatus(voided.ID, model.StatusVoid)

	wantSummary(t, "the drafts", e.draftLines(e.draftSummary()),
		"all: 4",
		"2026-12: 1",
		"  card: 1 [TWD out 60 in 0 net -60]",
		"2026-11: 2",
		"  cash: 2 [TWD out 150 in 0 net -150]",
		"2026-10: 1",
		"  cash: 1 [TWD out 80 in 0 net -80]",
	)

	// Confirming a draft takes it out of every number, and the last draft of a month takes
	// the month with it.
	e.setStatus(nov14.ID, model.StatusConfirmed)
	wantSummary(t, "with one of November confirmed", e.draftLines(e.draftSummary()),
		"all: 3",
		"2026-12: 1",
		"  card: 1 [TWD out 60 in 0 net -60]",
		"2026-11: 1",
		"  cash: 1 [TWD out 30 in 0 net -30]",
		"2026-10: 1",
		"  cash: 1 [TWD out 80 in 0 net -80]",
	)
	e.setStatus(nov20.ID, model.StatusConfirmed)
	wantSummary(t, "with November confirmed", e.draftLines(e.draftSummary()),
		"all: 2",
		"2026-12: 1",
		"  card: 1 [TWD out 60 in 0 net -60]",
		"2026-10: 1",
		"  cash: 1 [TWD out 80 in 0 net -80]",
	)

	// Discarding a draft deletes it.
	if err := e.inTx(func(ctx context.Context) error { return e.txns.Delete(ctx, dec20.ID) }); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	wantSummary(t, "with December discarded", e.draftLines(e.draftSummary()),
		"all: 1",
		"2026-10: 1",
		"  cash: 1 [TWD out 80 in 0 net -80]",
	)
}

func TestDraftSummaryMonthIsTheMonthOfTheDayADraftHappenedOnAcrossAYear(t *testing.T) {
	e := newLedgerEnv(t)
	for i, day := range []time.Time{
		on(2026, time.October, 31), on(2026, time.November, 1), on(2026, time.November, 30), on(2026, time.December, 1),
		on(2026, time.December, 31), on(2027, time.January, 1),
	} {
		e.draftExpense(fmt.Sprintf("summary-day-%04d", i), day, e.cash, "10")
	}

	got := e.draftSummary()

	wantSummary(t, "the months", e.draftLines(got),
		"all: 6",
		"2027-01: 1",
		"  cash: 1 [TWD out 10 in 0 net -10]",
		"2026-12: 2",
		"  cash: 2 [TWD out 20 in 0 net -20]",
		"2026-11: 2",
		"  cash: 2 [TWD out 20 in 0 net -20]",
		"2026-10: 1",
		"  cash: 1 [TWD out 10 in 0 net -10]",
	)
	for _, m := range got.Months {
		if m.Month.Day() != 1 || m.Month.Location() != time.UTC {
			t.Errorf("month %s is not the first day of a month at midnight UTC", m.Month)
		}
	}
}

func TestATransferIsADraftOfBothAccountsAndOneOfTheMonthAndAFeeDoesNotMakeASecondDraftOfAnAccount(t *testing.T) {
	fee := func(e *ledgerEnv, from model.Account) model.Fee {
		return model.Fee{Account: from, Instrument: e.twd, Category: e.fees, Amount: dec("15")}
	}
	tests := []struct {
		name string
		make func(e *ledgerEnv)
		want []string
	}{
		{
			name: "a transfer between two accounts",
			make: func(e *ledgerEnv) { e.draftTransfer("transfer-0000001", nov(5), e.bank, e.cash, e.twd, "1000") },
			want: []string{
				"all: 1",
				"2026-11: 1",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "a fee paid from the account the money leaves: two account postings to it, one draft",
			make: func(e *ledgerEnv) {
				e.draftTransfer("transfer-0000002", nov(5), e.bank, e.cash, e.twd, "1000", fee(e, e.bank))
			},
			want: []string{
				"all: 1",
				"2026-11: 1",
				"  bank: 1 [TWD out 1015 in 0 net -1015]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "a fee paid from a third account, which the draft moves too",
			make: func(e *ledgerEnv) {
				e.draftTransfer("transfer-0000003", nov(5), e.bank, e.cash, e.twd, "1000", fee(e, e.card))
			},
			want: []string{
				"all: 1",
				"2026-11: 1",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  card: 1 [TWD out 15 in 0 net -15]",
				"  cash: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "two drafts of one account",
			make: func(e *ledgerEnv) {
				e.draftExpense("expense-00000001", nov(5), e.cash, "120")
				e.draftExpense("expense-00000002", nov(6), e.cash, "30")
			},
			want: []string{
				"all: 2",
				"2026-11: 2",
				"  cash: 2 [TWD out 150 in 0 net -150]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newLedgerEnv(t)
			tt.make(e)

			wantSummary(t, "the drafts", e.draftLines(e.draftSummary()), tt.want...)
		})
	}
}

func TestTheDraftsThatHaveNoAccountPostingAreTheGroupOfNoAccountOfTheirMonthAndLast(t *testing.T) {
	e := newLedgerEnv(t)
	// An expense a party paid has a posting to the party and none to an account. The origin of a
	// purchase paid in instalments has one of the instalment book, which is no account posting,
	// and its periods have account postings, charged on the days they fall due. The origin of
	// the conversion of a bill into instalments pays the bill off, with an account posting.
	e.draftPaidByParty("summary-paid-001", nov(8), "90")
	header := e.header("summary-plan-0001", ledgerDay)
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: header, Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("12000")}},
	}, model.InstalmentSchedule{
		Periods: []model.InstalmentPeriod{
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 5), Principal: dec("6000"), Interest: decPtr("30")},
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 5), Principal: dec("6000")},
		},
		InterestCategory: &e.interest,
	})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	e.storePlan(plan)
	e.storePlan(e.billInstalmentOf("summary-bill-0001"))

	wantSummary(t, "the drafts", e.draftLines(e.draftSummary()),
		"all: 8",
		"2027-01: 2",
		"  bank: 1 [TWD out 1000 in 0 net -1000]",
		"  card: 1 [TWD out 6000 in 0 net -6000]",
		"2026-12: 2",
		"  bank: 1 [TWD out 1000 in 0 net -1000]",
		"  card: 1 [TWD out 6030 in 0 net -6030]",
		"2026-11: 4",
		"  bank: 2 [TWD out 1000 in 3000 net 2000]",
		"  (none): 2 []",
	)
}

func TestAnAccountOfADraftSummaryHasAnAmountForEachInstrumentByCodeAndOutflowInflowAndNet(t *testing.T) {
	e := newLedgerEnv(t)
	// The ids of the instruments are the base currency's, which is the least, and then USD and BTC:
	// not the order of the codes. The cash has some of all three.
	e.draftTransfer("move-btc-000001", nov(5), e.cash, e.bank, e.btc, "0.1")
	dollars, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("cash-usd-000001", nov(6)), Account: &e.cash, Instrument: e.usd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("20"), BaseAmount: decPtr("640")}},
	})
	if err != nil {
		t.Fatalf("NewExpense in dollars: %v", err)
	}
	e.store(dollars)
	e.draftExpense("cash-twd-000001", nov(7), e.cash, "120")
	e.draftExpense("cash-twd-000002", nov(8), e.cash, "30")
	// What comes in: an opening balance and an income, both drafts.
	e.store(e.opening("cash-open-00001", e.cash, "1000"))
	income, err := model.NewIncome(model.IncomeParams{
		Header: e.header("cash-income-001", nov(9)), Account: e.cash, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("50")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.store(income)

	wantSummary(t, "the drafts", e.draftLines(e.draftSummary()),
		"all: 6",
		"2026-11: 6",
		"  bank: 1 [BTC out 0 in 0.1 net 0.1]",
		"  cash: 6 [BTC out 0.1 in 0 net -0.1, TWD out 150 in 1050 net 900, USD out 20 in 0 net -20]",
	)
}

func TestTheAccountsOfAMonthAreByNameAndNotByIdAndTheDraftsOfNoAccountComeLast(t *testing.T) {
	e := newLedgerEnv(t)
	// The accounts were made in the order cash, bank, card, so that the order of their ids is not
	// the order of their names.
	e.draftExpense("order-cash-00001", nov(5), e.cash, "1")
	e.draftExpense("order-card-00001", nov(5), e.card, "2")
	e.draftExpense("order-bank-00001", nov(5), e.bank, "3")
	e.draftPaidByParty("order-paid-00001", nov(5), "4")

	wantSummary(t, "the drafts", e.draftLines(e.draftSummary()),
		"all: 4",
		"2026-11: 4",
		"  bank: 1 [TWD out 3 in 0 net -3]",
		"  card: 1 [TWD out 2 in 0 net -2]",
		"  cash: 1 [TWD out 1 in 0 net -1]",
		"  (none): 1 []",
	)
	if got := e.draftSummary().Months[0].Accounts; got[0].AccountID == nil || *got[0].AccountID != e.bank.ID || got[3].AccountID != nil {
		t.Errorf("accounts = %+v, want the bank first, and the group of no account last with no account id", got)
	}
}

func TestDraftSummaryOfALedgerWithNoDraftIsEmptyAndNotAnError(t *testing.T) {
	e := newLedgerEnv(t)

	got, err := e.txns.DraftSummary(context.Background())
	if err != nil || len(got.Months) != 0 || got.Count() != 0 {
		t.Errorf("DraftSummary = %+v, %v, want no month, a count of 0 and no error", got, err)
	}

	// Nor does a ledger that has confirmed transactions only.
	e.storeConfirmed(e.expenseOf(e.header("summary-conf-001", nov(5)), "10"))
	if got, err = e.txns.DraftSummary(context.Background()); err != nil || len(got.Months) != 0 {
		t.Errorf("DraftSummary of confirmed transactions only = %+v, %v, want no month", got, err)
	}
}

// draftSummaryInterruptedBy returns what DraftSummary returns when change is made by another
// connection after its first statement has run and before the next one is sent.
func (e *ledgerEnv) draftSummaryInterruptedBy(change func()) model.DraftSummary {
	e.t.Helper()
	tracer := &afterStatement{prefix: "-- name: ListDraftMonths", hook: change}
	cfg := e.pool.Config()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		e.t.Fatalf("open a pool with a tracer: %v", err)
	}
	defer pool.Close()

	got, err := postgres.NewTransactionRepo(pool).DraftSummary(context.Background())
	if err != nil {
		e.t.Fatalf("DraftSummary: %v", err)
	}
	if !tracer.ran {
		e.t.Fatal("the change was never made: no statement counted the drafts of the months")
	}
	return got
}

// A draft that is confirmed between two of the statements of the summary is in all of them:
// the counts and the amounts are of the same drafts, as the three are read in one snapshot.
// Read apart, the month would say 3 and its account 2.
func TestDraftSummaryIsReadInOneSnapshot(t *testing.T) {
	e := newLedgerEnv(t)
	e.draftExpense("snapshot-draft-01", nov(5), e.cash, "100")
	confirmedMeanwhile := e.draftExpense("snapshot-draft-02", nov(6), e.cash, "10")
	e.draftExpense("snapshot-draft-03", nov(7), e.cash, "1")

	got := e.draftSummaryInterruptedBy(func() { e.setStatus(confirmedMeanwhile.ID, model.StatusConfirmed) })

	wantSummary(t, "the summary as it was when it was read", e.draftLines(got),
		"all: 3",
		"2026-11: 3",
		"  cash: 3 [TWD out 111 in 0 net -111]",
	)
	if now := e.get(confirmedMeanwhile.ID); now.Status != model.StatusConfirmed {
		t.Errorf("the transaction is %s now, want it confirmed by the other connection", now.Status)
	}
	wantSummary(t, "the summary after", e.draftLines(e.draftSummary()),
		"all: 2",
		"2026-11: 2",
		"  cash: 2 [TWD out 101 in 0 net -101]",
	)
}
