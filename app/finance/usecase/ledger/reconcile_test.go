package ledger_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// held is what the ledger says the account holds of the instrument on the day: the
// quantity of the one balance the repo has for them, zero when it has none.
func (e *env) held(account model.Account, instrument model.Instrument, on time.Time) decimal.Decimal {
	e.t.Helper()
	balances, err := e.txns.Balances(context.Background(), repo.BalanceFilter{AccountID: &account.ID, InstrumentID: &instrument.ID, Day: on})
	if err != nil {
		e.t.Fatalf("Balances: %v", err)
	}
	if len(balances) == 0 {
		return decimal.Zero
	}
	return balances[0].Quantity
}

// confirmedOpening records what the account holds of the instrument on the day
// of the tests, and confirms it, which is how a balance gets there.
func (e *env) confirmedOpening(key string, account model.Account, instrument model.Instrument, amount string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.SetOpeningBalance(context.Background(), usecase.SetOpeningBalanceInput{
		RecordDetails: e.details(key), AccountID: account.ID, InstrumentID: instrument.ID, Amount: dec(amount),
	})
	if err != nil {
		e.t.Fatalf("SetOpeningBalance(%q): %v", key, err)
	}
	return e.confirm(out.Transaction)
}

// confirm confirms the draft, and fails the test when that is refused.
func (e *env) confirm(draft usecase.Transaction) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{draft.ID}})
	if err != nil {
		e.t.Fatalf("ConfirmTransactions: %v", err)
	}
	return out.Transactions[0]
}

// reconcileOf is a reconciliation of the account on the day of the tests.
func (e *env) reconcileOf(key string, account model.Account, instrument model.Instrument, actual string) usecase.ReconcileInput {
	return usecase.ReconcileInput{
		RecordDetails: e.details(key), AccountID: account.ID, InstrumentID: instrument.ID, ActualBalance: dec(actual),
	}
}

func TestReconcileBelowTheRecordedBalanceDraftsAnUnrecordedExpenseThatSettlesTheBalanceOnceConfirmed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")

	out, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.RecordedBalance.Equal(dec("1000")) || !out.Difference.Equal(dec("-120")) || out.Duplicate {
		t.Fatalf("output = %+v, want recorded 1000 and difference -120", out)
	}
	if out.Transaction == nil || out.Transaction.Kind != "reconciliation" || out.Transaction.Status != "draft" {
		t.Fatalf("transaction = %+v, want a draft reconciliation", out.Transaction)
	}
	wantLines(t, e.lines(*out.Transaction), "account(cash) -120 TWD", "expense(未記錄) +120 TWD, base +120")
	// A draft changes no balance, so it is still 1000 until it is confirmed.
	if got := e.held(e.cash, e.twd, day); !got.Equal(dec("1000")) {
		t.Errorf("balance before the draft is confirmed = %s, want 1000", got)
	}

	e.confirm(*out.Transaction)
	if got := e.held(e.cash, e.twd, day); !got.Equal(dec("880")) {
		t.Errorf("balance once confirmed = %s, want the actual balance, 880", got)
	}
	again, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00002", e.cash, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile again: %v", err)
	}
	if !again.RecordedBalance.Equal(dec("880")) || !again.Difference.IsZero() || again.Transaction != nil {
		t.Errorf("output = %+v, want nothing left to reconcile", again)
	}
}

func TestReconcileAboveTheRecordedBalanceDraftsAnUnrecordedIncomeThatSettlesTheBalanceOnceConfirmed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")

	out, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "1080"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.RecordedBalance.Equal(dec("1000")) || !out.Difference.Equal(dec("80")) || out.Transaction == nil {
		t.Fatalf("output = %+v, want recorded 1000, difference 80 and a draft", out)
	}
	wantLines(t, e.lines(*out.Transaction), "account(cash) +80 TWD", "income(未記錄) -80 TWD, base -80")

	e.confirm(*out.Transaction)
	if got := e.held(e.cash, e.twd, day); !got.Equal(dec("1080")) {
		t.Errorf("balance once confirmed = %s, want the actual balance, 1080", got)
	}
}

func TestReconcileWithNoDifferenceRecordsNothing(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	inserts := e.txns.inserts

	out, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "1000.00"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if out.Transaction != nil || out.Duplicate || !out.Difference.IsZero() || !out.RecordedBalance.Equal(dec("1000")) {
		t.Errorf("output = %+v, want recorded 1000, no difference and no transaction", out)
	}
	if e.txns.inserts != inserts {
		t.Errorf("%d transactions stored, want none: a reconciliation without a difference is not kept", e.txns.inserts-inserts)
	}
}

