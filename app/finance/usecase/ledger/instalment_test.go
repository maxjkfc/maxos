package ledger_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// schedule is count periods of the principal, falling due on the 5th of each month
// from December 2026, whose interest is booked to the category of interest.
func (e *env) schedule(count int, principal string) usecase.InstalmentSchedule {
	periods := make([]usecase.InstalmentPeriod, count)
	for i := range periods {
		periods[i] = usecase.InstalmentPeriod{DueOn: time.Date(2026, time.Month(12+i), 5, 0, 0, 0, 0, time.UTC), Principal: dec(principal)}
	}
	return usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &e.interest.ID}
}

// instalmentExpense is an expense of 12,000 on household things, charged to the
// bank as if it were a card, in 12 periods of 1,000 on the 5th of each month, with
// 30 of interest on the first.
func (e *env) instalmentExpense(key string) usecase.RecordExpenseInput {
	schedule := e.schedule(12, "1000")
	schedule.Periods[0].Interest = decPtr("30")
	return usecase.RecordExpenseInput{
		RecordDetails: e.details(key),
		AccountID:     &e.bank.ID,
		InstrumentID:  e.twd.ID,
		Lines:         []usecase.CategoryLine{{CategoryID: e.household.ID, Amount: dec("12000")}},
		Instalments:   &schedule,
	}
}

// recordPlan records the expense of instalmentExpense, and returns its origin, a
// draft.
func (e *env) recordPlan(key string) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), e.instalmentExpense(key))
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return out.Transaction
}

// planOf reads the plan with the origin.
func (e *env) planOf(origin usecase.Transaction) usecase.InstalmentPlan {
	e.t.Helper()
	plan, err := e.svc.GetInstalmentPlan(context.Background(), origin.ID)
	if err != nil {
		e.t.Fatalf("GetInstalmentPlan: %v", err)
	}
	return plan
}

// membersOf returns the ids of the transactions of the plan, the origin and the
// periods, in the order of their ids.
func (e *env) membersOf(origin usecase.Transaction) []uuid.UUID {
	e.t.Helper()
	plan := e.planOf(origin)
	return sortedIDs(append(ids(plan.Periods...), plan.Origin.ID)...)
}

