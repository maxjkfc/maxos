package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// on is a day, as a date at midnight UTC, the way the callers of the use cases give
// one.
func on(year int, month time.Month, d int) time.Time {
	return time.Date(year, month, d, 0, 0, 0, 0, time.UTC)
}

// The tests of the statements are of the card of the env, which closes on the 5th
// and is to be paid by the 20th, on the 14th of November 2026: the statement of the
// 6th of October to the 5th of November has closed, and the one of the 6th of
// November to the 5th of December is open.

// cardExpenseInput is an expense of the amount on the card.
func (e *env) cardExpenseInput(key string, day time.Time, amount string) usecase.RecordExpenseInput {
	return usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: key, OccurredOn: day},
		AccountID:     &e.card.ID,
		InstrumentID:  e.twd.ID,
		Lines:         []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec(amount)}},
	}
}

// cardDraft records an expense on the card, a draft.
func (e *env) cardDraft(key string, day time.Time, amount string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), e.cardExpenseInput(key, day, amount))
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return out.Transaction
}

// spendOnCard records an expense on the card and confirms it.
func (e *env) spendOnCard(key string, day time.Time, amount string) usecase.Transaction {
	e.t.Helper()
	return e.confirm(e.cardDraft(key, day, amount))
}

// payCard moves money from cash into the card, which is a payment of what it owes,
// and confirms it.
func (e *env) payCard(key string, day time.Time, amount string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: key, OccurredOn: day},
		FromAccountID: e.cash.ID, ToAccountID: e.card.ID, InstrumentID: e.twd.ID, Amount: dec(amount),
	})
	if err != nil {
		e.t.Fatalf("RecordTransfer(%q): %v", key, err)
	}
	return e.confirm(out.Transaction)
}

// postingDate sets the posting date of the transaction, and fails the test when it
// is refused.
func (e *env) postingDate(txn usecase.Transaction, postedOn *time.Time) usecase.Transaction {
	e.t.Helper()
	got, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: txn.ID, PostedOn: postedOn})
	if err != nil {
		e.t.Fatalf("SetPostingDate: %v", err)
	}
	return got
}

// statement reads the statement of the card that has the day, today for nil.
func (e *env) statement(day *time.Time) usecase.CardStatement {
	e.t.Helper()
	got, err := e.svc.GetCardStatement(context.Background(), usecase.GetCardStatementInput{AccountID: e.card.ID, ClosingOn: day})
	if err != nil {
		e.t.Fatalf("GetCardStatement: %v", err)
	}
	return got
}

// at puts the clock at noon, in the timezone of the application, of the day.
func (e *env) at(day time.Time) {
	e.clock.now = time.Date(day.Year(), day.Month(), day.Day(), 12, 0, 0, 0, taipei)
}

// figures is what the statement says of the instrument, as "previous charges
// credits closing", and "-" when it says nothing of it.
func figures(s usecase.CardStatementSummary, instrument model.Instrument) string {
	for _, f := range s.Figures {
		if f.InstrumentID == instrument.ID {
			return fmt.Sprintf("%s %s %s %s", f.Previous, f.Charges, f.Credits, f.Closing)
		}
	}
	return "-"
}

// daysOf is a statement as the days it covers.
func daysOf(s usecase.CardStatementSummary) string {
	return s.From.Format(time.DateOnly) + ".." + s.ClosingOn.Format(time.DateOnly)
}

// historyOfTheCard puts on the card what the tests of the statements read: 700 in
// September, 1,000 in October, 500 on the 5th of November, which is the day the
// statement closes, 200 and 300 later in November and a payment of 400 on the 12th.
// It returns the purchase of the 5th.
func (e *env) historyOfTheCard() (onClosingDate usecase.Transaction) {
	e.t.Helper()
	e.spendOnCard("card-spend-sep20", on(2026, time.September, 20), "700")
	e.spendOnCard("card-spend-oct20", on(2026, time.October, 20), "1000")
	onClosingDate = e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")
	e.spendOnCard("card-spend-nov06", on(2026, time.November, 6), "200")
	e.spendOnCard("card-spend-nov10", on(2026, time.November, 10), "300")
	e.payCard("card-payment-nov12", on(2026, time.November, 12), "400")
	return onClosingDate
}

func TestSetPostingDateGivesChangesAndTakesAwayTheDateOfADraftAndOfAConfirmedTransaction(t *testing.T) {
	for _, tt := range []struct {
		name    string
		confirm bool
	}{{"a draft", false}, {"a confirmed transaction", true}} {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			txn := e.cardDraft("card-spend-nov05", on(2026, time.November, 5), "500")
			if tt.confirm {
				txn = e.confirm(txn)
			}
			if txn.PostedOn != nil {
				t.Fatalf("posted on %v when recorded, want none", txn.PostedOn)
			}
			opened := e.tx.calls

			given := e.postingDate(txn, new(on(2026, time.November, 6)))

			if given.PostedOn == nil || !given.PostedOn.Equal(on(2026, time.November, 6)) || given.Status != txn.Status || given.ID != txn.ID {
				t.Fatalf("transaction = %+v, want the same transaction, %s, posted on the 6th", given, txn.Status)
			}
			if stored := e.txns.byID[txn.ID]; stored.PostedOn == nil || !stored.PostedOn.Equal(on(2026, time.November, 6)) {
				t.Errorf("stored posting date = %v, want the 6th", stored.PostedOn)
			}
			if got := e.tx.calls - opened; got != 1 {
				t.Errorf("%d database transactions opened, want 1: the read, the lock and the write are one", got)
			}

			changed := e.postingDate(txn, new(on(2026, time.November, 9)))
			if changed.PostedOn == nil || !changed.PostedOn.Equal(on(2026, time.November, 9)) {
				t.Errorf("changed posting date = %v, want the 9th", changed.PostedOn)
			}

			cleared := e.postingDate(txn, nil)
			if cleared.PostedOn != nil || e.txns.byID[txn.ID].PostedOn != nil {
				t.Errorf("posting date after clearing it = %v, stored %v, want none", cleared.PostedOn, e.txns.byID[txn.ID].PostedOn)
			}
		})
	}
}