func TestReconcileOfAnInstrumentThatIsNotACurrencyAdjustsTheQuantity(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.btc, "1")

	out, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.btc, "2.5"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.Difference.Equal(dec("1.5")) || out.Transaction == nil {
		t.Fatalf("output = %+v, want a difference of 1.5 and a draft", out)
	}
	wantLines(t, e.lines(*out.Transaction), "account(cash) +1.5 BTC", "adjustment -1.5 BTC")

	// It has no amount in the base currency, and is refused one.
	in := e.reconcileOf("reconcile-00002", e.cash, e.btc, "3")
	in.DifferenceBaseAmount = decPtr("100")
	e.confirm(*out.Transaction)
	inserts := e.txns.inserts
	if _, err := e.svc.Reconcile(context.Background(), in); !errors.Is(err, model.ErrBaseAmountNotAllowed) {
		t.Fatalf("err = %v, want ErrBaseAmountNotAllowed", err)
	}
	if e.txns.inserts != inserts {
		t.Errorf("%d transactions stored by the refused call, want none", e.txns.inserts-inserts)
	}
}

func TestReconcileOfAForeignCurrencyAsksForTheBaseAmountOfTheDifferenceAndTakesItOnAnotherCall(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.usd, "100")
	inserts := e.txns.inserts

	_, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.usd, "95"))

	var need *model.DifferenceBaseAmountRequiredError
	if !errors.As(err, &need) || !need.Difference.Equal(dec("-5")) || need.InstrumentCode != "USD" {
		t.Fatalf("err = %v, want a *DifferenceBaseAmountRequiredError that carries the difference, -5 USD", err)
	}
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Errorf("err = %v, want it to be ErrBaseAmountRequired too", err)
	}
	if e.txns.inserts != inserts {
		t.Fatalf("%d transactions stored by the refused call, want none", e.txns.inserts-inserts)
	}

	// The same key may be used again: nothing was recorded under it.
	in := e.reconcileOf("reconcile-00001", e.cash, e.usd, "95")
	in.DifferenceBaseAmount = decPtr("160")
	out, err := e.svc.Reconcile(context.Background(), in)
	if err != nil {
		t.Fatalf("Reconcile with the base amount: %v", err)
	}
	if out.Duplicate || out.Transaction == nil {
		t.Fatalf("output = %+v, want a new draft", out)
	}
	wantLines(t, e.lines(*out.Transaction), "account(cash) -5 USD", "expense(未記錄) +5 USD, base +160")
}

func TestReconcileOfAnAccountThatOwesGoesPastZeroAndBelow(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.bank, e.twd, "-5000")

	out, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.bank, e.twd, "-5200"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.RecordedBalance.Equal(dec("-5000")) || !out.Difference.Equal(dec("-200")) || out.Transaction == nil {
		t.Fatalf("output = %+v, want recorded -5000 and a difference of -200", out)
	}
	wantLines(t, e.lines(*out.Transaction), "account(bank) -200 TWD", "expense(未記錄) +200 TWD, base +200")
}

func TestReconcileRefusesWhileDraftsAreWaitingAndCountsThem(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	e.recordLunch("lunch-0000000a")
	e.recordLunch("lunch-0000000b")
	inserts := e.txns.inserts

	_, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "880"))

	var pending *model.PendingDraftsError
	if !errors.As(err, &pending) || pending.Count != 2 {
		t.Fatalf("err = %v, want a *PendingDraftsError that counts the 2 drafts", err)
	}
	if !errors.Is(err, model.ErrPendingDrafts) {
		t.Errorf("err = %v, want it to be ErrPendingDrafts too", err)
	}
	if e.txns.inserts != inserts {
		t.Errorf("%d transactions stored by the refused call, want none", e.txns.inserts-inserts)
	}
}

func TestReconcileIsNotHeldUpByDraftsThatCannotChangeWhatItCounts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")

	// A draft of the next day, one of another account, and one of another
	// instrument of the same account: none is in the balance of cash in TWD up to
	// the day.
	nextDay := e.lunch("lunch-nextday-01")
	nextDay.OccurredOn = day.AddDate(0, 0, 1)
	otherAccount := e.lunch("lunch-bank-00001")
	otherAccount.AccountID = &e.bank.ID
	otherInstrument := e.lunch("lunch-usd-000001")
	otherInstrument.InstrumentID = e.usd.ID
	otherInstrument.Lines[0].BaseAmount = decPtr("3800")
	for _, in := range []usecase.RecordExpenseInput{nextDay, otherAccount, otherInstrument} {
		if _, err := e.svc.RecordExpense(ctx, in); err != nil {
			t.Fatalf("RecordExpense(%q): %v", in.IdempotencyKey, err)
		}
	}

	out, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "900"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.Difference.Equal(dec("-100")) || out.Transaction == nil {
		t.Errorf("output = %+v, want the reconciliation to go ahead with a difference of -100", out)
	}
}

