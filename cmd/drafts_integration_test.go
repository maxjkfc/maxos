package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// The drafts that are waiting, through the real use cases on a real PostgreSQL: what the
// summary says of them, and that the list says the same.

// draftsApp is what the tests of the drafts use besides the ledger app.
type draftsApp struct {
	fx.In

	GetDraftSummary    usecase.GetDraftSummary
	GetPreviewBalances usecase.GetPreviewBalances
	RecordIncome       usecase.RecordIncome
	RecordExchange     usecase.RecordExchange
}

// draftsWorld is the ledger with what the tests of the drafts refer to: four accounts,
// categories, two parties, US dollars, and a clock that the test moves, which starts on the
// 14th of November 2026.
type draftsWorld struct {
	ledgerApp
	draftsApp
	t   *testing.T
	clk *testClock

	cash, bank, wallet, card uuid.UUID
	food, fees, salary       uuid.UUID
	amy, bob, usd            uuid.UUID

	names map[uuid.UUID]string
	keys  int
}

func newDraftsWorld(t *testing.T) *draftsWorld {
	t.Helper()
	ctx := context.Background()
	clk := newTestClock(day)
	var (
		l     ledgerApp
		reads draftsApp
	)
	app := fx.New(
		fx.NopLogger,
		fx.Supply(testpg.New(t)),
		fx.Provide(func() financeclock.Clock { return clk }),
		cmd.CatalogModule(),
		cmd.FinanceModule(),
		fx.Populate(&l, &reads),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the modules: %v", err)
	}
	w := &draftsWorld{ledgerApp: l, draftsApp: reads, t: t, clk: clk, names: map[uuid.UUID]string{model.BaseCurrencyID: "TWD"}}

	account := func(name, kind string) uuid.UUID {
		a, err := w.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: name, Kind: kind})
		if err != nil {
			t.Fatalf("CreateAccount(%q): %v", name, err)
		}
		w.names[a.ID] = name
		return a.ID
	}
	category := func(direction, name string) uuid.UUID {
		c, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: direction, Name: name})
		if err != nil {
			t.Fatalf("CreateCategory(%q): %v", name, err)
		}
		return c.ID
	}
	party := func(name string) uuid.UUID {
		p, err := w.CreateParty.CreateParty(ctx, catalogusecase.CreatePartyInput{Name: name})
		if err != nil {
			t.Fatalf("CreateParty(%q): %v", name, err)
		}
		return p.ID
	}
	w.cash, w.bank, w.wallet, w.card = account("cash", "cash"), account("bank", "bank"), account("wallet", "wallet"), account("card", "card")
	w.food, w.fees, w.salary = category("expense", "Food"), category("expense", "Fees"), category("income", "Salary")
	w.amy, w.bob = party("Amy"), party("Bob")
	usd, err := w.CreateInstrument.CreateInstrument(ctx, usecase.CreateInstrumentInput{Code: "USD", Name: "US dollar", Kind: "fiat", IsCurrency: true, Decimals: 2})
	if err != nil {
		t.Fatalf("CreateInstrument: %v", err)
	}
	w.usd = usd.ID
	w.names[w.usd] = "USD"
	return w
}

// key makes an idempotency key that has not been used.
func (w *draftsWorld) key() string {
	w.keys++
	return fmt.Sprintf("drafts-e2e-%04d", w.keys)
}

// recorded is the details of a record on the day, with a key of its own.
func (w *draftsWorld) recorded(on time.Time) usecase.RecordDetails {
	d := w.details(w.key())
	d.OccurredOn = on
	return d
}

// expense records an expense of the amount of TWD on food, paid from the account.
func (w *draftsWorld) expense(on time.Time, account uuid.UUID, amount string) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: w.recorded(on), AccountID: &account, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: w.food, Amount: dec(amount)}},
	})
	if err != nil {
		w.t.Fatalf("RecordExpense: %v", err)
	}
	return out.Transaction
}

// paidByParty records an expense of the amount of TWD on food that the party paid for me,
// which no account is of.
func (w *draftsWorld) paidByParty(on time.Time, party uuid.UUID, amount string) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: w.recorded(on), PaidByPartyID: &party, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: w.food, Amount: dec(amount)}},
	})
	if err != nil {
		w.t.Fatalf("RecordExpense paid by a party: %v", err)
	}
	return out.Transaction
}

