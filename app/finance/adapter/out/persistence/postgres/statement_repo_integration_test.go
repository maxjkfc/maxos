package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// The statements of a card are read from the confirmed account postings to it. These
// tests are of the card "visa", which closes its statement on the 5th and is paid
// by the 20th: the statement of the 6th of October to the 5th of November, and the
// one of the 6th of November to the 5th of December, are the ones they read. The
// repo is given the days of a statement and does not look at the closing day: that
// is the model's.

// statementEnv is a ledgerEnv with a card, and a clock that tests move by naming
// the day they read as of.
type statementEnv struct {
	*ledgerEnv
	visa model.Account
}

func newStatementEnv(t *testing.T) *statementEnv {
	t.Helper()
	e := newLedgerEnv(t)
	return &statementEnv{ledgerEnv: e, visa: insertCard(t, postgres.NewAccountRepo(e.pool), "visa", new(5), new(20))}
}

// names says what to call each id in what the tests print: the fixtures of the
// ledger, and the visa.
func (e *statementEnv) names() map[uuid.UUID]string {
	names := e.ledgerEnv.names()
	names[e.visa.ID] = "visa"
	return names
}

// holds says what the visa holds on the day, one balance to a line, as "visa TWD
// -1200", with what is unbilled when unbilled is set.
func (e *statementEnv) holds(day time.Time, unbilled bool) []string {
	e.t.Helper()
	got, err := e.txns.Balances(context.Background(), repo.BalanceFilter{AccountID: &e.visa.ID, Day: day})
	if err != nil {
		e.t.Fatalf("Balances: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got))
	for i, b := range got {
		lines[i] = fmt.Sprintf("%s %s %s", names[b.AccountID], names[b.InstrumentID], b.Quantity)
		if unbilled {
			lines[i] += fmt.Sprintf(" unbilled %s", b.UnbilledInstalment)
		}
	}
	return lines
}

// expenseOn makes the draft of an expense of the amount TWD on the visa, dated the
// day, with the posting date.
func (e *statementEnv) expenseOn(key string, day time.Time, amount string, postedOn *time.Time) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.header(key, day), Account: &e.visa, Instrument: e.twd, BaseCurrency: e.twd,
		Lines:    []model.CategoryLine{{Category: e.food, Amount: dec(amount)}},
		PostedOn: postedOn,
	})
	if err != nil {
		e.t.Fatalf("NewExpense: %v", err)
	}
	return txn
}

// spend stores an expense on the visa and confirms it. The key is the name of the
// transaction in what the tests print.
func (e *statementEnv) spend(key string, day time.Time, amount string) model.Transaction {
	e.t.Helper()
	return e.storeConfirmed(e.expenseOn(key, day, amount, nil))
}

// spendPosted is spend with a posting date.
func (e *statementEnv) spendPosted(key string, day time.Time, amount string, postedOn time.Time) model.Transaction {
	e.t.Helper()
	return e.storeConfirmed(e.expenseOn(key, day, amount, &postedOn))
}

// draft stores an expense on the visa that is not confirmed.
func (e *statementEnv) draft(key string, day time.Time, amount string) model.Transaction {
	e.t.Helper()
	return e.store(e.expenseOn(key, day, amount, nil))
}

// payment stores a payment to the visa from the bank, a transfer into it, and
// confirms it.
func (e *statementEnv) payment(key string, day time.Time, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewTransfer(model.TransferParams{
		Header: e.header(key, day), From: e.bank, To: e.visa, Instrument: e.twd, Amount: dec(amount), BaseCurrency: e.twd,
	})
	if err != nil {
		e.t.Fatalf("NewTransfer: %v", err)
	}
	return e.storeConfirmed(txn)
}

// setPostedOn gives the transaction the posting date, or takes it away with nil, as
// the use case does: with the transaction locked, in a database transaction.
func (e *statementEnv) setPostedOn(id uuid.UUID, postedOn *time.Time) (model.Transaction, error) {
	var got model.Transaction
	err := e.inTx(func(ctx context.Context) error {
		if _, err := e.txns.GetForUpdate(ctx, id); err != nil {
			return err
		}
		var err error
		got, err = e.txns.SetPostedOn(ctx, id, postedOn)
		return err
	})
	return got, err
}

// statementOf is the statement of the visa that has the day.
func statementOf(day time.Time) model.StatementPeriod {
	return model.StatementContaining(5, day)
}

// figures says what each statement of the visa has, as of today, one list for each
// statement and a line for each instrument in it, as "TWD previous charges credits
// closing".
func (e *statementEnv) figures(today time.Time, periods ...model.StatementPeriod) [][]string {
	e.t.Helper()
	got, err := e.txns.StatementFigures(context.Background(), e.visa.ID, periods, today)
	if err != nil {
		e.t.Fatalf("StatementFigures: %v", err)
	}
	if len(got) != len(periods) {
		e.t.Fatalf("StatementFigures returned %d lists for %d statements", len(got), len(periods))
	}
	return e.formatFigures(got)
}

func (e *statementEnv) formatFigures(figures [][]model.StatementFigures) [][]string {
	names := e.names()
	out := make([][]string, len(figures))
	for i, list := range figures {
		out[i] = []string{}
		for _, f := range list {
			out[i] = append(out[i], fmt.Sprintf("%s %s %s %s %s", names[f.InstrumentID], f.Previous, f.Charges, f.Credits, f.Closing))
		}
	}
	return out
}

