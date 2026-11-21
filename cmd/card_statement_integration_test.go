package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The statements of a card, through the real use cases on a real PostgreSQL. The
// card closes its statement on the 5th and is to be paid by the 20th. Its statement
// of the 6th of October to the 5th of November has closed on the 14th of November,
// and the one of the 6th of November to the 5th of December is open.

// statementOf says what the statement is: the days it covers, when it is due and
// whether it has closed.
func statementOf(s usecase.CardStatementSummary) string {
	due := "no due date"
	if s.DueOn != nil {
		due = "due " + s.DueOn.Format(time.DateOnly)
	}
	state := "open"
	if s.Closed {
		state = "closed"
	}
	return fmt.Sprintf("%s..%s %s %s", s.From.Format(time.DateOnly), s.ClosingOn.Format(time.DateOnly), due, state)
}

// twdOf says what the statement has of the base currency as "previous charges
// credits closing", and "-" when it says nothing of it.
func twdOf(s usecase.CardStatementSummary) string {
	for _, f := range s.Figures {
		if f.InstrumentID == model.BaseCurrencyID {
			return fmt.Sprintf("%s %s %s %s", f.Previous, f.Charges, f.Credits, f.Closing)
		}
	}
	return "-"
}

// linesOf says what is on the statement, one transaction to a line, in the order it
// comes: the statement date, the day it happened, the posting date, and what it
// moved on the card.
func linesOf(s usecase.CardStatement) []string {
	lines := make([]string, len(s.Transactions))
	for i, txn := range s.Transactions {
		posted := "-"
		if txn.Transaction.PostedOn != nil {
			posted = txn.Transaction.PostedOn.Format("01-02")
		}
		var amounts []string
		for _, a := range txn.Amounts {
			amounts = append(amounts, a.Quantity.String())
		}
		lines[i] = fmt.Sprintf("%s happened %s posted %s: %s", txn.StatementDate.Format("01-02"), txn.Transaction.OccurredOn.Format("01-02"), posted, strings.Join(amounts, ","))
	}
	return lines
}

// readStatement reads the statement of the card that has the day, today for nil.
func (w *budgetWorld) readStatement(day *time.Time) usecase.CardStatement {
	w.t.Helper()
	got, err := w.GetCardStatement.GetCardStatement(context.Background(), usecase.GetCardStatementInput{AccountID: w.card, ClosingOn: day})
	if err != nil {
		w.t.Fatalf("GetCardStatement: %v", err)
	}
	return got
}

// heldOn says what the card holds of the base currency on the day, today for nil.
func (w *budgetWorld) heldOn(day *time.Time) string {
	w.t.Helper()
	out, err := w.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &w.card, AsOf: day})
	if err != nil {
		w.t.Fatalf("GetBalances: %v", err)
	}
	if len(out.Balances) == 0 {
		return "nothing"
	}
	return out.Balances[0].Quantity.String()
}

// cardExpense records an expense of the amount on the card, of the area Home, on the
// day, with the posting date when there is one, and confirms it.
func (w *budgetWorld) cardExpense(on time.Time, amount string, postedOn *time.Time) usecase.Transaction {
	w.t.Helper()
	d := w.details(w.key())
	d.OccurredOn, d.AreaID = on, &w.home
	out, err := w.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:    []usecase.CategoryLine{{CategoryID: w.food, Amount: dec(amount)}},
		PostedOn: postedOn,
	})
	if err != nil {
		w.t.Fatalf("RecordExpense on the card: %v", err)
	}
	w.confirm(w.t, out.Transaction)
	return out.Transaction
}

// payCard pays the card from the bank on the day, and confirms it.
func (w *budgetWorld) payCard(on time.Time, amount string) usecase.Transaction {
	w.t.Helper()
	d := w.details(w.key())
	d.OccurredOn = on
	out, err := w.RecordTransfer.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.bank, ToAccountID: w.card, InstrumentID: model.BaseCurrencyID, Amount: dec(amount),
	})
	if err != nil {
		w.t.Fatalf("RecordTransfer to the card: %v", err)
	}
	w.confirm(w.t, out.Transaction)
	return out.Transaction
}