// transfer records a transfer of the amount of TWD between two accounts, with the fees.
func (w *draftsWorld) transfer(on time.Time, from, to uuid.UUID, amount string, fees ...usecase.Fee) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordTransfer.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: w.recorded(on), FromAccountID: from, ToAccountID: to, InstrumentID: model.BaseCurrencyID, Amount: dec(amount), Fees: fees,
	})
	if err != nil {
		w.t.Fatalf("RecordTransfer: %v", err)
	}
	return out.Transaction
}

// fee is a fee of the amount of TWD that the account pays.
func (w *draftsWorld) fee(account uuid.UUID, amount string) usecase.Fee {
	return usecase.Fee{AccountID: account, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec(amount)}
}

// income records an income of the amount of TWD, as a salary, into the account.
func (w *draftsWorld) income(on time.Time, account uuid.UUID, amount string) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordIncome.RecordIncome(context.Background(), usecase.RecordIncomeInput{
		RecordDetails: w.recorded(on), AccountID: account, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: w.salary, Amount: dec(amount)}},
	})
	if err != nil {
		w.t.Fatalf("RecordIncome: %v", err)
	}
	return out.Transaction
}

// exchange records 3,200 TWD from the first account changed into 100 USD in the second.
func (w *draftsWorld) exchange(on time.Time, from, to uuid.UUID) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordExchange.RecordExchange(context.Background(), usecase.RecordExchangeInput{
		RecordDetails: w.recorded(on), FromAccountID: from, ToAccountID: to,
		FromInstrumentID: model.BaseCurrencyID, ToInstrumentID: w.usd, FromAmount: dec("3200"), ToAmount: dec("100"),
	})
	if err != nil {
		w.t.Fatalf("RecordExchange: %v", err)
	}
	return out.Transaction
}

// evenSchedule is a schedule that splits the principal into as many equal periods as there
// are days, each falling due on one of them.
func evenSchedule(principal string, due ...time.Time) usecase.InstalmentSchedule {
	periods := make([]usecase.InstalmentPeriod, len(due))
	for i, d := range due {
		periods[i] = usecase.InstalmentPeriod{DueOn: d, Principal: dec(principal).Div(decimal.NewFromInt(int64(len(due))))}
	}
	return usecase.InstalmentSchedule{Periods: periods}
}

// plan records a purchase of food on the account of the principal in TWD, paid in as many
// periods as there are days, and returns its origin, a draft.
func (w *draftsWorld) plan(on time.Time, account uuid.UUID, principal string, due ...time.Time) usecase.Transaction {
	w.t.Helper()
	s := evenSchedule(principal, due...)
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: w.recorded(on), AccountID: &account, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: w.food, Amount: dec(principal)}},
		Instalments: &s,
	})
	if err != nil {
		w.t.Fatalf("RecordExpense in instalments: %v", err)
	}
	return out.Transaction
}

// billInstalment records the conversion of 3,000 TWD that the account owes into three
// periods of 1,000, and returns its origin, a draft.
func (w *draftsWorld) billInstalment(on time.Time, account uuid.UUID, due ...time.Time) usecase.Transaction {
	w.t.Helper()
	out, err := w.RecordBillInstalment.RecordBillInstalment(context.Background(), usecase.RecordBillInstalmentInput{
		RecordDetails: w.recorded(on), AccountID: account, InstrumentID: model.BaseCurrencyID, Principal: dec("3000"), Schedule: evenSchedule("3000", due...),
	})
	if err != nil {
		w.t.Fatalf("RecordBillInstalment: %v", err)
	}
	return out.Transaction
}

// summary reads the summary of the drafts, and fails the test when it cannot.
func (w *draftsWorld) summary() usecase.GetDraftSummaryOutput {
	w.t.Helper()
	out, err := w.GetDraftSummary.GetDraftSummary(context.Background(), usecase.GetDraftSummaryInput{})
	if err != nil {
		w.t.Fatalf("GetDraftSummary: %v", err)
	}
	return out
}

