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

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// What these tests are about: setting the posting date of a transaction while other
// calls act on the same transaction. The date is written on a row that confirming,
// voiding and discarding lock, so the calls must queue for it as they do for each
// other, see what the one before left, and never wait for each other in a circle. A
// call that gives a transaction of a plan a date is refused without a lock, so that
// it never waits for the locks of the plan.
//
// As in finance_plan_concurrency_integration_test.go, an overlap that matters is made
// to happen exactly: the tracer holds the first call right after it has locked the
// transaction, the others start then, and the first goes on only when each of them
// has finished or is waiting for a lock, which PostgreSQL says. And where every
// overlap has to be right, calls start together, over and over, with no sleep.

// postingDate is the posting date the calls of these tests give, on the 20th of
// November: after the 14th, the day of the transactions.
var postingDate = dateOn(2026, time.November, 20)

// cardDraft records an expense of 120 on the card dated the 14th, a draft.
func (a *plansApp) cardDraft(t *testing.T, card uuid.UUID, key string) usecase.Transaction {
	t.Helper()
	return a.lunch(t, card, key)
}

// setPostingDate sets the posting date of the transaction, with the context of the
// call, which is the deadline it has.
func (a *plansApp) setPostingDate(ctx context.Context, id uuid.UUID, postedOn time.Time) error {
	_, err := a.l.SetPostingDate.SetPostingDate(ctx, usecase.SetPostingDateInput{TransactionID: id, PostedOn: &postedOn})
	return err
}

// stateOf says what the transaction is now: its status and its posting date, as
// "confirmed posted 2026-11-20", and "gone" when it is not there any more.
func (a *plansApp) stateOf(t *testing.T, id uuid.UUID) string {
	t.Helper()
	txn, err := a.l.GetTransaction.GetTransaction(context.Background(), id)
	if errors.Is(err, model.ErrNotFound) {
		return "gone"
	}
	if err != nil {
		t.Fatalf("GetTransaction(%s): %v", id, err)
	}
	posted := "no posting date"
	if txn.PostedOn != nil {
		posted = "posted " + txn.PostedOn.Format(time.DateOnly)
	}
	return txn.Status + " " + posted
}

// Setting the date waits for a void that holds the transaction, and finds it void:
// the order is the void first, the date second, which is refused. Were the status
// looked at before the lock, the date would be put on a void transaction.
func TestSettingThePostingDateWhileTheTransactionIsBeingVoidedWaitsAndIsRefused(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	txn := a.cardDraft(t, a.card(t), "set-while-void-001")
	a.l.confirm(t, txn)

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			_, errs[i] = a.l.Void.VoidTransaction(ctx, txn.ID)
			return
		}
		errs[i] = a.setPostingDate(ctx, txn.ID, postingDate)
	})

	if errs[0] != nil {
		t.Errorf("the void = %v, want it to succeed: it held the lock", errs[0])
	}
	if !errors.Is(errs[1], model.ErrTransactionVoid) {
		t.Errorf("the date = %v, want ErrTransactionVoid: what it waited for was voided", errs[1])
	}
	if got := a.stateOf(t, txn.ID); got != "void no posting date" {
		t.Errorf("the transaction is %q, want it void, with no date", got)
	}
}

// The other way round: the date holds the transaction, and the void waits for it and
// then voids what has the date, which is the order the date first, the void second.
func TestVoidingWhileThePostingDateIsBeingSetWaitsForItAndVoidsTheTransactionWithTheDate(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	txn := a.cardDraft(t, a.card(t), "void-while-set-001")
	a.l.confirm(t, txn)

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			errs[i] = a.setPostingDate(ctx, txn.ID, postingDate)
			return
		}
		_, errs[i] = a.l.Void.VoidTransaction(ctx, txn.ID)
	})

	for i, err := range errs {
		if isDeadlock(err) || err != nil {
			t.Errorf("call %d = %v, want it to succeed: the void waits for the date and then voids the transaction", i, err)
		}
	}
	if got := a.stateOf(t, txn.ID); got != "void posted 2026-11-20" {
		t.Errorf("the transaction is %q, want it void, with the date that was set before", got)
	}
}