func sortedIDs(ids ...uuid.UUID) []uuid.UUID {
	out := slices.Clone(ids)
	slices.SortFunc(out, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return out
}

// statuses returns the status of each of the transactions stored, by id.
func (e *env) statuses() map[uuid.UUID]model.Status {
	out := make(map[uuid.UUID]model.Status, len(e.txns.byID))
	for id, txn := range e.txns.byID {
		out[id] = txn.Status
	}
	return out
}

func TestRecordExpenseWithInstalmentsRecordsTheOriginAndEveryPeriodAsDraftsInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)

	out, err := e.svc.RecordExpense(context.Background(), e.instalmentExpense("laptop-00000001"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	origin := out.Transaction
	if out.Duplicate || origin.Kind != "expense" || origin.Status != "draft" || origin.IdempotencyKey != "laptop-00000001" {
		t.Fatalf("output = %+v, want a new draft expense with the key of the request", out)
	}
	if origin.PlanID == nil || *origin.PlanID != origin.ID || origin.PlanSeq == nil || *origin.PlanSeq != 0 {
		t.Errorf("origin: plan %v, position %v, want its own id and the position 0", origin.PlanID, origin.PlanSeq)
	}
	wantLines(t, e.lines(origin), "instalment(bank) -12000 TWD", "expense(household) +12000 TWD, base +12000")
	// The fake refuses a period stored before its origin, as the table does, so that
	// the periods were stored after it is shown too; and it took one database
	// transaction for all of it.
	if e.tx.calls != 1 || e.txns.inserts != 13 {
		t.Errorf("%d database transactions and %d inserts, want 1 and 13: the origin and each of the 12 periods", e.tx.calls, e.txns.inserts)
	}

	plan := e.planOf(origin)
	if plan.Origin.ID != origin.ID || len(plan.Periods) != 12 {
		t.Fatalf("plan = %+v, want the origin and 12 periods", plan)
	}
	for i, period := range plan.Periods {
		seq := i + 1
		due := time.Date(2026, time.Month(12+i), 5, 0, 0, 0, 0, time.UTC)
		if period.Kind != "instalment" || period.Status != "draft" || period.PlanID == nil || *period.PlanID != origin.ID ||
			period.PlanSeq == nil || *period.PlanSeq != seq || !period.OccurredOn.Equal(due) {
			t.Errorf("period %d: kind %s, status %s, plan %v, position %v, on %s; want a draft instalment of the plan on %s",
				seq, period.Kind, period.Status, period.PlanID, period.PlanSeq, period.OccurredOn.Format(time.DateOnly), due.Format(time.DateOnly))
		}
		if period.ConfirmedAt != nil || period.CreatedAt.IsZero() {
			t.Errorf("period %d: confirmed at %v, created at %v, want a draft that was stored", seq, period.ConfirmedAt, period.CreatedAt)
		}
	}
	wantLines(t, e.lines(plan.Periods[0]), "instalment(bank) +1000 TWD", "account(bank) -1030 TWD", "expense(interest) +30 TWD, base +30")
	wantLines(t, e.lines(plan.Periods[11]), "instalment(bank) +1000 TWD", "account(bank) -1000 TWD")
}

func TestTheKeysOfThePeriodsOfAPlanAreMadeOfThePlanAndNeverComeFromTheCaller(t *testing.T) {
	e := newEnv(t)
	first, second := e.recordPlan("laptop-00000001"), e.recordPlan("laptop-00000002")

	seen := map[string]bool{}
	for _, origin := range []usecase.Transaction{first, second} {
		for i, period := range e.planOf(origin).Periods {
			want := model.PeriodIdempotencyKey(origin.ID, i+1)
			if period.IdempotencyKey != want {
				t.Errorf("key of period %d = %q, want %q, made of the plan and the position", i+1, period.IdempotencyKey, want)
			}
			if strings.Contains(period.IdempotencyKey, origin.IdempotencyKey) {
				t.Errorf("key of period %d = %q has the key of the request in it", i+1, period.IdempotencyKey)
			}
			if seen[period.IdempotencyKey] {
				t.Errorf("key %q is the key of more than one period", period.IdempotencyKey)
			}
			seen[period.IdempotencyKey] = true
		}
	}
	if len(seen) != 24 {
		t.Errorf("%d different keys for the periods of two plans of 12, want 24", len(seen))
	}
}

// The key of a period is the ledger's own. A request that gave one would be answered
// with that period, as a duplicate, so every use case that takes a key refuses the
// form they are made in, before anything is looked up or written.
func TestEveryRecordRefusesAKeyInTheFormOfThoseOfPeriodsAndAnswersWithNoPeriod(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	origin := e.recordPlan("laptop-00000001")
	period := e.planOf(origin).Periods[0]
	d := usecase.RecordDetails{IdempotencyKey: period.IdempotencyKey, OccurredOn: day}
	stored, opened := len(e.txns.byID), e.tx.calls

	calls := map[string]func() error{
		"RecordExpense": func() error {
			_, err := e.svc.RecordExpense(ctx, usecase.RecordExpenseInput{RecordDetails: d})
			return err
		},
		"RecordExpense in instalments": func() error {
			_, err := e.svc.RecordExpense(ctx, usecase.RecordExpenseInput{RecordDetails: d, Instalments: &usecase.InstalmentSchedule{}})
			return err
		},
		"RecordIncome": func() error {
			_, err := e.svc.RecordIncome(ctx, usecase.RecordIncomeInput{RecordDetails: d})
			return err
		},
		"RecordTransfer": func() error {
			_, err := e.svc.RecordTransfer(ctx, usecase.RecordTransferInput{RecordDetails: d})
			return err
		},
		"RecordExchange": func() error {
			_, err := e.svc.RecordExchange(ctx, usecase.RecordExchangeInput{RecordDetails: d})
			return err
		},
		"RecordTrade": func() error { _, err := e.svc.RecordTrade(ctx, usecase.RecordTradeInput{RecordDetails: d}); return err },
		"RecordDealing": func() error {
			_, err := e.svc.RecordDealing(ctx, usecase.RecordDealingInput{RecordDetails: d})
			return err
		},
		"RecordReward": func() error {
			_, err := e.svc.RecordReward(ctx, usecase.RecordRewardInput{RecordDetails: d})
			return err
		},
		"SetOpeningBalance": func() error {
			_, err := e.svc.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{RecordDetails: d})
			return err
		},
		"RecordBillInstalment": func() error {
			_, err := e.svc.RecordBillInstalment(ctx, usecase.RecordBillInstalmentInput{RecordDetails: d})
			return err
		},
		"Reconcile": func() error { _, err := e.svc.Reconcile(ctx, usecase.ReconcileInput{RecordDetails: d}); return err },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			err := call()

			if !errors.Is(err, model.ErrReservedIdempotencyKey) {
				t.Fatalf("err = %v, want ErrReservedIdempotencyKey: the key is that of period 1 of a plan", err)
			}
		})
	}
	if len(e.txns.byID) != stored || e.tx.calls != opened {
		t.Errorf("%d transactions stored and %d database transactions opened, want the %d and %d there were: nothing is looked up or written",
			len(e.txns.byID), e.tx.calls, stored, opened)
	}
}

func TestRecordExpenseWithInstalmentsAnsweredWithAKeyUsedBeforeReturnsTheOriginAndWritesNothing(t *testing.T) {
	e := newEnv(t)
	first := e.recordPlan("laptop-00000001")
	inserts, stored := e.txns.inserts, len(e.txns.byID)

	// Whatever the second request says, it is the first one that stands, and it is
	// not even looked at: the account is archived by now.
	e.accounts.archive(e.bank.ID)
	again := e.instalmentExpense("laptop-00000001")
	again.Lines[0].Amount = dec("999")
	again.Instalments.Periods = again.Instalments.Periods[:3]
	out, err := e.svc.RecordExpense(context.Background(), again)
	if err != nil {
		t.Fatalf("RecordExpense with the key again: %v", err)
	}

	if !out.Duplicate || out.Transaction.ID != first.ID || out.Transaction.Kind != "expense" {
		t.Fatalf("output = %+v, want the origin recorded the first time, marked as a duplicate", out)
	}
	wantLines(t, e.lines(out.Transaction), "instalment(bank) -12000 TWD", "expense(household) +12000 TWD, base +12000")
	if e.txns.inserts != inserts || len(e.txns.byID) != stored {
		t.Errorf("%d inserts and %d stored, want nothing written the second time, %d and %d", e.txns.inserts, len(e.txns.byID), inserts, stored)
	}
}