func wantFigures(t *testing.T, what string, got [][]string, want ...[]string) {
	t.Helper()
	if len(want) == 0 {
		want = [][]string{}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

// statement reads the statement of the visa that has the day, as of today.
func (e *statementEnv) statement(day, today time.Time) repo.StatementContent {
	e.t.Helper()
	got, err := e.txns.Statement(context.Background(), repo.StatementFilter{AccountID: e.visa.ID, Period: statementOf(day), Today: today})
	if err != nil {
		e.t.Fatalf("Statement: %v", err)
	}
	return got
}

// keys names the transactions, in the order they are in.
func keys(txns []model.Transaction) []string {
	out := make([]string, len(txns))
	for i, txn := range txns {
		out[i] = txn.IdempotencyKey
	}
	return out
}

func TestAStatementHasItsFirstAndLastDayAndNotTheDaysAroundIt(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("day-before-1", on(2026, time.October, 5), "1")      // the closing date of the one before
	e.spend("first-day-10", on(2026, time.October, 6), "10")     // the day after it
	e.spend("last-day-100", on(2026, time.November, 5), "100")   // the closing date
	e.spend("next-day-1000", on(2026, time.November, 6), "1000") // the day after it
	today := on(2026, time.December, 20)

	got := e.figures(today, statementOf(on(2026, time.October, 5)), statementOf(on(2026, time.October, 6)), statementOf(on(2026, time.November, 6)))

	wantFigures(t, "the statements closing on the 5th of October, November and December", got,
		[]string{"TWD 0 1 0 -1"},          // the 5th of October closes it, with nothing before
		[]string{"TWD -1 110 0 -111"},     // the 6th of October and the 5th of November are on it, and not the 6th of November
		[]string{"TWD -111 1000 0 -1111"}, // the 6th of November starts the next
	)
	if got := keys(e.statement(on(2026, time.October, 20), today).Transactions); !slices.Equal(got, []string{"first-day-10", "last-day-100"}) {
		t.Errorf("transactions of the statement of the 6th of October = %q, want the first day and the closing date, oldest first", got)
	}
	if got := keys(e.statement(on(2026, time.November, 20), today).Transactions); !slices.Equal(got, []string{"next-day-1000"}) {
		t.Errorf("transactions of the next statement = %q, want the day after the closing date", got)
	}
}

// A balance is what has happened by a day, whatever day the bank put it on: the
// purchase on the closing date is in the balance of the closing date, and on the
// statement of it until it is posted on the next. The two answer different
// questions, which is why a posting date moves one and not the other.
func TestAPostingDateMovesATransactionToTheNextStatementAndABalanceStillCountsItByTheDayItHappened(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("history-700", on(2026, time.September, 20), "700")
	moved := e.spend("moved-500", on(2026, time.November, 5), "500") // on the closing date
	e.spend("later-300", on(2026, time.November, 10), "300")
	today := on(2026, time.November, 14)
	closed, open := statementOf(on(2026, time.November, 5)), statementOf(on(2026, time.November, 6))
	balanceOn := func(day time.Time) string {
		return strings.Join(e.holds(day, false), ";")
	}

	wantFigures(t, "before the posting date", e.figures(today, closed, open),
		[]string{"TWD -700 500 0 -1200"}, []string{"TWD -1200 300 0 -1500"})
	if got := keys(e.statement(on(2026, time.November, 5), today).Transactions); !slices.Equal(got, []string{"moved-500"}) {
		t.Fatalf("closed statement before = %q, want the purchase of the closing date", got)
	}
	if got := balanceOn(on(2026, time.November, 5)); got != "visa TWD -1200" {
		t.Fatalf("balance of the 5th before = %q, want %q", got, "visa TWD -1200")
	}

	posted, err := e.setPostedOn(moved.ID, new(on(2026, time.November, 6)))
	if err != nil {
		t.Fatalf("SetPostedOn: %v", err)
	}
	if posted.PostedOn == nil || !posted.PostedOn.Equal(on(2026, time.November, 6)) || posted.Status != model.StatusConfirmed || len(posted.Postings) != 2 {
		t.Fatalf("SetPostedOn returned %+v, want the confirmed transaction with its postings, posted on the 6th", posted)
	}

	// Out of the statement it was on and into the next.
	wantFigures(t, "after the posting date", e.figures(today, closed, open),
		[]string{"TWD -700 0 0 -700"}, []string{"TWD -700 800 0 -1500"})
	if got := keys(e.statement(on(2026, time.November, 5), today).Transactions); len(got) != 0 {
		t.Errorf("closed statement after = %q, want it to have lost the purchase", got)
	}
	nextGot := e.statement(on(2026, time.November, 6), today).Transactions
	if got := keys(nextGot); !slices.Equal(got, []string{"moved-500", "later-300"}) {
		t.Fatalf("next statement after = %q, want the purchase that was posted on its first day, then the later one", got)
	}
	if nextGot[0].PostedOn == nil || !nextGot[0].PostedOn.Equal(on(2026, time.November, 6)) || !nextGot[0].OccurredOn.Equal(on(2026, time.November, 5)) {
		t.Errorf("the moved transaction = posted %v, occurred %v, want posted on the 6th and happened on the 5th", nextGot[0].PostedOn, nextGot[0].OccurredOn)
	}
	// What a balance counts is the day it happened: nothing about it moved.
	if got := balanceOn(on(2026, time.November, 5)); got != "visa TWD -1200" {
		t.Errorf("balance of the 5th after = %q, want it unchanged, %q: the purchase of the 5th is still in it", got, "visa TWD -1200")
	}
	if got := balanceOn(on(2026, time.November, 4)); got != "visa TWD -700" {
		t.Errorf("balance of the 4th = %q, want the history alone, %q", got, "visa TWD -700")
	}

	// And back, by taking the date away.
	if _, err := e.setPostedOn(moved.ID, nil); err != nil {
		t.Fatalf("SetPostedOn(nil): %v", err)
	}
	wantFigures(t, "once the posting date is taken away", e.figures(today, closed, open),
		[]string{"TWD -700 500 0 -1200"}, []string{"TWD -1200 300 0 -1500"})
}

func TestAPostingDateTwoDaysIntoTheNextStatementPutsATransactionOnThatOneAndNotOnTheOneItHappenedIn(t *testing.T) {
	e := newStatementEnv(t)
	// A purchase of the 3rd of November, on the statement that closes on the 5th, that
	// the bank put on the statement that closes on the 5th of December, as of the 7th:
	// it is on that one by its posting date, though it happened in the one before.
	e.spendPosted("late-bank-250", on(2026, time.November, 3), "250", on(2026, time.November, 7))
	today := on(2026, time.November, 14)

	wantFigures(t, "the statement it happened in and the one it was posted on", e.figures(today, statementOf(on(2026, time.November, 3)), statementOf(on(2026, time.November, 7))),
		[]string{}, []string{"TWD 0 250 0 -250"})
}

func TestDraftsAndVoidTransactionsAreOnNoStatementAndTheDraftsAreCounted(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("confirmed-100", on(2026, time.November, 7), "100")
	e.draft("draft-in-range-10", on(2026, time.November, 8), "10")
	posted := e.expenseOn("draft-posted-in-1", on(2026, time.November, 3), "1", new(on(2026, time.November, 9)))
	e.store(posted)                                                  // happened on the closed statement, posted on this one
	e.draft("draft-after-range", on(2026, time.December, 6), "1000") // on the next statement
	e.draft("draft-before-range", on(2026, time.November, 5), "10000")
	voided := e.spend("voided-100000", on(2026, time.November, 9), "100000")
	e.setStatus(voided.ID, model.StatusVoid)
	e.draft("draft-future-dated", on(2026, time.November, 30), "100000") // after today, in the range
	// A draft counts once, however many postings it has to the card: this one has two,
	// the amount and the fee that is charged to the same card.
	withFee, err := model.NewTransfer(model.TransferParams{
		Header: e.header("draft-two-postings", on(2026, time.November, 11)), From: e.visa, To: e.bank, Instrument: e.twd, Amount: dec("400"), BaseCurrency: e.twd,
		Fees: []model.Fee{{Account: e.visa, Instrument: e.twd, Category: e.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	e.store(withFee)
	// What has no posting to the card is not its draft.
	e.store(e.expenseOf(e.header("draft-of-cash", on(2026, time.November, 12)), "7"))
	today := on(2026, time.November, 14)

	got := e.statement(on(2026, time.November, 20), today)

	if got.Drafts != 4 {
		t.Errorf("%d drafts, want 4: the one in the range, the one posted in it, the one dated after today and the one with two postings, which counts once", got.Drafts)
	}
	wantFigures(t, "figures", e.formatFigures([][]model.StatementFigures{got.Figures}), []string{"TWD 0 100 0 -100"})
	if got := keys(got.Transactions); !slices.Equal(got, []string{"confirmed-100"}) {
		t.Errorf("transactions = %q, want the confirmed one alone", got)
	}
	// The statement it happened on has none of them: one of the drafts is dated in it
	// and posted in the next, and one is before the statement.
	if closed := e.statement(on(2026, time.November, 5), today); closed.Drafts != 1 {
		t.Errorf("closed statement has %d drafts, want 1: the one dated the 5th", closed.Drafts)
	}
}

func TestWhatIsDatedAfterTodayIsOnNoStatementUntilItsDay(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("history-300", on(2026, time.October, 10), "300")
	e.spend("dated-20th-50", on(2026, time.November, 20), "50")
	e.spendPosted("posted-25th-7", on(2026, time.November, 10), "7", on(2026, time.November, 25)) // happened before today, posted after it
	open := statementOf(on(2026, time.November, 20))

	tests := []struct {
		name    string
		today   time.Time
		want    []string
		wantTxn []string
	}{
		{"before either", on(2026, time.November, 14), []string{"TWD -300 0 0 -300"}, nil},
		{"the day before the purchase of the 20th", on(2026, time.November, 19), []string{"TWD -300 0 0 -300"}, nil},
		{"the 20th, the day of the purchase, which counts from it", on(2026, time.November, 20), []string{"TWD -300 50 0 -350"}, []string{"dated-20th-50"}},
		{"the day before the posting date", on(2026, time.November, 24), []string{"TWD -300 50 0 -350"}, []string{"dated-20th-50"}},
		{"the 25th, the day it was posted, which counts from it", on(2026, time.November, 25), []string{"TWD -300 57 0 -357"}, []string{"dated-20th-50", "posted-25th-7"}},
		{"long after", on(2027, time.March, 1), []string{"TWD -300 57 0 -357"}, []string{"dated-20th-50", "posted-25th-7"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantFigures(t, "figures", e.figures(tt.today, open), tt.want)
			got := e.statement(on(2026, time.November, 20), tt.today)
			if keys := keys(got.Transactions); !slices.Equal(keys, tt.wantTxn) && (len(keys) != 0 || len(tt.wantTxn) != 0) {
				t.Errorf("transactions = %q, want %q", keys, tt.wantTxn)
			}
			// What it adds up is what it lists.
			if len(got.Figures) != 1 {
				t.Fatalf("%d figures, want 1", len(got.Figures))
			}
			listed := decimal.Zero
			for _, txn := range got.Transactions {
				for _, p := range txn.Postings {
					if p.Book == model.BookAccount {
						listed = listed.Add(p.Quantity)
					}
				}
			}
			if net := got.Figures[0].Credits.Sub(got.Figures[0].Charges); !net.Equal(listed) {
				t.Errorf("figures net %s, transactions add up to %s, want them the same", net, listed)
			}
		})
	}

	// A closed statement is all there is of it, whatever today is after it.
	closed := statementOf(on(2026, time.October, 10))
	wantFigures(t, "the statement of the 10th of October, read on the day it closes and before it", [][]string{
		e.figures(on(2026, time.November, 5), closed)[0], e.figures(on(2026, time.October, 9), closed)[0],
	}, []string{"TWD 0 300 0 -300"}, []string{})
}

func TestAnInstalmentPeriodIsOnTheStatementOfItsDueDayAndItsOriginAndWhatIsUnbilledAreOnNone(t *testing.T) {
	e := newStatementEnv(t)
	periods := []model.InstalmentPeriod{
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 5), Principal: dec("1000"), Interest: decPtr("30")}, // the closing date
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 6), Principal: dec("1000")},                         // the day after
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 5), Principal: dec("1000")},
	}
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: e.header("plan-purchase-3000", on(2026, time.November, 10)), Account: &e.visa, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("3000")}},
	}, model.InstalmentSchedule{Periods: periods, InterestCategory: &e.interest})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	e.setPlanStatus(e.storePlan(plan), model.StatusConfirmed)
	// The statements around it, each by a day it has: the one that closes on the 5th of
	// November, the one that closes on the 5th of December, which is the day the first
	// period is due, the one after it, and the one after that.
	october, first := statementOf(on(2026, time.November, 5)), statementOf(on(2026, time.November, 10))
	second, third := statementOf(on(2026, time.December, 10)), statementOf(on(2027, time.January, 10))

	// Before the first period is due: the purchase of 3,000 is on no statement, as the
	// card has not been charged anything, and what is unbilled is not part of one.
	wantFigures(t, "on the 20th of November", e.figures(on(2026, time.November, 20), october, first), []string{}, []string{})
	if got := e.statement(on(2026, time.November, 10), on(2026, time.November, 20)); len(got.Transactions) != 0 {
		t.Errorf("transactions = %q, want none: the origin of the plan has no account posting", keys(got.Transactions))
	}
	if got := e.holds(on(2026, time.November, 20), true); !slices.Equal(got, []string{"visa TWD 0 unbilled -3000"}) {
		t.Fatalf("balances = %q, want what is unbilled and nothing charged", got)
	}

	// Each period is on the statement of its due day: the 5th of December is the
	// closing date, so the first period is on that statement, with its interest; the 6th
	// is the first day of the next, which has the period of the 5th of January too.
	today := on(2027, time.February, 1)
	wantFigures(t, "once they are all due", e.figures(today, october, first, second, third),
		[]string{},
		[]string{"TWD 0 1030 0 -1030"},
		[]string{"TWD -1030 2000 0 -3030"},
		[]string{"TWD -3030 0 0 -3030"},
	)
	if got := keys(e.statement(on(2026, time.December, 6), today).Transactions); len(got) != 2 {
		t.Errorf("the statement of the 6th of December has %d transactions, want the period of the 6th and the one of the 5th of January", len(got))
	}
	if got := keys(e.statement(on(2026, time.December, 5), today).Transactions); len(got) != 1 {
		t.Errorf("the statement closing on the 5th of December has %d transactions, want the first period only", len(got))
	}
}