func TestSetPostingDateGivingTheDateItHasSucceedsAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	txn := e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")

	first := e.postingDate(txn, new(on(2026, time.November, 6)))
	again := e.postingDate(txn, new(on(2026, time.November, 6)))

	if again.PostedOn == nil || !again.PostedOn.Equal(on(2026, time.November, 6)) || again.Status != "confirmed" {
		t.Fatalf("second call = %+v, want it to succeed with the same date", again)
	}
	if !reflect.DeepEqual(first, again) {
		t.Errorf("second call = %+v, want what the first returned, %+v", again, first)
	}
	// And so is clearing what is not there.
	e.postingDate(txn, nil)
	if cleared := e.postingDate(txn, nil); cleared.PostedOn != nil {
		t.Errorf("clearing twice = %+v, want no date", cleared)
	}
}

func TestSetPostingDateOnlyTouchesThePostingDate(t *testing.T) {
	e := newEnv(t)
	txn := e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")
	before := e.txns.byID[txn.ID]
	balanceBefore := e.held(e.card, e.twd, on(2026, time.November, 5))

	got := e.postingDate(txn, new(on(2026, time.November, 6)))

	after := e.txns.byID[txn.ID]
	want := before
	want.PostedOn = after.PostedOn
	if !reflect.DeepEqual(after, want) {
		t.Errorf("stored = %+v, want what it was, with the posting date only", after)
	}
	if !got.OccurredOn.Equal(on(2026, time.November, 5)) {
		t.Errorf("occurred on %s, want the 5th: the posting date is not the day it happened", got.OccurredOn.Format(time.DateOnly))
	}
	// A balance goes by the day it happened, so the purchase of the 5th is still in
	// the balance of the 5th, though the statement of the 5th does not have it.
	if balanceAfter := e.held(e.card, e.twd, on(2026, time.November, 5)); !balanceAfter.Equal(balanceBefore) || !balanceAfter.Equal(dec("-500")) {
		t.Errorf("balance of the 5th after = %s, before %s, want -500 both times", balanceAfter, balanceBefore)
	}
}

func TestSetPostingDateRefusesWhatCannotHaveOneAndWritesNothing(t *testing.T) {
	e := newEnv(t)
	cashLunch := e.confirmedLunch("lunch-0000001") // paid from cash
	voided := e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	earlyDraft := e.cardDraft("card-spend-nov10", on(2026, time.November, 10), "300")
	paidByFriend := func() usecase.Transaction {
		friend := e.refs.addParty("Amy", false)
		out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
			RecordDetails: e.details("friend-paid-0001"), PaidByPartyID: &friend, InstrumentID: e.twd.ID,
			Lines: []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("90")}},
		})
		if err != nil {
			t.Fatalf("RecordExpense paid by a party: %v", err)
		}
		return out.Transaction
	}()
	unknown := uuid.Must(uuid.NewV7())

	tests := []struct {
		name     string
		id       uuid.UUID
		postedOn *time.Time
		want     error
		locked   bool
	}{
		{"an expense paid from cash, which is not a card", cashLunch.ID, new(on(2026, time.November, 20)), model.ErrNoCardPosting, true},
		{"an expense a party paid, which has no account posting", paidByFriend.ID, new(on(2026, time.November, 20)), model.ErrNoCardPosting, true},
		{"a void transaction", voided.ID, new(on(2026, time.November, 20)), model.ErrTransactionVoid, false},
		{"a date before the day it happened", earlyDraft.ID, new(on(2026, time.November, 9)), model.ErrPostedBeforeOccurred, true},
		{"a transaction that is not there", unknown, new(on(2026, time.November, 20)), model.ErrNotFound, false},
		{"taking the date away from an expense paid from cash", cashLunch.ID, nil, model.ErrNoCardPosting, true},
		{"taking the date away from a void transaction", voided.ID, nil, model.ErrTransactionVoid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			locks, writes := len(e.txns.locked), len(e.txns.postedOnSet)

			_, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: tt.id, PostedOn: tt.postedOn})

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if got := len(e.txns.postedOnSet) - writes; got != 0 {
				t.Errorf("%d posting dates written by a call that was refused, want none", got)
			}
			wantLocks := 0
			if tt.locked {
				wantLocks = 1
			}
			if got := len(e.txns.locked) - locks; got != wantLocks {
				t.Errorf("%d locks taken, want %d: a transaction that is refused as it is read is not locked", got, wantLocks)
			}
		})
	}
	if stored := e.txns.byID[earlyDraft.ID]; stored.PostedOn != nil {
		t.Errorf("a draft whose date was refused has the posting date %v, want none", stored.PostedOn)
	}
}

