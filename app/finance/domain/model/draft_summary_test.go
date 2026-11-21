package model_test

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// The summary of the drafts is a function of the drafts the repo reads and of today, so
// that every rule of it is shown here without a database.

// draftAccount and draftInstrument are an account and an instrument as the summary sees
// them: an id, and the name or the code that the summary puts them in order by.
type draftAccount struct {
	id   uuid.UUID
	name string
}

type draftInstrument struct {
	id   uuid.UUID
	code string
}

func newDraftAccount(name string) draftAccount {
	return draftAccount{id: uuid.Must(uuid.NewV7()), name: name}
}

func newDraftInstrument(code string) draftInstrument {
	return draftInstrument{id: uuid.Must(uuid.NewV7()), code: code}
}

// accountBook is a posting of the account book to the account.
func accountBook(a draftAccount, in draftInstrument, quantity string) model.DraftPosting {
	return model.DraftPosting{
		AccountID: a.id, AccountName: a.name, Book: model.BookAccount,
		InstrumentID: in.id, InstrumentCode: in.code, Quantity: dec(quantity),
	}
}

// instalmentBook is a posting of the instalment book to the account: principal that is
// owed and has not been charged.
func instalmentBook(a draftAccount, in draftInstrument, quantity string) model.DraftPosting {
	p := accountBook(a, in, quantity)
	p.Book = model.BookInstalment
	return p
}

// draftOf is a draft of the kind on the day, with the postings that have an account.
func draftOf(kind model.Kind, d time.Time, postings ...model.DraftPosting) model.DraftTransaction {
	return model.DraftTransaction{ID: uuid.Must(uuid.NewV7()), Kind: kind, OccurredOn: d, Postings: postings}
}

// draftPlanOrigin makes the draft the origin of its own plan.
func draftPlanOrigin(t model.DraftTransaction) model.DraftTransaction {
	t.PlanID, t.PlanSeq = new(t.ID), new(0)
	return t
}

// draftPlanPeriod is the period of the plan whose origin is given, at the place in it,
// falling due on the day, which charges what the postings say.
func draftPlanPeriod(origin model.DraftTransaction, seq int, due time.Time, postings ...model.DraftPosting) model.DraftTransaction {
	t := draftOf(model.KindInstalment, due, postings...)
	t.PlanID, t.PlanSeq = new(*origin.PlanID), new(seq)
	return t
}

// draftPlan is a plan of the account that splits principal, bought on the day of the
// origin, with a period for each of the days it falls due on, each charging principal
// divided by the number of periods, as the account postings and the instalment postings
// of the periods say: the origin has an instalment posting alone.
func draftPlan(a draftAccount, in draftInstrument, bought time.Time, principal int, due ...time.Time) []model.DraftTransaction {
	origin := draftPlanOrigin(draftOf(model.KindExpense, bought, instalmentBook(a, in, fmt.Sprint(-principal))))
	txns := []model.DraftTransaction{origin}
	each := principal / len(due)
	for i, d := range due {
		txns = append(txns, draftPlanPeriod(origin, i+1, d, instalmentBook(a, in, fmt.Sprint(each)), accountBook(a, in, fmt.Sprint(-each))))
	}
	return txns
}

// summarize works the summary out as of today, and fails the test when it cannot.
func summarize(t *testing.T, today time.Time, txns ...model.DraftTransaction) model.DraftSummary {
	t.Helper()
	got, err := model.SummarizeDrafts(txns, today)
	if err != nil {
		t.Fatalf("SummarizeDrafts: %v", err)
	}
	return got
}

// draftAmounts writes amounts as "TWD out 120 in 0 net -120", one for each instrument,
// in the order they come.
func draftAmounts(amounts []model.DraftAmount) string {
	parts := make([]string, len(amounts))
	for i, a := range amounts {
		parts[i] = fmt.Sprintf("%s out %s in %s net %s", a.InstrumentCode, a.Outflow, a.Inflow, a.Net())
	}
	return strings.Join(parts, ", ")
}