// Confirming and setting the date of a draft, in either order, leave a confirmed
// transaction with the date: the one that comes second waits for the first and works
// on what it left.
func TestConfirmingAndSettingThePostingDateOfADraftInEitherOrderLeavesItConfirmedWithTheDate(t *testing.T) {
	for _, tt := range []struct {
		name      string
		dateFirst bool
	}{{"the date is held first", true}, {"the confirmation is held first", false}} {
		t.Run(tt.name, func(t *testing.T) {
			a := newPlansApp(t, 4, lockStatement)
			txn := a.cardDraft(t, a.card(t), "confirm-and-set-0001")

			errs := make([]error, 2)
			a.heldTogether(t, 2, func(ctx context.Context, i int) {
				if (i == 0) == tt.dateFirst {
					errs[i] = a.setPostingDate(ctx, txn.ID, postingDate)
					return
				}
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{txn.ID}})
			})

			for i, err := range errs {
				if err != nil {
					t.Errorf("call %d = %v, want it to succeed", i, err)
				}
			}
			if got := a.stateOf(t, txn.ID); got != "confirmed posted 2026-11-20" {
				t.Errorf("the transaction is %q, want it confirmed, with the date", got)
			}
			a.wantConfirmedOnce(t)
		})
	}
}

// A draft that is discarded while the date is being set is gone when the date gets
// the lock: it is not found, and no date is written anywhere.
func TestSettingThePostingDateWhileTheDraftIsBeingDiscardedFindsItGone(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	txn := a.cardDraft(t, a.card(t), "set-while-discard-1")

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			errs[i] = a.l.Discard.DiscardDraft(ctx, txn.ID)
			return
		}
		errs[i] = a.setPostingDate(ctx, txn.ID, postingDate)
	})

	if errs[0] != nil {
		t.Errorf("the discard = %v, want it to succeed: it held the lock", errs[0])
	}
	if !errors.Is(errs[1], model.ErrNotFound) {
		t.Errorf("the date = %v, want ErrNotFound: the draft was discarded while it waited", errs[1])
	}
	if got := a.stateOf(t, txn.ID); got != "gone" {
		t.Errorf("the transaction is %q, want it gone", got)
	}
}

// What a call of the stress test did, as what no call can have done in some order
// of them: "ok", or the refusal it got. An error that is not a refusal of the status
// is none of that, and a deadlock above all.
func refusal(err error) (string, bool) {
	switch {
	case err == nil:
		return "ok", true
	case errors.Is(err, model.ErrNotDraft):
		return "not a draft", true
	case errors.Is(err, model.ErrNotConfirmed):
		return "not confirmed", true
	case errors.Is(err, model.ErrNotFound):
		return "not found", true
	case errors.Is(err, model.ErrTransactionVoid):
		return "void", true
	}
	return "", false
}

// sequential is a transaction as one call after another leaves it: the model that
// the stress test checks what really happened against.
type sequential struct {
	status string // "draft", "confirmed", "void" or "gone"
	posted string // the posting date, or ""
}

// call is one of the calls that run together, and what it does to a transaction when
// it runs alone.
type call struct {
	name string
	run  func(ctx context.Context, a *plansApp, id uuid.UUID) error
	// apply says what it returns when it runs on the state, and what it leaves.
	apply func(s sequential) (string, sequential)
}

func setCall(name string, day time.Time) call {
	return call{
		name: name,
		run:  func(ctx context.Context, a *plansApp, id uuid.UUID) error { return a.setPostingDate(ctx, id, day) },
		apply: func(s sequential) (string, sequential) {
			switch s.status {
			case "gone":
				return "not found", s
			case "void":
				return "void", s
			}
			s.posted = day.Format(time.DateOnly)
			return "ok", s
		},
	}
}

// permutations returns every order of the n numbers 0 to n-1.
func permutations(n int) [][]int {
	if n == 0 {
		return [][]int{{}}
	}
	var out [][]int
	for _, rest := range permutations(n - 1) {
		for at := 0; at <= len(rest); at++ {
			order := slices.Insert(slices.Clone(rest), at, n-1)
			out = append(out, order)
		}
	}
	return out
}