func TestReconcileCountsTheBalanceOfTheDayAndNotTheDaysAfterIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	// 120 spent on the 15th, confirmed: it is after the 14th.
	after := e.lunch("lunch-nextday-01")
	after.OccurredOn = day.AddDate(0, 0, 1)
	out, err := e.svc.RecordExpense(ctx, after)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	e.confirm(out.Transaction)

	onTheDay, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "1000"))
	if err != nil {
		t.Fatalf("Reconcile on the day: %v", err)
	}
	nextDay := e.reconcileOf("reconcile-00002", e.cash, e.twd, "880")
	nextDay.OccurredOn = day.AddDate(0, 0, 1)
	theNext, err := e.svc.Reconcile(ctx, nextDay)
	if err != nil {
		t.Fatalf("Reconcile on the next day: %v", err)
	}

	if !onTheDay.RecordedBalance.Equal(dec("1000")) || onTheDay.Transaction != nil {
		t.Errorf("on the 14th: %+v, want the balance of that day, 1000, which the actual balance matches", onTheDay)
	}
	if !theNext.RecordedBalance.Equal(dec("880")) || theNext.Transaction != nil {
		t.Errorf("on the 15th: %+v, want the balance of that day, 880, which the actual balance matches", theNext)
	}
}

// The day is the one a time falls on as its own location reads it, as everywhere
// in the ledger, so a balance observed just after midnight in Taipei is the
// balance of that day, though it is still the day before in UTC.
func TestReconcileTakesTheDayAsTheLocationOfTheTimeReadsIt(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	spent := e.lunch("lunch-nextday-01")
	spent.OccurredOn = day.AddDate(0, 0, 1)
	out, err := e.svc.RecordExpense(context.Background(), spent)
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	e.confirm(out.Transaction)
	taipei := time.FixedZone("UTC+8", 8*60*60)
	in := e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")
	in.OccurredOn = time.Date(2026, 11, 15, 0, 30, 0, 0, taipei) // 16:30 on the 14th in UTC

	got, err := e.svc.Reconcile(context.Background(), in)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !got.RecordedBalance.Equal(dec("880")) || got.Transaction != nil {
		t.Errorf("output = %+v, want the balance of the 15th, 880, which the actual balance matches", got)
	}
}

func TestReconcileWithAKeyUsedBeforeReturnsTheDraftRecordedThen(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	first, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The draft of the first call is itself a draft waiting, and a call that asks
	// for nothing new does not stumble on it: it is answered first. Whatever it
	// says is not looked at either.
	e.accounts.archive(e.cash.ID)
	again, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "5"))
	if err != nil {
		t.Fatalf("Reconcile with the key again: %v", err)
	}

	if !again.Duplicate || again.Transaction == nil || again.Transaction.ID != first.Transaction.ID {
		t.Fatalf("output = %+v, want the first draft, marked as a duplicate", again)
	}
	if !again.RecordedBalance.IsZero() || !again.Difference.IsZero() {
		t.Errorf("recorded %s, difference %s, want neither worked out for a duplicate", again.RecordedBalance, again.Difference)
	}
	if e.txns.inserts != 2 {
		t.Errorf("%d transactions stored, want the opening balance and the first draft only", e.txns.inserts)
	}
}

func TestReconcileRefusesASecondReconciliationWhileTheFirstDraftWaits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	if _, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	_, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00002", e.cash, e.twd, "880"))

	var pending *model.PendingDraftsError
	if !errors.As(err, &pending) || pending.Count != 1 {
		t.Fatalf("err = %v, want the draft of the first call counted: adjusting twice would book the difference twice", err)
	}
}

func TestReconcileLocksTheAccountBeforeItLooksAtAnythingElse(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	log := &callLog{}
	e.accounts.log, e.txns.log = log, log

	if _, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The lock comes first, so that a reconciliation of the account that starts
	// meanwhile waits before it reads anything, and the key comes before the drafts
	// and the balance, so that a retry that waited is answered with the draft of
	// the call it repeats and not refused for it.
	want := []string{"lock the account", "look up the key", "count the drafts", "read the balance"}
	if !slices.Equal(log.calls, want) {
		t.Errorf("calls = %q, want %q", log.calls, want)
	}
	if !slices.Equal(e.accounts.locked, []uuid.UUID{e.cash.ID}) {
		t.Errorf("locked %v, want the account that is reconciled, once", e.accounts.locked)
	}
}

