package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// How the test waits for the calls it makes overlap: it asks PostgreSQL every
// pollEvery who is waiting, and gives up after pollLimit, saying which calls are
// neither finished nor waiting.
const (
	pollEvery = 5 * time.Millisecond
	pollLimit = 20 * time.Second
)

// callKey marks the context of a call with its number, so that the tracer can tell
// which backend of PostgreSQL serves which call.
type callKey struct{}

type statementKey struct{}

// callTracer is a pgx tracer. It notes which backend serves each call, and runs
// hook, in the call that gets there first, as a statement that begins with prefix
// has been read to its end and before the next statement of that call is sent. It
// puts what the test does exactly between two statements of one call, with no
// sleep to hope that it lands there. The calls that get there later go on without
// waiting for the hook, which a sync.Once would make them do.
type callTracer struct {
	prefix string
	hook   func()
	fired  atomic.Bool
	// callOf maps the process id of a backend to the number of the call that last
	// used it.
	callOf sync.Map
}

func (c *callTracer) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if call, ok := ctx.Value(callKey{}).(int); ok {
		c.callOf.Store(conn.PgConn().PID(), call)
	}
	return context.WithValue(ctx, statementKey{}, data.SQL)
}

func (c *callTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryEndData) {
	if sql, _ := ctx.Value(statementKey{}).(string); strings.HasPrefix(sql, c.prefix) && c.fired.CompareAndSwap(false, true) {
		c.hook()
	}
}

// waiting returns the numbers of the calls whose backend is waiting for a lock,
// as db, a pool that the tracer is not on, finds out from PostgreSQL.
func (c *callTracer) waiting(ctx context.Context, db *pgxpool.Pool) (map[int]bool, error) {
	rows, err := db.Query(ctx, `SELECT pid FROM pg_stat_activity
		WHERE datname = current_database() AND wait_event_type = 'Lock'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	calls := map[int]bool{}
	for rows.Next() {
		var pid int32
		if err := rows.Scan(&pid); err != nil {
			return nil, err
		}
		if call, ok := c.callOf.Load(uint32(pid)); ok {
			calls[call.(int)] = true
		}
	}
	return calls, rows.Err()
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
//
// It does not wait for ever either. If a call is neither finished nor waiting
// after pollLimit, the test fails and names the calls, and lets the first one go
// on. Every call has a deadline of its own, so that a call that is stuck for
// another reason ends with an error and does not hang the test.
func reconcileTogether(t *testing.T, keys ...string) overlapped {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*pollLimit)
	t.Cleanup(cancel)
	calls := len(keys)
	twd := model.BaseCurrencyID

	direct := testpg.New(t)
	tracer := &callTracer{prefix: "-- name: CountPendingDrafts"}
	cfg := direct.Config()
	cfg.ConnConfig.Tracer = tracer
	cfg.MaxConns = int32(calls) + 1 // one for each call to hold, and one over
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open a pool with a tracer: %v", err)
	}
	t.Cleanup(traced.Close)
	l := newLedgerAppOn(t, traced, newTestClock(day))

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
		got.outs[i], got.errs[i] = l.Reconcile.Reconcile(context.WithValue(ctx, callKey{}, i), usecase.ReconcileInput{
			RecordDetails: l.details(keys[i]), AccountID: account.ID, InstrumentID: twd, ActualBalance: dec("1980"),
		})
	}
	var (
		others   sync.WaitGroup
		finished = make([]atomic.Bool, calls)
	)
	tracer.hook = func() {
		for i := 1; i < calls; i++ {
			others.Add(1)
			go func() {
				defer others.Done()
				reconcile(i)
				finished[i].Store(true)
			}()
		}
		deadline := time.Now().Add(pollLimit)
		for {
			waiting, err := tracer.waiting(ctx, direct)
			if err != nil {
				t.Errorf("ask PostgreSQL which calls are waiting for a lock: %v", err)
				return
			}
			var neither []string
			for i := 1; i < calls; i++ {
				if !finished[i].Load() && !waiting[i] {
					neither = append(neither, fmt.Sprintf("%d (%s)", i, keys[i]))
				}
			}
			if len(neither) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("%s after the first call was held, calls %s are neither finished nor waiting for a lock",
					pollLimit, strings.Join(neither, ", "))
				return
			}
			time.Sleep(pollEvery)
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
