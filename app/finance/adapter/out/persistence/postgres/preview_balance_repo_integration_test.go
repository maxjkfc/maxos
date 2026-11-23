package postgres_test

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// A preview balance is what an account would hold once the drafts were confirmed: what it holds
// now and what the drafts add to it. The statement that reads it is one SQL query that adds up
// the postings of the confirmed transactions and of the drafts, by the day; the rules of which
// postings are the drafts' are those of model.SummarizeDrafts, which adds them up too, by month
// and account. These tests show the rows of the query, and hold it to the summary and to the
// balances.

// previewOn reads the preview balances on the day and writes each as a line, such as
// "cash TWD 680 = confirmed 800 + drafts -120", with " unbilled -11000" when something is unbilled,
// in the order the repo gives them.
func (e *ledgerEnv) previewOn(day time.Time) []string {
	e.t.Helper()
	got, err := e.txns.PreviewBalances(context.Background(), day)
	if err != nil {
		e.t.Fatalf("PreviewBalances: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got))
	for i, b := range got {
		lines[i] = fmt.Sprintf("%s %s %s = confirmed %s + drafts %s", names[b.AccountID], names[b.InstrumentID], b.Quantity(), b.Confirmed, b.Drafts)
		if !b.UnbilledInstalment.IsZero() {
			lines[i] += fmt.Sprintf(" unbilled %s", b.UnbilledInstalment)
		}
	}
	return lines
}

// wantPreview fails the test unless the preview balances on the day are the lines wanted.
func (e *ledgerEnv) wantPreview(day time.Time, want ...string) {
	e.t.Helper()
	if got := e.previewOn(day); !slices.Equal(got, want) {
		e.t.Errorf("preview balances on %s:\n got %q\nwant %q", day.Format(time.DateOnly), got, want)
	}
}

// previews reads the preview balances on the day, by account and instrument, and fails the test
// when it cannot.
func (e *ledgerEnv) previews(day time.Time) map[accountInstrument]model.PreviewBalance {
	e.t.Helper()
	got, err := e.txns.PreviewBalances(context.Background(), day)
	if err != nil {
		e.t.Fatalf("PreviewBalances: %v", err)
	}
	byKey := make(map[accountInstrument]model.PreviewBalance, len(got))
	for _, p := range got {
		k := accountInstrument{p.AccountID, p.InstrumentID}
		if _, again := byKey[k]; again {
			e.t.Errorf("%s %s is in the preview balances twice", e.names()[k.account], e.names()[k.instrument])
		}
		byKey[k] = p
	}
	return byKey
}

// accountInstrument is what a balance is of.
type accountInstrument struct{ account, instrument uuid.UUID }

// draftIncome stores the draft of an income of the amount of TWD, as a salary, into the account.
func (e *ledgerEnv) draftIncome(key string, day time.Time, account model.Account, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewIncome(model.IncomeParams{
		Header: e.header(key, day), Account: account, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec(amount)}},
	})
	if err != nil {
		e.t.Fatalf("NewIncome: %v", err)
	}
	return e.store(txn)
}

// The drafts are added to what is confirmed, each in its own account and by the day it happened
// on, and a confirmed transaction that was voided afterwards is in neither.
func TestPreviewBalancesAddWhatTheDraftsWouldChangeToWhatIsConfirmedByTheDayAndLeaveOutWhatIsVoid(t *testing.T) {
	e := newLedgerEnv(t)
	// The cash holds 1,000 less 200, and the bank holds nothing. A lunch of 2,000 was confirmed
	// and then voided: it is not there at all.
	e.storeConfirmed(e.openingOn("prev-opening-0001", nov(1), e.cash, e.twd, "1000"))
	e.setStatus(e.draftExpense("prev-lunch-000001", nov(5), e.cash, "200").ID, model.StatusConfirmed)
	voided := e.setStatus(e.draftExpense("prev-lunch-000002", nov(6), e.cash, "2000").ID, model.StatusConfirmed)
	e.setStatus(voided.ID, model.StatusVoid)
	// Waiting: a lunch of 120 today (the 14th) and one of 80 tomorrow, a salary of 50,000 into
	// the bank, a purchase of 60 on the card dated the 20th of December, and an expense that a
	// party paid for me, which no account is of.
	lunch := e.draftExpense("prev-lunch-000003", nov(14), e.cash, "120")
	e.draftExpense("prev-lunch-000004", nov(15), e.cash, "80")
	e.draftIncome("prev-salary-00001", nov(3), e.bank, "50000")
	e.draftExpense("prev-card-0000001", on(2026, time.December, 20), e.card, "60")
	e.draftPaidByParty("prev-paid-0000001", nov(8), "90")

	// Before anything has happened there is nothing to say.
	e.wantPreview(on(2026, time.October, 31))
	// On the 14th the lunch of today is in and the one of tomorrow is not, and an account
	// whose drafts are all after the day is not there; the bank has nothing confirmed and
	// still has a balance; what a party paid is nobody's balance.
	e.wantPreview(nov(14),
		"bank TWD 50000 = confirmed 0 + drafts 50000",
		"cash TWD 680 = confirmed 800 + drafts -120",
	)
	// Tomorrow the 80 is in.
	e.wantPreview(nov(15),
		"bank TWD 50000 = confirmed 0 + drafts 50000",
		"cash TWD 600 = confirmed 800 + drafts -200",
	)
	e.wantPreview(on(2026, time.December, 20),
		"bank TWD 50000 = confirmed 0 + drafts 50000",
		"card TWD -60 = confirmed 0 + drafts -60",
		"cash TWD 600 = confirmed 800 + drafts -200",
	)

	// Confirming a draft moves it from the one part to the other, and the balance is as it was.
	e.setStatus(lunch.ID, model.StatusConfirmed)
	e.wantPreview(nov(14),
		"bank TWD 50000 = confirmed 0 + drafts 50000",
		"cash TWD 680 = confirmed 680 + drafts 0",
	)
}

