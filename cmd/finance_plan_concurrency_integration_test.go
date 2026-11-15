package cmd_test

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// What these tests are about: calls on instalment plans that overlap in time. A plan
// is many transactions, so a call that acts on one takes many locks, and the calls
// must take them so that two of them cannot each wait for what the other holds, and
// so that what a call does to a plan is all of it or none of it.
//
// Two kinds of test are here. Where one particular overlap matters, the test makes
// it happen exactly, as finance_concurrency_integration_test.go does: the tracer
// holds the first call right after it has taken its first lock, the others start
// then, and the first goes on only when each of them has finished or is waiting for
// a lock, which PostgreSQL says. Where the property is that every overlap is right,
// the test runs a fixed number of rounds of calls that start together, with no sleep
// to wait for anything, and checks what no order of the calls could have left.

// lockStatement is what the statement that locks a transaction begins with: the
// first call is held after the first of them.
const lockStatement = "-- name: GetTransactionForUpdate"

// stressRounds is how many rounds a test that runs calls together makes. It is
// fixed, so that a run is as long every time, and so that a failure comes back.
const stressRounds = 20

// tracers is several tracers as one, which is what a pool takes.
type tracers []pgx.QueryTracer

func (ts tracers) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	for _, t := range ts {
		ctx = t.TraceQueryStart(ctx, conn, data)
	}
	return ctx
}

func (ts tracers) TraceQueryEnd(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryEndData) {
	for _, t := range ts {
		t.TraceQueryEnd(ctx, conn, data)
	}
}

// statusTracer counts how many times each transaction was set to confirmed. A
// transaction is confirmed once; twice is two calls that each found it a draft.
type statusTracer struct {
	mu        sync.Mutex
	confirmed map[uuid.UUID]int
}

func (s *statusTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if !strings.HasPrefix(data.SQL, "-- name: SetTransactionStatus") {
		return ctx
	}
	var (
		id     uuid.UUID
		status string
	)
	for _, arg := range data.Args {
		switch v := arg.(type) {
		case uuid.UUID:
			id = v
		case string:
			status = v
		}
	}
	if status == "confirmed" {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.confirmed == nil {
			s.confirmed = map[uuid.UUID]int{}
		}
		s.confirmed[id]++
	}
	return ctx
}

func (s *statusTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// twice returns the transactions that were set to confirmed more than once.
func (s *statusTracer) twice() []uuid.UUID {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []uuid.UUID
	for id, n := range s.confirmed {
		if n > 1 {
			out = append(out, id)
		}
	}
	return out
}

// plansApp is the ledger as these tests need it: on a pool that has the tracers
// and a connection for each call, and with another pool, which the tracers are
// not on, to ask PostgreSQL with.
type plansApp struct {
	l              ledgerApp
	direct         *pgxpool.Pool
	hold           *callTracer
	holdPrefix     string
	statuses       *statusTracer
	food, interest uuid.UUID
	accounts       int
}

// newPlansApp puts the app together. holdPrefix, when it is not empty, is what
// the statement begins with that the first call is held after (see heldTogether),
// which the tracer waits for only once heldTogether runs: what the test does before
// is not held.
func newPlansApp(t *testing.T, conns int, holdPrefix string) *plansApp {
	t.Helper()
	ctx := context.Background()
	a := &plansApp{direct: testpg.New(t), statuses: &statusTracer{}}
	ts := tracers{a.statuses}
	if holdPrefix != "" {
		a.holdPrefix = holdPrefix
		a.hold = &callTracer{prefix: "-- the tracer is not armed"}
		ts = append(ts, a.hold)
	}
	cfg := a.direct.Config()
	cfg.ConnConfig.Tracer = ts
	cfg.MaxConns = int32(conns)
	traced, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open a pool with the tracers: %v", err)
	}
	t.Cleanup(traced.Close)
	a.l = newLedgerAppOn(t, traced, newTestClock(day))

	category := func(name string) uuid.UUID {
		c, err := a.l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: name})
		if err != nil {
			t.Fatalf("CreateCategory(%q): %v", name, err)
		}
		return c.ID
	}
	a.food, a.interest = category("Food"), category("Interest")
	return a
}