// postedOn sets the posting date of the transaction, and fails the test when that is
// refused.
func (w *budgetWorld) postedOn(txn usecase.Transaction, on *time.Time) usecase.Transaction {
	w.t.Helper()
	got, err := w.SetPostingDate.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: txn.ID, PostedOn: on})
	if err != nil {
		w.t.Fatalf("SetPostingDate: %v", err)
	}
	return got
}

// cardDays gives the card its closing day and payment due day.
func (w *budgetWorld) cardDays(closingDay, dueDay *int) {
	w.t.Helper()
	got, err := w.SetAccountStatementDays.SetAccountStatementDays(context.Background(), usecase.SetAccountStatementDaysInput{ID: w.card, ClosingDay: closingDay, PaymentDueDay: dueDay})
	if err != nil {
		w.t.Fatalf("SetAccountStatementDays: %v", err)
	}
	if (got.ClosingDay == nil) != (closingDay == nil) || (got.ClosingDay != nil && *got.ClosingDay != *closingDay) {
		w.t.Fatalf("closing day = %v, want %v", got.ClosingDay, closingDay)
	}
}

// The purchase on the closing date is moved to the next statement by a posting date
// given after it was confirmed. What follows the posting date is the statements, and
// nothing else: the balance of the 5th still has the purchase, as the budgets of
// October and November still have what they had. A purchase of the last day of
// October that the bank only put on the 6th of November goes with it, which has the
// posting date from the start and crosses a month as well as a statement.
func TestACardStatementFollowsThePostingDateAndNothingElseDoesThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t) // the 14th of November 2026
	october, november := dateOn(2026, time.October, 1), dateOn(2026, time.November, 1)
	w.cardDays(new(5), new(20))
	w.setArea(w.home, october, "5000")
	w.setArea(w.home, november, "5000")

	w.cardExpense(dateOn(2026, time.October, 20), "1000", nil)
	w.cardExpense(dateOn(2026, time.October, 31), "400", new(dateOn(2026, time.November, 6)))
	moved := w.cardExpense(dateOn(2026, time.November, 5), "500", nil) // the closing date
	w.cardExpense(dateOn(2026, time.November, 6), "200", nil)
	w.cardExpense(dateOn(2026, time.November, 10), "300", nil)
	w.payCard(dateOn(2026, time.November, 12), "700")
	fifth := dateOn(2026, time.November, 5)

	// Before the posting date is given: the purchase of the 5th is on the statement it
	// closes with, and the one of the 31st of October is on the next since it is posted
	// there.
	closed, open := w.readStatement(&fifth), w.readStatement(nil)
	wantStatus(t, "the statement that has closed", []string{statementOf(closed.CardStatementSummary), twdOf(closed.CardStatementSummary)},
		"2026-10-06..2026-11-05 due 2026-11-20 closed", "0 1500 0 -1500")
	wantStatus(t, "the statement that is open", []string{statementOf(open.CardStatementSummary), twdOf(open.CardStatementSummary)},
		"2026-11-06..2026-12-05 due 2026-12-20 open", "-1500 900 700 -1700")
	balances := []string{w.heldOn(&fifth), w.heldOn(nil)}
	wantStatus(t, "what the card holds on the 5th and today", balances, "-1900", "-1700")
	areasBefore := [][]string{w.areas(october), w.areas(november)}
	wantStatus(t, "October", areasBefore[0], "Home: budget 5000, spent 1400 (1400 with projects), allocations [], unallocated 1400")
	wantStatus(t, "November", areasBefore[1], "Home: budget 5000, spent 1000 (1000 with projects), allocations [], unallocated 1000")

	// The bank put the purchase of the 5th on the next statement: it is given the date,
	// after it is confirmed.
	got := w.postedOn(moved, new(dateOn(2026, time.November, 6)))
	if got.Status != "confirmed" || got.PostedOn == nil || !got.PostedOn.Equal(dateOn(2026, time.November, 6)) || !got.OccurredOn.Equal(fifth) {
		t.Fatalf("transaction = %+v, want it confirmed, happened on the 5th and posted on the 6th", got)
	}

	closed, open = w.readStatement(&fifth), w.readStatement(nil)
	wantStatus(t, "the statement that has closed, after", []string{twdOf(closed.CardStatementSummary)}, "0 1000 0 -1000")
	wantStatus(t, "the statement that is open, after", []string{twdOf(open.CardStatementSummary)}, "-1000 1400 700 -1700")
	wantStatus(t, "its transactions", linesOf(closed), "10-20 happened 10-20 posted -: -1000")
	// By statement date, then the day they happened: the payment, the 10th, and the
	// three of the 6th, the one that happened that day before the two that happened
	// before it.
	wantStatus(t, "the transactions of the open statement", linesOf(open),
		"11-12 happened 11-12 posted -: 700",
		"11-10 happened 11-10 posted -: -300",
		"11-06 happened 11-06 posted -: -200",
		"11-06 happened 11-05 posted 11-06: -500",
		"11-06 happened 10-31 posted 11-06: -400",
	)

	// Nothing but the statements moved.
	wantStatus(t, "what the card holds on the 5th and today, after", []string{w.heldOn(&fifth), w.heldOn(nil)}, balances...)
	wantStatus(t, "October, after", w.areas(october), areasBefore[0]...)
	wantStatus(t, "November, after", w.areas(november), areasBefore[1]...)
	read, err := w.GetTransaction.GetTransaction(ctx, moved.ID)
	if err != nil || read.PostedOn == nil || !read.PostedOn.Equal(dateOn(2026, time.November, 6)) || !read.OccurredOn.Equal(fifth) {
		t.Errorf("GetTransaction = %+v, %v, want the posting date, and the day it happened as it was", read, err)
	}
	listed, err := w.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{AccountID: &w.card, Status: "confirmed"})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	withDate := 0
	for _, txn := range listed.Transactions {
		if txn.PostedOn != nil {
			withDate++
		}
	}
	if withDate != 2 {
		t.Errorf("%d listed transactions carry a posting date, want the 2 that have one", withDate)
	}

	// The list of the statements, newest first, which close on the 5th from October to
	// December: each closes at what the next starts from.
	statements, err := w.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
		AccountID: w.card, From: dateOn(2026, time.October, 5), To: dateOn(2026, time.December, 5),
	})
	if err != nil {
		t.Fatalf("ListCardStatements: %v", err)
	}
	var lines []string
	for _, s := range statements.Statements {
		lines = append(lines, statementOf(s)+": "+twdOf(s))
	}
	wantStatus(t, "the statements", lines,
		"2026-11-06..2026-12-05 due 2026-12-20 open: -1000 1400 700 -1700",
		"2026-10-06..2026-11-05 due 2026-11-20 closed: 0 1000 0 -1000",
		"2026-09-06..2026-10-05 due 2026-10-20 closed: -",
	)
	empty, err := w.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
		AccountID: w.card, From: dateOn(2026, time.November, 6), To: dateOn(2026, time.December, 4),
	})
	if err != nil || len(empty.Statements) != 0 {
		t.Errorf("the statements closing between the 6th of November and the 4th of December = %+v, %v, want none", empty.Statements, err)
	}

	// A draft is waiting for the statement, and is not in its figures until it is
	// confirmed.
	d := w.details(w.key())
	d.OccurredOn = dateOn(2026, time.November, 8)
	draft, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("150")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	if got := w.readStatement(nil); got.Drafts != 1 || twdOf(got.CardStatementSummary) != "-1000 1400 700 -1700" {
		t.Errorf("with a draft: %d drafts, figures %q, want 1 draft and the figures as they were", got.Drafts, twdOf(got.CardStatementSummary))
	}
	w.confirm(t, draft.Transaction)
	if got := w.readStatement(nil); got.Drafts != 0 || twdOf(got.CardStatementSummary) != "-1000 1550 700 -1850" {
		t.Errorf("once confirmed: %d drafts, figures %q, want none and the 150 in the charges", got.Drafts, twdOf(got.CardStatementSummary))
	}

	// The closing date comes: the open statement closes the day after it.
	w.clk.setDay(dateOn(2026, time.December, 5))
	if got := w.readStatement(nil); statementOf(got.CardStatementSummary) != "2026-11-06..2026-12-05 due 2026-12-20 open" {
		t.Errorf("on the closing date: %s, want it still open", statementOf(got.CardStatementSummary))
	}
	w.clk.setDay(dateOn(2026, time.December, 6))
	if got := w.readStatement(nil); statementOf(got.CardStatementSummary) != "2026-12-06..2027-01-05 due 2027-01-20 open" {
		t.Errorf("the day after: %s, want the next statement, open", statementOf(got.CardStatementSummary))
	}
	december5 := dateOn(2026, time.December, 5)
	if got := w.readStatement(&december5); !got.Closed || twdOf(got.CardStatementSummary) != "-1000 1550 700 -1850" {
		t.Errorf("the statement of the 5th of December: closed %v, figures %q, want it closed with what it had", got.Closed, twdOf(got.CardStatementSummary))
	}
}