func TestRecordExpenseWithInstalmentsWhoseKeyAnotherCallRecordsMeanwhileReturnsThatOriginAndWritesNoPeriod(t *testing.T) {
	e := newEnv(t)
	concurrent := model.Transaction{
		ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft, OccurredOn: day, IdempotencyKey: "laptop-00000001",
	}
	e.txns.concurrent = &concurrent

	out, err := e.svc.RecordExpense(context.Background(), e.instalmentExpense("laptop-00000001"))
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}

	if !out.Duplicate || out.Transaction.ID != concurrent.ID {
		t.Fatalf("output = %+v, want the transaction the other call recorded, marked as a duplicate", out)
	}
	if e.txns.inserts != 0 || len(e.txns.byID) != 1 {
		t.Errorf("%d inserts and %d stored, want none by this call and only the other's transaction: no period of a plan that was not recorded", e.txns.inserts, len(e.txns.byID))
	}
}

func TestRecordExpenseWithInstalmentsRefusesWhatCannotBeAPlanAndStoresNothing(t *testing.T) {
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordExpenseInput)
		want   error
	}{
		{"principals that do not add up to the expense", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.Periods[11].Principal = dec("999")
		}, model.ErrPrincipalMismatch},
		{"periods that do not fall due on later days", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.Periods[3].DueOn = in.Instalments.Periods[2].DueOn
		}, model.ErrDueDatesNotIncreasing},
		{"a first period before the purchase", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.Periods[0].DueOn = day.AddDate(0, 0, -1)
		}, model.ErrFirstDueTooEarly},
		{"no periods", func(e *env, in *usecase.RecordExpenseInput) { in.Instalments.Periods = nil }, model.ErrNoPeriods},
		{"interest with no category", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.InterestCategoryID = nil
		}, model.ErrInterestCategoryRequired},
		{"an interest category that does not exist", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.InterestCategoryID = &unknown
		}, model.ErrNotFound},
		{"an archived interest category", func(e *env, in *usecase.RecordExpenseInput) {
			e.categories.archive(e.interest.ID)
		}, model.ErrArchived},
		{"an interest category of incomes", func(e *env, in *usecase.RecordExpenseInput) {
			in.Instalments.InterestCategoryID = &e.salary.ID
		}, model.ErrWrongCategoryDirection},
		{"an expense that a party paid", func(e *env, in *usecase.RecordExpenseInput) {
			party := e.refs.addParty("Amy", false)
			in.AccountID, in.PaidByPartyID = nil, &party
		}, model.ErrInstalmentsWithPaidBy},
		{"no account to charge", func(e *env, in *usecase.RecordExpenseInput) { in.AccountID = nil }, model.ErrNoPayer},
		{"an instrument that is not a currency", func(e *env, in *usecase.RecordExpenseInput) { in.InstrumentID = e.btc.ID }, model.ErrNotCurrency},
		{"an archived account", func(e *env, in *usecase.RecordExpenseInput) { e.accounts.archive(e.bank.ID) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := e.instalmentExpense("refused-0000001")
			tt.mutate(e, &in)

			out, err := e.svc.RecordExpense(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			// Nothing of the origin or of any period is left: all of it is made before any
			// of it is written.
			if out.Transaction.ID != uuid.Nil || e.txns.inserts != 0 || len(e.txns.byID) != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestRecordBillInstalmentRecordsTheConversionOfTheDebtAndItsPeriodsAsDrafts(t *testing.T) {
	e := newEnv(t)
	in := usecase.RecordBillInstalmentInput{
		RecordDetails: e.details("bill-00000001"),
		AccountID:     e.bank.ID,
		InstrumentID:  e.twd.ID,
		Principal:     dec("12000"),
		Schedule:      e.schedule(12, "1000"),
		Fees:          []usecase.Fee{{AccountID: e.cash.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("100")}},
	}
	in.Schedule.Periods[0].Interest = decPtr("30")

	out, err := e.svc.RecordBillInstalment(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordBillInstalment: %v", err)
	}

	origin := out.Transaction
	if out.Duplicate || origin.Kind != "instalment" || origin.Status != "draft" || origin.IdempotencyKey != "bill-00000001" {
		t.Fatalf("output = %+v, want a new draft instalment with the key of the request", out)
	}
	if origin.PlanID == nil || *origin.PlanID != origin.ID || origin.PlanSeq == nil || *origin.PlanSeq != 0 {
		t.Errorf("origin: plan %v, position %v, want its own id and the position 0", origin.PlanID, origin.PlanSeq)
	}
	wantLines(t, e.lines(origin),
		"account(bank) +12000 TWD",
		"instalment(bank) -12000 TWD",
		"account(cash) -100 TWD",
		"expense(fees) +100 TWD, base +100",
	)
	if e.tx.calls != 1 || e.txns.inserts != 13 {
		t.Errorf("%d database transactions and %d inserts, want 1 and 13", e.tx.calls, e.txns.inserts)
	}
	plan := e.planOf(origin)
	wantLines(t, e.lines(plan.Periods[0]), "instalment(bank) +1000 TWD", "account(bank) -1030 TWD", "expense(interest) +30 TWD, base +30")
	wantLines(t, e.lines(plan.Periods[5]), "instalment(bank) +1000 TWD", "account(bank) -1000 TWD")

	// The key of the request is the origin's: using it again returns the origin.
	again, err := e.svc.RecordBillInstalment(context.Background(), in)
	if err != nil {
		t.Fatalf("RecordBillInstalment with the key again: %v", err)
	}
	if !again.Duplicate || again.Transaction.ID != origin.ID || e.txns.inserts != 13 {
		t.Errorf("output = %+v, %d inserts, want the origin again as a duplicate, and nothing written", again, e.txns.inserts)
	}
}

func TestRecordBillInstalmentRefusesWhatCannotBeAPlanAndStoresNothing(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordBillInstalmentInput)
		want   error
	}{
		{"principals that do not add up to the debt", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.Principal = dec("12001")
		}, model.ErrPrincipalMismatch},
		{"a debt that is not in a currency", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.InstrumentID = e.btc.ID
		}, model.ErrNotCurrency},
		{"an account that does not exist", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.AccountID = uuid.Must(uuid.NewV7())
		}, model.ErrNotFound},
		{"a fee that is booked to a category that does not exist", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.Fees = []usecase.Fee{{AccountID: e.cash.ID, InstrumentID: e.twd.ID, CategoryID: uuid.Must(uuid.NewV7()), Amount: dec("100")}}
		}, model.ErrNotFound},
		{"interest with no category", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.Schedule.Periods[0].Interest = decPtr("30")
			in.Schedule.InterestCategoryID = nil
		}, model.ErrInterestCategoryRequired},
		{"a first period before the conversion", func(e *env, in *usecase.RecordBillInstalmentInput) {
			in.Schedule.Periods[0].DueOn = day.AddDate(0, 0, -1)
		}, model.ErrFirstDueTooEarly},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := usecase.RecordBillInstalmentInput{
				RecordDetails: e.details("bill-refused-001"), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
				Principal: dec("3000"), Schedule: e.schedule(3, "1000"),
			}
			tt.mutate(e, &in)

			out, err := e.svc.RecordBillInstalment(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if out.Transaction.ID != uuid.Nil || e.txns.inserts != 0 || len(e.txns.byID) != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestRecordTradeRefusesWhatIsNotATradeAndStoresNothing(t *testing.T) {
	unknown := uuid.Must(uuid.NewV7())
	tests := []struct {
		name   string
		mutate func(e *env, in *usecase.RecordTradeInput)
		want   error
	}{
		{"a currency as what is traded", func(e *env, in *usecase.RecordTradeInput) { in.InstrumentID = e.usd.ID }, model.ErrIsCurrency},
		{"a payment in something that is not a currency", func(e *env, in *usecase.RecordTradeInput) { in.CurrencyID = e.btc.ID }, model.ErrNotCurrency},
		{"a direction that is neither", func(e *env, in *usecase.RecordTradeInput) { in.Direction = "keep" }, model.ErrInvalidTradeDirection},
		{"an account that does not exist", func(e *env, in *usecase.RecordTradeInput) { in.AccountID = unknown }, model.ErrNotFound},
		{"a cash account that does not exist", func(e *env, in *usecase.RecordTradeInput) { in.CashAccountID = unknown }, model.ErrNotFound},
		{"an instrument that does not exist", func(e *env, in *usecase.RecordTradeInput) { in.InstrumentID = unknown }, model.ErrNotFound},
		{"a currency that does not exist", func(e *env, in *usecase.RecordTradeInput) { in.CurrencyID = unknown }, model.ErrNotFound},
		{"an archived cash account", func(e *env, in *usecase.RecordTradeInput) { e.accounts.archive(e.bank.ID) }, model.ErrArchived},
		{"a quantity of nothing", func(e *env, in *usecase.RecordTradeInput) { in.Quantity = dec("0") }, model.ErrAmountNotPositive},
		{"a fee that is booked to a category that does not exist", func(e *env, in *usecase.RecordTradeInput) {
			in.Fees = []usecase.Fee{{AccountID: e.bank.ID, InstrumentID: e.twd.ID, CategoryID: unknown, Amount: dec("15")}}
		}, model.ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			in := usecase.RecordTradeInput{
				RecordDetails: e.details("trade-refused-1"), Direction: "buy",
				AccountID: e.cash.ID, InstrumentID: e.btc.ID, Quantity: dec("0.1"),
				CashAccountID: e.bank.ID, CurrencyID: e.twd.ID, Amount: dec("300000"),
			}
			tt.mutate(e, &in)

			out, err := e.svc.RecordTrade(context.Background(), in)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if out.Transaction.ID != uuid.Nil || e.txns.inserts != 0 {
				t.Errorf("output = %+v, %d inserts, want nothing stored", out, e.txns.inserts)
			}
		})
	}
}