// card makes a card that nothing else is charged to, so that what it holds is
// what one round did.
func (a *plansApp) card(t *testing.T) uuid.UUID {
	t.Helper()
	a.accounts++
	card, err := a.l.CreateAccount.CreateAccount(context.Background(), usecase.CreateAccountInput{Name: fmt.Sprintf("card-%03d", a.accounts), Kind: "card"})
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	return card.ID
}

// scheduleOf is count periods of 1,000 that fall due on the 5th of each month from
// December 2026, with 30 of interest on the first.
func scheduleOf(interest uuid.UUID, count int) *usecase.InstalmentSchedule {
	periods := make([]usecase.InstalmentPeriod, count)
	for i := range periods {
		periods[i] = usecase.InstalmentPeriod{DueOn: dateOn(2026, time.Month(12+i), 5), Principal: dec("1000")}
	}
	periods[0].Interest = new(dec("30"))
	return &usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &interest}
}

// plan records a plan of count periods of 1,000 on the card, a draft, and returns
// its origin.
func (a *plansApp) plan(t *testing.T, card uuid.UUID, key string, count int) usecase.Transaction {
	t.Helper()
	out, err := a.l.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: a.l.details(key), AccountID: &card, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: a.food, Amount: decimal.NewFromInt(int64(1000 * count))}},
		Instalments: scheduleOf(a.interest, count),
	})
	if err != nil {
		t.Fatalf("RecordExpense(%q) in instalments: %v", key, err)
	}
	return out.Transaction
}

// lunch records an expense of 120 on the card that is in no plan, a draft.
func (a *plansApp) lunch(t *testing.T, card uuid.UUID, key string) usecase.Transaction {
	t.Helper()
	out, err := a.l.RecordExpense.RecordExpense(context.Background(), usecase.RecordExpenseInput{
		RecordDetails: a.l.details(key), AccountID: &card, InstrumentID: model.BaseCurrencyID,
		Lines: []usecase.CategoryLine{{CategoryID: a.food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return out.Transaction
}

// planOf reads the plan.
func (a *plansApp) planOf(t *testing.T, planID uuid.UUID) usecase.InstalmentPlan {
	t.Helper()
	plan, err := a.l.GetInstalmentPlan.GetInstalmentPlan(context.Background(), planID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan(%s): %v", planID, err)
	}
	return plan
}

// statusOf is the status that every transaction of the plan has, and "" when the
// plan is not there. A plan that is of one status in part and of another in the
// rest is half applied, which no call may leave, and fails the test.
func (a *plansApp) statusOf(t *testing.T, planID uuid.UUID) string {
	t.Helper()
	out, err := a.l.ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{PlanID: &planID, Limit: 200})
	if err != nil {
		t.Fatalf("ListTransactions of plan %s: %v", planID, err)
	}
	if len(out.Transactions) == 0 {
		return ""
	}
	status := out.Transactions[0].Status
	for _, txn := range out.Transactions {
		if txn.Status != status {
			t.Errorf("plan %s is half %s and half %s: %s is %s", planID, status, txn.Status, txn.ID, txn.Status)
			break
		}
	}
	return status
}

// wantConfirmedOnce fails the test for a transaction that was confirmed twice.
func (a *plansApp) wantConfirmedOnce(t *testing.T) {
	t.Helper()
	if twice := a.statuses.twice(); len(twice) > 0 {
		t.Errorf("%d transactions were set to confirmed more than once, as %v: two calls each found them drafts", len(twice), twice)
	}
}

// isDeadlock reports whether PostgreSQL ended the call to break a deadlock.
func isDeadlock(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "40P01"
}

// unexpected says what is wrong with the error of a call that ran with others, and
// is empty when there is nothing wrong. The refusals that a call can get because
// another got there first are not wrong. A database error of any kind is, a
// deadlock above all, and so is anything else.
func unexpected(err error) string {
	if err == nil {
		return ""
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return fmt.Sprintf("database error %s: %v", pgErr.Code, err)
	}
	for _, refusal := range []error{model.ErrNotDraft, model.ErrNotConfirmed, model.ErrNotFound, model.ErrPartOfPlan, model.ErrPlanVoid, model.ErrPendingDrafts} {
		if errors.Is(err, refusal) {
			return ""
		}
	}
	return fmt.Sprintf("an error that no order of the calls explains: %v", err)
}

// together runs n calls that start at the same moment, as far as the scheduler goes,
// and returns when all of them have finished. Each has a deadline of its own, so
// that a call that waits for ever ends with an error that names it, and does not
// hang the test to the timeout of go test.
func together(n int, call func(ctx context.Context, i int)) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	for i := range n {
		ready.Add(1)
		done.Add(1)
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			ctx, cancel := context.WithTimeout(context.Background(), pollLimit)
			defer cancel()
			call(ctx, i)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// heldTogether runs n calls that overlap exactly, as heldCalls does: call 0 is held
// right after the statement that the tracer waits for, which is its first lock.
func (a *plansApp) heldTogether(t *testing.T, n int, run func(ctx context.Context, i int)) {
	t.Helper()
	a.hold.prefix = a.holdPrefix
	heldCalls(t, a.hold, a.direct, n, func(i int) string { return fmt.Sprintf("call %d", i) }, run)
}

// Two calls confirm the same two plans, one asking for them the other way round
// from the other. Each plan is many rows. Whatever order a call took its locks in
// as it went, the first would hold the rows of one plan and wait for the other's,
// and the second the other way round. The calls take them in one order, so the
// second waits for the first, which is what the test finds, and then finds the
// plans confirmed, which is a refusal and not a deadlock.
func TestConfirmsOfTwoPlansAskedInOppositeOrdersWaitForEachOtherAndDoNotDeadlock(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	card := a.card(t)
	first := a.plan(t, card, "opposite-plan-a-001", 3)
	second := a.plan(t, card, "opposite-plan-b-001", 3)

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		ids := []uuid.UUID{first.ID, second.ID}
		if i == 1 {
			ids = []uuid.UUID{second.ID, first.ID}
		}
		_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: ids})
	})

	for i, err := range errs {
		if isDeadlock(err) {
			t.Errorf("call %d was ended by PostgreSQL to break a deadlock: %v", i, err)
		}
	}
	if errs[0] != nil {
		t.Errorf("call 0 = %v, want it to confirm both plans: it held the first lock", errs[0])
	}
	if !errors.Is(errs[1], model.ErrNotDraft) {
		t.Errorf("call 1 = %v, want ErrNotDraft: what it waited for was confirmed by call 0", errs[1])
	}
	for _, plan := range []usecase.Transaction{first, second} {
		if got := a.statusOf(t, plan.ID); got != "confirmed" {
			t.Errorf("plan %s is %q, want confirmed whole", plan.ID, got)
		}
	}
	a.wantConfirmedOnce(t)
}