// A transfer between two of my accounts moves both of them: what leaves the one and what arrives in
// the other are in the balance of each, and what it has cost, which is the fee, is what the drafts
// take away from the accounts all together.
func TestPreviewBalancesHaveATransferBetweenMyOwnAccountsInBothOfThemAndOnlyTheFeeInTotal(t *testing.T) {
	e := newLedgerEnv(t)
	e.storeConfirmed(e.openingOn("prev-opening-0001", nov(1), e.bank, e.twd, "5000"))
	// 1,000 from the bank to the cash, the bank paying a fee of 15, and 500 from the cash back to
	// the bank, with a fee of 10 that the card pays.
	e.draftTransfer("prev-transfer-001", nov(10), e.bank, e.cash, e.twd, "1000",
		model.Fee{Account: e.bank, Instrument: e.twd, Category: e.fees, Amount: dec("15")})
	e.draftTransfer("prev-transfer-002", nov(11), e.cash, e.bank, e.twd, "500",
		model.Fee{Account: e.card, Instrument: e.twd, Category: e.fees, Amount: dec("10")})

	e.wantPreview(nov(14),
		"bank TWD 4485 = confirmed 5000 + drafts -515",
		"card TWD -10 = confirmed 0 + drafts -10",
		"cash TWD 500 = confirmed 0 + drafts 500",
	)

	// All together the drafts take away 25, the two fees: the rest of what leaves one of the
	// accounts arrives in another.
	total := decimal.Zero
	for _, p := range e.previews(nov(14)) {
		total = total.Add(p.Drafts)
	}
	if !total.Equal(dec("-25")) {
		t.Errorf("the drafts change the accounts by %s all together, want -25: the two fees", total)
	}
}

// A draft plan changes the balance of its account from the day each period falls due, as the
// summary of the drafts says: the origin of a purchase paid in instalments posts to the instalment
// book only, and a period charges the account its principal and its interest. What is unbilled is of
// confirmed plans, so a draft plan has none to list, and the balance there will be once it is
// confirmed is the same, with the unbilled instalment beside it.
func TestPreviewBalancesHaveAPeriodOfADraftPlanFromTheDayItFallsDueAndTheUnbilledInstalmentOnlyOnceThePlanIsConfirmed(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.storePlan(e.planOf("prev-plan-00000001")) // 12,000 on the card on the 14th; the 5th of each month from December

	// Nothing is charged to the card before the first period falls due, whatever the origin of the
	// plan says of the principal that is not due.
	e.wantPreview(nov(14))
	e.wantPreview(on(2026, time.December, 4))
	// The first period falls due on the 5th of December: its principal and its interest.
	e.wantPreview(on(2026, time.December, 5), "card TWD -1030 = confirmed 0 + drafts -1030")
	e.wantPreview(on(2027, time.January, 4), "card TWD -1030 = confirmed 0 + drafts -1030")
	e.wantPreview(on(2027, time.January, 5), "card TWD -2030 = confirmed 0 + drafts -2030")
	e.wantPreview(on(2027, time.November, 5), "card TWD -12030 = confirmed 0 + drafts -12030")

	// Confirmed, the plan is in the balances, and what the preview said of the card is what is
	// confirmed; the unbilled instalment is there now, and was not before.
	e.setPlanStatus(plan, model.StatusConfirmed)
	e.wantPreview(on(2026, time.December, 5), "card TWD -1030 = confirmed -1030 + drafts 0 unbilled -11000")
	e.wantPreview(nov(14), "card TWD 0 = confirmed 0 + drafts 0 unbilled -12000")

	// A voided plan is in neither.
	e.setPlanStatus(plan, model.StatusVoid)
	e.wantPreview(on(2026, time.December, 5))
}