// Calls on one draft that start together, over and over: a confirmation, two dates, a
// void and a discard. Whichever way they overlap, no call may be ended for a deadlock
// or fail for any reason but the refusal of what it found, and what they did together
// has to be what some order of them would have done one after the other: each call
// returned what it returns in that order, and the transaction is what that order
// leaves.
func TestSettingPostingDatesWhileTheTransactionIsConfirmedVoidedAndDiscardedIsExplainedByOneOrder(t *testing.T) {
	a := newPlansApp(t, 8, "")
	card := a.card(t)
	calls := []call{
		{
			name: "confirm",
			run: func(ctx context.Context, a *plansApp, id uuid.UUID) error {
				_, err := a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{id}})
				return err
			},
			apply: func(s sequential) (string, sequential) {
				switch s.status {
				case "gone":
					return "not found", s
				case "draft":
					s.status = "confirmed"
					return "ok", s
				}
				return "not a draft", s
			},
		},
		setCall("date of the 20th", dateOn(2026, time.November, 20)),
		setCall("date of the 25th", dateOn(2026, time.November, 25)),
		{
			name: "void",
			run: func(ctx context.Context, a *plansApp, id uuid.UUID) error {
				_, err := a.l.Void.VoidTransaction(ctx, id)
				return err
			},
			apply: func(s sequential) (string, sequential) {
				switch s.status {
				case "gone":
					return "not found", s
				case "confirmed":
					s.status = "void"
					return "ok", s
				}
				return "not confirmed", s
			},
		},
		{
			name: "discard",
			run: func(ctx context.Context, a *plansApp, id uuid.UUID) error {
				return a.l.Discard.DiscardDraft(ctx, id)
			},
			apply: func(s sequential) (string, sequential) {
				switch s.status {
				case "gone":
					return "not found", s
				case "draft":
					s.status = "gone"
					s.posted = ""
					return "ok", s
				}
				return "not a draft", s
			},
		},
	}
	orders := permutations(len(calls))

	for round := range stressRounds {
		txn := a.cardDraft(t, card, fmt.Sprintf("explained-by-one-%03d", round))

		errs := make([]error, len(calls))
		together(len(calls), func(ctx context.Context, i int) {
			errs[i] = calls[i].run(ctx, a, txn.ID)
		})

		got := make([]string, len(calls))
		for i, err := range errs {
			outcome, ok := refusal(err)
			if !ok {
				t.Errorf("round %d, %s: %v: neither a success nor the refusal of what it found", round, calls[i].name, err)
				if isDeadlock(err) {
					t.Errorf("round %d, %s was ended by PostgreSQL to break a deadlock", round, calls[i].name)
				}
			}
			got[i] = outcome
		}
		final := a.stateOf(t, txn.ID)

		explained := false
		for _, order := range orders {
			state := sequential{status: "draft"}
			want := make([]string, len(calls))
			for _, i := range order {
				want[i], state = calls[i].apply(state)
			}
			posted := "no posting date"
			if state.posted != "" {
				posted = "posted " + state.posted
			}
			if state.status == "gone" {
				posted = ""
			}
			wantFinal := state.status + " " + posted
			if state.status == "gone" {
				wantFinal = "gone"
			}
			if slices.Equal(want, got) && wantFinal == final {
				explained = true
				break
			}
		}
		if !explained {
			var lines []string
			for i, c := range calls {
				lines = append(lines, fmt.Sprintf("%s: %s", c.name, got[i]))
			}
			t.Errorf("round %d: no order of the calls leaves what happened: %s; the transaction is %q", round, strings.Join(lines, "; "), final)
		}
	}
	a.wantConfirmedOnce(t)
}

// Dates set on the periods of a plan, and on its origin, while the plan is confirmed
// and rescheduled by calls that take the locks of the whole of it. A date is refused
// with no lock, so it does not wait for any of them, takes no part in any circle
// among them, and is never written; or the period it names was replaced by the
// reschedule, which is not found.
func TestSettingThePostingDateOfAPlanWhileItIsConfirmedAndRescheduledIsAlwaysRefusedAndNeverDeadlocks(t *testing.T) {
	a := newPlansApp(t, 10, "")
	card := a.card(t)
	for round := range stressRounds {
		plan := a.plan(t, card, fmt.Sprintf("plan-dates-%03d-xxxxx", round), 3)
		old := a.planOf(t, plan.ID).Periods
		targets := append([]uuid.UUID{plan.ID}, ids(old...)...)

		errs := make([]error, 2+len(targets))
		together(len(errs), func(ctx context.Context, i int) {
			switch i {
			case 0:
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.ID}})
			case 1:
				_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.two(plan.ID))
			default:
				errs[i] = a.setPostingDate(ctx, targets[i-2], postingDate)
			}
		})

		for i, err := range errs[:2] {
			if msg := unexpected(err); msg != "" {
				t.Errorf("round %d, call %d: %s", round, i, msg)
			}
		}
		for i, err := range errs[2:] {
			switch {
			case errors.Is(err, model.ErrPartOfPlan), errors.Is(err, model.ErrNotFound):
			case err == nil:
				t.Errorf("round %d: the date of %s was set, want a transaction of a plan refused", round, targets[i])
			default:
				t.Errorf("round %d: the date of %s = %v, want ErrPartOfPlan or, for a period that was replaced, ErrNotFound", round, targets[i], err)
			}
		}
		if got := a.statusOf(t, plan.ID); got != "confirmed" && got != "draft" {
			t.Errorf("round %d: the plan is %q, want it confirmed or, when the confirmation lost to nothing, a draft", round, got)
		}
		// No transaction of the plan has a date.
		listed, err := a.l.ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{PlanID: &plan.ID, Limit: 200})
		if err != nil {
			t.Fatalf("ListTransactions: %v", err)
		}
		for _, txn := range listed.Transactions {
			if txn.PostedOn != nil {
				t.Errorf("round %d: transaction %s of the plan has the posting date %s", round, txn.ID, txn.PostedOn.Format(time.DateOnly))
			}
		}
	}
	a.wantConfirmedOnce(t)
}