// The same four calls started together over and over: two confirm the plans in
// opposite orders, one discards the second and one voids the first. Whichever way
// they interleave, none may be ended for a deadlock, a plan is of one status or not
// there, and the outcomes are those of some order they could have run in.
func TestConfirmingTwoPlansInOppositeOrdersWhileOneIsDiscardedAndOneVoidedNeverDeadlocksOrHalfApplies(t *testing.T) {
	a := newPlansApp(t, 8, "")
	for round := range stressRounds {
		card := a.card(t)
		pa := a.plan(t, card, fmt.Sprintf("mixed-plan-a-%03d", round), 3)
		pb := a.plan(t, card, fmt.Sprintf("mixed-plan-b-%03d", round), 3)

		errs := make([]error, 4)
		together(4, func(ctx context.Context, i int) {
			switch i {
			case 0:
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{pa.ID, pb.ID}})
			case 1:
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{pb.ID, pa.ID}})
			case 2:
				errs[i] = a.l.Discard.DiscardDraft(ctx, pb.ID)
			case 3:
				_, errs[i] = a.l.Void.VoidTransaction(ctx, pa.ID)
			}
		})

		for i, err := range errs {
			if msg := unexpected(err); msg != "" {
				t.Errorf("round %d, call %d: %s", round, i, msg)
			}
		}
		first, second, discarded, voided := errs[0] == nil, errs[1] == nil, errs[2] == nil, errs[3] == nil
		confirmed := first || second
		if (first && second) || confirmed == discarded || (voided && !confirmed) {
			t.Errorf("round %d: confirmations %v and %v, discard %v, void %v succeeded: no order the calls could have run in leaves that",
				round, first, second, discarded, voided)
			continue
		}
		wantA, wantB := "draft", "confirmed"
		if voided {
			wantA = "void"
		} else if confirmed {
			wantA = "confirmed"
		}
		if discarded {
			wantB = ""
		}
		if gotA, gotB := a.statusOf(t, pa.ID), a.statusOf(t, pb.ID); gotA != wantA || gotB != wantB {
			t.Errorf("round %d: the plans are %q and %q, want %q and %q for what succeeded", round, gotA, gotB, wantA, wantB)
		}
	}
	a.wantConfirmedOnce(t)
}