// The conversion of a bill into instalments pays the bill off with a posting of the account book
// on the day it is made, and then charges each period: the origin of this plan changes a balance
// as a period does.
func TestPreviewBalancesHaveTheOriginOfADraftBillInstalmentAndItsPeriodsDueByTheDay(t *testing.T) {
	e := newLedgerEnv(t)
	e.storePlan(e.billInstalmentOf("prev-bill-000001")) // the bank's 3,000, on the 16th; 1,000 due on the 20th of November, December and January

	e.wantPreview(nov(15))
	e.wantPreview(nov(16), "bank TWD 3000 = confirmed 0 + drafts 3000")
	e.wantPreview(nov(19), "bank TWD 3000 = confirmed 0 + drafts 3000")
	e.wantPreview(nov(20), "bank TWD 2000 = confirmed 0 + drafts 2000")
	e.wantPreview(on(2027, time.January, 20), "bank TWD 0 = confirmed 0 + drafts 0")
}

// aWorldOfEveryKindOfDraft stores a draft of each of the ten kinds, a purchase in instalments and a
// conversion of a bill, and, besides, what is confirmed, void, dated later, and nobody's.
func (e *ledgerEnv) aWorldOfEveryKindOfDraft() {
	e.t.Helper()
	e.storedOfEveryKind()
	e.storeConfirmed(e.openingOn("prev-opening-0001", nov(1), e.bank, e.twd, "9000"))
	e.storeConfirmed(e.openingOn("prev-opening-0002", nov(1), e.cash, e.usd, "40"))
	e.setStatus(e.draftExpense("prev-lunch-000001", nov(5), e.cash, "200").ID, model.StatusConfirmed)
	e.setPlanStatus(e.storePlan(e.planOf("prev-plan-confirmed1")), model.StatusConfirmed)
	voided := e.setStatus(e.draftExpense("prev-lunch-000002", nov(6), e.cash, "2000").ID, model.StatusConfirmed)
	e.setStatus(voided.ID, model.StatusVoid)
	e.draftExpense("prev-future-00001", on(2026, time.December, 20), e.card, "60")
	e.draftPaidByParty("prev-paid-0000001", nov(9), "90")
	e.draftTransfer("prev-transfer-001", nov(10), e.bank, e.cash, e.twd, "1000",
		model.Fee{Account: e.card, Instrument: e.twd, Category: e.fees, Amount: dec("15")})
}

// daysOfTheWorld are days before the drafts of aWorldOfEveryKindOfDraft, among them, and after.
var daysOfTheWorld = []time.Time{
	on(2026, time.October, 31), nov(1), nov(4), nov(7), nov(10), nov(14), nov(16), nov(20),
	on(2026, time.December, 5), on(2026, time.December, 20), on(2027, time.January, 20), forever,
}