// draftLines writes the summary: a line for all the drafts, and for each month, the
// newest first, a line with how many happened in it, how many of them move money
// between my own accounts and what they would move, with a line under it for each
// account in the order they come, the drafts of none as "(none)". The names say how to
// write the accounts.
func draftLines(names map[uuid.UUID]string, s model.DraftSummary) []string {
	lines := []string{fmt.Sprintf("all: %d, own transfers %d [%s]", s.Count, s.OwnTransfers, draftAmounts(s.Amounts))}
	for _, m := range s.Months {
		lines = append(lines, fmt.Sprintf("%s: %d, own transfers %d [%s]", m.Month.Format("2006-01"), m.Count, m.OwnTransfers, draftAmounts(m.Amounts)))
		for _, a := range m.Accounts {
			name := "(none)"
			if a.AccountID != nil {
				name = names[*a.AccountID]
			}
			lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, draftAmounts(a.Amounts)))
		}
	}
	return lines
}

// wantDraftLines fails the test unless the summary is the lines wanted, in that order:
// the order of the summary is part of what it promises.
func wantDraftLines(t *testing.T, names map[uuid.UUID]string, s model.DraftSummary, want ...string) {
	t.Helper()
	if got := draftLines(names, s); !slices.Equal(got, want) {
		t.Errorf("summary:\n got %q\nwant %q", got, want)
	}
}

// namesOf says what to call each account in the lines of a summary.
func namesOf(accounts ...draftAccount) map[uuid.UUID]string {
	names := make(map[uuid.UUID]string, len(accounts))
	for _, a := range accounts {
		names[a.id] = a.name
	}
	return names
}