// Confirm, void and discard of one plan, with a second confirm to race the first,
// started together over and over. Exactly one outcome is left each round: one
// confirmation and the plan confirmed, or voided after it, or no confirmation and
// the plan discarded.
func TestConfirmVoidAndDiscardOfOnePlanAtOnceLeaveOneConsistentOutcome(t *testing.T) {
	a := newPlansApp(t, 8, "")
	for round := range stressRounds {
		plan := a.plan(t, a.card(t), fmt.Sprintf("one-plan-%03d-xxxx", round), 3)

		errs := make([]error, 4)
		together(4, func(ctx context.Context, i int) {
			switch i {
			case 0, 1:
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.ID}})
			case 2:
				_, errs[i] = a.l.Void.VoidTransaction(ctx, plan.ID)
			case 3:
				errs[i] = a.l.Discard.DiscardDraft(ctx, plan.ID)
			}
		})

		for i, err := range errs {
			if msg := unexpected(err); msg != "" {
				t.Errorf("round %d, call %d: %s", round, i, msg)
			}
		}
		confirmations := 0
		for _, err := range errs[:2] {
			if err == nil {
				confirmations++
			}
		}
		voided, discarded := errs[2] == nil, errs[3] == nil
		var want string
		switch {
		case confirmations == 1 && !discarded && voided:
			want = "void"
		case confirmations == 1 && !discarded:
			want = "confirmed"
		case confirmations == 0 && discarded && !voided:
			want = ""
		default:
			t.Errorf("round %d: %d confirmations, void %v, discard %v succeeded: no order the calls could have run in leaves that",
				round, confirmations, voided, discarded)
			continue
		}
		if got := a.statusOf(t, plan.ID); got != want {
			t.Errorf("round %d: the plan is %q, want %q for what succeeded", round, got, want)
		}
	}
	a.wantConfirmedOnce(t)
}

// The period of a plan that falls due on the day of a reconciliation is a draft
// that the reconciliation must not look past. Confirming the plan while the account
// is reconciled has two ways to go: the reconciliation comes first and is refused
// for the draft that is pending, or the confirmation does and the reconciliation
// counts the period. It must never be neither: a reconciliation that does not see
// the draft and does not count the period either would book the period as a
// difference.
func TestConfirmingAPlanWhileItsAccountIsReconciledNeverReconcilesAgainstAStaleBalance(t *testing.T) {
	a := newPlansApp(t, 6, "")
	for round := range stressRounds {
		card := a.card(t)
		plan := a.plan(t, card, fmt.Sprintf("reconcile-plan-%03d", round), 3)

		var (
			confirmErr, reconcileErr error
			reconciled               usecase.ReconcileOutput
		)
		together(2, func(ctx context.Context, i int) {
			if i == 0 {
				_, confirmErr = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.ID}})
				return
			}
			details := a.l.details(fmt.Sprintf("reconcile-round-%03d", round))
			details.OccurredOn = dateOn(2026, time.December, 5)
			reconciled, reconcileErr = a.l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
				RecordDetails: details, AccountID: card, InstrumentID: model.BaseCurrencyID, ActualBalance: dec("-1030"),
			})
		})

		if confirmErr != nil {
			t.Errorf("round %d: the confirmation = %v, want it to succeed: nothing it needs is taken", round, confirmErr)
		}
		var pending *model.PendingDraftsError
		switch {
		case reconcileErr == nil:
			// It counted the period, so the card holds 1,030 less and is the 1,030 less that
			// it was told: nothing to book. A balance of 0 with a difference of 1,030 is the
			// stale one.
			if !reconciled.RecordedBalance.Equal(dec("-1030")) || !reconciled.Difference.IsZero() || reconciled.Transaction != nil {
				t.Errorf("round %d: reconciled against a recorded balance of %s, a difference of %s and a draft %v, want the -1030 of the confirmed period, no difference and no draft",
					round, reconciled.RecordedBalance, reconciled.Difference, reconciled.Transaction)
			}
		case errors.As(reconcileErr, &pending):
			if pending.Count != 1 {
				t.Errorf("round %d: %d drafts pending, want the 1 period that falls due on the day", round, pending.Count)
			}
		default:
			t.Errorf("round %d: the reconciliation = %v, want it to succeed or be refused for the draft that is pending", round, reconcileErr)
		}
		if got := a.statusOf(t, plan.ID); got != "confirmed" {
			t.Errorf("round %d: the plan is %q, want confirmed", round, got)
		}
		drafts, err := a.l.ListTransactions.ListTransactions(context.Background(), usecase.ListTransactionsInput{Status: "draft", AccountID: &card, Limit: 50})
		if err != nil || len(drafts.Transactions) != 0 {
			t.Errorf("round %d: drafts of the card = %d, %v, want none: no difference is booked", round, len(drafts.Transactions), err)
		}
	}
	a.wantConfirmedOnce(t)
}