// amountsText writes amounts as "TWD out 120 in 0 net -120", one for each instrument, in
// the order they come.
func (w *draftsWorld) amountsText(amounts []usecase.DraftAmount) string {
	parts := make([]string, len(amounts))
	for i, a := range amounts {
		parts[i] = fmt.Sprintf("%s out %s in %s net %s", w.names[a.InstrumentID], a.Outflow, a.Inflow, a.Net)
	}
	return strings.Join(parts, ", ")
}

// summaryLines writes the summary: a line for all the drafts, and for each month, the
// newest first, one with how many happened in it, how many of them move money between my own
// accounts and what they would move, and under it a line for each account in the order they
// come, the drafts of none as "(none)".
func (w *draftsWorld) summaryLines(out usecase.GetDraftSummaryOutput) []string {
	lines := []string{fmt.Sprintf("all: %d, own transfers %d [%s]", out.Count, out.OwnTransfers, w.amountsText(out.Amounts))}
	for _, m := range out.Months {
		lines = append(lines, fmt.Sprintf("%s: %d, own transfers %d [%s]", m.Month.Format("2006-01"), m.Count, m.OwnTransfers, w.amountsText(m.Amounts)))
		for _, a := range m.Accounts {
			name := "(none)"
			if a.AccountID != nil {
				name = w.names[*a.AccountID]
			}
			lines = append(lines, fmt.Sprintf("  %s: %d [%s]", name, a.Count, w.amountsText(a.Amounts)))
		}
	}
	return lines
}

// listedDrafts lists the drafts of the month, of the account when one is given, of none when
// none is true, and of all the accounts otherwise; with no month, of every month. It leaves the
// periods of plans out, as the summary counts a plan once, by its origin. It fails the test when
// it cannot, or when the page is not all there is.
func (w *draftsWorld) listedDrafts(month *time.Time, account *uuid.UUID, none bool) []usecase.Transaction {
	w.t.Helper()
	in := usecase.ListTransactionsInput{Status: "draft", ExcludePlanPeriods: true, AccountID: account, WithoutAccount: none, Limit: usecase.MaxListLimit}
	if month != nil {
		last := model.LastDayOf(*month)
		in.From, in.To = month, &last
	}
	out, err := w.ListTransactions.ListTransactions(context.Background(), in)
	if err != nil {
		w.t.Fatalf("ListTransactions of the drafts %+v: %v", in, err)
	}
	if out.HasMore {
		w.t.Fatalf("ListTransactions of the drafts %+v: more than a page", in)
	}
	return out.Transactions
}

// wantListsAgree fails the test unless every count of the summary is the number of drafts that
// the list finds for it: for all the drafts, for each month, and for each account of the month
// and for none, the drafts of that month of that account or of no account.
func (w *draftsWorld) wantListsAgree(summary usecase.GetDraftSummaryOutput) {
	w.t.Helper()
	if got := len(w.listedDrafts(nil, nil, false)); got != summary.Count {
		w.t.Errorf("the summary counts %d drafts, and the list of the drafts has %d", summary.Count, got)
	}
	for _, m := range summary.Months {
		month := m.Month
		if got := len(w.listedDrafts(&month, nil, false)); got != m.Count {
			w.t.Errorf("%s: the summary counts %d drafts, and the list has %d", month.Format("2006-01"), m.Count, got)
		}
		for _, a := range m.Accounts {
			name := "no account"
			if a.AccountID != nil {
				name = w.names[*a.AccountID]
			}
			if got := len(w.listedDrafts(&month, a.AccountID, a.AccountID == nil)); got != a.Count {
				w.t.Errorf("%s, %s: the summary counts %d drafts, and the list has %d", month.Format("2006-01"), name, a.Count, got)
			}
		}
	}
}

// balanceKey is an account and an instrument.
type balanceKey struct{ account, instrument uuid.UUID }

// balances is what each account holds of each instrument today, by the clock.
func (w *draftsWorld) balances() map[balanceKey]decimal.Decimal {
	w.t.Helper()
	out, err := w.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{})
	if err != nil {
		w.t.Fatalf("GetBalances: %v", err)
	}
	held := make(map[balanceKey]decimal.Decimal, len(out.Balances))
	for _, b := range out.Balances {
		held[balanceKey{b.AccountID, b.InstrumentID}] = b.Quantity
	}
	return held
}