func TestConfirmTransactionsThroughTheOriginConfirmsTheWholePlanInOneDatabaseTransaction(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	lunch := e.recordLunch("lunch-0000001a")
	opened, changes := e.tx.calls, len(e.txns.statusSet)

	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{origin.ID, lunch.ID}})
	if err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}

	// What comes back is what was asked for, in the order it was asked for: the plan
	// as its origin. The periods are read with GetInstalmentPlan.
	if got := ids(out.Transactions...); !slices.Equal(got, []uuid.UUID{origin.ID, lunch.ID}) {
		t.Fatalf("confirmed %v, want %v: the origin and the lunch in the order given", got, []uuid.UUID{origin.ID, lunch.ID})
	}
	for id, status := range e.statuses() {
		if status != model.StatusConfirmed {
			t.Errorf("transaction %s is %s, want everything of the plan, and the lunch, confirmed", id, status)
		}
	}
	if len(e.txns.byID) != 14 {
		t.Errorf("%d transactions stored, want the 13 of the plan and the lunch", len(e.txns.byID))
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1 for all of them", got)
	}
	if got := len(e.txns.statusSet) - changes; got != 14 {
		t.Errorf("%d changes of status, want one for each of the 14", got)
	}
	for _, period := range e.planOf(origin).Periods {
		if period.Status != "confirmed" || period.ConfirmedAt == nil {
			t.Errorf("period %d is %s, want it confirmed with its origin", *period.PlanSeq, period.Status)
		}
	}
}