// What cannot have a posting date is refused, with an error that says why, and
// nothing is written: an expense paid from cash, a date before the day it happened,
// the origin and a period of an instalment plan, and a void transaction.
func TestAPostingDateThatCannotBeIsRefusedAndNothingIsWrittenThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	w.cardDays(new(5), new(20))
	cashLunch := w.spend(dateOn(2026, time.November, 5), &w.home, nil, w.cash, w.food, "100")
	onCard := w.cardExpense(dateOn(2026, time.November, 5), "500", nil)
	voided := w.cardExpense(dateOn(2026, time.November, 6), "50", nil)
	if _, err := w.Void.VoidTransaction(ctx, voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	d := w.details(w.key())
	d.OccurredOn = dateOn(2026, time.November, 10)
	plan, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("12000")}},
		Instalments: instalmentsOf(w.interest),
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	w.confirm(t, plan.Transaction)
	period := w.planPeriods(plan.Transaction)[0]
	before := w.readStatement(nil)

	tests := []struct {
		name string
		id   usecase.Transaction
		on   *time.Time
		want error
	}{
		{"an expense paid from cash", cashLunch, new(dateOn(2026, time.November, 6)), model.ErrNoCardPosting},
		{"a date before the day it happened", onCard, new(dateOn(2026, time.November, 4)), model.ErrPostedBeforeOccurred},
		{"the origin of an instalment plan", plan.Transaction, new(dateOn(2026, time.November, 11)), model.ErrPartOfPlan},
		{"a period of an instalment plan", period, new(dateOn(2026, time.December, 6)), model.ErrPartOfPlan},
		{"a void transaction", voided, new(dateOn(2026, time.November, 7)), model.ErrTransactionVoid},
		{"taking the date away from an expense paid from cash", cashLunch, nil, model.ErrNoCardPosting},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := w.SetPostingDate.SetPostingDate(ctx, usecase.SetPostingDateInput{TransactionID: tt.id.ID, PostedOn: tt.on})

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			got, err := w.GetTransaction.GetTransaction(ctx, tt.id.ID)
			if err != nil || got.PostedOn != nil {
				t.Errorf("GetTransaction = %+v, %v, want no posting date: the call was refused", got, err)
			}
		})
	}
	after := w.readStatement(nil)
	if twdOf(after.CardStatementSummary) != twdOf(before.CardStatementSummary) || !slices.Equal(linesOf(after), linesOf(before)) {
		t.Errorf("the statement changed by refused calls: %q on %q, was %q on %q",
			twdOf(after.CardStatementSummary), linesOf(after), twdOf(before.CardStatementSummary), linesOf(before))
	}

	// An expense can be recorded with its posting date when it is made, and it has to be
	// the expense of a card.
	d = w.details(w.key())
	if _, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.cash, InstrumentID: model.BaseCurrencyID,
		Lines:    []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("10")}},
		PostedOn: new(dateOn(2026, time.November, 20)),
	}); !errors.Is(err, model.ErrNoCardPosting) {
		t.Errorf("RecordExpense from cash with a posting date: err = %v, want ErrNoCardPosting", err)
	}

	// Only a card has statements.
	for name, account := range map[string]usecase.GetCardStatementInput{"the bank": {AccountID: w.bank}, "cash": {AccountID: w.cash}} {
		if _, err := w.GetCardStatement.GetCardStatement(ctx, account); !errors.Is(err, model.ErrNoStatements) {
			t.Errorf("the statement of %s: err = %v, want ErrNoStatements", name, err)
		}
	}
	if _, err := w.SetAccountStatementDays.SetAccountStatementDays(ctx, usecase.SetAccountStatementDaysInput{ID: w.bank, ClosingDay: new(5)}); !errors.Is(err, model.ErrNotACard) {
		t.Errorf("a closing day for the bank: err = %v, want ErrNotACard", err)
	}
}