func TestSetPostingDateOfAnInstalmentPlanIsRefusedWithoutLockingIt(t *testing.T) {
	e := newEnv(t)
	schedule := e.schedule(3, "1000")
	out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: e.details("laptop-00000001"), AccountID: &e.card.ID, InstrumentID: e.twd.ID,
		Lines:       []usecase.CategoryLine{{CategoryID: e.household.ID, Amount: dec("3000")}},
		Instalments: &schedule,
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	origin := e.confirm(out.Transaction)
	plan := e.planOf(origin)
	members := append([]usecase.Transaction{plan.Origin}, plan.Periods...)
	locksBefore := len(e.txns.locked) // confirming the plan took the locks of the plan

	for _, member := range members {
		_, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: member.ID, PostedOn: new(on(2027, time.January, 20))})

		if !errors.Is(err, model.ErrPartOfPlan) {
			t.Errorf("transaction %s of the plan (position %d): err = %v, want ErrPartOfPlan", member.ID, *member.PlanSeq, err)
		}
	}

	if got := len(e.txns.locked) - locksBefore; got != 0 {
		t.Errorf("%d transactions locked, want none: the plan is acted on through its origin, and a period is never locked by a call that does not hold its origin", got)
	}
	if len(e.txns.postedOnSet) != 0 {
		t.Errorf("%d posting dates written, want none", len(e.txns.postedOnSet))
	}
}

// The status is read again once the lock is held: what the lock waited for may have
// changed it, and a date must not be given to a transaction that another call has
// voided meanwhile.
func TestSetPostingDateChecksTheTransactionThatTheLockReturns(t *testing.T) {
	t.Run("one that is voided while the call waits for the lock", func(t *testing.T) {
		e := newEnv(t)
		txn := e.spendOnCard("card-spend-nov05", on(2026, time.November, 5), "500")
		e.txns.beforeLock = func(id uuid.UUID) {
			voided := e.txns.byID[id]
			voided.Status = model.StatusVoid
			e.txns.byID[id] = voided
		}

		_, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: txn.ID, PostedOn: new(on(2026, time.November, 6))})

		if !errors.Is(err, model.ErrTransactionVoid) {
			t.Fatalf("err = %v, want ErrTransactionVoid: the transaction was confirmed when it was read and void when the lock came", err)
		}
		if len(e.txns.postedOnSet) != 0 {
			t.Errorf("%d posting dates written, want none", len(e.txns.postedOnSet))
		}
	})
	t.Run("one that is discarded while the call waits for the lock", func(t *testing.T) {
		e := newEnv(t)
		draft := e.cardDraft("card-spend-nov05", on(2026, time.November, 5), "500")
		e.txns.beforeLock = func(id uuid.UUID) { delete(e.txns.byID, id) }

		_, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: draft.ID, PostedOn: new(on(2026, time.November, 6))})

		if !errors.Is(err, model.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("one that is confirmed while the call waits for the lock", func(t *testing.T) {
		e := newEnv(t)
		draft := e.cardDraft("card-spend-nov05", on(2026, time.November, 5), "500")
		e.txns.beforeLock = func(id uuid.UUID) {
			confirmed := e.txns.byID[id]
			confirmed.Status = model.StatusConfirmed
			e.txns.byID[id] = confirmed
		}

		got, err := e.svc.SetPostingDate(context.Background(), usecase.SetPostingDateInput{TransactionID: draft.ID, PostedOn: new(on(2026, time.November, 6))})

		if err != nil || got.Status != "confirmed" || got.PostedOn == nil {
			t.Fatalf("got %+v, %v, want the transaction as it is once confirmed, with the date", got, err)
		}
	})
}

func TestSetPostingDateTakesTheDayOfTheDateInItsOwnLocation(t *testing.T) {
	e := newEnv(t)
	txn := e.cardDraft("card-spend-nov05", on(2026, time.November, 5), "500")
	// The 6th in Taipei, though it is still the 5th in UTC.
	justAfterMidnight := time.Date(2026, time.November, 6, 0, 30, 0, 0, taipei)

	got := e.postingDate(txn, &justAfterMidnight)

	if got.PostedOn == nil || !got.PostedOn.Equal(on(2026, time.November, 6)) {
		t.Fatalf("posted on %v, want the 6th at midnight UTC", got.PostedOn)
	}
}

func TestRecordExpenseOnACardTakesAPostingDate(t *testing.T) {
	e := newEnv(t)
	in := e.cardExpenseInput("card-spend-nov05", on(2026, time.November, 5), "500")
	in.PostedOn = new(on(2026, time.November, 6))

	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	got := out.Transaction
	if got.PostedOn == nil || !got.PostedOn.Equal(on(2026, time.November, 6)) || !got.OccurredOn.Equal(on(2026, time.November, 5)) || got.Status != "draft" {
		t.Fatalf("transaction = %+v, want a draft of the 5th posted on the 6th", got)
	}
	read, err := e.svc.GetTransaction(context.Background(), got.ID)
	if err != nil || read.PostedOn == nil || !read.PostedOn.Equal(on(2026, time.November, 6)) {
		t.Errorf("GetTransaction = %+v, %v, want the posting date it was recorded with", read, err)
	}
	e.txns.listResult = []model.Transaction{e.txns.byID[got.ID]}
	listed, err := e.svc.ListTransactions(context.Background(), usecase.ListTransactionsInput{})
	if err != nil || len(listed.Transactions) != 1 || listed.Transactions[0].PostedOn == nil || !listed.Transactions[0].PostedOn.Equal(on(2026, time.November, 6)) {
		t.Errorf("ListTransactions = %+v, %v, want the posting date it was recorded with", listed, err)
	}
}