// The periods of a plan are locked by whoever holds its origin, and are not waited
// for: a date asked for a period is refused at once while every transaction of the
// plan is locked by another connection, as it is while a call holds the plan. Were
// the period locked first, the call would wait for the other connection, and end
// with the deadline it has.
func TestSettingThePostingDateOfAPlanPeriodDoesNotWaitForTheLocksOfThePlan(t *testing.T) {
	a := newPlansApp(t, 2, "")
	plan := a.plan(t, a.card(t), "plan-not-locked-001", 3)
	members := append([]uuid.UUID{plan.ID}, ids(a.planOf(t, plan.ID).Periods...)...)

	ctx := context.Background()
	holder, err := a.direct.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT id FROM fin_transaction WHERE id = ANY($1) FOR UPDATE`, members); err != nil {
		t.Fatalf("lock the plan on another connection: %v", err)
	}

	for i, id := range members {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		start := time.Now()
		err := a.setPostingDate(callCtx, id, postingDate)
		took := time.Since(start)
		cancel()

		if !errors.Is(err, model.ErrPartOfPlan) {
			t.Errorf("transaction %d of the plan: err = %v after %s, want ErrPartOfPlan at once", i, err, took)
		}
		if took > 5*time.Second {
			t.Errorf("transaction %d of the plan: refused after %s, want it not to wait for the locks of the plan", i, took)
		}
	}
}

// A call that finds a transaction held by another connection waits for it, and does
// as soon as it is let go: the lock is the same row lock the other calls take. The
// date is not set before the transaction that holds it ends.
func TestSettingThePostingDateWaitsForAnotherConnectionThatHoldsTheTransaction(t *testing.T) {
	a := newPlansApp(t, 2, "")
	txn := a.cardDraft(t, a.card(t), "set-waits-for-row-1")
	a.l.confirm(t, txn)

	ctx := context.Background()
	holder, err := a.direct.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = holder.Rollback(ctx) }()
	if _, err := holder.Exec(ctx, `SELECT id FROM fin_transaction WHERE id = $1 FOR UPDATE`, txn.ID); err != nil {
		t.Fatalf("lock the transaction on another connection: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		callCtx, cancel := context.WithTimeout(ctx, pollLimit)
		defer cancel()
		done <- a.setPostingDate(callCtx, txn.ID, postingDate)
	}()

	// It is waiting for the lock when PostgreSQL says some backend is.
	deadline := time.Now().Add(pollLimit)
	for {
		var waiting int
		if err := a.direct.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting); err != nil {
			t.Fatalf("ask PostgreSQL who is waiting: %v", err)
		}
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the call returned %v while another connection held the transaction, want it to wait", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("no call is waiting for the lock")
		}
		time.Sleep(pollEvery)
	}
	if got := a.stateOf(t, txn.ID); got != "confirmed no posting date" {
		t.Errorf("the transaction is %q while it is held, want it as it was", got)
	}

	if err := holder.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("the call = %v, want it to succeed once the transaction is let go", err)
	}
	if got := a.stateOf(t, txn.ID); got != "confirmed posted 2026-11-20" {
		t.Errorf("the transaction is %q, want it confirmed, with the date", got)
	}
}