func TestReconcileStopsWhereTheAccountCannotBeLocked(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	inserts := e.txns.inserts
	boom := errors.New("lock timeout")
	e.accounts.lockErr = boom

	_, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "880"))

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure of the lock", err)
	}
	if e.txns.inserts != inserts {
		t.Errorf("%d transactions stored, want the opening balance only", e.txns.inserts)
	}
}

// An account the lock does not find is no reason to refuse a key that was used
// before: it is answered before the request is validated, whatever it names.
func TestReconcileAnswersAKeyUsedBeforeWhateverAccountTheCallNames(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	first, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", e.cash, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	nowhere := model.Account{ID: uuid.Must(uuid.NewV7())}
	again, err := e.svc.Reconcile(ctx, e.reconcileOf("reconcile-00001", nowhere, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile with the key again: %v", err)
	}

	if !again.Duplicate || again.Transaction == nil || again.Transaction.ID != first.Transaction.ID {
		t.Errorf("output = %+v, want the first draft, marked as a duplicate", again)
	}
}

func TestReconcileReturnsWhatAConcurrentCallRecordedUnderTheKey(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	winner := model.Transaction{
		ID: uuid.Must(uuid.NewV7()), Kind: model.KindReconciliation, Status: model.StatusDraft, OccurredOn: day,
		IdempotencyKey: "reconcile-raced1", CreatedAt: epoch,
	}
	// The other call commits between the look-up of the key and the insert.
	e.txns.concurrent = &winner

	out, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-raced1", e.cash, e.twd, "880"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	if !out.Duplicate || out.Transaction == nil || out.Transaction.ID != winner.ID {
		t.Fatalf("output = %+v, want the draft of the call that got there first", out)
	}
	if !out.Difference.IsZero() || !out.RecordedBalance.IsZero() {
		t.Errorf("recorded %s, difference %s, want what this call worked out left out: it is not the answer", out.RecordedBalance, out.Difference)
	}
}

func TestReconcileRefuses(t *testing.T) {
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.ReconcileInput)
		want   error
	}{
		{"an account that does not exist", func(e *env, in *usecase.ReconcileInput) { in.AccountID = unknown }, model.ErrNotFound},
		{"an instrument that does not exist", func(e *env, in *usecase.ReconcileInput) { in.InstrumentID = unknown }, model.ErrNotFound},
		{"an archived account", func(e *env, in *usecase.ReconcileInput) { e.accounts.archive(e.cash.ID) }, model.ErrArchived},
		{"an archived instrument", func(e *env, in *usecase.ReconcileInput) { e.instruments.archive(e.twd.ID) }, model.ErrArchived},
		{"an actual balance finer than the instrument", func(e *env, in *usecase.ReconcileInput) { in.ActualBalance = dec("880.123") }, model.ErrTooManyDecimals},
		{"no date", func(e *env, in *usecase.ReconcileInput) { in.OccurredOn = time.Time{} }, model.ErrDateRequired},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")
			tt.mutate(e, &in)

			out, err := e.svc.Reconcile(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if out.Transaction != nil || e.txns.inserts != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestReconcileRefusesABadKeyBeforeItTouchesAnything(t *testing.T) {
	e := newEnv(t)

	for _, key := range []string{"", "short", "   1234567   "} {
		var keyErr *model.IdempotencyKeyLengthError
		_, err := e.svc.Reconcile(context.Background(), e.reconcileOf(key, e.cash, e.twd, "880"))
		if !errors.As(err, &keyErr) {
			t.Errorf("key %q: err = %v, want an *IdempotencyKeyLengthError", key, err)
		}
	}
	if e.tx.calls != 0 {
		t.Errorf("%d database transactions opened, want none", e.tx.calls)
	}
}

func TestReconcileReportsWhatItCannotRead(t *testing.T) {
	boom := errors.New("connection reset")

	t.Run("the balance", func(t *testing.T) {
		e := newEnv(t)
		e.txns.readErr = boom

		if _, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the failure of the store", err)
		}
	})
	t.Run("the insert", func(t *testing.T) {
		e := newEnv(t)
		e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
		e.txns.insertErr = boom

		if _, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.cash, e.twd, "880")); !errors.Is(err, boom) {
			t.Fatalf("err = %v, want the failure of the store", err)
		}
	})
}