// planPeriods reads the periods of the plan.
func (w *budgetWorld) planPeriods(origin usecase.Transaction) []usecase.Transaction {
	w.t.Helper()
	plan, err := w.GetInstalmentPlan.GetInstalmentPlan(context.Background(), origin.ID)
	if err != nil {
		w.t.Fatalf("GetInstalmentPlan: %v", err)
	}
	return plan.Periods
}

// Two consequences that are documented rather than guarded. The closing day is not
// historised, so changing it cuts the past statements again; and Reconcile goes by
// the day a transaction happened, so against a bank that has posted what happened
// before the closing date after it, what to compare is the Closing of the statement,
// and not the balance of the closing date.
func TestTheClosingDayIsNotHistorisedAndReconcileGoesByTheDayATransactionHappenedThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	w := newBudgetWorld(t)
	w.cardDays(new(5), new(20))
	w.cardExpense(dateOn(2026, time.October, 20), "1000", nil)
	moved := w.cardExpense(dateOn(2026, time.November, 5), "500", nil)
	w.cardExpense(dateOn(2026, time.November, 7), "200", nil)
	w.postedOn(moved, new(dateOn(2026, time.November, 6)))
	fifth := dateOn(2026, time.November, 5)

	// The bank's statement closing on the 5th says 1,000 owed: that is the Closing of
	// the statement, which has not got the purchase the bank posted on the 6th.
	statement := w.readStatement(&fifth)
	if twdOf(statement.CardStatementSummary) != "0 1000 0 -1000" {
		t.Fatalf("statement = %q, want the bank's: 1,000 owed at the close", twdOf(statement.CardStatementSummary))
	}
	// The balance of the 5th is 500 more, which is what Reconcile compares with what
	// it is told: so the bank's 1,000 on the 5th makes a difference of 500 where there
	// is none.
	if got := w.heldOn(&fifth); got != "-1500" {
		t.Fatalf("balance of the 5th = %s, want -1500, by the day it happened", got)
	}
	d := w.details(w.key())
	d.OccurredOn = fifth
	reconciled, err := w.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
		RecordDetails: d, AccountID: w.card, InstrumentID: model.BaseCurrencyID, ActualBalance: dec("-1000"),
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !reconciled.RecordedBalance.Equal(dec("-1500")) || !reconciled.Difference.Equal(dec("500")) {
		t.Errorf("Reconcile = recorded %s, difference %s, want recorded -1500 and a difference of 500: it goes by the day it happened", reconciled.RecordedBalance, reconciled.Difference)
	}
	if reconciled.Transaction == nil {
		t.Fatal("Reconcile drafted nothing, want the draft of the difference")
	}
	if err := w.Discard.DiscardDraft(ctx, reconciled.Transaction.ID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}

	// The closing day is changed to the 10th: the statements are cut again, from the
	// day the card has now. The statement of the 5th of November is gone, and the day
	// is on the one that closes on the 10th, with what that has.
	w.cardDays(new(10), new(25))
	recut := w.readStatement(&fifth)
	wantStatus(t, "the statement of the 5th after the closing day is changed", []string{statementOf(recut.CardStatementSummary), twdOf(recut.CardStatementSummary)},
		"2026-10-11..2026-11-10 due 2026-11-25 closed", "0 1700 0 -1700")
	list, err := w.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
		AccountID: w.card, From: dateOn(2026, time.October, 1), To: dateOn(2026, time.November, 30),
	})
	if err != nil {
		t.Fatalf("ListCardStatements: %v", err)
	}
	var closings []string
	for _, s := range list.Statements {
		closings = append(closings, s.ClosingOn.Format("01-02"))
	}
	wantStatus(t, "the closing dates", closings, "11-10", "10-10")

	// And with no closing day the card has no statements.
	w.cardDays(nil, nil)
	if _, err := w.GetCardStatement.GetCardStatement(ctx, usecase.GetCardStatementInput{AccountID: w.card}); !errors.Is(err, model.ErrNoStatements) {
		t.Errorf("GetCardStatement of a card with no closing day: err = %v, want ErrNoStatements", err)
	}
	if _, err := w.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
		AccountID: w.card, From: dateOn(2026, time.October, 1), To: dateOn(2026, time.November, 30),
	}); !errors.Is(err, model.ErrNoStatements) {
		t.Errorf("ListCardStatements of a card with no closing day: err = %v, want ErrNoStatements", err)
	}
}
