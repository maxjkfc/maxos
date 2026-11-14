package cmd_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// afterStatement is a pgx tracer that runs hook, in the call that gets there
// first, as a statement that begins with prefix has been read to its end and
// before the next statement of that call is sent. It puts what the test does
// exactly between two statements of one call, with no sleep to hope that it lands
// there. The calls that get there later go on without waiting for the hook, which
// a sync.Once would make them do.
type afterStatement struct {
	prefix string
	hook   func()
	fired  atomic.Bool
}

type statementKey struct{}

func (a *afterStatement) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, statementKey{}, data.SQL)
}

func (a *afterStatement) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if sql, _ := ctx.Value(statementKey{}).(string); strings.HasPrefix(sql, a.prefix) && a.fired.CompareAndSwap(false, true) {
		a.hook()
	}
}

// overlapped is what calls of Reconcile on one account that overlap in time
// returned: the output and the error of each, in the order of their keys.
type overlapped struct {
	l         ledgerApp
	accountID uuid.UUID
	outs      []usecase.ReconcileOutput
	errs      []error
}

// reconcileTogether makes a call of Reconcile for each key, all of them for an
// account that holds 20 less than the ledger says, and makes them overlap exactly,
// not by luck. The first call is held right after it has counted the drafts of
// the account, which is the point where a reconciliation without a lock has
// learnt what the others must not be allowed to. The others are started then, and
// the first call is let go only when each of them has either finished (nothing
// kept it from going all the way) or is waiting for a lock (the first call keeps
// it from going on). The test finds out which by asking PostgreSQL who is waiting
// for a lock, so it does not matter how long anything takes.
func reconcileTogether(t *testing.T, keys ...string) overlapped {
	t.Helper()
	ctx := context.Background()
	calls := len(keys)
	twd := model.BaseCurrencyID

	direct := testpg.New(t)
	tracer := &afterStatement{prefix: "-- name: CountPendingDrafts"}
	cfg := direct.Config()
	cfg.ConnConfig.Tracer = tracer
	cfg.MaxConns = int32(calls) + 1 // one for each call to hold, and one over
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open a pool with a tracer: %v", err)
	}
	t.Cleanup(traced.Close)
	l := newLedgerAppOn(t, traced)

	account, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: "cash", Kind: "cash"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	opening, err := l.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
		RecordDetails: l.details("opening-race-001"), AccountID: account.ID, InstrumentID: twd, Amount: dec("2000"),
	})
	if err != nil {
		t.Fatalf("SetOpeningBalance: %v", err)
	}
	l.confirm(t, opening.Transaction)

	got := overlapped{l: l, accountID: account.ID, outs: make([]usecase.ReconcileOutput, calls), errs: make([]error, calls)}
	reconcile := func(i int) {
		got.outs[i], got.errs[i] = l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
			RecordDetails: l.details(keys[i]), AccountID: account.ID, InstrumentID: twd, ActualBalance: dec("1980"),
		})
	}
	var (
		others   sync.WaitGroup
		finished atomic.Int32
	)
	tracer.hook = func() {
		for i := 1; i < calls; i++ {
			others.Add(1)
			go func() {
				defer others.Done()
				reconcile(i)
				finished.Add(1)
			}()
		}
		for {
			var waiting int
			err := direct.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity
				WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&waiting)
			if err != nil {
				t.Errorf("count the calls that wait for a lock: %v", err)
				return
			}
			if waiting+int(finished.Load()) >= calls-1 {
				return
			}
		}
	}

	reconcile(0)
	others.Wait()
	return got
}

// drafts returns the drafts of the account that the calls left.
func (o overlapped) drafts(t *testing.T) []usecase.Transaction {
	t.Helper()
	out, err := o.l.ListTransactions.ListTransactions(context.Background(),
		usecase.ListTransactionsInput{Status: "draft", AccountID: &o.accountID})
	if err != nil {
		t.Fatalf("ListTransactions: %v", err)
	}
	return out.Transactions
}

// Reconciliations of one account that run at the same time, each with a key of
// its own, must book the difference once: the first one drafts it, and the others
// are refused because that draft is waiting. Without a lock on the account they
// all read the same balance and the same empty list of drafts, and each books the
// difference again.
func TestReconciliationsOfOneAccountAtTheSameTimeBookTheDifferenceOnce(t *testing.T) {
	got := reconcileTogether(t, "reconcile-race-00", "reconcile-race-01", "reconcile-race-02", "reconcile-race-03")

	if got.errs[0] != nil {
		t.Errorf("the first call: %v, want it to draft the difference", got.errs[0])
	}
	for i := 1; i < len(got.errs); i++ {
		if !errors.Is(got.errs[i], model.ErrPendingDrafts) {
			t.Errorf("call %d: err = %v, want ErrPendingDrafts: the draft of the first call is waiting", i, got.errs[i])
		}
	}
	drafts := got.drafts(t)
	if len(drafts) != 1 {
		t.Errorf("%d drafts of the account, want exactly 1", len(drafts))
	}
	if len(drafts) > 0 {
		got.l.confirm(t, drafts...)
	}
	if balance := got.l.balance(t, got.accountID, model.BaseCurrencyID); !balance.Equal(dec("1980")) {
		t.Errorf("cash once the drafts are confirmed = %s, want the actual balance, 1980: the difference was booked more than once", balance)
	}
}

// The retry of a call that is still running has the key of that call. It has to
// be answered with the draft of the call, as a retry that comes later is, and not
// be refused because that draft is waiting: so the key is looked up after the
// account is locked, and not before, when it is not there yet.
func TestARetryOfAReconciliationThatIsStillRunningIsAnsweredWithItsDraft(t *testing.T) {
	got := reconcileTogether(t, "reconcile-retry-0", "reconcile-retry-0")

	for i, err := range got.errs {
		if err != nil {
			t.Fatalf("call %d: %v, want both to be answered", i, err)
		}
	}
	first, retry := got.outs[0], got.outs[1]
	if first.Transaction == nil || first.Duplicate {
		t.Errorf("the first call: %+v, want the draft it recorded, not marked as a duplicate", first)
	}
	if !retry.Duplicate || retry.Transaction == nil || first.Transaction == nil || retry.Transaction.ID != first.Transaction.ID {
		t.Errorf("the retry: %+v, want the draft of the first call, marked as a duplicate", retry)
	}
	if drafts := got.drafts(t); len(drafts) != 1 {
		t.Errorf("%d drafts of the account, want exactly 1", len(drafts))
	}
}