// Calls that confirm what they pick at random of three plans and three ordinary
// drafts, each plan by its origin and some of its periods beside it, started
// together over and over. A call is all or nothing, so a plan or a draft ends up
// confirmed exactly when one of the calls that asked for it succeeded, and by one
// call only.
func TestOverlappingConfirmsOfPlansAndOrdinaryDraftsConfirmEachOfThemOnceAtMost(t *testing.T) {
	a := newPlansApp(t, 10, "")
	const plans, lunches, calls = 3, 3, 5
	type unit struct {
		origin  usecase.Transaction
		periods []uuid.UUID
		plan    bool
	}
	for round := range stressRounds {
		card := a.card(t)
		units := make([]unit, 0, plans+lunches)
		for i := range plans {
			origin := a.plan(t, card, fmt.Sprintf("random-plan-%03d-%d", round, i), 3)
			units = append(units, unit{origin: origin, periods: ids(a.planOf(t, origin.ID).Periods...), plan: true})
		}
		for i := range lunches {
			units = append(units, unit{origin: a.lunch(t, card, fmt.Sprintf("random-lunch-%03d-%d", round, i))})
		}

		// What each call asks for is chosen before they start, from the round, so that
		// a failure is the same on the next run.
		rng := rand.New(rand.NewPCG(uint64(round)+1, 42))
		type call struct {
			ids   []uuid.UUID
			units []int
		}
		asked := make([]call, calls)
		for i := range asked {
			for u, un := range units {
				if rng.IntN(2) == 0 {
					continue
				}
				asked[i].units = append(asked[i].units, u)
				asked[i].ids = append(asked[i].ids, un.origin.ID)
				for _, period := range un.periods {
					if rng.IntN(2) == 0 {
						asked[i].ids = append(asked[i].ids, period)
					}
				}
			}
			rng.Shuffle(len(asked[i].ids), func(x, y int) { asked[i].ids[x], asked[i].ids[y] = asked[i].ids[y], asked[i].ids[x] })
		}

		errs := make([]error, calls)
		together(calls, func(ctx context.Context, i int) {
			if len(asked[i].ids) == 0 {
				return
			}
			_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: asked[i].ids})
		})

		for i, err := range errs {
			if msg := unexpected(err); msg != "" {
				t.Errorf("round %d, call %d: %s", round, i, msg)
			}
		}
		for u, un := range units {
			successes := 0
			for i, c := range asked {
				if len(c.ids) > 0 && errs[i] == nil && slices.Contains(c.units, u) {
					successes++
				}
			}
			var got string
			if un.plan {
				got = a.statusOf(t, un.origin.ID)
			} else {
				txn, err := a.l.GetTransaction.GetTransaction(context.Background(), un.origin.ID)
				if err != nil {
					t.Fatalf("GetTransaction: %v", err)
				}
				got = txn.Status
			}
			want := "draft"
			if successes == 1 {
				want = "confirmed"
			}
			if successes > 1 || got != want {
				t.Errorf("round %d: unit %d was asked for by %d successful calls and is %q, want one call at most, and %q", round, u, successes, got, want)
			}
		}
	}
	a.wantConfirmedOnce(t)
}