func TestRecordExpenseRefusesAPostingDateThatCannotBeAndStoresNothing(t *testing.T) {
	e := newEnv(t)
	friend := e.refs.addParty("Amy", false)
	tests := []struct {
		name string
		in   func() usecase.RecordExpenseInput
		want error
	}{
		{"a date before the day it happened", func() usecase.RecordExpenseInput {
			in := e.cardExpenseInput("card-spend-0001", on(2026, time.November, 5), "500")
			in.PostedOn = new(on(2026, time.November, 4))
			return in
		}, model.ErrPostedBeforeOccurred},
		{"an expense from an account that is not a card", func() usecase.RecordExpenseInput {
			in := e.lunch("lunch-0000001")
			in.PostedOn = new(on(2026, time.November, 20))
			return in
		}, model.ErrNoCardPosting},
		{"an expense a party paid", func() usecase.RecordExpenseInput {
			return usecase.RecordExpenseInput{
				RecordDetails: e.details("friend-paid-0001"), PaidByPartyID: &friend, InstrumentID: e.twd.ID,
				Lines:    []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("90")}},
				PostedOn: new(on(2026, time.November, 20)),
			}
		}, model.ErrNoCardPosting},
		{"an expense paid in instalments, whose origin and periods have no posting date", func() usecase.RecordExpenseInput {
			schedule := e.schedule(3, "1000")
			return usecase.RecordExpenseInput{
				RecordDetails: e.details("laptop-00000001"), AccountID: &e.card.ID, InstrumentID: e.twd.ID,
				Lines:       []usecase.CategoryLine{{CategoryID: e.household.ID, Amount: dec("3000")}},
				Instalments: &schedule,
				PostedOn:    new(on(2026, time.November, 20)),
			}
		}, model.ErrPartOfPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.RecordExpense(context.Background(), tt.in())

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if e.txns.inserts != 0 {
				t.Errorf("%d transactions stored, want none", e.txns.inserts)
			}
		})
	}
}

func TestGetCardStatementDefaultsToTheStatementThatIsOpenToday(t *testing.T) {
	e := newEnv(t)
	e.historyOfTheCard()

	got := e.statement(nil)

	if daysOf(got.CardStatementSummary) != "2026-11-06..2026-12-05" || got.Closed {
		t.Fatalf("statement = %s, closed %v, want the open one, the 6th of November to the 5th of December", daysOf(got.CardStatementSummary), got.Closed)
	}
	if got.DueOn == nil || !got.DueOn.Equal(on(2026, time.December, 20)) {
		t.Errorf("due on %v, want the 20th of December: the first 20th after it closes", got.DueOn)
	}
	asked := e.txns.statementsAsked[len(e.txns.statementsAsked)-1]
	if asked.AccountID != e.card.ID || !asked.Today.Equal(on(2026, time.November, 14)) {
		t.Errorf("the repo was asked about %+v, want the card and today as the clock says, the 14th", asked)
	}
	if asked.Period.From != got.From || asked.Period.ClosingOn != got.ClosingOn {
		t.Errorf("the repo was asked for %s, want the statement that is returned", asked.Period.ClosingOn.Format(time.DateOnly))
	}
}

func TestGetCardStatementOfADayReturnsTheStatementThatHasIt(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name       string
		day        time.Time
		wantDays   string
		wantDue    string
		wantClosed bool
	}{
		{"a day in a statement that has closed", on(2026, time.October, 20), "2026-10-06..2026-11-05", "2026-11-20", true},
		{"its closing date", on(2026, time.November, 5), "2026-10-06..2026-11-05", "2026-11-20", true},
		{"its first day", on(2026, time.October, 6), "2026-10-06..2026-11-05", "2026-11-20", true},
		{"the day after its closing date, which is on the next", on(2026, time.November, 6), "2026-11-06..2026-12-05", "2026-12-20", false},
		{"the closing date of the open one", on(2026, time.December, 5), "2026-11-06..2026-12-05", "2026-12-20", false},
		{"one that is yet to come", on(2027, time.January, 20), "2027-01-06..2027-02-05", "2027-02-20", false},
		{"across the end of the year", on(2026, time.December, 20), "2026-12-06..2027-01-05", "2027-01-20", false},
		{"the day in the timezone of the time that names it", time.Date(2026, time.November, 6, 0, 30, 0, 0, taipei), "2026-11-06..2026-12-05", "2026-12-20", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := e.statement(&tt.day)

			if daysOf(got.CardStatementSummary) != tt.wantDays || got.DueOn == nil || got.DueOn.Format(time.DateOnly) != tt.wantDue || got.Closed != tt.wantClosed {
				t.Fatalf("statement = %s due %v closed %v, want %s due %s closed %v", daysOf(got.CardStatementSummary), got.DueOn, got.Closed, tt.wantDays, tt.wantDue, tt.wantClosed)
			}
		})
	}
}