func TestADraftAmountNetIsWhatComesInLessWhatGoesOut(t *testing.T) {
	usd := uuid.Must(uuid.NewV7())
	tests := []struct {
		name            string
		outflow, inflow string
		want            string
	}{
		{"only an outflow, which is a decrease", "120", "0", "-120"},
		{"only an inflow", "0", "50000", "50000"},
		{"both, and more out", "1000.5", "250.25", "-750.25"},
		{"both, and more in", "10", "30.5", "20.5"},
		{"both the same, which moves nothing in all", "1000", "1000", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := model.DraftAmount{InstrumentID: usd, InstrumentCode: "USD", Outflow: dec(tt.outflow), Inflow: dec(tt.inflow)}
			if got := a.Net(); !got.Equal(dec(tt.want)) {
				t.Errorf("Net = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNoDraftIsNothingAndNotAnError(t *testing.T) {
	got, err := model.SummarizeDrafts(nil, on(2026, time.November, 14))
	if err != nil || got.Count != 0 || got.OwnTransfers != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("SummarizeDrafts of no draft = %+v, %v, want a count of 0 and no month, with no error", got, err)
	}
}

// What the reviewer found with five items recorded: the expense of 750 from the cash, a
// transfer of 3,000 between two accounts, a purchase on the card paid in instalments
// (its origin and 3 periods of 2,000, none due yet), an expense that a party paid and
// one more that another party paid. They showed as 8 drafts, with 9,750 going out and
// 6,750 less in all: the periods and the transfer were taken for what they are not.
func TestFiveRecordedItemsAreFiveDraftsAndOnlyTheExpenseMovesTheBalances(t *testing.T) {
	twd := newDraftInstrument("TWD")
	cash, bank, wallet, card := newDraftAccount("cash"), newDraftAccount("bank"), newDraftAccount("wallet"), newDraftAccount("card")
	txns := []model.DraftTransaction{
		draftOf(model.KindExpense, on(2026, time.November, 5), accountBook(cash, twd, "-750")),
		draftOf(model.KindTransfer, on(2026, time.November, 6), accountBook(bank, twd, "-3000"), accountBook(wallet, twd, "3000")),
	}
	txns = append(txns, draftPlan(card, twd, on(2026, time.November, 10), 6000,
		on(2026, time.December, 5), on(2027, time.January, 5), on(2027, time.February, 5))...)
	txns = append(txns,
		draftOf(model.KindExpense, on(2026, time.November, 8)),
		draftOf(model.KindExpense, on(2026, time.November, 9)),
	)

	got := summarize(t, on(2026, time.November, 14), txns...)

	// The month and all the drafts move 750: the transfer moves money from one account
	// to another and the periods have not fallen due. The accounts still show the
	// transfer, which does change their balances.
	wantDraftLines(t, namesOf(cash, bank, wallet, card), got,
		"all: 5, own transfers 1 [TWD out 750 in 0 net -750]",
		"2026-11: 5, own transfers 1 [TWD out 750 in 0 net -750]",
		"  bank: 1 [TWD out 3000 in 0 net -3000]",
		"  card: 1 []",
		"  cash: 1 [TWD out 750 in 0 net -750]",
		"  wallet: 1 [TWD out 0 in 3000 net 3000]",
		"  (none): 2 []",
	)
}

func TestAPlanIsOneDraftInTheMonthOfItsOriginWhateverNumberOfPeriodsItHas(t *testing.T) {
	twd := newDraftInstrument("TWD")
	card := newDraftAccount("card")
	for _, periods := range []int{1, 3, 12} {
		t.Run(fmt.Sprintf("%d periods", periods), func(t *testing.T) {
			due := make([]time.Time, periods)
			for i := range due {
				due[i] = on(2026, time.Month(12+i), 5) // a month may be past 12: time.Date carries it over
			}

			got := summarize(t, on(2026, time.November, 14), draftPlan(card, twd, on(2026, time.November, 14), 12000, due...)...)

			// One draft, of the card, in November: the month of its origin, though its periods
			// fall due in the months after it.
			wantDraftLines(t, namesOf(card), got,
				"all: 1, own transfers 0 []",
				"2026-11: 1, own transfers 0 []",
				"  card: 1 []",
			)
		})
	}
}

func TestAPeriodThatIsDueIsInTheMonthAndTheAccountOfItsPlan(t *testing.T) {
	twd := newDraftInstrument("TWD")
	card := newDraftAccount("card")
	// 12,000 on the card bought on the 14th of November: three periods, the first with
	// 30 of interest, which the account is charged on top of its principal.
	origin := draftPlanOrigin(draftOf(model.KindExpense, on(2026, time.November, 14), instalmentBook(card, twd, "-3000")))
	plan := []model.DraftTransaction{
		origin,
		draftPlanPeriod(origin, 1, on(2026, time.December, 5), instalmentBook(card, twd, "1000"), accountBook(card, twd, "-1030")),
		draftPlanPeriod(origin, 2, on(2027, time.January, 5), instalmentBook(card, twd, "1000"), accountBook(card, twd, "-1000")),
		draftPlanPeriod(origin, 3, on(2027, time.February, 5), instalmentBook(card, twd, "1000"), accountBook(card, twd, "-1000")),
	}
	tests := []struct {
		today time.Time
		want  string
	}{
		{on(2026, time.December, 4), ""},                            // the day before the first falls due
		{on(2026, time.December, 5), "TWD out 1030 in 0 net -1030"}, // on the day it falls due: from its day on
		{on(2027, time.January, 4), "TWD out 1030 in 0 net -1030"},  // the second is not due yet
		{on(2027, time.January, 5), "TWD out 2030 in 0 net -2030"},  // two are
		{on(2027, time.February, 5), "TWD out 3030 in 0 net -3030"}, // all three
		{on(2030, time.January, 1), "TWD out 3030 in 0 net -3030"},  // nothing more to come
		{on(2026, time.November, 14), ""},                           // the day it was bought
		{on(2026, time.November, 13), ""},                           // before it was
	}
	for _, tt := range tests {
		t.Run(tt.today.Format(time.DateOnly), func(t *testing.T) {
			got := summarize(t, tt.today, plan...)

			// The periods are in the month and the account of the plan, and never in a month of
			// their own: there is one draft, and it is the plan.
			wantDraftLines(t, namesOf(card), got,
				fmt.Sprintf("all: 1, own transfers 0 [%s]", tt.want),
				fmt.Sprintf("2026-11: 1, own transfers 0 [%s]", tt.want),
				fmt.Sprintf("  card: 1 [%s]", tt.want),
			)
		})
	}
}

func TestADraftIsCountedFromTheStartAndIsInTheAmountsFromItsDayOn(t *testing.T) {
	twd := newDraftInstrument("TWD")
	cash := newDraftAccount("cash")
	lunch := draftOf(model.KindExpense, on(2026, time.November, 15), accountBook(cash, twd, "-120"))

	// The day before, it is waiting like any other draft, and changes no balance yet.
	got := summarize(t, on(2026, time.November, 14), lunch)
	wantDraftLines(t, namesOf(cash), got,
		"all: 1, own transfers 0 []",
		"2026-11: 1, own transfers 0 []",
		"  cash: 1 []",
	)

	// On its day it does.
	got = summarize(t, on(2026, time.November, 15), lunch)
	wantDraftLines(t, namesOf(cash), got,
		"all: 1, own transfers 0 [TWD out 120 in 0 net -120]",
		"2026-11: 1, own transfers 0 [TWD out 120 in 0 net -120]",
		"  cash: 1 [TWD out 120 in 0 net -120]",
	)
}

func TestATransferBetweenOwnAccountsIsInTheAccountsAndNotInTheTotals(t *testing.T) {
	twd, usd := newDraftInstrument("TWD"), newDraftInstrument("USD")
	bank, wallet, card := newDraftAccount("bank"), newDraftAccount("wallet"), newDraftAccount("card")
	names := namesOf(bank, wallet, card)
	tests := []struct {
		name     string
		postings []model.DraftPosting
		want     []string
	}{
		{
			name:     "without a fee",
			postings: []model.DraftPosting{accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000")},
			want: []string{
				"all: 1, own transfers 1 []",
				"2026-11: 1, own transfers 1 []",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  wallet: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name:     "a fee paid from the account the money leaves is what is left over in the outflow",
			postings: []model.DraftPosting{accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"), accountBook(bank, twd, "-15")},
			want: []string{
				"all: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"2026-11: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"  bank: 1 [TWD out 1015 in 0 net -1015]",
				"  wallet: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name:     "a fee paid from the account the money arrives at",
			postings: []model.DraftPosting{accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"), accountBook(wallet, twd, "-15")},
			want: []string{
				"all: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"2026-11: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  wallet: 1 [TWD out 15 in 1000 net 985]",
			},
		},
		{
			name:     "a fee paid from a third account, which the draft is of too",
			postings: []model.DraftPosting{accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"), accountBook(card, twd, "-15")},
			want: []string{
				"all: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"2026-11: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  card: 1 [TWD out 15 in 0 net -15]",
				"  wallet: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name:     "a fee in another instrument is not part of what moves between the accounts",
			postings: []model.DraftPosting{accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"), accountBook(card, usd, "-1")},
			want: []string{
				"all: 1, own transfers 1 [USD out 1 in 0 net -1]",
				"2026-11: 1, own transfers 1 [USD out 1 in 0 net -1]",
				"  bank: 1 [TWD out 1000 in 0 net -1000]",
				"  card: 1 [USD out 1 in 0 net -1]",
				"  wallet: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarize(t, on(2026, time.November, 14), draftOf(model.KindTransfer, on(2026, time.November, 6), tt.postings...))

			wantDraftLines(t, names, got, tt.want...)
		})
	}
}

func TestWhatMovesBetweenOwnAccountsIsPerTransactionAndAnInstrumentThatNothingMovesInTotalIsLeftOut(t *testing.T) {
	twd, btc := newDraftInstrument("TWD"), newDraftInstrument("BTC")
	cash, bank := newDraftAccount("cash"), newDraftAccount("bank")

	// Two transfers of the same instrument between the same accounts, in opposite ways,
	// and a lunch: each transfer is its own part, and the lunch is what moves.
	got := summarize(t, on(2026, time.November, 14),
		draftOf(model.KindTransfer, on(2026, time.November, 5), accountBook(cash, twd, "-500"), accountBook(bank, twd, "500")),
		draftOf(model.KindTransfer, on(2026, time.November, 6), accountBook(bank, twd, "-300"), accountBook(cash, twd, "300")),
		draftOf(model.KindTransfer, on(2026, time.November, 7), accountBook(cash, btc, "-0.1"), accountBook(bank, btc, "0.1")),
		draftOf(model.KindExpense, on(2026, time.November, 8), accountBook(cash, twd, "-120")),
	)

	// The bitcoin that moved from the cash to the bank is in neither, and so is not an
	// amount of the month nor of all the drafts, but it is one of each account.
	wantDraftLines(t, namesOf(cash, bank), got,
		"all: 4, own transfers 3 [TWD out 120 in 0 net -120]",
		"2026-11: 4, own transfers 3 [TWD out 120 in 0 net -120]",
		"  bank: 3 [BTC out 0 in 0.1 net 0.1, TWD out 300 in 500 net 200]",
		"  cash: 4 [BTC out 0.1 in 0 net -0.1, TWD out 620 in 300 net -320]",
	)
}

func TestAnExchangeOrATradeMovesTwoInstrumentsAndIsNotBetweenOwnAccounts(t *testing.T) {
	twd, usd, btc := newDraftInstrument("TWD"), newDraftInstrument("USD"), newDraftInstrument("BTC")
	cash, bank := newDraftAccount("cash"), newDraftAccount("bank")
	names := namesOf(cash, bank)
	tests := []struct {
		name string
		txn  model.DraftTransaction
		want []string
	}{
		{
			name: "an exchange in one account",
			txn:  draftOf(model.KindExchange, on(2026, time.November, 6), accountBook(bank, twd, "-3200"), accountBook(bank, usd, "100")),
			want: []string{
				"all: 1, own transfers 0 [TWD out 3200 in 0 net -3200, USD out 0 in 100 net 100]",
				"2026-11: 1, own transfers 0 [TWD out 3200 in 0 net -3200, USD out 0 in 100 net 100]",
				"  bank: 1 [TWD out 3200 in 0 net -3200, USD out 0 in 100 net 100]",
			},
		},
		{
			name: "an exchange from one account into another",
			txn:  draftOf(model.KindExchange, on(2026, time.November, 6), accountBook(cash, twd, "-3200"), accountBook(bank, usd, "100")),
			want: []string{
				"all: 1, own transfers 0 [TWD out 3200 in 0 net -3200, USD out 0 in 100 net 100]",
				"2026-11: 1, own transfers 0 [TWD out 3200 in 0 net -3200, USD out 0 in 100 net 100]",
				"  bank: 1 [USD out 0 in 100 net 100]",
				"  cash: 1 [TWD out 3200 in 0 net -3200]",
			},
		},
		{
			name: "a trade",
			txn:  draftOf(model.KindTrade, on(2026, time.November, 6), accountBook(bank, twd, "-300000"), accountBook(cash, btc, "0.1")),
			want: []string{
				"all: 1, own transfers 0 [BTC out 0 in 0.1 net 0.1, TWD out 300000 in 0 net -300000]",
				"2026-11: 1, own transfers 0 [BTC out 0 in 0.1 net 0.1, TWD out 300000 in 0 net -300000]",
				"  bank: 1 [TWD out 300000 in 0 net -300000]",
				"  cash: 1 [BTC out 0 in 0.1 net 0.1]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantDraftLines(t, names, summarize(t, on(2026, time.November, 14), tt.txn), tt.want...)
		})
	}
}

// A fee that is paid in the instrument that comes in is no money moving between own
// accounts: only a transfer is, whatever else comes and goes in an instrument.
func TestOnlyATransferMovesMoneyBetweenOwnAccountsAndAFeeInTheInstrumentThatComesInIsNotOne(t *testing.T) {
	twd := newDraftInstrument("TWD")
	bank, card := newDraftAccount("bank"), newDraftAccount("card")
	names := namesOf(bank, card)
	tests := []struct {
		name string
		txn  model.DraftTransaction
		want []string
	}{
		{
			name: "an income with a fee",
			txn:  draftOf(model.KindIncome, on(2026, time.November, 6), accountBook(bank, twd, "50000"), accountBook(bank, twd, "-30")),
			want: []string{
				"all: 1, own transfers 0 [TWD out 30 in 50000 net 49970]",
				"2026-11: 1, own transfers 0 [TWD out 30 in 50000 net 49970]",
				"  bank: 1 [TWD out 30 in 50000 net 49970]",
			},
		},
		{
			name: "a dealing received with a fee",
			txn:  draftOf(model.KindDealing, on(2026, time.November, 6), accountBook(bank, twd, "1000"), accountBook(card, twd, "-15")),
			want: []string{
				"all: 1, own transfers 0 [TWD out 15 in 1000 net 985]",
				"2026-11: 1, own transfers 0 [TWD out 15 in 1000 net 985]",
				"  bank: 1 [TWD out 0 in 1000 net 1000]",
				"  card: 1 [TWD out 15 in 0 net -15]",
			},
		},
		{
			name: "the conversion of a bill into instalments with a fee",
			txn: draftPlanOrigin(draftOf(model.KindInstalment, on(2026, time.November, 6),
				accountBook(card, twd, "3000"), instalmentBook(card, twd, "-3000"), accountBook(card, twd, "-15"))),
			want: []string{
				"all: 1, own transfers 0 [TWD out 15 in 3000 net 2985]",
				"2026-11: 1, own transfers 0 [TWD out 15 in 3000 net 2985]",
				"  card: 1 [TWD out 15 in 3000 net 2985]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantDraftLines(t, names, summarize(t, on(2026, time.November, 14), tt.txn), tt.want...)
		})
	}
}

func TestATransferThatIsNotDueMovesNothingYetAndIsNotOneOfTheOwnTransfers(t *testing.T) {
	twd := newDraftInstrument("TWD")
	bank, wallet := newDraftAccount("bank"), newDraftAccount("wallet")
	transfer := draftOf(model.KindTransfer, on(2026, time.November, 15), accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"))

	// It is waiting, and of both accounts, and the amounts of nothing have it.
	wantDraftLines(t, namesOf(bank, wallet), summarize(t, on(2026, time.November, 14), transfer),
		"all: 1, own transfers 0 []",
		"2026-11: 1, own transfers 0 []",
		"  bank: 1 []",
		"  wallet: 1 []",
	)
	// From its day on it is what it is.
	wantDraftLines(t, namesOf(bank, wallet), summarize(t, on(2026, time.November, 15), transfer),
		"all: 1, own transfers 1 []",
		"2026-11: 1, own transfers 1 []",
		"  bank: 1 [TWD out 1000 in 0 net -1000]",
		"  wallet: 1 [TWD out 0 in 1000 net 1000]",
	)
}

func TestADraftIsOfEveryAccountItHasAPostingToOfAnyBookAndOnceOfEach(t *testing.T) {
	twd := newDraftInstrument("TWD")
	bank, card, wallet := newDraftAccount("bank"), newDraftAccount("card"), newDraftAccount("wallet")
	names := namesOf(bank, card, wallet)
	tests := []struct {
		name string
		txns []model.DraftTransaction
		want []string
	}{
		{
			name: "the origin of a purchase paid in instalments, whose posting to the card is of the instalment book",
			txns: draftPlan(card, twd, on(2026, time.November, 6), 3000, on(2026, time.December, 5)),
			want: []string{
				"all: 1, own transfers 0 []",
				"2026-11: 1, own transfers 0 []",
				"  card: 1 []",
			},
		},
		{
			name: "the conversion of a bill into instalments, which pays the bill off with a posting to the card of the account book",
			txns: []model.DraftTransaction{draftPlanOrigin(draftOf(model.KindInstalment, on(2026, time.November, 6),
				accountBook(card, twd, "3000"), instalmentBook(card, twd, "-3000")))},
			want: []string{
				"all: 1, own transfers 0 [TWD out 0 in 3000 net 3000]",
				"2026-11: 1, own transfers 0 [TWD out 0 in 3000 net 3000]",
				"  card: 1 [TWD out 0 in 3000 net 3000]",
			},
		},
		{
			name: "two postings to one account are one draft of it",
			txns: []model.DraftTransaction{draftOf(model.KindTransfer, on(2026, time.November, 6),
				accountBook(bank, twd, "-1000"), accountBook(wallet, twd, "1000"), accountBook(bank, twd, "-15"))},
			want: []string{
				"all: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"2026-11: 1, own transfers 1 [TWD out 15 in 0 net -15]",
				"  bank: 1 [TWD out 1015 in 0 net -1015]",
				"  wallet: 1 [TWD out 0 in 1000 net 1000]",
			},
		},
		{
			name: "two drafts of one account are two of it",
			txns: []model.DraftTransaction{
				draftOf(model.KindExpense, on(2026, time.November, 5), accountBook(bank, twd, "-120")),
				draftOf(model.KindExpense, on(2026, time.November, 6), accountBook(bank, twd, "-30")),
			},
			want: []string{
				"all: 2, own transfers 0 [TWD out 150 in 0 net -150]",
				"2026-11: 2, own transfers 0 [TWD out 150 in 0 net -150]",
				"  bank: 2 [TWD out 150 in 0 net -150]",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantDraftLines(t, names, summarize(t, on(2026, time.November, 14), tt.txns...), tt.want...)
		})
	}
}

func TestADraftThatHasNoPostingToAnAccountIsOfNoneWhichComesLastAndHasNoAmounts(t *testing.T) {
	twd := newDraftInstrument("TWD")
	cash := newDraftAccount("cash")

	got := summarize(t, on(2026, time.November, 14),
		draftOf(model.KindExpense, on(2026, time.November, 8)), // an expense that a party paid
		draftOf(model.KindExpense, on(2026, time.November, 9), accountBook(cash, twd, "-120")),
	)

	wantDraftLines(t, namesOf(cash), got,
		"all: 2, own transfers 0 [TWD out 120 in 0 net -120]",
		"2026-11: 2, own transfers 0 [TWD out 120 in 0 net -120]",
		"  cash: 1 [TWD out 120 in 0 net -120]",
		"  (none): 1 []",
	)
}

func TestTheAmountsOfAllTheDraftsAreThoseOfTheMonthsAddedUp(t *testing.T) {
	twd, usd := newDraftInstrument("TWD"), newDraftInstrument("USD")
	cash, bank := newDraftAccount("cash"), newDraftAccount("bank")

	got := summarize(t, on(2026, time.December, 31),
		draftOf(model.KindExpense, on(2026, time.November, 5), accountBook(cash, twd, "-120")),
		draftOf(model.KindIncome, on(2026, time.November, 6), accountBook(bank, twd, "5000")),
		draftOf(model.KindTransfer, on(2026, time.November, 7), accountBook(bank, twd, "-700"), accountBook(cash, twd, "700")),
		draftOf(model.KindExpense, on(2026, time.December, 5), accountBook(cash, twd, "-30")),
		draftOf(model.KindExpense, on(2026, time.December, 6), accountBook(bank, usd, "-1")),
	)

	wantDraftLines(t, namesOf(cash, bank), got,
		"all: 5, own transfers 1 [TWD out 150 in 5000 net 4850, USD out 1 in 0 net -1]",
		"2026-12: 2, own transfers 0 [TWD out 30 in 0 net -30, USD out 1 in 0 net -1]",
		"  bank: 1 [USD out 1 in 0 net -1]",
		"  cash: 1 [TWD out 30 in 0 net -30]",
		"2026-11: 3, own transfers 1 [TWD out 120 in 5000 net 4880]",
		"  bank: 2 [TWD out 700 in 5000 net 4300]",
		"  cash: 2 [TWD out 120 in 700 net 580]",
	)
}

func TestTheSummaryIsInTheOrderOfMonthsAccountNamesAndInstrumentCodesWhateverTheOrderOfTheDrafts(t *testing.T) {
	// The ids of the instruments are made in the order BTC, USD, TWD, USDT and twd, and
	// those of the accounts as they are written below, so that no order of ids is the
	// order of the names or of the codes. USD comes before USDT, and TWD before twd, as
	// bytes do.
	btc, usd, twd, usdt, lower := newDraftInstrument("BTC"), newDraftInstrument("USD"), newDraftInstrument("TWD"), newDraftInstrument("USDT"), newDraftInstrument("twd")
	wallet, card, bank := newDraftAccount("wallet"), newDraftAccount("card"), newDraftAccount("bank")
	// Two accounts of one name, which only their ids tell apart: the lesser id first.
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	cashLow, cashHigh := draftAccount{id: ids[0], name: "cash"}, draftAccount{id: ids[1], name: "cash"}
	names := namesOf(wallet, card, bank)
	names[cashLow.id], names[cashHigh.id] = "cash (lesser id)", "cash (greater id)"

	txns := []model.DraftTransaction{
		draftOf(model.KindExpense, on(2026, time.November, 1), accountBook(wallet, usdt, "-1")),
		draftOf(model.KindExpense, on(2026, time.November, 2), accountBook(wallet, lower, "-2")),
		draftOf(model.KindExpense, on(2026, time.November, 3), accountBook(wallet, usd, "-3")),
		draftOf(model.KindExpense, on(2026, time.November, 4), accountBook(wallet, twd, "-4")),
		draftOf(model.KindExpense, on(2026, time.November, 5), accountBook(wallet, btc, "-5")),
		draftOf(model.KindExpense, on(2026, time.November, 6), accountBook(cashHigh, twd, "-1")),
		draftOf(model.KindExpense, on(2026, time.November, 7), accountBook(card, twd, "-1")),
		draftOf(model.KindExpense, on(2026, time.November, 8), accountBook(cashLow, twd, "-1")),
		draftOf(model.KindExpense, on(2026, time.November, 9), accountBook(bank, twd, "-1")),
		draftOf(model.KindExpense, on(2026, time.November, 10)),
		draftOf(model.KindExpense, on(2027, time.January, 2), accountBook(bank, twd, "-1")),
		draftOf(model.KindExpense, on(2025, time.December, 31), accountBook(bank, twd, "-1")),
	}
	want := []string{
		"all: 12, own transfers 0 [BTC out 5 in 0 net -5, TWD out 10 in 0 net -10, USD out 3 in 0 net -3, USDT out 1 in 0 net -1, twd out 2 in 0 net -2]",
		"2027-01: 1, own transfers 0 [TWD out 1 in 0 net -1]",
		"  bank: 1 [TWD out 1 in 0 net -1]",
		"2026-11: 10, own transfers 0 [BTC out 5 in 0 net -5, TWD out 8 in 0 net -8, USD out 3 in 0 net -3, USDT out 1 in 0 net -1, twd out 2 in 0 net -2]",
		"  bank: 1 [TWD out 1 in 0 net -1]",
		"  card: 1 [TWD out 1 in 0 net -1]",
		"  cash (lesser id): 1 [TWD out 1 in 0 net -1]",
		"  cash (greater id): 1 [TWD out 1 in 0 net -1]",
		"  wallet: 5 [BTC out 5 in 0 net -5, TWD out 4 in 0 net -4, USD out 3 in 0 net -3, USDT out 1 in 0 net -1, twd out 2 in 0 net -2]",
		"  (none): 1 []",
		"2025-12: 1, own transfers 0 [TWD out 1 in 0 net -1]",
		"  bank: 1 [TWD out 1 in 0 net -1]",
	}

	today := on(2027, time.January, 2) // the day of the last of the drafts: all of them are due
	got := summarize(t, today, txns...)
	wantDraftLines(t, names, got, want...)
	for _, m := range got.Months {
		if m.Month.Day() != 1 || m.Month.Location() != time.UTC || m.Month.Hour() != 0 {
			t.Errorf("month %s is not the first day of a month at midnight UTC", m.Month)
		}
	}

	// The order the repo reads the drafts in is of no account.
	reversed := slices.Clone(txns)
	slices.Reverse(reversed)
	wantDraftLines(t, names, summarize(t, today, reversed...), want...)
}

func TestAPeriodWhoseOriginIsNotAmongTheDraftsIsMalformed(t *testing.T) {
	twd := newDraftInstrument("TWD")
	card := newDraftAccount("card")
	origin := draftPlanOrigin(draftOf(model.KindExpense, on(2026, time.November, 14), instalmentBook(card, twd, "-1000")))
	period := draftPlanPeriod(origin, 1, on(2026, time.December, 5), instalmentBook(card, twd, "1000"), accountBook(card, twd, "-1000"))
	noPlan := period
	noPlan.PlanID = nil

	for name, txns := range map[string][]model.DraftTransaction{
		"a period whose origin is not a draft": {period},
		"a period of no plan":                  {origin, noPlan},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := model.SummarizeDrafts(txns, on(2026, time.November, 14)); !errors.Is(err, model.ErrMalformedPlan) {
				t.Errorf("err = %v, want ErrMalformedPlan", err)
			}
		})
	}
}