func TestConfirmTransactionsRefusesAPeriodByItsOwnIdAndConfirmsNothing(t *testing.T) {
	tests := []struct {
		name string
		ids  func(origin, period, lunch usecase.Transaction) []uuid.UUID
	}{
		{"a period alone", func(_, period, _ usecase.Transaction) []uuid.UUID { return []uuid.UUID{period.ID} }},
		{"a period with a draft that could be confirmed", func(_, period, lunch usecase.Transaction) []uuid.UUID {
			return []uuid.UUID{lunch.ID, period.ID}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			origin := e.recordPlan("laptop-00000001")
			lunch := e.recordLunch("lunch-0000001a")
			period := e.planOf(origin).Periods[3]

			_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: tt.ids(origin, period, lunch)})

			if !errors.Is(err, model.ErrPartOfPlan) {
				t.Fatalf("err = %v, want ErrPartOfPlan", err)
			}
			if !strings.Contains(err.Error(), period.ID.String()) || !strings.Contains(err.Error(), origin.ID.String()) {
				t.Errorf("err = %q, want it to name the period and the plan it is part of", err)
			}
			for id, status := range e.statuses() {
				if status != model.StatusDraft {
					t.Errorf("transaction %s is %s, want everything left a draft: it is all or nothing", id, status)
				}
			}
			if len(e.txns.statusSet) != 0 {
				t.Errorf("%d changes of status written, want none", len(e.txns.statusSet))
			}
		})
	}
}

// What a caller that confirms every draft it lists sends: the origin of a plan and
// periods of it, which are drafts of their own. The plan is confirmed once, as a
// whole, and what comes back is what was given, each once and in the order given.
func TestConfirmTransactionsGivenAPlanAndSomeOfItsPeriodsConfirmsThePlanOnceAndReturnsWhatWasGiven(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	notAsked := e.recordPlan("laptop-00000002")
	lunch := e.recordLunch("lunch-0000001a")
	periods := ids(e.planOf(origin).Periods...)
	opened, changes := e.tx.calls, len(e.txns.statusSet)

	// A period before its origin, one twice, and the lunch between: the order is the caller's.
	given := []uuid.UUID{periods[4], lunch.ID, origin.ID, periods[0], periods[4]}
	out, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: given})
	if err != nil {
		t.Fatalf("ConfirmTransactions: %v, want a period to be covered by its origin, which is given too", err)
	}

	if got, want := ids(out.Transactions...), []uuid.UUID{periods[4], lunch.ID, origin.ID, periods[0]}; !slices.Equal(got, want) {
		t.Errorf("confirmed %v, want each id given once, in the order given,\n  %v", got, want)
	}
	for _, txn := range out.Transactions {
		if txn.Status != "confirmed" || txn.ConfirmedAt == nil {
			t.Errorf("transaction %s is %s in the output, want it as it is confirmed", txn.ID, txn.Status)
		}
	}
	// All of the plan, though only two periods were given, and nothing of the other plan.
	notConfirmed := map[uuid.UUID]bool{}
	for _, id := range e.membersOf(notAsked) {
		notConfirmed[id] = true
	}
	for id, status := range e.statuses() {
		if want := map[bool]model.Status{true: model.StatusDraft, false: model.StatusConfirmed}[notConfirmed[id]]; status != want {
			t.Errorf("transaction %s is %s, want %s", id, status, want)
		}
	}
	// Each transaction of the plan is confirmed once, and in one database transaction.
	written := e.txns.statusSet[changes:]
	if want := sortedIDs(append(e.membersOf(origin), lunch.ID)...); !slices.Equal(sortedIDs(written...), want) {
		t.Errorf("status set for %v, want each of the 13 of the plan and the lunch once,\n  %v", sortedIDs(written...), want)
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1", got)
	}
	if want := sortedIDs(append(e.membersOf(origin), lunch.ID)...); !slices.Equal(e.txns.locked, want) {
		t.Errorf("locked\n  %v\nwant each transaction once, in the order of the ids,\n  %v", e.txns.locked, want)
	}
}