// A statement is closed when its closing date is before today: on the closing date
// itself it is still open, and a day later it is closed.
func TestAStatementIsClosedFromTheDayAfterItsClosingDate(t *testing.T) {
	e := newEnv(t)
	statement := on(2026, time.November, 5)
	tests := []struct {
		today time.Time
		want  bool
	}{
		{on(2026, time.November, 4), false},
		{on(2026, time.November, 5), false},
		{on(2026, time.November, 6), true},
	}
	for _, tt := range tests {
		e.at(tt.today)

		got := e.statement(&statement)

		if got.Closed != tt.want {
			t.Errorf("on %s the statement closing on the 5th is closed = %v, want %v", tt.today.Format(time.DateOnly), got.Closed, tt.want)
		}
	}

	// With today as the closing date, the statement of today is the one that closes.
	e.at(on(2026, time.November, 5))
	if got := e.statement(nil); daysOf(got.CardStatementSummary) != "2026-10-06..2026-11-05" || got.Closed {
		t.Errorf("on the closing date the statement of today is %s closed %v, want the one that closes today, still open", daysOf(got.CardStatementSummary), got.Closed)
	}
	e.at(on(2026, time.November, 6))
	if got := e.statement(nil); daysOf(got.CardStatementSummary) != "2026-11-06..2026-12-05" || got.Closed {
		t.Errorf("the day after the statement of today is %s closed %v, want the next, open", daysOf(got.CardStatementSummary), got.Closed)
	}
}

func TestGetCardStatementAddsUpTheConfirmedPostingsToTheCardPerInstrument(t *testing.T) {
	e := newEnv(t)
	e.historyOfTheCard()
	// A purchase in dollars on the card is another instrument on the same statement.
	usdLine := usecase.CategoryLine{CategoryID: e.food.ID, Amount: dec("50"), BaseAmount: decPtr("1600")}
	out, err := e.svc.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "card-spend-usd-1", OccurredOn: on(2026, time.November, 8)},
		AccountID:     &e.card.ID, InstrumentID: e.usd.ID, Lines: []usecase.CategoryLine{usdLine},
	})
	if err != nil {
		t.Fatalf("RecordExpense in dollars: %v", err)
	}
	e.confirm(out.Transaction)

	open := e.statement(nil)
	closed := e.statement(new(on(2026, time.October, 20)))

	// 700 + 1,000 + 500 owed at the end of the closed one; the open one starts from
	// that, spends 500 and pays 400.
	if got := figures(closed.CardStatementSummary, e.twd); got != "-700 1500 0 -2200" {
		t.Errorf("closed statement in TWD = %q, want %q: previous, charges, credits, closing", got, "-700 1500 0 -2200")
	}
	if got := figures(open.CardStatementSummary, e.twd); got != "-2200 500 400 -2300" {
		t.Errorf("open statement in TWD = %q, want %q", got, "-2200 500 400 -2300")
	}
	if got := figures(open.CardStatementSummary, e.usd); got != "0 50 0 -50" {
		t.Errorf("open statement in USD = %q, want %q", got, "0 50 0 -50")
	}
	if got := figures(closed.CardStatementSummary, e.usd); got != "-" {
		t.Errorf("closed statement in USD = %q, want none: nothing was posted to dollars on it", got)
	}
	for _, s := range []usecase.CardStatement{closed, open} {
		for _, f := range s.Figures {
			if want := f.Previous.Sub(f.Charges).Add(f.Credits); !f.Closing.Equal(want) {
				t.Errorf("closing %s is not previous %s less charges %s plus credits %s", f.Closing, f.Previous, f.Charges, f.Credits)
			}
		}
	}
	var closedTWD, openTWD usecase.StatementFigures
	for _, f := range closed.Figures {
		if f.InstrumentID == e.twd.ID {
			closedTWD = f
		}
	}
	for _, f := range open.Figures {
		if f.InstrumentID == e.twd.ID {
			openTWD = f
		}
	}
	if !closedTWD.Closing.Equal(openTWD.Previous) {
		t.Errorf("the closed statement closes at %s and the open one starts from %s, want them the same", closedTWD.Closing, openTWD.Previous)
	}
}