// confirmDrafts confirms every draft there is, as the list of the drafts has them, in one call.
func (w *draftsWorld) confirmDrafts() {
	w.t.Helper()
	drafts := w.listedDrafts(nil, nil, false)
	ids := make([]uuid.UUID, len(drafts))
	for i, d := range drafts {
		ids[i] = d.ID
	}
	if _, err := w.Confirm.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: ids}); err != nil {
		w.t.Fatalf("ConfirmTransactions of every draft listed: %v", err)
	}
}

// Five things were recorded: an expense of 750 from an account, a transfer of 3,000 between two
// others, a purchase on the card paid in instalments (its origin and 3 periods of 2,000), and
// two expenses that parties paid. They were shown as 8 drafts, with 9,750 going out and 6,750 less
// in all: the three periods and the transfer were taken for what they are not.
func TestFiveRecordedItemsAreFiveDraftsAndOnlyWhatChangesABalanceTodayIsInTheAmountsThroughTheRealUseCases(t *testing.T) {
	w := newDraftsWorld(t)
	w.expense(dateOn(2026, time.November, 5), w.cash, "750")
	w.transfer(dateOn(2026, time.November, 6), w.bank, w.wallet, "3000")
	w.plan(dateOn(2026, time.November, 10), w.card, "6000", dateOn(2026, time.December, 5), dateOn(2027, time.January, 5), dateOn(2027, time.February, 5))
	w.paidByParty(dateOn(2026, time.November, 8), w.amy, "90")
	w.paidByParty(dateOn(2026, time.November, 9), w.bob, "40")

	// On the 14th of November the plan is one draft, of the card, in the month of its origin,
	// and moves nothing yet; the transfer is in the accounts it moves, and in no total.
	got := w.summary()
	wantStatus(t, "the five items, on the 14th of November", w.summaryLines(got),
		"all: 5, own transfers 1 [TWD out 750 in 0 net -750]",
		"2026-11: 5, own transfers 1 [TWD out 750 in 0 net -750]",
		"  bank: 1 [TWD out 3000 in 0 net -3000]",
		"  card: 1 []",
		"  cash: 1 [TWD out 750 in 0 net -750]",
		"  wallet: 1 [TWD out 0 in 3000 net 3000]",
		"  (none): 2 []",
	)
	w.wantListsAgree(got)

	// On the 5th of December the first period of the plan falls due: the card is charged 2,000
	// on that day, whatever the month the plan is in, and the plan is still one draft.
	w.clk.setDay(dateOn(2026, time.December, 5))
	got = w.summary()
	wantStatus(t, "the five items, on the 5th of December", w.summaryLines(got),
		"all: 5, own transfers 1 [TWD out 2750 in 0 net -2750]",
		"2026-11: 5, own transfers 1 [TWD out 2750 in 0 net -2750]",
		"  bank: 1 [TWD out 3000 in 0 net -3000]",
		"  card: 1 [TWD out 2000 in 0 net -2000]",
		"  cash: 1 [TWD out 750 in 0 net -750]",
		"  wallet: 1 [TWD out 0 in 3000 net 3000]",
		"  (none): 2 []",
	)
	w.wantListsAgree(got)

	// Confirming every draft there is changes each balance by what the summary said it would, on
	// the day it was asked: the net of each account, and, as the transfer nets to nothing, of all
	// the accounts together what the amounts of all the drafts say.
	before := w.balances()
	w.confirmDrafts()
	after := w.balances()
	said := map[balanceKey]decimal.Decimal{}
	var total decimal.Decimal
	for _, m := range got.Months {
		for _, a := range m.Accounts {
			for _, amount := range a.Amounts {
				k := balanceKey{*a.AccountID, amount.InstrumentID}
				said[k] = said[k].Add(amount.Net)
				total = total.Add(amount.Net)
			}
		}
	}
	for k := range said {
		if changed := after[k].Sub(before[k]); !changed.Equal(said[k]) {
			t.Errorf("%s changed by %s once the drafts were confirmed, and the summary said %s", w.names[k.account], changed, said[k])
		}
	}
	if len(got.Amounts) != 1 || !got.Amounts[0].Net.Equal(total) {
		t.Errorf("the amounts of all the drafts are %+v, want a net of %s, which is what the accounts change by", got.Amounts, total)
	}

	// Nothing is waiting any more: the plan was confirmed as a whole, and its periods with it.
	if got = w.summary(); got.Count != 0 || got.OwnTransfers != 0 || len(got.Months) != 0 || len(got.Amounts) != 0 {
		t.Errorf("summary = %+v, want nothing waiting once every draft is confirmed", got)
	}
}