func TestTheConversionOfABillIntoInstalmentsIsACreditAndItsPeriodsAreChargesOnTheirDays(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("the-bill-3000", on(2026, time.October, 20), "3000") // what the card owes
	periods := []model.InstalmentPeriod{
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.November, 20), Principal: dec("1000")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 20), Principal: dec("1000")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 20), Principal: dec("1000")},
	}
	bill, err := model.NewBillInstalment(model.BillInstalmentParams{
		Header: e.header("bill-conversion-1", on(2026, time.November, 12)), Account: e.visa, Instrument: e.twd, BaseCurrency: e.twd,
		Principal: dec("3000"), Schedule: model.InstalmentSchedule{Periods: periods},
	})
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}
	e.setPlanStatus(e.storePlan(bill), model.StatusConfirmed)

	got := e.figures(on(2027, time.February, 1),
		statementOf(on(2026, time.October, 20)), statementOf(on(2026, time.November, 20)), statementOf(on(2026, time.December, 20)), statementOf(on(2027, time.January, 20)))

	wantFigures(t, "the statements of October to January", got,
		[]string{"TWD 0 3000 0 -3000"},
		[]string{"TWD -3000 1000 3000 -1000"}, // the conversion pays the bill off, the first period is charged
		[]string{"TWD -1000 1000 0 -2000"},
		[]string{"TWD -2000 1000 0 -3000"},
	)
}