func TestGetCardStatementListsItsTransactionsByStatementDateAndAMovedOneIsOnTheNext(t *testing.T) {
	e := newEnv(t)
	moved := e.historyOfTheCard() // 500 on the 5th, the closing date

	before := e.statement(new(on(2026, time.November, 5)))
	e.postingDate(moved, new(on(2026, time.November, 6)))
	closed := e.statement(new(on(2026, time.November, 5)))
	open := e.statement(nil)

	lines := func(s usecase.CardStatement) []string {
		var out []string
		for _, txn := range s.Transactions {
			posted := "-"
			if txn.Transaction.PostedOn != nil {
				posted = txn.Transaction.PostedOn.Format("01-02")
			}
			var amounts []string
			for _, a := range txn.Amounts {
				amounts = append(amounts, signed(a.Quantity)+" "+e.names()[a.InstrumentID])
			}
			out = append(out, fmt.Sprintf("%s happened %s posted %s %s %s", txn.StatementDate.Format("01-02"), txn.Transaction.OccurredOn.Format("01-02"), posted, txn.Transaction.Kind, strings.Join(amounts, ",")))
		}
		return out
	}
	// Before the date, the purchase of the 5th is the last of the closed one.
	if got := lines(before); len(got) != 2 || got[0] != "11-05 happened 11-05 posted - expense -500 TWD" {
		t.Fatalf("closed statement before = %q, want the purchase of the 5th first of its two", got)
	}
	wantClosed := []string{"10-20 happened 10-20 posted - expense -1000 TWD"}
	if got := lines(closed); !slices.Equal(got, wantClosed) {
		t.Errorf("closed statement after = %q, want %q: the purchase of the 5th is out of it", got, wantClosed)
	}
	// Newest statement date first; for the same statement date, the day it happened
	// newest first: the purchase of the 6th, then the one of the 5th that was posted on
	// the 6th.
	wantOpen := []string{
		"11-12 happened 11-12 posted - transfer +400 TWD",
		"11-10 happened 11-10 posted - expense -300 TWD",
		"11-06 happened 11-06 posted - expense -200 TWD",
		"11-06 happened 11-05 posted 11-06 expense -500 TWD",
	}
	if got := lines(open); !slices.Equal(got, wantOpen) {
		t.Errorf("open statement = %q, want %q", got, wantOpen)
	}

	if got := figures(closed.CardStatementSummary, e.twd); got != "-700 1000 0 -1700" {
		t.Errorf("closed statement after the date = %q, want %q", got, "-700 1000 0 -1700")
	}
	if got := figures(open.CardStatementSummary, e.twd); got != "-1700 1000 400 -2300" {
		t.Errorf("open statement after the date = %q, want %q: it starts from what the closed one ends with", got, "-1700 1000 400 -2300")
	}
	// The transaction as returned carries what GetTransaction says of it.
	for _, txn := range open.Transactions {
		if txn.Transaction.ID == moved.ID && (txn.Transaction.Status != "confirmed" || len(txn.Transaction.Postings) != 2) {
			t.Errorf("the moved transaction = %+v, want it as GetTransaction returns it, with its postings", txn.Transaction)
		}
	}
}

func TestGetCardStatementCountsWhatIsDatedUpToTodayAsTheClockSaysAndNoMore(t *testing.T) {
	e := newEnv(t)
	e.spendOnCard("card-spend-nov10", on(2026, time.November, 10), "300")
	e.spendOnCard("card-spend-nov20", on(2026, time.November, 20), "250") // dated after today, the 14th
	e.payCard("card-payment-dec01", on(2026, time.December, 1), "100")

	tests := []struct {
		name     string
		today    time.Time
		want     string
		wantLine int
	}{
		{"the 14th: only what has happened", on(2026, time.November, 14), "0 300 0 -300", 1},
		{"the day before the purchase of the 20th", on(2026, time.November, 19), "0 300 0 -300", 1},
		{"the 20th, the day of a purchase, which counts from its day", on(2026, time.November, 20), "0 550 0 -550", 2},
		{"the 30th", on(2026, time.November, 30), "0 550 0 -550", 2},
		{"the 1st of December, with the payment", on(2026, time.December, 1), "0 550 100 -450", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.at(tt.today)

			got := e.statement(new(on(2026, time.November, 20)))

			if f := figures(got.CardStatementSummary, e.twd); f != tt.want || len(got.Transactions) != tt.wantLine {
				t.Fatalf("figures %q on %d transactions, want %q on %d", f, len(got.Transactions), tt.want, tt.wantLine)
			}
			if asked := e.txns.statementsAsked[len(e.txns.statementsAsked)-1]; !asked.Today.Equal(model.DateOf(tt.today)) {
				t.Errorf("the repo was asked about %s, want today as the clock reads it, %s", asked.Today.Format(time.DateOnly), tt.today.Format(time.DateOnly))
			}
		})
	}

	// A clock just after midnight in Taipei is still the day before in UTC: today is the
	// day it is in the timezone of the clock.
	e.clock.now = time.Date(2026, time.November, 20, 0, 30, 0, 0, taipei)
	if got := e.statement(new(on(2026, time.November, 20))); figures(got.CardStatementSummary, e.twd) != "0 550 0 -550" {
		t.Errorf("half past midnight on the 20th in Taipei: figures %q, want the purchase of the 20th counted", figures(got.CardStatementSummary, e.twd))
	}
}

func TestGetCardStatementCountsTheDraftsThatAreOnItAndLeavesThemOutOfTheFigures(t *testing.T) {
	e := newEnv(t)
	e.historyOfTheCard()
	e.cardDraft("card-draft-nov08", on(2026, time.November, 8), "100")
	postedLater := e.cardDraft("card-draft-nov03", on(2026, time.November, 3), "80") // on the closed one by its day
	e.postingDate(postedLater, new(on(2026, time.November, 7)))                      // and on the open one by its posting date
	e.cardDraft("card-draft-nov30", on(2026, time.November, 30), "60")               // dated after today, in the range
	e.cardDraft("card-draft-dec20", on(2026, time.December, 20), "40")               // on a later statement
	voided := e.spendOnCard("card-spend-nov09", on(2026, time.November, 9), "30")
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	open := e.statement(nil)
	closed := e.statement(new(on(2026, time.November, 5)))

	if open.Drafts != 3 {
		t.Errorf("open statement: %d drafts, want 3: the 8th, the one posted on the 7th and the one of the 30th, which is dated after today and would be on the statement from its day", open.Drafts)
	}
	if closed.Drafts != 0 {
		t.Errorf("closed statement: %d drafts, want none: the draft of the 3rd was posted on the 7th", closed.Drafts)
	}
	// A draft counts nowhere else, and neither does a void transaction.
	if got := figures(open.CardStatementSummary, e.twd); got != "-2200 500 400 -2300" {
		t.Errorf("open statement figures = %q, want %q: the drafts and the void transaction are not in them", got, "-2200 500 400 -2300")
	}
	if len(open.Transactions) != 3 {
		t.Errorf("open statement lists %d transactions, want the 3 confirmed ones", len(open.Transactions))
	}
}