// The counts of the summary are the numbers of drafts that the list of the drafts finds: for a
// month, for an account of it, and for no account. Drafts of every shape: a plan in instalments
// and the conversion of a bill, whose periods are no drafts of their own, a transfer whose fee a
// third account pays, an exchange, an income, expenses that parties paid, and some in other
// months, one of them dated after today.
func TestEveryCountOfTheSummaryIsTheNumberOfDraftsTheListFindsThroughTheRealUseCases(t *testing.T) {
	w := newDraftsWorld(t)
	w.expense(dateOn(2026, time.October, 30), w.cash, "80")
	w.expense(dateOn(2026, time.November, 5), w.cash, "120")
	w.transfer(dateOn(2026, time.November, 6), w.bank, w.wallet, "1000", w.fee(w.card, "15"))
	w.transfer(dateOn(2026, time.November, 7), w.wallet, w.cash, "500", w.fee(w.wallet, "10"))
	w.income(dateOn(2026, time.November, 3), w.bank, "50000")
	w.exchange(dateOn(2026, time.November, 9), w.bank, w.cash)
	w.paidByParty(dateOn(2026, time.November, 8), w.amy, "90")
	w.paidByParty(dateOn(2026, time.December, 1), w.bob, "40")
	periods := make([]time.Time, 12)
	for i := range periods {
		periods[i] = dateOn(2026, time.Month(12+i), 5)
	}
	origin := w.plan(dateOn(2026, time.November, 10), w.card, "12000", periods...)
	w.billInstalment(dateOn(2026, time.November, 16), w.bank, dateOn(2026, time.November, 20), dateOn(2026, time.December, 20), dateOn(2027, time.January, 20))
	w.expense(dateOn(2026, time.December, 20), w.card, "60") // after today

	got := w.summary()
	w.wantListsAgree(got)
	if got.Count != 11 || len(got.Months) != 3 {
		t.Fatalf("%d drafts in %d months, want 11 in 3: the two plans are one draft each, whatever their periods, in the months of their origins", got.Count, len(got.Months))
	}

	// Confirming the plan through its origin takes the whole plan out of the summary and of the
	// list: its periods, which are no drafts of their own, go with it.
	if _, err := w.Confirm.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{origin.ID}}); err != nil {
		t.Fatalf("ConfirmTransactions of the origin: %v", err)
	}
	after := w.summary()
	if after.Count != got.Count-1 {
		t.Errorf("%d drafts once the plan is confirmed, want %d", after.Count, got.Count-1)
	}
	w.wantListsAgree(after)
	if inPlan, err := w.ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{Status: "draft", PlanID: &origin.ID}); err != nil || len(inPlan.Transactions) != 0 {
		t.Errorf("drafts of the plan = %d, %v, want none: the plan is confirmed with its periods", len(inPlan.Transactions), err)
	}
}

