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

// A preview balance is what an account would hold, and owe in instalments that are not yet due, once
// the drafts were confirmed: each of the two figures of a balance as it is now, and what the drafts add
// to it. The statement that reads it is one SQL query that adds up the postings of the confirmed
// transactions and of the drafts, by the day, in the two books a balance is made of; the rules of which
// postings of the account book are the drafts' are those of model.SummarizeDrafts, which adds them up
// too, by month and account. These tests show the rows of the query, and hold it to the summary and to
// the balances: what GetBalances says once every draft is confirmed is what the preview said before.

// previewOn reads the preview balances on the day and writes each as a line, such as
// "cash TWD 680 = confirmed 800 + drafts -120", with " | unbilled -11000 = confirmed 0 + drafts -11000"
// when something is unbilled, in the order the repo gives them.
func (e *ledgerEnv) previewOn(day time.Time) []string {
	e.t.Helper()
	got, err := e.txns.PreviewBalances(context.Background(), day)
	if err != nil {
		e.t.Fatalf("PreviewBalances: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got.Balances))
	for i, b := range got.Balances {
		lines[i] = fmt.Sprintf("%s %s %s = confirmed %s + drafts %s", names[b.AccountID], names[b.InstrumentID], b.Held.Total(), b.Held.Confirmed, b.Held.Drafts)
		if !b.Unbilled.Confirmed.IsZero() || !b.Unbilled.Drafts.IsZero() {
			lines[i] += fmt.Sprintf(" | unbilled %s = confirmed %s + drafts %s", b.Unbilled.Total(), b.Unbilled.Confirmed, b.Unbilled.Drafts)
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

// previews reads the preview balances on the day, by account and instrument, and the number of
// drafts that wait, and fails the test when it cannot.
func (e *ledgerEnv) previews(day time.Time) (map[accountInstrument]model.PreviewBalance, int) {
	e.t.Helper()
	got, err := e.txns.PreviewBalances(context.Background(), day)
	if err != nil {
		e.t.Fatalf("PreviewBalances: %v", err)
	}
	byKey := make(map[accountInstrument]model.PreviewBalance, len(got.Balances))
	for _, p := range got.Balances {
		k := accountInstrument{p.AccountID, p.InstrumentID}
		if _, again := byKey[k]; again {
			e.t.Errorf("%s %s is in the preview balances twice", e.names()[k.account], e.names()[k.instrument])
		}
		byKey[k] = p
	}
	return byKey, got.Drafts
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

	// The five drafts wait, whatever day they are of and whatever they move: the one of the 15th, the
	// one of December and the one that a party paid are waiting and in no figure on the 14th.
	if _, drafts := e.previews(nov(14)); drafts != 5 {
		t.Errorf("%d drafts wait, want 5", drafts)
	}

	// Confirming a draft moves it from the one part to the other, and the balance is as it was.
	e.setStatus(lunch.ID, model.StatusConfirmed)
	e.wantPreview(nov(14),
		"bank TWD 50000 = confirmed 0 + drafts 50000",
		"cash TWD 680 = confirmed 680 + drafts 0",
	)
	if _, drafts := e.previews(nov(14)); drafts != 4 {
		t.Errorf("%d drafts wait once one is confirmed, want 4", drafts)
	}
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
	byKey, _ := e.previews(nov(14))
	for _, p := range byKey {
		total = total.Add(p.Held.Drafts)
	}
	if !total.Equal(dec("-25")) {
		t.Errorf("the drafts change the accounts by %s all together, want -25: the two fees", total)
	}
}

// A draft plan changes what the account holds from the day each period falls due, as the summary of the
// drafts says, and what it owes in instalments from the day of the origin: the origin of a purchase paid
// in instalments posts to the instalment book only, and a period gives back to the instalment book what it
// charges the account, its principal, and charges the account its interest too. Once it is confirmed, the
// balance there will be is what the preview said of it.
func TestPreviewBalancesHaveAPeriodOfADraftPlanFromTheDayItFallsDueAndWhatItStillOwesFromTheDayOfItsOrigin(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.storePlan(e.planOf("prev-plan-00000001")) // 12,000 on the card on the 14th; the 5th of each month from December

	// The card owes the whole principal from the day of the origin, and holds nothing of it: nothing
	// is charged to the card until the first period falls due.
	e.wantPreview(nov(14), "card TWD 0 = confirmed 0 + drafts 0 | unbilled -12000 = confirmed 0 + drafts -12000")
	e.wantPreview(on(2026, time.December, 4), "card TWD 0 = confirmed 0 + drafts 0 | unbilled -12000 = confirmed 0 + drafts -12000")
	// The first period falls due on the 5th of December: its principal and its interest are charged,
	// and the principal is no longer to come.
	e.wantPreview(on(2026, time.December, 5), "card TWD -1030 = confirmed 0 + drafts -1030 | unbilled -11000 = confirmed 0 + drafts -11000")
	e.wantPreview(on(2027, time.January, 4), "card TWD -1030 = confirmed 0 + drafts -1030 | unbilled -11000 = confirmed 0 + drafts -11000")
	e.wantPreview(on(2027, time.January, 5), "card TWD -2030 = confirmed 0 + drafts -2030 | unbilled -10000 = confirmed 0 + drafts -10000")
	// Every period has fallen due: what is left to come is nothing, and nothing is said of it.
	e.wantPreview(on(2027, time.November, 5), "card TWD -12030 = confirmed 0 + drafts -12030")

	// Confirmed, the plan is in the balances, and what the preview said of the card is what is
	// confirmed, in both figures.
	e.setPlanStatus(plan, model.StatusConfirmed)
	e.wantPreview(on(2026, time.December, 5), "card TWD -1030 = confirmed -1030 + drafts 0 | unbilled -11000 = confirmed -11000 + drafts 0")
	e.wantPreview(nov(14), "card TWD 0 = confirmed 0 + drafts 0 | unbilled -12000 = confirmed -12000 + drafts 0")

	// A voided plan is in neither.
	e.setPlanStatus(plan, model.StatusVoid)
	e.wantPreview(on(2026, time.December, 5))
}

// The conversion of a bill into instalments pays the bill off with a posting of the account book on the
// day it is made, owes the principal again with one of the instalment book, and each period gives back
// what it charges. An account that owes 3,000, and converts it in a draft into three periods of 1,000, owes
// 3,000 until the day of the origin, and from then on what it holds and what it has still to pay are two
// figures: a preview that left the second out would say that it owes less than it will. The conversion
// makes no debt, and moves it from the one figure to the other, so that the drafts of the two add up to
// nothing.
func TestPreviewBalancesHaveTheDebtOfADraftBillInstalmentInBothFiguresAndItMovesBetweenThemAndNeverMakesAny(t *testing.T) {
	e := newLedgerEnv(t)
	e.storeConfirmed(e.openingOn("prev-owed-0000001", nov(1), e.bank, e.twd, "-3000")) // the bank account owes 3,000
	plan := e.storePlan(e.billInstalmentOf("prev-bill-000001"))                        // converted on the 16th; 1,000 due on the 20th of November, December and January

	e.wantPreview(nov(15), "bank TWD -3000 = confirmed -3000 + drafts 0")
	e.wantPreview(nov(16), "bank TWD 0 = confirmed -3000 + drafts 3000 | unbilled -3000 = confirmed 0 + drafts -3000")
	e.wantPreview(nov(19), "bank TWD 0 = confirmed -3000 + drafts 3000 | unbilled -3000 = confirmed 0 + drafts -3000")
	// The first period falls due on the 20th: the account holds -1,000, and 2,000 is still to come.
	e.wantPreview(nov(20), "bank TWD -1000 = confirmed -3000 + drafts 2000 | unbilled -2000 = confirmed 0 + drafts -2000")
	e.wantPreview(on(2026, time.December, 20), "bank TWD -2000 = confirmed -3000 + drafts 1000 | unbilled -1000 = confirmed 0 + drafts -1000")
	e.wantPreview(on(2027, time.January, 20), "bank TWD -3000 = confirmed -3000 + drafts 0")

	// The drafts of a conversion cancel in the two figures, on every day: what it pays off is what it owes
	// again, and a period gives back what it charges.
	days := map[time.Time][2]string{
		nov(16): {"0", "-3000"}, nov(20): {"-1000", "-2000"}, on(2026, time.December, 20): {"-2000", "-1000"}, on(2027, time.January, 20): {"-3000", "0"},
	}
	previews := map[time.Time]model.PreviewBalance{}
	for day := range days {
		byKey, _ := e.previews(day)
		p := byKey[accountInstrument{e.bank.ID, e.twd.ID}]
		previews[day] = p
		if sum := p.Held.Drafts.Add(p.Unbilled.Drafts); !sum.IsZero() {
			t.Errorf("%s: the drafts of the conversion add %s to what the account holds and %s to what it owes in instalments, which make %s, want nothing",
				day.Format(time.DateOnly), p.Held.Drafts, p.Unbilled.Drafts, sum)
		}
	}

	// Once the plan is confirmed, the balances say of the account on each day what the two figures of the
	// preview added up to, in both.
	e.setPlanStatus(plan, model.StatusConfirmed)
	for day, pair := range days {
		got, err := e.txns.Balances(context.Background(), repo.BalanceFilter{AccountID: &e.bank.ID, Day: day})
		if err != nil || len(got) != 1 {
			t.Fatalf("Balances on %s = %v, %v, want one", day.Format(time.DateOnly), got, err)
		}
		p := previews[day]
		if !got[0].Quantity.Equal(dec(pair[0])) || !got[0].UnbilledInstalment.Equal(dec(pair[1])) ||
			!got[0].Quantity.Equal(p.Held.Total()) || !got[0].UnbilledInstalment.Equal(p.Unbilled.Total()) {
			t.Errorf("%s: the balance is %s and %s unbilled once the plan is confirmed, and the preview said %s and %s, want %s and %s",
				day.Format(time.DateOnly), got[0].Quantity, got[0].UnbilledInstalment, p.Held.Total(), p.Unbilled.Total(), pair[0], pair[1])
		}
	}
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
// of every account and instrument, the drafts of what it holds are the net of the amounts of the
// summary of all the months, and of all the accounts together they are the net of the drafts. The
// confirmed part of each figure is what the balances say, the number of drafts is the one the summary
// counts, and the figures of the instalment book are the same sum of the instalment postings.
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
			previews, drafts := e.previews(day)

			// The drafts: the net of the summary, account by account.
			for k, p := range previews {
				if !p.Held.Drafts.Equal(said[k]) {
					t.Errorf("%s %s: the drafts add %s to what it holds, and the summary of the drafts says a net of %s", names[k.account], names[k.instrument], p.Held.Drafts, said[k])
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
				together[p.InstrumentID] = together[p.InstrumentID].Add(p.Held.Drafts)
			}
			for _, a := range summary.Amounts {
				together[a.InstrumentID] = together[a.InstrumentID].Sub(a.Net())
			}
			for instrument, rest := range together {
				if !rest.IsZero() {
					t.Errorf("%s: the drafts of the accounts and the summary of the drafts differ by %s", names[instrument], rest)
				}
			}
			// How many drafts wait is how many the summary counts, whatever day they are of.
			if drafts != summary.Count {
				t.Errorf("%d drafts wait, and the summary counts %d", drafts, summary.Count)
			}

			// What is confirmed, in each figure, is what the balances say, and zero in a balance that only
			// the drafts make.
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
				case !p.Held.Confirmed.Equal(b.Quantity) || !p.Unbilled.Confirmed.Equal(b.UnbilledInstalment):
					t.Errorf("%s %s: the preview has %s confirmed and %s unbilled, the balances say %s and %s",
						names[k.account], names[k.instrument], p.Held.Confirmed, p.Unbilled.Confirmed, b.Quantity, b.UnbilledInstalment)
				}
			}
			for k, p := range previews {
				if _, found := held[k]; !found && p.HasConfirmed() {
					t.Errorf("%s %s has no balance, and the preview has %s confirmed and %s unbilled", names[k.account], names[k.instrument], p.Held.Confirmed, p.Unbilled.Confirmed)
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

// Confirming every draft there is changes no figure: what was the drafts' is then confirmed, which is
// what a preview says, and the balances say of each account and instrument, in both figures, what the
// preview said before: the debt in instalments that a draft plan makes is in it from the day of its
// origin, and is not left to appear when the plan is confirmed.
func TestConfirmingEveryDraftMakesTheBalancesWhatThePreviewSaidInBothFigures(t *testing.T) {
	e := newLedgerEnv(t)
	e.aWorldOfEveryKindOfDraft()
	rows, err := e.txns.DraftTransactions(context.Background())
	if err != nil {
		t.Fatalf("DraftTransactions: %v", err)
	}
	before := make([]map[accountInstrument]model.PreviewBalance, len(daysOfTheWorld))
	for i, day := range daysOfTheWorld {
		before[i], _ = e.previews(day)
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
		after, drafts := e.previews(day)
		if drafts != 0 {
			t.Errorf("%s: %d drafts wait once every draft is confirmed", day.Format(time.DateOnly), drafts)
		}
		balances, err := e.txns.Balances(context.Background(), repo.BalanceFilter{Day: day})
		if err != nil {
			t.Fatalf("Balances: %v", err)
		}
		held := map[accountInstrument]model.Balance{}
		for _, b := range balances {
			held[accountInstrument{b.AccountID, b.InstrumentID}] = b
		}
		for k, b := range before[i] {
			name := day.Format(time.DateOnly) + ": " + names[k.account] + " " + names[k.instrument]
			// The balances say what the preview said, in both figures.
			got, found := held[k]
			if !found {
				t.Errorf("%s: the preview had a balance of %s and %s unbilled, and the balances have none once the drafts are confirmed", name, b.Held.Total(), b.Unbilled.Total())
				continue
			}
			if !got.Quantity.Equal(b.Held.Total()) || !got.UnbilledInstalment.Equal(b.Unbilled.Total()) {
				t.Errorf("%s: the preview said %s held and %s unbilled, and the balances say %s and %s once every draft is confirmed",
					name, b.Held.Total(), b.Unbilled.Total(), got.Quantity, got.UnbilledInstalment)
			}
			// And the preview then is all confirmed, and is the same.
			a := after[k]
			if !a.Held.Drafts.IsZero() || !a.Unbilled.Drafts.IsZero() || !a.Held.Confirmed.Equal(b.Held.Total()) || !a.Unbilled.Confirmed.Equal(b.Unbilled.Total()) {
				t.Errorf("%s: %s held and %s unbilled confirmed, and %s and %s of drafts, once every draft is confirmed, want %s and %s, all confirmed",
					name, a.Held.Confirmed, a.Unbilled.Confirmed, a.Held.Drafts, a.Unbilled.Drafts, b.Held.Total(), b.Unbilled.Total())
			}
		}
		// A balance that is new has nothing in it: the drafts that make a balance are of the instalment
		// book or of the account book, and either is in the row of the preview from the day it counts.
		for k, a := range after {
			if _, found := before[i][k]; !found && (!a.Held.Total().IsZero() || !a.Unbilled.Total().IsZero()) {
				t.Errorf("%s: %s %s holds %s and owes %s in instalments once the drafts are confirmed, and had no balance before",
					day.Format(time.DateOnly), names[k.account], names[k.instrument], a.Held.Total(), a.Unbilled.Total())
			}
		}
		if len(after) != len(before[i]) {
			t.Errorf("%s: %d balances before the drafts were confirmed and %d after", day.Format(time.DateOnly), len(before[i]), len(after))
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