// The preview and the summary of the drafts are two sums of the same postings, and are one rule:
// of every account and instrument, the drafts of the preview are the net of the amounts of the
// summary of all the months, and of all the accounts together they are the net of the drafts. The
// confirmed part and the unbilled instalment are what the balances say.
func TestPreviewBalancesAreTheBalancesAndTheNetOfTheSummaryOfTheDraftsAccountByAccountOnEveryDay(t *testing.T) {
	e := newLedgerEnv(t)
	e.aWorldOfEveryKindOfDraft()
	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		t.Fatalf("DraftTransactions: %v", err)
	}
	names := e.names()

	for _, day := range daysOfTheWorld {
		t.Run(day.Format(time.DateOnly), func(t *testing.T) {
			summary, err := model.SummarizeDrafts(rows, day)
			if err != nil {
				t.Fatalf("SummarizeDrafts: %v", err)
			}
			said := map[accountInstrument]decimal.Decimal{}
			for _, month := range summary.Months {
				for _, account := range month.Accounts {
					if account.AccountID == nil {
						continue
					}
					for _, a := range account.Amounts {
						k := accountInstrument{*account.AccountID, a.InstrumentID}
						said[k] = said[k].Add(a.Net())
					}
				}
			}
			previews := e.previews(day)

			// The drafts: the net of the summary, account by account.
			for k, p := range previews {
				if !p.Drafts.Equal(said[k]) {
					t.Errorf("%s %s: the drafts add %s, and the summary of the drafts says a net of %s", names[k.account], names[k.instrument], p.Drafts, said[k])
				}
			}
			for k, net := range said {
				if _, found := previews[k]; !found && !net.IsZero() {
					t.Errorf("%s %s: the summary says a net of %s, and the preview has no balance of it", names[k.account], names[k.instrument], net)
				}
			}
			// All the accounts together: what the drafts change, which the transfers between my own
			// accounts leave out, as they change nothing in all.
			together := map[uuid.UUID]decimal.Decimal{}
			for _, p := range previews {
				together[p.InstrumentID] = together[p.InstrumentID].Add(p.Drafts)
			}
			for _, a := range summary.Amounts {
				together[a.InstrumentID] = together[a.InstrumentID].Sub(a.Net())
			}
			for instrument, rest := range together {
				if !rest.IsZero() {
					t.Errorf("%s: the drafts of the accounts and the summary of the drafts differ by %s", names[instrument], rest)
				}
			}

			// What is confirmed and what is unbilled are what the balances say, and zero in a balance
			// that only the drafts make.
			balances, err := e.txns.Balances(context.Background(), repo.BalanceFilter{Day: day})
			if err != nil {
				t.Fatalf("Balances: %v", err)
			}
			held := map[accountInstrument]model.Balance{}
			for _, b := range balances {
				k := accountInstrument{b.AccountID, b.InstrumentID}
				held[k] = b
				p, found := previews[k]
				switch {
				case !found:
					t.Errorf("%s %s has a balance and no preview", names[k.account], names[k.instrument])
				case !p.Confirmed.Equal(b.Quantity) || !p.UnbilledInstalment.Equal(b.UnbilledInstalment):
					t.Errorf("%s %s: the preview has %s confirmed and %s unbilled, the balances say %s and %s",
						names[k.account], names[k.instrument], p.Confirmed, p.UnbilledInstalment, b.Quantity, b.UnbilledInstalment)
				}
			}
			for k, p := range previews {
				if _, found := held[k]; !found && (!p.Confirmed.IsZero() || !p.UnbilledInstalment.IsZero()) {
					t.Errorf("%s %s has no balance, and the preview has %s confirmed and %s unbilled", names[k.account], names[k.instrument], p.Confirmed, p.UnbilledInstalment)
				}
			}
			// The check is of something: once everything has happened the drafts make balances that
			// nothing confirmed does.
			if day.Equal(forever) && (len(held) == 0 || len(previews) <= len(held)) {
				t.Errorf("%d balances and %d preview balances once everything has happened, want the drafts to make some of their own", len(held), len(previews))
			}
		})
	}
}

// Confirming every draft there is changes no quantity: what was the drafts' is then confirmed, which
// is what a preview says. The unbilled instalment of the plans that were drafts is listed then, which
// it was not before: a plan has it once it is confirmed.
func TestConfirmingEveryDraftLeavesThePreviewQuantitiesAsTheyWereAndMakesThemConfirmed(t *testing.T) {
	e := newLedgerEnv(t)
	e.aWorldOfEveryKindOfDraft()
	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		t.Fatalf("DraftTransactions: %v", err)
	}
	before := make([]map[accountInstrument]model.PreviewBalance, len(daysOfTheWorld))
	for i, day := range daysOfTheWorld {
		before[i] = e.previews(day)
	}

	// Confirmed as the use case does, in one database transaction, a plan by its origin and then
	// its periods.
	slices.SortStableFunc(rows, func(a, b model.DraftTransaction) int { return planPosition(a) - planPosition(b) })
	err = e.inTx(func(ctx context.Context) error {
		for _, txn := range rows {
			if _, err := e.txns.SetStatus(ctx, txn.ID, model.StatusConfirmed); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("confirm the drafts: %v", err)
	}

	names := e.names()
	for i, day := range daysOfTheWorld {
		after := e.previews(day)
		for k, b := range before[i] {
			a, found := after[k]
			switch {
			case !found:
				t.Errorf("%s: %s %s had a balance of %s, and has none once the drafts are confirmed", day.Format(time.DateOnly), names[k.account], names[k.instrument], b.Quantity())
			case !a.Quantity().Equal(b.Quantity()) || !a.Drafts.IsZero() || !a.Confirmed.Equal(b.Quantity()):
				t.Errorf("%s: %s %s was %s (%s confirmed and %s of drafts), and is %s once they are confirmed (%s and %s): want the same, all confirmed",
					day.Format(time.DateOnly), names[k.account], names[k.instrument], b.Quantity(), b.Confirmed, b.Drafts, a.Quantity(), a.Confirmed, a.Drafts)
			}
		}
		// A balance that is new is of an unbilled instalment only: a plan whose periods are not due.
		for k, a := range after {
			if _, found := before[i][k]; !found && !a.Quantity().IsZero() {
				t.Errorf("%s: %s %s holds %s once the drafts are confirmed, and had no balance before", day.Format(time.DateOnly), names[k.account], names[k.instrument], a.Quantity())
			}
		}
	}
}

// planPosition is the place of a transaction in its plan, and zero for one that is in none.
func planPosition(t model.DraftTransaction) int {
	if t.PlanSeq == nil {
		return 0
	}
	return *t.PlanSeq
}