// A plan is its origin in the list of the drafts, as it is in the summary, when the list is
// asked to leave the periods of plans out; the status draft alone lists them too, which are
// drafts, each on its own day, and so does the list of the plan. A list is of an account or of no
// account, not both.
func TestAListOfDraftsHasAPlanAsItsOriginOnlyWhenAskedToLeaveThePeriodsOutThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newDraftsWorld(t)
	periods := make([]time.Time, 12)
	for i := range periods {
		periods[i] = dateOn(2026, time.Month(12+i), 5)
	}
	origin := w.plan(dateOn(2026, time.November, 14), w.card, "12000", periods...)
	lunch := w.expense(dateOn(2026, time.November, 14), w.cash, "120")

	list := func(in usecase.ListTransactionsInput) []uuid.UUID {
		t.Helper()
		in.Limit = usecase.MaxListLimit
		out, err := w.ListTransactions.ListTransactions(ctx, in)
		if err != nil {
			t.Fatalf("ListTransactions(%+v): %v", in, err)
		}
		return ids(out.Transactions...)
	}
	sorted := func(ids ...uuid.UUID) []uuid.UUID {
		slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
		return ids
	}
	december := func(in usecase.ListTransactionsInput) usecase.ListTransactionsInput {
		in.From, in.To = new(dateOn(2026, time.December, 1)), new(dateOn(2026, time.December, 31))
		return in
	}

	// Asked to leave the periods out, the list of the drafts is what the summary counts: the plan by
	// its origin, and the lunch.
	drafts := list(usecase.ListTransactionsInput{Status: "draft", ExcludePlanPeriods: true})
	if !slices.Equal(sorted(drafts...), sorted(origin.ID, lunch.ID)) {
		t.Errorf("the drafts, periods left out = %d transactions, want the origin of the plan and the lunch: a plan is one draft", len(drafts))
	}
	if got := list(usecase.ListTransactionsInput{ExcludePlanPeriods: true}); len(got) != 2 {
		t.Errorf("the transactions, periods left out = %d, want the origin of the plan and the lunch", len(got))
	}
	if got := list(usecase.ListTransactionsInput{PlanID: &origin.ID, ExcludePlanPeriods: true}); !slices.Equal(got, []uuid.UUID{origin.ID}) {
		t.Errorf("the transactions of the plan, periods left out = %v, want its origin", got)
	}
	if got := list(december(usecase.ListTransactionsInput{Status: "draft", ExcludePlanPeriods: true})); len(got) != 0 {
		t.Errorf("the drafts of December, periods left out = %d, want none: the origin of the plan is of November", len(got))
	}

	// The status draft alone does not leave them out: a period of a draft plan is a draft of its
	// day, and the list says so, in the month it falls due in.
	if got := list(usecase.ListTransactionsInput{Status: "draft"}); len(got) != 14 {
		t.Errorf("the drafts = %d transactions, want the origin of the plan, its 12 periods and the lunch", len(got))
	}
	if got := list(december(usecase.ListTransactionsInput{Status: "draft"})); len(got) != 1 {
		t.Errorf("the drafts of December = %d transactions, want the first period of the plan, which falls due on the 5th", len(got))
	}
	if got := list(usecase.ListTransactionsInput{Status: "draft", PlanID: &origin.ID}); len(got) != 13 {
		t.Errorf("the drafts of the plan = %d transactions, want its origin and its 12 periods", len(got))
	}
	if got := list(usecase.ListTransactionsInput{PlanID: &origin.ID}); len(got) != 13 {
		t.Errorf("the transactions of the plan = %d, want its origin and its 12 periods", len(got))
	}
	if got := list(usecase.ListTransactionsInput{}); len(got) != 14 {
		t.Errorf("the transactions = %d, want all 14: the periods are transactions like the others", len(got))
	}
	// The origin of the plan has a posting to the card, of the instalment book, and so has each
	// period, so the card has them all; the lunch, paid from the cash, is not listed for it, nor
	// for no account.
	if got := list(usecase.ListTransactionsInput{Status: "draft", AccountID: &w.card}); len(got) != 13 {
		t.Errorf("the drafts of the card = %d, want the origin of the plan and its 12 periods", len(got))
	}
	if got := list(usecase.ListTransactionsInput{Status: "draft", AccountID: &w.card, ExcludePlanPeriods: true}); !slices.Equal(got, []uuid.UUID{origin.ID}) {
		t.Errorf("the drafts of the card, periods left out = %v, want the origin of the plan", got)
	}
	if got := list(usecase.ListTransactionsInput{Status: "draft", WithoutAccount: true, ExcludePlanPeriods: true}); len(got) != 0 {
		t.Errorf("the drafts of no account = %v, want none", got)
	}

	_, err := w.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{Status: "draft", AccountID: &w.card, WithoutAccount: true})
	if !errors.Is(err, model.ErrConflictingFilters) {
		t.Errorf("a list of an account and of no account: err = %v, want ErrConflictingFilters", err)
	}
}