func TestGetCardStatementHasNoDueDateWhenTheCardHasNoPaymentDueDay(t *testing.T) {
	e := newEnv(t)
	noDue := e.accounts.addCard("no due", new(5), nil)

	got, err := e.svc.GetCardStatement(context.Background(), usecase.GetCardStatementInput{AccountID: noDue.ID})
	if err != nil {
		t.Fatalf("GetCardStatement: %v", err)
	}
	if got.DueOn != nil || daysOf(got.CardStatementSummary) != "2026-11-06..2026-12-05" {
		t.Errorf("statement = %s due %v, want the open statement with no due date", daysOf(got.CardStatementSummary), got.DueOn)
	}
	if got.Figures == nil || got.Transactions == nil {
		t.Errorf("figures %v, transactions %v, want empty lists and not nil: a statement with nothing on it", got.Figures, got.Transactions)
	}
}

func TestStatementsOfAnAccountThatHasNoneAreRefused(t *testing.T) {
	e := newEnv(t)
	noDays := e.accounts.addCard("no days", nil, nil)
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name    string
		account uuid.UUID
		want    error
	}{
		{"an account that is not a card", e.cash.ID, model.ErrNoStatements},
		{"a card with no closing day", noDays.ID, model.ErrNoStatements},
		{"an account that is not there", unknown, model.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.GetCardStatement(context.Background(), usecase.GetCardStatementInput{AccountID: tt.account})
			if !errors.Is(err, tt.want) {
				t.Errorf("GetCardStatement: err = %v, want %v", err, tt.want)
			}

			_, err = e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
				AccountID: tt.account, From: on(2026, time.October, 1), To: on(2026, time.December, 31),
			})
			if !errors.Is(err, tt.want) {
				t.Errorf("ListCardStatements: err = %v, want %v", err, tt.want)
			}
		})
	}
	if len(e.txns.statementsAsked) != 0 || len(e.txns.figuresAsked) != 0 {
		t.Errorf("the repo was asked %d and %d times, want it not asked about an account that has no statements", len(e.txns.statementsAsked), len(e.txns.figuresAsked))
	}
}

func TestGetCardStatementReportsAFailureOfTheStoreWithWhatItWasReading(t *testing.T) {
	e := newEnv(t)
	boom := errors.New("connection reset")
	e.txns.readErr = boom

	_, err := e.svc.GetCardStatement(context.Background(), usecase.GetCardStatementInput{AccountID: e.card.ID})

	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "card") {
		t.Fatalf("err = %v, want the failure of the store, with the card named", err)
	}
	_, err = e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{AccountID: e.card.ID, From: on(2026, time.October, 1), To: on(2026, time.December, 31)})
	if !errors.Is(err, boom) {
		t.Fatalf("ListCardStatements: err = %v, want the failure of the store", err)
	}
}

func TestListCardStatementsListsTheStatementsClosingInTheRangeNewestFirst(t *testing.T) {
	e := newEnv(t)
	e.historyOfTheCard()

	got, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
		AccountID: e.card.ID, From: on(2026, time.September, 5), To: on(2026, time.December, 5),
	})
	if err != nil {
		t.Fatalf("ListCardStatements: %v", err)
	}

	var days []string
	for _, s := range got.Statements {
		days = append(days, daysOf(s))
	}
	want := []string{"2026-11-06..2026-12-05", "2026-10-06..2026-11-05", "2026-09-06..2026-10-05", "2026-08-06..2026-09-05"}
	if !slices.Equal(days, want) {
		t.Fatalf("statements = %v, want %v: the closing dates from the 5th of September to the 5th of December, both ends included, newest first", days, want)
	}
	for i, s := range got.Statements {
		wantClosed := s.ClosingOn.Before(on(2026, time.November, 14))
		wantDue := []string{"2026-12-20", "2026-11-20", "2026-10-20", "2026-09-20"}[i]
		if s.Closed != wantClosed || s.DueOn == nil || s.DueOn.Format(time.DateOnly) != wantDue {
			t.Errorf("statement %s: closed %v due %v, want closed %v due %s", daysOf(s), s.Closed, s.DueOn, wantClosed, wantDue)
		}
	}
	// The figures: nothing for the one before anything was posted, then the history.
	wantFigures := []string{"-2200 500 400 -2300", "-700 1500 0 -2200", "0 700 0 -700", "-"}
	for i, s := range got.Statements {
		if f := figures(s, e.twd); f != wantFigures[i] {
			t.Errorf("statement %s in TWD = %q, want %q", daysOf(s), f, wantFigures[i])
		}
	}

	// Read together, in one statement of the store, as of today.
	if len(e.txns.figuresAsked) != 1 {
		t.Fatalf("the store was asked for figures %d times, want once for all the statements, which is what makes them agree", len(e.txns.figuresAsked))
	}
	asked := e.txns.figuresAsked[0]
	if asked.account != e.card.ID || len(asked.periods) != 4 || !asked.today.Equal(on(2026, time.November, 14)) {
		t.Errorf("asked %+v, want the card, 4 statements and today as the clock says", asked)
	}
}