// rescheduleTo is the schedule of a plan of 3,000 in the periods of the pairs of a
// day and an amount, with no interest.
func (a *plansApp) rescheduleTo(planID uuid.UUID, periods ...usecase.InstalmentPeriod) usecase.RescheduleInstalmentInput {
	return usecase.RescheduleInstalmentInput{PlanID: planID, Schedule: usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &a.interest}}
}

// two is the plan of 3,000 as two periods of 1,500.
func (a *plansApp) two(planID uuid.UUID) usecase.RescheduleInstalmentInput {
	return a.rescheduleTo(planID,
		usecase.InstalmentPeriod{DueOn: dateOn(2026, time.December, 20), Principal: dec("1500")},
		usecase.InstalmentPeriod{DueOn: dateOn(2027, time.January, 20), Principal: dec("1500")},
	)
}

// one is the plan of 3,000 as one period.
func (a *plansApp) one(planID uuid.UUID) usecase.RescheduleInstalmentInput {
	return a.rescheduleTo(planID, usecase.InstalmentPeriod{DueOn: dateOn(2027, time.March, 1), Principal: dec("3000")})
}

// scheduleKey is a plan's schedule as one string, which tells one schedule from
// another: the day and the principal of each period.
func scheduleKey(plan usecase.InstalmentPlan) string {
	var parts []string
	for _, period := range plan.Periods {
		principal := decimal.Zero
		for _, p := range postingsOf(period, "instalment") {
			principal = principal.Add(p.Quantity)
		}
		parts = append(parts, period.OccurredOn.Format(time.DateOnly)+" "+principal.String())
	}
	return strings.Join(parts, ", ")
}

// Rescheduling puts new periods in the place of the old ones, which the others
// must not take for what the plan is made of: a call that confirms or voids a plan
// while it is rescheduled waits for the origin, and then acts on the periods that
// are there, not on those that were. The first call is held with the origin
// locked, which is its first lock.
func TestConfirmingAPlanWhileItIsRescheduledConfirmsTheNewPeriods(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	plan := a.plan(t, a.card(t), "reschedule-confirm-1", 3)

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.two(plan.ID))
			return
		}
		_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.ID}})
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d = %v, want it to succeed: the confirmation waits for the origin and finds the new periods", i, err)
		}
	}
	got := a.planOf(t, plan.ID)
	if want := "2026-12-20 1500, 2027-01-20 1500"; scheduleKey(got) != want {
		t.Errorf("schedule = %q, want the new one, %q", scheduleKey(got), want)
	}
	if status := a.statusOf(t, plan.ID); status != "confirmed" {
		t.Errorf("the plan is %q, want the origin and the 2 new periods confirmed", status)
	}
	a.wantConfirmedOnce(t)
}

func TestVoidingAPlanWhileItIsRescheduledVoidsTheNewPeriods(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	plan := a.plan(t, a.card(t), "reschedule-void-0001", 3)
	a.l.confirm(t, plan)

	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.one(plan.ID))
			return
		}
		_, errs[i] = a.l.Void.VoidTransaction(ctx, plan.ID)
	})

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d = %v, want it to succeed: the void waits for the origin and finds the new period", i, err)
		}
	}
	if got := a.planOf(t, plan.ID); len(got.Periods) != 1 {
		t.Errorf("%d periods, want the 1 of the new schedule", len(got.Periods))
	}
	if status := a.statusOf(t, plan.ID); status != "void" {
		t.Errorf("the plan is %q, want the origin and the new period void", status)
	}
}