// A period is covered by its own origin, and by no other: the origin of another
// plan does not make it right.
func TestConfirmTransactionsRefusesAPeriodWhoseOriginIsNotAmongTheIdsEvenWithTheOriginOfAnotherPlan(t *testing.T) {
	e := newEnv(t)
	first, second := e.recordPlan("laptop-00000001"), e.recordPlan("laptop-00000002")
	period := e.planOf(first).Periods[3]

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{second.ID, period.ID}})

	if !errors.Is(err, model.ErrPartOfPlan) || !strings.Contains(err.Error(), period.ID.String()) || !strings.Contains(err.Error(), first.ID.String()) {
		t.Fatalf("err = %v, want ErrPartOfPlan naming the period and its plan", err)
	}
	for id, status := range e.statuses() {
		if status != model.StatusDraft {
			t.Errorf("transaction %s is %s, want everything left a draft: it is all or nothing", id, status)
		}
	}
	if slices.Contains(e.txns.locked, period.ID) {
		t.Error("the period that is refused was locked")
	}
}

// All the rows a call touches are locked in the order of their ids, so that two
// calls that have some in common cannot each hold what the other waits for.
func TestConfirmTransactionsLocksEveryTransactionOfTheCallInTheOrderOfTheirIds(t *testing.T) {
	e := newEnv(t)
	first, second := e.recordPlan("laptop-00000001"), e.recordPlan("laptop-00000002")
	lunch := e.recordLunch("lunch-0000001a")
	want := sortedIDs(append(append(e.membersOf(first), e.membersOf(second)...), lunch.ID)...)
	if len(want) != 27 {
		t.Fatalf("%d transactions to lock, want the 13 of each plan and the lunch", len(want))
	}
	// Asked for in the order that is the worst for it: the other way round.
	asked := slices.Clone(sortedIDs(lunch.ID, first.ID, second.ID))
	slices.Reverse(asked)

	if _, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: asked}); err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}

	if !slices.Equal(e.txns.locked, want) {
		t.Errorf("locked\n  %v\nwant every transaction of the call once, in the order of the ids,\n  %v", e.txns.locked, want)
	}
}

func TestVoidTransactionAndDiscardDraftLockEveryTransactionOfThePlanInTheOrderOfTheirIds(t *testing.T) {
	e := newEnv(t)
	discarded := e.recordPlan("laptop-00000001")
	voided := e.recordPlan("laptop-00000002")
	e.confirm(voided)
	e.txns.locked = nil

	if err := e.svc.DiscardDraft(context.Background(), discarded.ID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}
	if want := sortedIDs(append(ids(e.planOf(voided).Periods...), voided.ID)...); len(e.txns.locked) != 13 {
		t.Errorf("DiscardDraft locked %d, want the 13 of the plan (%d of the other plan are not)", len(e.txns.locked), len(want))
	} else if !slices.IsSortedFunc(e.txns.locked, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) }) {
		t.Errorf("DiscardDraft locked %v, want the order of the ids", e.txns.locked)
	}

	e.txns.locked = nil
	if _, err := e.svc.VoidTransaction(context.Background(), voided.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	if want := e.membersOf(voided); !slices.Equal(e.txns.locked, want) {
		t.Errorf("VoidTransaction locked\n  %v\nwant the 13 of the plan in the order of the ids,\n  %v", e.txns.locked, want)
	}
}

// A period that is refused is not locked: the call may hold the lock of another
// transaction it asks for, and waiting for the lock of a period while holding that
// one is how it could wait for the call that has the plan of the period, which
// waits for it.
func TestConfirmTransactionsDoesNotLockAPeriodItRefuses(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	lunch := e.recordLunch("lunch-0000001a")
	period := e.planOf(origin).Periods[0]

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{period.ID, lunch.ID}})

	if !errors.Is(err, model.ErrPartOfPlan) {
		t.Fatalf("err = %v, want ErrPartOfPlan", err)
	}
	if !slices.Equal(e.txns.locked, []uuid.UUID{lunch.ID}) {
		t.Errorf("locked %v, want only the lunch: the period is refused without a lock", e.txns.locked)
	}
}

func TestConfirmTransactionsOfAPlanWithAPeriodThatIsNotADraftConfirmsNothing(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	stray := e.planOf(origin).Periods[4]
	txn := e.txns.byID[stray.ID]
	txn.Status, txn.ConfirmedAt = model.StatusConfirmed, &epoch
	e.txns.byID[stray.ID] = txn

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{origin.ID}})

	if !errors.Is(err, model.ErrNotDraft) || !strings.Contains(err.Error(), stray.ID.String()) {
		t.Fatalf("err = %v, want ErrNotDraft naming the period", err)
	}
	for id, status := range e.statuses() {
		if id != stray.ID && status != model.StatusDraft {
			t.Errorf("transaction %s is %s, want the rest of the plan left a draft", id, status)
		}
	}
	if len(e.txns.statusSet) != 0 {
		t.Errorf("%d changes of status written, want none", len(e.txns.statusSet))
	}
}