func TestTheClosingOfAListedStatementIsThePreviousOfTheNextOne(t *testing.T) {
	e := newEnv(t)
	moved := e.historyOfTheCard()
	e.postingDate(moved, new(on(2026, time.November, 6)))

	got, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
		AccountID: e.card.ID, From: on(2026, time.July, 1), To: on(2027, time.February, 28),
	})
	if err != nil {
		t.Fatalf("ListCardStatements: %v", err)
	}

	statements := slices.Clone(got.Statements)
	slices.Reverse(statements) // oldest first
	for i := 1; i < len(statements); i++ {
		older, newer := statements[i-1], statements[i]
		if !newer.From.Equal(older.ClosingOn.AddDate(0, 0, 1)) {
			t.Errorf("statement %s follows %s, want it to start the day after that closes", daysOf(newer), daysOf(older))
		}
		// An instrument that is not listed is zero all through.
		closing, previous := "0", "0"
		for _, f := range older.Figures {
			if f.InstrumentID == e.twd.ID {
				closing = f.Closing.String()
			}
		}
		for _, f := range newer.Figures {
			if f.InstrumentID == e.twd.ID {
				previous = f.Previous.String()
			}
		}
		if closing != previous {
			t.Errorf("%s closes at %s and %s starts from %s, want them the same", daysOf(older), closing, daysOf(newer), previous)
		}
	}
	// A statement that is yet to come has nothing on it, and carries what the card owes
	// into it: all that was spent less what was paid, which the posting date moved from
	// one statement to the next without changing.
	newest := got.Statements[0]
	if f := figures(newest, e.twd); f != "-2300 0 0 -2300" || newest.Closed {
		t.Errorf("statement %s in TWD = %q, closed %v, want %q and open", daysOf(newest), f, newest.Closed, "-2300 0 0 -2300")
	}
}

func TestListCardStatementsOfARangeWithNoClosingDateIsEmpty(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
		AccountID: e.card.ID, From: on(2026, time.November, 6), To: on(2026, time.December, 4),
	})

	if err != nil {
		t.Fatalf("ListCardStatements: %v", err)
	}
	if got.Statements == nil || len(got.Statements) != 0 {
		t.Errorf("statements = %#v, want an empty list: the 5th is not in the range", got.Statements)
	}
	if len(e.txns.figuresAsked) != 0 {
		t.Errorf("the store was asked for figures %d times, want it not asked when there is no statement", len(e.txns.figuresAsked))
	}
}

func TestListCardStatementsTakesTheDaysInTheirOwnLocationAndBothEnds(t *testing.T) {
	e := newEnv(t)

	got, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
		AccountID: e.card.ID,
		// The 5th in Taipei just after midnight, which is still the 4th in UTC, and the
		// 5th in Taipei just before the end of the day.
		From: time.Date(2026, time.November, 5, 0, 30, 0, 0, taipei),
		To:   time.Date(2026, time.November, 5, 23, 30, 0, 0, taipei),
	})

	if err != nil || len(got.Statements) != 1 || daysOf(got.Statements[0]) != "2026-10-06..2026-11-05" {
		t.Fatalf("statements = %+v, %v, want the one that closes on the 5th", got.Statements, err)
	}
}

func TestListCardStatementsRefusesARangeThatCannotBeListed(t *testing.T) {
	e := newEnv(t)
	tests := []struct {
		name     string
		from, to time.Time
		want     error
	}{
		{"no start", time.Time{}, on(2026, time.December, 31), model.ErrDateRangeRequired},
		{"no end", on(2026, time.October, 1), time.Time{}, model.ErrDateRangeRequired},
		{"an end before the start", on(2026, time.December, 1), on(2026, time.November, 30), model.ErrDateRangeReversed},
		{"a range longer than the longest", on(2016, time.October, 1), on(2026, time.October, 2), model.ErrStatementRangeTooWide},
		{"a range of every day there is", on(1970, time.January, 1), on(9999, time.December, 31), model.ErrStatementRangeTooWide},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{AccountID: e.card.ID, From: tt.from, To: tt.to})

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if len(e.txns.figuresAsked) != 0 {
				t.Errorf("the store was asked for figures, want a range that is refused not read")
			}
		})
	}

	// The longest range is allowed, to its last day.
	got, err := e.svc.ListCardStatements(context.Background(), usecase.ListCardStatementsInput{
		AccountID: e.card.ID, From: on(2016, time.October, 5), To: on(2026, time.October, 5),
	})
	if err != nil || len(got.Statements) != 121 {
		t.Errorf("ten years: %d statements, %v, want 121 and no error: the closing dates of the 5th from October 2016 to October 2026, both ends included", len(got.Statements), err)
	}
}