// A caller that gives the origin of a plan and the periods of it waits for the origin
// while the plan is rescheduled, and then the periods it gave are not the plan's any
// more: they are gone, which it is told as it is for an id that never was. It is not
// answered with transactions that are empty, and nothing is confirmed. The first call
// is held with the origin locked, which is its first lock.
func TestConfirmingThePeriodsOfAPlanWhileItIsRescheduledIsRefusedAndNotAnsweredWithEmptyTransactions(t *testing.T) {
	a := newPlansApp(t, 4, lockStatement)
	plan := a.plan(t, a.card(t), "reschedule-listed-01", 3)
	old := ids(a.planOf(t, plan.ID).Periods...)

	var confirmed usecase.ConfirmTransactionsOutput
	errs := make([]error, 2)
	a.heldTogether(t, 2, func(ctx context.Context, i int) {
		if i == 0 {
			_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.two(plan.ID))
			return
		}
		confirmed, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: append([]uuid.UUID{plan.ID}, old...)})
	})

	if errs[0] != nil {
		t.Errorf("reschedule = %v, want it to succeed", errs[0])
	}
	if !errors.Is(errs[1], model.ErrNotFound) {
		t.Fatalf("confirm = %v, want ErrNotFound: the periods it gave are gone", errs[1])
	}
	for _, id := range old {
		if !strings.Contains(errs[1].Error(), id.String()) {
			t.Errorf("confirm = %q, want it to name the period %s", errs[1], id)
		}
	}
	if len(confirmed.Transactions) != 0 {
		t.Errorf("confirm answered with %d transactions, want none: it is refused", len(confirmed.Transactions))
	}
	if status := a.statusOf(t, plan.ID); status != "draft" {
		t.Errorf("the plan is %q, want it left a draft: a refused call confirms nothing", status)
	}
	if want := "2026-12-20 1500, 2027-01-20 1500"; scheduleKey(a.planOf(t, plan.ID)) != want {
		t.Errorf("schedule = %q, want the new one, %q", scheduleKey(a.planOf(t, plan.ID)), want)
	}
	a.wantConfirmedOnce(t)
}

// Rescheduling twice, confirming and voiding one plan, started together over and
// over. A reschedule that wins or loses to the others is not an error unless it
// finds the plan void. The plan ends whole: of one status, of one of the schedules
// that was asked for or the one it had, with the same 3,000 split, and the same
// call never confirms a transaction twice.
func TestReschedulingAPlanWhileItIsConfirmedAndVoidedNeverDeadlocksAndLeavesOneScheduleWhole(t *testing.T) {
	a := newPlansApp(t, 8, "")
	for round := range stressRounds {
		plan := a.plan(t, a.card(t), fmt.Sprintf("reschedule-race-%03d", round), 3)
		original := scheduleKey(a.planOf(t, plan.ID))

		errs := make([]error, 4)
		together(4, func(ctx context.Context, i int) {
			switch i {
			case 0:
				_, errs[i] = a.l.Confirm.ConfirmTransactions(ctx, usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{plan.ID}})
			case 1:
				_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.two(plan.ID))
			case 2:
				_, errs[i] = a.l.RescheduleInstalment.RescheduleInstalment(ctx, a.one(plan.ID))
			case 3:
				_, errs[i] = a.l.Void.VoidTransaction(ctx, plan.ID)
			}
		})

		for i, err := range errs {
			if msg := unexpected(err); msg != "" {
				t.Errorf("round %d, call %d: %s", round, i, msg)
			}
		}
		if errs[0] != nil {
			t.Errorf("round %d: the confirmation = %v, want it to succeed: no call takes the plan from it but the void, which waits for it", round, errs[0])
		}
		voided := errs[3] == nil
		wantStatus := "confirmed"
		if voided {
			wantStatus = "void"
		}
		if got := a.statusOf(t, plan.ID); got != wantStatus {
			t.Errorf("round %d: the plan is %q, want %q", round, got, wantStatus)
		}

		// The schedule is the last that was written, one of those that succeeded.
		final := a.planOf(t, plan.ID)
		got := scheduleKey(final)
		candidates := map[string]bool{}
		if errs[1] == nil {
			candidates["2026-12-20 1500, 2027-01-20 1500"] = true
		}
		if errs[2] == nil {
			candidates["2027-03-01 3000"] = true
		}
		if len(candidates) == 0 {
			candidates[original] = true
		}
		if !candidates[got] {
			t.Errorf("round %d: the schedule is %q, want one that was written: %v", round, got, candidates)
		}
	}
	a.wantConfirmedOnce(t)
}

// ids returns the ids of the transactions.
func ids(txns ...usecase.Transaction) []uuid.UUID {
	out := make([]uuid.UUID, len(txns))
	for i, txn := range txns {
		out[i] = txn.ID
	}
	return out
}