func TestConfirmTransactionsOfAPlanThatLosesAPeriodWhileItWaitsForTheLockConfirmsNothing(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	victim := e.planOf(origin).Periods[2].ID
	// Another call discards the plan while this one waits for the lock of the period:
	// what is left of it when the lock is given is nothing.
	e.txns.beforeLock = func(id uuid.UUID) {
		if id == victim {
			delete(e.txns.byID, id)
		}
	}

	_, err := e.svc.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: []uuid.UUID{origin.ID}})

	if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), victim.String()) {
		t.Fatalf("err = %v, want ErrNotFound naming the period", err)
	}
	if len(e.txns.statusSet) != 0 {
		t.Errorf("%d changes of status written, want none: the rest of the plan stays as it was", len(e.txns.statusSet))
	}
}

func TestVoidTransactionThroughTheOriginVoidsTheWholePlanWhateverPeriodsHaveFallenDue(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	e.confirm(origin)
	// The first four periods have been charged by now.
	e.clock.now = time.Date(2027, 3, 6, 9, 0, 0, 0, taipei)
	opened := e.tx.calls

	got, err := e.svc.VoidTransaction(context.Background(), origin.ID)
	if err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}

	if got.ID != origin.ID || got.Status != "void" || got.VoidedAt == nil || got.ConfirmedAt == nil {
		t.Fatalf("transaction = %+v, want the origin, void, with the time it was confirmed and the time it was voided", got)
	}
	for _, period := range e.planOf(origin).Periods {
		if period.Status != "void" || period.VoidedAt == nil {
			t.Errorf("period %d is %s, want it void with its plan", *period.PlanSeq, period.Status)
		}
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1", got)
	}
}

func TestVoidTransactionRefusesAPeriodByItsOwnIdAndAPlanThatIsNotConfirmed(t *testing.T) {
	e := newEnv(t)
	confirmed := e.recordPlan("laptop-00000001")
	e.confirm(confirmed)
	draft := e.recordPlan("laptop-00000002")
	period := e.planOf(confirmed).Periods[7]
	changes := len(e.txns.statusSet)

	if _, err := e.svc.VoidTransaction(context.Background(), period.ID); !errors.Is(err, model.ErrPartOfPlan) {
		t.Errorf("a period: err = %v, want ErrPartOfPlan", err)
	}
	if _, err := e.svc.VoidTransaction(context.Background(), draft.ID); !errors.Is(err, model.ErrNotConfirmed) {
		t.Errorf("a plan that is a draft: err = %v, want ErrNotConfirmed", err)
	}
	if len(e.txns.statusSet) != changes {
		t.Errorf("%d changes of status written, want none", len(e.txns.statusSet)-changes)
	}
	if _, err := e.svc.VoidTransaction(context.Background(), confirmed.ID); err != nil {
		t.Fatalf("VoidTransaction: %v", err)
	}
	if _, err := e.svc.VoidTransaction(context.Background(), confirmed.ID); !errors.Is(err, model.ErrNotConfirmed) {
		t.Errorf("a plan that is void already: err = %v, want ErrNotConfirmed", err)
	}
}

func TestDiscardDraftThroughTheOriginDeletesThePeriodsAndThenTheOrigin(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	lunch := e.recordLunch("lunch-0000001a")
	other := e.recordPlan("laptop-00000002")
	opened := e.tx.calls

	// The fake does as the table does: the origin of a plan cannot be deleted while a
	// period of it is stored, so it is only deleted last that it can be deleted.
	if err := e.svc.DiscardDraft(context.Background(), origin.ID); err != nil {
		t.Fatalf("DiscardDraft: %v", err)
	}

	if len(e.txns.deleted) != 13 || e.txns.deleted[12] != origin.ID {
		t.Fatalf("deleted %v, want the 12 periods and then the origin last", e.txns.deleted)
	}
	if _, found := e.txns.byID[lunch.ID]; !found {
		t.Error("the lunch is gone, want it left alone")
	}
	if _, err := e.svc.GetInstalmentPlan(context.Background(), other.ID); err != nil {
		t.Errorf("the other plan: %v, want it left alone", err)
	}
	if _, err := e.svc.GetInstalmentPlan(context.Background(), origin.ID); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("the discarded plan: err = %v, want ErrNotFound", err)
	}
	if got := e.tx.calls - opened; got != 1 {
		t.Errorf("%d database transactions opened, want 1", got)
	}
}

func TestDiscardDraftRefusesAPeriodByItsOwnIdAndAPlanThatIsConfirmed(t *testing.T) {
	e := newEnv(t)
	draft := e.recordPlan("laptop-00000001")
	confirmed := e.recordPlan("laptop-00000002")
	e.confirm(confirmed)
	period := e.planOf(draft).Periods[0]

	if err := e.svc.DiscardDraft(context.Background(), period.ID); !errors.Is(err, model.ErrPartOfPlan) {
		t.Errorf("a period: err = %v, want ErrPartOfPlan", err)
	}
	if err := e.svc.DiscardDraft(context.Background(), confirmed.ID); !errors.Is(err, model.ErrNotDraft) {
		t.Errorf("a plan that is confirmed: err = %v, want ErrNotDraft", err)
	}
	if len(e.txns.deleted) != 0 || len(e.txns.byID) != 26 {
		t.Errorf("deleted %v, %d stored, want nothing deleted of the 26 transactions of two plans", e.txns.deleted, len(e.txns.byID))
	}
}