func TestPaymentsRefundsAndRewardsAreCreditsAndWhatIsSpentIsCharges(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("charge-1000", on(2026, time.November, 7), "1000")
	e.spend("charge-250", on(2026, time.November, 8), "250.50")
	e.payment("payment-400", on(2026, time.November, 12), "400") // a transfer into the card
	refund, err := model.NewIncome(model.IncomeParams{
		Header: e.header("refund-50", on(2026, time.November, 13)), Account: e.visa, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.salary, Amount: dec("50")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}
	e.storeConfirmed(refund)
	reward, err := model.NewReward(model.RewardParams{
		Header: e.header("reward-20", on(2026, time.November, 13)), Account: e.visa, Instrument: e.twd, BaseCurrency: e.twd, Amount: dec("20"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	e.storeConfirmed(reward)
	// A transfer out of the card is a charge, and so is the fee that is paid from it.
	out, err := model.NewTransfer(model.TransferParams{
		Header: e.header("cash-advance-100", on(2026, time.November, 13)), From: e.visa, To: e.bank, Instrument: e.twd, Amount: dec("100"), BaseCurrency: e.twd,
		Fees: []model.Fee{{Account: e.visa, Instrument: e.twd, Category: e.fees, Amount: dec("3")}},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	e.storeConfirmed(out)
	// The other accounts' postings are none of the card's.
	e.storeConfirmed(e.expenseOf(e.header("cash-lunch-9999", on(2026, time.November, 9)), "9999"))

	got := e.figures(on(2026, time.November, 20), statementOf(on(2026, time.November, 20)))

	// Charged: 1000 + 250.50 + 100 + 3; credited: 400 + 50 + 20.
	wantFigures(t, "the statement", got, []string{"TWD 0 1353.5 470 -883.5"})
}

func TestAForeignCurrencyCardHasFiguresForEachOfItsInstruments(t *testing.T) {
	e := newStatementEnv(t)
	usdExpense := func(key string, day time.Time, amount, base string) model.Transaction {
		txn, err := model.NewExpense(model.ExpenseParams{
			Header: e.header(key, day), Account: &e.visa, Instrument: e.usd, BaseCurrency: e.twd,
			Lines: []model.CategoryLine{{Category: e.food, Amount: dec(amount), BaseAmount: decPtr(base)}},
		})
		if err != nil {
			t.Fatalf("NewExpense in dollars: %v", err)
		}
		return txn
	}
	e.spend("twd-lunch-300", on(2026, time.October, 20), "300")
	e.storeConfirmed(usdExpense("usd-hotel-100", on(2026, time.October, 21), "100", "3200"))
	e.spend("twd-lunch-200", on(2026, time.November, 8), "200")
	e.storeConfirmed(usdExpense("usd-taxi-30", on(2026, time.November, 9), "30", "960"))
	usdPayment, err := model.NewTransfer(model.TransferParams{
		Header: e.header("usd-payment-60", on(2026, time.November, 10)), From: e.bank, To: e.visa, Instrument: e.usd, Amount: dec("60"), BaseCurrency: e.twd,
	})
	if err != nil {
		t.Fatalf("NewTransfer in dollars: %v", err)
	}
	e.storeConfirmed(usdPayment)
	// An instrument that has been paid off and not been posted to since is not listed.
	e.storeConfirmed(usdExpense("usd-small-5", on(2026, time.July, 1), "5", "160"))
	settle, err := model.NewTransfer(model.TransferParams{
		Header: e.header("usd-settle-5", on(2026, time.July, 2)), From: e.bank, To: e.visa, Instrument: e.usd, Amount: dec("5"), BaseCurrency: e.twd,
	})
	if err != nil {
		t.Fatalf("NewTransfer in dollars: %v", err)
	}
	e.storeConfirmed(settle)
	today := on(2026, time.November, 20)

	got := e.figures(today, statementOf(on(2026, time.October, 20)), statementOf(on(2026, time.November, 20)), statementOf(on(2026, time.August, 20)))

	wantFigures(t, "the statements, by instrument code", got,
		[]string{"TWD 0 300 0 -300", "USD 0 100 0 -100"},
		[]string{"TWD -300 200 0 -500", "USD -100 30 60 -70"},
		[]string{}, // dollars that were paid off in July are not on the statement of August
	)
	if open := e.statement(on(2026, time.November, 20), today); len(open.Figures) != 2 || len(open.Transactions) != 3 {
		t.Errorf("the open statement has %d instruments and %d transactions, want 2 and 3", len(open.Figures), len(open.Transactions))
	}
}

// The closing of a statement is what the card owes at the end of it, and it is what
// the next starts from: whatever is in between, the figures say what the postings
// say, which this adds up in another way. The figures of all the statements are
// from one statement of SQL.
func TestTheClosingOfAStatementIsThePreviousOfTheNextAndWhatThePostingsAddUpTo(t *testing.T) {
	e := newStatementEnv(t)
	dates := []time.Time{
		on(2026, time.July, 6), on(2026, time.July, 28), on(2026, time.August, 5), on(2026, time.August, 6), on(2026, time.September, 5),
		on(2026, time.September, 20), on(2026, time.October, 5), on(2026, time.October, 6), on(2026, time.October, 31),
		on(2026, time.November, 5), on(2026, time.November, 6), on(2026, time.November, 21), on(2026, time.December, 5), on(2026, time.December, 6),
	}
	for i, day := range dates {
		switch {
		case i%3 == 0:
			e.payment(fmt.Sprintf("payment-%02d", i), day, fmt.Sprint(100*(i+1)))
		case i%5 == 0:
			e.spendPosted(fmt.Sprintf("posted-%02d", i), day, fmt.Sprint(10*(i+1)), day.AddDate(0, 0, 3))
		default:
			e.spend(fmt.Sprintf("spend-%02d", i), day, fmt.Sprint(10*(i+1)))
		}
	}
	e.draft("draft-ignored", on(2026, time.October, 15), "100000")
	today := on(2026, time.November, 30)
	periods := model.StatementsClosingBetween(5, on(2026, time.June, 5), on(2027, time.January, 5))

	got, err := e.txns.StatementFigures(context.Background(), e.visa.ID, periods, today)
	if err != nil {
		t.Fatalf("StatementFigures: %v", err)
	}

	// What the confirmed postings to the card add up to by each closing date, by their
	// statement dates and no later than today, worked out here from the stored
	// transactions and not by the statement.
	all, err := e.txns.List(context.Background(), repo.TransactionFilter{Status: model.StatusConfirmed, AccountID: &e.visa.ID, Limit: 200})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	owed := func(closing time.Time) decimal.Decimal {
		sum := decimal.Zero
		for _, txn := range all {
			if txn.StatementDate().After(closing) || txn.StatementDate().After(today) {
				continue
			}
			for _, p := range txn.Postings {
				if p.Book == model.BookAccount && *p.AccountID == e.visa.ID {
					sum = sum.Add(p.Quantity)
				}
			}
		}
		return sum
	}
	figureOf := func(list []model.StatementFigures) model.StatementFigures {
		for _, f := range list {
			if f.InstrumentID == e.twd.ID {
				return f
			}
		}
		return model.StatementFigures{InstrumentID: e.twd.ID} // nothing is posted to it, and nothing is carried
	}
	if len(got) != len(periods) || len(periods) < 6 {
		t.Fatalf("%d lists of figures for %d statements, want one for each of at least 6", len(got), len(periods))
	}
	for i, period := range periods {
		f := figureOf(got[i])
		if want := f.Previous.Sub(f.Charges).Add(f.Credits); !f.Closing.Equal(want) {
			t.Errorf("%s: closing %s, want previous %s less charges %s plus credits %s = %s", days(period), f.Closing, f.Previous, f.Charges, f.Credits, want)
		}
		if want := owed(period.ClosingOn); !f.Closing.Equal(want) {
			t.Errorf("%s: closing %s, want what the postings add up to by the closing date, %s", days(period), f.Closing, want)
		}
		if want := owed(period.From.AddDate(0, 0, -1)); !f.Previous.Equal(want) {
			t.Errorf("%s: previous %s, want what the postings add up to by the day before it, %s", days(period), f.Previous, want)
		}
		if i > 0 {
			if before := figureOf(got[i-1]); !before.Closing.Equal(f.Previous) {
				t.Errorf("%s closes at %s, and %s starts from %s, want them the same", days(periods[i-1]), before.Closing, days(period), f.Previous)
			}
		}
	}
}

// days is a statement as the days it covers.
func days(p model.StatementPeriod) string {
	return p.From.Format(time.DateOnly) + ".." + p.ClosingOn.Format(time.DateOnly)
}

func TestStatementFiguresAreOfTheAccountAskedAboutAndNoOther(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("visa-lunch-100", on(2026, time.November, 7), "100")
	other := insertCard(t, postgres.NewAccountRepo(e.pool), "master", new(5), nil)
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("master-lunch-5", on(2026, time.November, 7)), Account: &other, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("5")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(txn)
	period := statementOf(on(2026, time.November, 7))
	today := on(2026, time.November, 30)

	wantFigures(t, "the visa", e.figures(today, period), []string{"TWD 0 100 0 -100"})
	got, err := e.txns.StatementFigures(context.Background(), other.ID, []model.StatementPeriod{period}, today)
	if err != nil {
		t.Fatalf("StatementFigures of the other card: %v", err)
	}
	wantFigures(t, "the other card", e.formatFigures(got), []string{"TWD 0 5 0 -5"})
	content, err := e.txns.Statement(context.Background(), repo.StatementFilter{AccountID: other.ID, Period: period, Today: today})
	if err != nil || len(content.Transactions) != 1 || content.Transactions[0].IdempotencyKey != "master-lunch-5" {
		t.Errorf("the other card's statement = %+v, %v, want its one transaction", content, err)
	}

	// An account that nothing was posted to has nothing, and no statement is none.
	empty, err := e.txns.StatementFigures(context.Background(), uuid.Must(uuid.NewV7()), []model.StatementPeriod{period}, today)
	if err != nil || len(empty) != 1 || len(empty[0]) != 0 {
		t.Errorf("figures of an account that is not there = %v, %v, want a list with nothing in it", empty, err)
	}
	none, err := e.txns.StatementFigures(context.Background(), e.visa.ID, nil, today)
	if err != nil || len(none) != 0 {
		t.Errorf("figures of no statement = %v, %v, want none", none, err)
	}
	nothing := e.statement(on(2025, time.January, 7), today)
	if len(nothing.Figures) != 0 || len(nothing.Transactions) != 0 || nothing.Drafts != 0 {
		t.Errorf("a statement before anything = %+v, want it empty", nothing)
	}
}

func TestTheTransactionsOfAStatementAreByStatementDateThenTheDayTheyHappenedThenOldestFirst(t *testing.T) {
	e := newStatementEnv(t)
	first := e.spend("a-nov-06", on(2026, time.November, 6), "1")
	posted := e.spendPosted("b-nov-05-posted-06", on(2026, time.November, 5), "2", on(2026, time.November, 6))
	second := e.spend("c-nov-06", on(2026, time.November, 6), "3") // made after the first
	e.spend("d-nov-10", on(2026, time.November, 10), "4")
	e.spend("e-nov-02-posted-10", on(2026, time.November, 2), "5") // dated before the statement, and not posted
	e.spendPosted("f-nov-03-posted-10", on(2026, time.November, 3), "6", on(2026, time.November, 10))
	if first.ID.String() >= second.ID.String() {
		t.Fatal("the ids are not in the order they were made in: the test cannot say which of the two was made first")
	}

	got := e.statement(on(2026, time.November, 20), on(2026, time.December, 1)).Transactions

	// The statement dates in order, the 6th and then the 10th. On the 6th: the one that
	// happened on the 5th and was posted on the 6th, then the two that happened on the
	// 6th, in the order they were made. On the 10th: the one that happened on the 3rd
	// and was posted on the 10th, then the one that happened on the 10th.
	want := []string{"b-nov-05-posted-06", "a-nov-06", "c-nov-06", "f-nov-03-posted-10", "d-nov-10"}
	if !slices.Equal(keys(got), want) {
		t.Fatalf("transactions = %q, want %q", keys(got), want)
	}
	// Each is whole: its postings, and the posting date it has.
	if len(got[4].Postings) != 2 || got[0].ID != posted.ID || got[0].PostedOn == nil || !got[0].PostedOn.Equal(on(2026, time.November, 6)) {
		t.Errorf("transactions = %+v, want them with their postings and posting dates", got)
	}
}

func TestInsertAndGetRoundTripThePostingDate(t *testing.T) {
	e := newStatementEnv(t)
	ctx := context.Background()
	without := e.store(e.expenseOn("round-trip-none", on(2026, time.November, 5), "10", nil))
	with := e.store(e.expenseOn("round-trip-date", on(2026, time.November, 5), "10", new(on(2026, time.November, 9))))

	for name, want := range map[string]model.Transaction{"without": without, "with": with} {
		got, err := e.txns.Get(ctx, want.ID)
		if err != nil {
			t.Fatalf("Get %s: %v", name, err)
		}
		e.wantSame("Get "+name, got, want)
		if got.PostedOn != nil != (name == "with") {
			t.Errorf("Get %s: posting date %v", name, got.PostedOn)
		}
	}
	if got := e.get(with.ID); got.PostedOn == nil || !got.PostedOn.Equal(on(2026, time.November, 9)) || got.PostedOn.Location() != time.UTC {
		t.Errorf("posting date = %v, want the 9th at midnight UTC", got.PostedOn)
	}
	listed, err := e.txns.List(ctx, repo.TransactionFilter{Limit: 10})
	if err != nil || len(listed) != 2 {
		t.Fatalf("List = %d transactions, %v, want 2", len(listed), err)
	}
	for _, txn := range listed {
		e.wantSame("List", txn, map[uuid.UUID]model.Transaction{without.ID: without, with.ID: with}[txn.ID])
	}
	byKey, err := e.txns.GetByIdempotencyKey(ctx, "round-trip-date")
	if err != nil || byKey.PostedOn == nil {
		t.Errorf("GetByIdempotencyKey = %+v, %v, want the posting date", byKey, err)
	}
	// The transaction a status change returns, and the locked one, have it too.
	if got := e.setStatus(with.ID, model.StatusConfirmed); got.PostedOn == nil {
		t.Errorf("SetStatus returned no posting date")
	}
	err = e.inTx(func(ctx context.Context) error {
		if got, err := e.txns.GetForUpdate(ctx, with.ID); err != nil || got.PostedOn == nil {
			t.Errorf("GetForUpdate = %+v, %v, want the posting date", got, err)
		}
		return nil
	})
	if err != nil {
		t.Errorf("a transaction that locks it: %v", err)
	}
}

func TestSetPostedOnSetsChangesAndClearsTheDateAndTheTableRefusesOneBeforeTheDayItHappened(t *testing.T) {
	e := newStatementEnv(t)
	txn := e.spend("posting-date-1", on(2026, time.November, 5), "10")

	for _, step := range []struct {
		name string
		to   *time.Time
	}{
		{"given", new(on(2026, time.November, 9))},
		{"changed", new(on(2026, time.December, 24))},
		{"the day it happened, which is allowed", new(on(2026, time.November, 5))},
		{"cleared", nil},
	} {
		got, err := e.setPostedOn(txn.ID, step.to)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if !sameTime(got.PostedOn, step.to) {
			t.Errorf("%s: posting date %v, want %v", step.name, got.PostedOn, step.to)
		}
		if read := e.get(txn.ID); !sameTime(read.PostedOn, step.to) {
			t.Errorf("%s: stored posting date %v, want %v", step.name, read.PostedOn, step.to)
		}
		e.wantSame(step.name, got, func() model.Transaction { want := txn; want.PostedOn = step.to; return want }())
	}

	// Only the date is written: the status and the times are as they were.
	if got := e.get(txn.ID); got.Status != model.StatusConfirmed || got.ConfirmedAt == nil || got.VoidedAt != nil {
		t.Errorf("transaction = %+v, want it as confirmed as it was", got)
	}

	// The table is the last guard of the order of the two days.
	_, err := e.setPostedOn(txn.ID, new(on(2026, time.November, 4)))
	wantConstraint(t, err, checkViolation, "fin_transaction_posted_on_check")
	if got := e.get(txn.ID); got.PostedOn != nil {
		t.Errorf("posting date = %v after a refused change, want it as it was, none", got.PostedOn)
	}
	early := e.expenseOn("posting-date-2", on(2026, time.November, 5), "10", nil)
	early.PostedOn = new(on(2026, time.November, 4)) // the model is skipped on purpose
	err = e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, early)
		return err
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_posted_on_check")

	if _, err := e.setPostedOn(uuid.Must(uuid.NewV7()), new(on(2026, time.November, 9))); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("SetPostedOn of an id that is not there: err = %v, want ErrNotFound", err)
	}
}

// The table is the last guard of the rule that a transaction of an instalment plan
// has no posting date: the origin of the plan, whose plan_id is its own id, as much
// as each of its periods. The model and the use cases refuse it first, so these go
// round them, by an update of what is stored and by an insert with the date set.
func TestTheTableRefusesAPostingDateOnTheOriginAndOnEachPeriodOfAnInstalmentPlan(t *testing.T) {
	e := newStatementEnv(t)
	newPlan := func(key string) model.InstalmentPlan {
		t.Helper()
		plan, err := model.NewInstalmentExpense(model.ExpenseParams{
			Header: e.header(key, on(2026, time.November, 10)), Account: &e.visa, Instrument: e.twd, BaseCurrency: e.twd,
			Lines: []model.CategoryLine{{Category: e.food, Amount: dec("2000")}},
		}, model.InstalmentSchedule{Periods: []model.InstalmentPeriod{
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 5), Principal: dec("1000")},
			{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 5), Principal: dec("1000")},
		}})
		if err != nil {
			t.Fatalf("NewInstalmentExpense: %v", err)
		}
		return plan
	}
	// After every day of the plan, so that the plan is all that is wrong with the date.
	postedOn := on(2027, time.February, 1)

	stored := e.storePlan(newPlan("plan-update-2000"))
	for _, txn := range stored.Transactions() {
		_, err := e.pool.Exec(context.Background(), `UPDATE fin_transaction SET posted_on = $2 WHERE id = $1`, txn.ID, postedOn)
		wantConstraint(t, err, checkViolation, "fin_transaction_posted_on_plan_check")
		if got := e.get(txn.ID); got.PostedOn != nil {
			t.Errorf("%s: posting date %v after a refused update, want none", txn.IdempotencyKey, got.PostedOn)
		}
	}

	for _, tt := range []struct {
		name string
		key  string
		give func(*model.InstalmentPlan)
	}{
		{"the origin", "plan-insert-origin", func(p *model.InstalmentPlan) { p.Origin.PostedOn = new(postedOn) }},
		{"a period", "plan-insert-period", func(p *model.InstalmentPlan) { p.Periods[1].PostedOn = new(postedOn) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			plan := newPlan(tt.key)
			tt.give(&plan)
			err := e.inTx(func(ctx context.Context) error {
				for _, txn := range plan.Transactions() {
					if _, err := e.txns.Insert(ctx, txn); err != nil {
						return err
					}
				}
				return nil
			})
			wantConstraint(t, err, checkViolation, "fin_transaction_posted_on_plan_check")
		})
	}
}

// The statement is read in one snapshot: a purchase that is confirmed on another
// connection after the figures are read must not be in the list of transactions that
// goes with them, as it would be in a list read after it, or the list would not be
// what the figures add up.
func TestAStatementIsReadInOneSnapshot(t *testing.T) {
	e := newStatementEnv(t)
	e.spend("before-100", on(2026, time.November, 7), "100")
	e.draft("draft-10", on(2026, time.November, 8), "10")
	arriving := e.store(e.expenseOn("arrives-1000", on(2026, time.November, 9), "1000", nil))
	today := on(2026, time.November, 30)
	late := e.draft("late-draft-1", on(2026, time.November, 10), "1")

	tracer := &afterStatement{prefix: "-- name: ListStatementFigures", hook: func() {
		// Another connection confirms the purchase and deletes a draft.
		e.setStatus(arriving.ID, model.StatusConfirmed)
		if _, err := e.pool.Exec(context.Background(), `DELETE FROM fin_transaction WHERE id = $1`, late.ID); err != nil {
			t.Errorf("delete a draft on another connection: %v", err)
		}
	}}
	cfg := e.pool.Config()
	cfg.ConnConfig.Tracer = tracer
	traced, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open a pool with a tracer: %v", err)
	}
	defer traced.Close()

	got, err := postgres.NewTransactionRepo(traced).Statement(context.Background(), repo.StatementFilter{
		AccountID: e.visa.ID, Period: statementOf(on(2026, time.November, 20)), Today: today,
	})
	if err != nil {
		t.Fatalf("Statement: %v", err)
	}

	if !tracer.ran {
		t.Fatal("the change was never made: no statement read the figures")
	}
	wantFigures(t, "figures", e.formatFigures([][]model.StatementFigures{got.Figures}), []string{"TWD 0 100 0 -100"})
	if keys := keys(got.Transactions); !slices.Equal(keys, []string{"before-100"}) {
		t.Errorf("transactions = %q, want the one the figures add up, without the purchase confirmed meanwhile", keys)
	}
	if got.Drafts != 3 {
		t.Errorf("%d drafts, want the 3 there were when the statement was begun: the one deleted meanwhile is counted, and the one confirmed too", got.Drafts)
	}
	// Read again, it is what it is now.
	after := e.statement(on(2026, time.November, 20), today)
	if len(after.Transactions) != 2 || after.Drafts != 1 {
		t.Errorf("read again: %d transactions and %d drafts, want the 2 confirmed and the 1 draft", len(after.Transactions), after.Drafts)
	}
}