func TestGetInstalmentPlanReturnsTheOriginAndThePeriodsInTheOrderTheyFallDue(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")

	plan, err := e.svc.GetInstalmentPlan(context.Background(), origin.ID)
	if err != nil {
		t.Fatalf("GetInstalmentPlan: %v", err)
	}

	if plan.Origin.ID != origin.ID || len(plan.Periods) != 12 {
		t.Fatalf("plan = %+v, want the origin and 12 periods", plan)
	}
	for i, period := range plan.Periods {
		if period.PlanSeq == nil || *period.PlanSeq != i+1 {
			t.Errorf("period at %d has position %v, want %d: in the order they fall due", i, period.PlanSeq, i+1)
		}
	}
	wantLines(t, e.lines(plan.Origin), "instalment(bank) -12000 TWD", "expense(household) +12000 TWD, base +12000")
}

func TestGetInstalmentPlanOfAnIdThatIsNotThatOfAPlanIsNotFound(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	lunch := e.recordLunch("lunch-0000001a")
	unknown := uuid.Must(uuid.NewV7())

	for name, id := range map[string]uuid.UUID{
		"a transaction that is in no plan": lunch.ID,
		"a period":                         e.planOf(origin).Periods[0].ID,
		"an id that is nothing":            unknown,
	} {
		_, err := e.svc.GetInstalmentPlan(context.Background(), id)
		if !errors.Is(err, model.ErrNotFound) || !strings.Contains(err.Error(), id.String()) {
			t.Errorf("%s: err = %v, want ErrNotFound naming the id", name, err)
		}
	}
}

func TestListInstalmentPlansLeavesOutTheFinishedUnlessAskedAndReadsTodaysDay(t *testing.T) {
	e := newEnv(t)
	next := time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)
	ongoing := model.InstalmentPlanSummary{
		PlanID: uuid.Must(uuid.NewV7()), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
		Principal: dec("12000"), Charged: dec("1000"), NextDueOn: &next,
	}
	notStarted := model.InstalmentPlanSummary{
		PlanID: uuid.Must(uuid.NewV7()), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
		Principal: dec("3000"), Charged: dec("0"), NextDueOn: &next,
	}
	finished := model.InstalmentPlanSummary{
		PlanID: uuid.Must(uuid.NewV7()), AccountID: e.bank.ID, InstrumentID: e.twd.ID,
		Principal: dec("6000"), Charged: dec("6000"),
	}
	e.txns.planRows = []model.InstalmentPlanSummary{ongoing, notStarted, finished}

	out, err := e.svc.ListInstalmentPlans(context.Background(), usecase.ListInstalmentPlansInput{})
	if err != nil {
		t.Fatalf("ListInstalmentPlans: %v", err)
	}

	if len(out.Plans) != 2 || out.Plans[0].PlanID != ongoing.PlanID || out.Plans[1].PlanID != notStarted.PlanID {
		t.Fatalf("plans = %+v, want the two that have something left to charge, in the order of the repo", out.Plans)
	}
	got := out.Plans[0]
	if got.AccountID != e.bank.ID || got.InstrumentID != e.twd.ID || !got.Principal.Equal(dec("12000")) ||
		!got.Charged.Equal(dec("1000")) || !got.Remaining.Equal(dec("11000")) || got.NextDueOn == nil || !got.NextDueOn.Equal(next) {
		t.Errorf("plan = %+v, want 12000 split, 1000 charged, 11000 remaining (as a size, not negative) and the next day it is due", got)
	}
	if !e.txns.plansDay.Equal(day) || e.txns.plansFor != nil {
		t.Errorf("the repo was asked about %s for %v, want today, 2026-11-14, for every account", e.txns.plansDay.Format(time.DateOnly), e.txns.plansFor)
	}
	// What is returned is the caller's own.
	*got.NextDueOn = time.Time{}
	if next.IsZero() || e.txns.planRows[0].NextDueOn.IsZero() {
		t.Error("changing the day of a returned plan changed what the repo returned")
	}

	// Asked for with the finished ones, for an account, on another day.
	e.clock.now = time.Date(2027, 2, 1, 8, 0, 0, 0, taipei)
	out, err = e.svc.ListInstalmentPlans(context.Background(), usecase.ListInstalmentPlansInput{AccountID: &e.bank.ID, IncludeFinished: true})
	if err != nil {
		t.Fatalf("ListInstalmentPlans with the finished: %v", err)
	}
	if len(out.Plans) != 3 || out.Plans[2].PlanID != finished.PlanID {
		t.Fatalf("plans = %+v, want the three of them, the finished one too", out.Plans)
	}
	if last := out.Plans[2]; !last.Remaining.IsZero() || last.NextDueOn != nil {
		t.Errorf("finished plan = %+v, want nothing remaining and no next day", last)
	}
	if want := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC); !e.txns.plansDay.Equal(want) || e.txns.plansFor == nil || *e.txns.plansFor != e.bank.ID {
		t.Errorf("the repo was asked about %s for %v, want 2027-02-01 for the bank", e.txns.plansDay.Format(time.DateOnly), e.txns.plansFor)
	}

	boom := errors.New("connection reset")
	e.txns.readErr = boom
	if _, err := e.svc.ListInstalmentPlans(context.Background(), usecase.ListInstalmentPlansInput{}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure of the repo", err)
	}
}
