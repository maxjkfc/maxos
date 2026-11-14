package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// planOf makes the drafts of an expense of 12,000 TWD on food, charged to the card
// in 12 periods of 1,000 that fall due on the 5th of each month from December
// 2026, with 30 of interest on the first, made on the 14th of November.
func (e *ledgerEnv) planOf(key string) model.InstalmentPlan {
	e.t.Helper()
	periods := make([]model.InstalmentPeriod, 12)
	for i := range periods {
		periods[i] = model.InstalmentPeriod{
			ID: uuid.Must(uuid.NewV7()), DueOn: time.Date(2026, time.Month(12+i), 5, 0, 0, 0, 0, time.UTC), Principal: dec("1000"),
		}
	}
	periods[0].Interest = decPtr("30")
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: e.header(key, ledgerDay), Account: &e.card, Instrument: e.twd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("12000")}},
	}, model.InstalmentSchedule{Periods: periods, InterestCategory: &e.interest})
	if err != nil {
		e.t.Fatalf("NewInstalmentExpense: %v", err)
	}
	return plan
}

// billInstalmentOf makes the drafts of the conversion of 3,000 TWD that the bank
// owes into 3 periods of 1,000, which fall due on the 20th of November, December
// and January, converted on the 16th of November.
func (e *ledgerEnv) billInstalmentOf(key string) model.InstalmentPlan {
	e.t.Helper()
	periods := []model.InstalmentPeriod{
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.November, 20), Principal: dec("1000")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 20), Principal: dec("1000")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 20), Principal: dec("1000")},
	}
	plan, err := model.NewBillInstalment(model.BillInstalmentParams{
		Header: e.header(key, nov(16)), Account: e.bank, Instrument: e.twd, BaseCurrency: e.twd, Principal: dec("3000"),
		Schedule: model.InstalmentSchedule{Periods: periods},
	})
	if err != nil {
		e.t.Fatalf("NewBillInstalment: %v", err)
	}
	return plan
}

// on is a day, as a date at midnight UTC.
func on(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// storePlan stores the plan as a use case does: in one database transaction, the
// origin first, which the periods refer to.
func (e *ledgerEnv) storePlan(plan model.InstalmentPlan) model.InstalmentPlan {
	e.t.Helper()
	err := e.inTx(func(ctx context.Context) error {
		for _, txn := range plan.Transactions() {
			if _, err := e.txns.Insert(ctx, txn); err != nil {
				return fmt.Errorf("insert %s: %w", txn.IdempotencyKey, err)
			}
		}
		return nil
	})
	if err != nil {
		e.t.Fatalf("store the plan: %v", err)
	}
	return plan
}

// setPlanStatus moves every transaction of the plan to the status, in one database
// transaction, as the use cases that confirm and void do.
func (e *ledgerEnv) setPlanStatus(plan model.InstalmentPlan, status model.Status) {
	e.t.Helper()
	err := e.inTx(func(ctx context.Context) error {
		for _, txn := range plan.Transactions() {
			if _, err := e.txns.SetStatus(ctx, txn.ID, status); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		e.t.Fatalf("set the status of the plan to %s: %v", status, err)
	}
}

// confirmedPlan stores a plan and confirms it.
func (e *ledgerEnv) confirmedPlan(key string) model.InstalmentPlan {
	e.t.Helper()
	plan := e.storePlan(e.planOf(key))
	e.setPlanStatus(plan, model.StatusConfirmed)
	return plan
}

// summaries returns where each confirmed plan stands on the day, one to a line,
// for the account or for every account when it is nil.
func (e *ledgerEnv) summaries(accountID *uuid.UUID, day time.Time) []string {
	e.t.Helper()
	got, err := e.txns.InstalmentPlans(context.Background(), accountID, day)
	if err != nil {
		e.t.Fatalf("InstalmentPlans: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got))
	for i, s := range got {
		next := "-"
		if s.NextDueOn != nil {
			next = s.NextDueOn.Format(time.DateOnly)
		}
		lines[i] = fmt.Sprintf("%s %s principal %s charged %s remaining %s next %s",
			names[s.AccountID], names[s.InstrumentID], s.Principal, s.Charged, s.Remaining(), next)
	}
	return lines
}

func TestThePostingTableChecksWhatInstalmentPostingsNeed(t *testing.T) {
	e := newLedgerEnv(t)
	card, food, party := e.card.ID, e.food.ID, e.partyA
	unknown := uuid.Must(uuid.NewV7())
	twd := e.twd.ID

	instalment := model.Posting{Seq: 1, Book: model.BookInstalment, AccountID: &card, InstrumentID: twd, Quantity: dec("-1")}
	with := func(p model.Posting, change func(p *model.Posting)) model.Posting {
		change(&p)
		return p
	}
	tests := []struct {
		name       string
		posting    model.Posting
		sqlState   string
		constraint string
	}{
		{"an instalment posting without an account", with(instalment, func(p *model.Posting) { p.AccountID = nil }), checkViolation, "fin_posting_account_id_check"},
		{"an instalment posting with a category", with(instalment, func(p *model.Posting) { p.CategoryID = &food }), checkViolation, "fin_posting_category_id_check"},
		{"an instalment posting with a party", with(instalment, func(p *model.Posting) { p.PartyID = &party }), checkViolation, "fin_posting_party_id_check"},
		{"an instalment posting with a base amount", with(instalment, func(p *model.Posting) { p.BaseAmount = decPtr("-1") }), checkViolation, "fin_posting_base_amount_check"},
		{"an instalment posting with a discount", with(instalment, func(p *model.Posting) { p.Discount = decPtr("1") }), checkViolation, "fin_posting_discount_check"},
		{"an instalment posting that moves nothing", with(instalment, func(p *model.Posting) { p.Quantity = dec("0") }), checkViolation, "fin_posting_quantity_check"},
		{"an instalment posting of an account that does not exist", with(instalment, func(p *model.Posting) { p.AccountID = &unknown }), foreignKeyViolation, "fin_posting_account_id_fkey"},
		// The other books keep what they needed: the account of an account posting is
		// still required, and no other book has one.
		{"an account posting without an account", with(instalment, func(p *model.Posting) { p.Book, p.AccountID = model.BookAccount, nil }), checkViolation, "fin_posting_account_id_check"},
		{"an expense posting with an account", with(instalment, func(p *model.Posting) {
			p.Book, p.CategoryID, p.BaseAmount, p.Quantity = model.BookExpense, &food, decPtr("1"), dec("1")
		}), checkViolation, "fin_posting_account_id_check"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn := model.Transaction{
				ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft,
				OccurredOn: ledgerDay, IdempotencyKey: fmt.Sprintf("check-instalmt-%02d", i), Postings: []model.Posting{tt.posting},
			}

			// The literals skip the model on purpose: the table is the last guard.
			err := e.inTx(func(ctx context.Context) error {
				_, err := e.txns.Insert(ctx, txn)
				return err
			})
			wantConstraint(t, err, tt.sqlState, tt.constraint)
		})
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
		t.Errorf("%d transactions stored, want none: each rolled back with its posting", got)
	}
}

func TestTheInstalmentBookKeepsTheBalanceGuard(t *testing.T) {
	e := newLedgerEnv(t)
	card, food := e.card.ID, e.food.ID
	origin := func(key string, instalment, expense string) model.Transaction {
		id := uuid.Must(uuid.NewV7())
		return model.Transaction{
			ID: id, Kind: model.KindExpense, Status: model.StatusDraft, OccurredOn: ledgerDay, IdempotencyKey: key,
			PlanID: &id, PlanSeq: new(0),
			Postings: []model.Posting{
				{Seq: 1, Book: model.BookInstalment, AccountID: &card, InstrumentID: e.twd.ID, Quantity: dec(instalment)},
				{Seq: 2, Book: model.BookExpense, CategoryID: &food, InstrumentID: e.twd.ID, Quantity: dec(expense), BaseAmount: decPtr(expense)},
			},
		}
	}
	insert := func(txn model.Transaction) error {
		return e.inTx(func(ctx context.Context) error {
			_, err := e.txns.Insert(ctx, txn)
			return err
		})
	}

	wantConstraint(t, insert(origin("unbalanced-plan-1", "-12000", "11000")), checkViolation, "fin_transaction_balanced")
	if err := insert(origin("balanced-plan-001", "-12000", "12000")); err != nil {
		t.Fatalf("a balanced origin: %v", err)
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 1 {
		t.Errorf("%d transactions stored, want only the balanced one", got)
	}
}

func TestTheTransactionTableChecksThePlanColumns(t *testing.T) {
	e := newLedgerEnv(t)
	card := e.card.ID
	twd := e.twd.ID
	other := uuid.Must(uuid.NewV7())
	mine := uuid.Must(uuid.NewV7())

	// A transaction of the kind with the place in the plan, whose postings balance:
	// the table does not look at the postings against the kind, so the same two do.
	txn := func(key string, id uuid.UUID, kind model.Kind, plan *uuid.UUID, seq *int) model.Transaction {
		return model.Transaction{
			ID: id, Kind: kind, Status: model.StatusDraft, OccurredOn: ledgerDay, IdempotencyKey: key, PlanID: plan, PlanSeq: seq,
			Postings: []model.Posting{
				{Seq: 1, Book: model.BookInstalment, AccountID: &card, InstrumentID: twd, Quantity: dec("1")},
				{Seq: 2, Book: model.BookAccount, AccountID: &card, InstrumentID: twd, Quantity: dec("-1")},
			},
		}
	}
	tests := []struct {
		name       string
		txn        model.Transaction
		sqlState   string
		constraint string
	}{
		{"a plan without a position", txn("plan-check-0001", mine, model.KindExpense, &other, nil), checkViolation, "fin_transaction_plan_check"},
		{"a position without a plan", txn("plan-check-0002", mine, model.KindExpense, nil, new(1)), checkViolation, "fin_transaction_plan_check"},
		{"a position before the origin", txn("plan-check-0003", mine, model.KindInstalment, &other, new(-1)), checkViolation, "fin_transaction_plan_seq_check"},
		{"an origin that is not the transaction the plan names", txn("plan-check-0004", mine, model.KindInstalment, &other, new(0)), checkViolation, "fin_transaction_plan_origin_check"},
		{"a period that is the transaction the plan names", txn("plan-check-0005", mine, model.KindInstalment, &mine, new(1)), checkViolation, "fin_transaction_plan_origin_check"},
		{"an instalment that is in no plan", txn("plan-check-0006", mine, model.KindInstalment, nil, nil), checkViolation, "fin_transaction_plan_kind_check"},
		{"an income in a plan", txn("plan-check-0007", mine, model.KindIncome, &mine, new(0)), checkViolation, "fin_transaction_plan_kind_check"},
		{"a transfer in a plan", txn("plan-check-0008", mine, model.KindTransfer, &other, new(2)), checkViolation, "fin_transaction_plan_kind_check"},
		{"an expense that is a period", txn("plan-check-0009", mine, model.KindExpense, &other, new(1)), checkViolation, "fin_transaction_plan_kind_check"},
		{"a period of a plan that does not exist", txn("plan-check-0010", mine, model.KindInstalment, &other, new(1)), foreignKeyViolation, "fin_transaction_plan_id_fkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.inTx(func(ctx context.Context) error {
				_, err := e.txns.Insert(ctx, tt.txn)
				return err
			})
			wantConstraint(t, err, tt.sqlState, tt.constraint)
		})
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
		t.Fatalf("%d transactions stored, want none", got)
	}

	t.Run("two periods in the same place of a plan", func(t *testing.T) {
		originID := uuid.Must(uuid.NewV7())
		origin := txn("plan-check-0011", originID, model.KindExpense, &originID, new(0))
		first := txn("plan-check-0012", uuid.Must(uuid.NewV7()), model.KindInstalment, &originID, new(1))
		again := txn("plan-check-0013", uuid.Must(uuid.NewV7()), model.KindInstalment, &originID, new(1))

		err := e.inTx(func(ctx context.Context) error {
			for _, item := range []model.Transaction{origin, first, again} {
				if _, err := e.txns.Insert(ctx, item); err != nil {
					return err
				}
			}
			return nil
		})

		wantConstraint(t, err, uniqueViolation, "fin_transaction_plan_id_plan_seq_key")
		if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
			t.Errorf("%d transactions stored, want none: the origin and the first period are rolled back with the second", got)
		}
	})
}

func TestTheTransactionTableTakesTheKindsOfTradesAndInstalments(t *testing.T) {
	e := newLedgerEnv(t)

	trade := e.expense("kind-trade-00001", "120")
	trade.Kind = model.KindTrade
	err := e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, trade)
		return err
	})
	if err != nil {
		t.Errorf("kind %q: %v, want it stored: the table does not check the postings against the kind", model.KindTrade, err)
	}

	// An instalment is always in a plan, so the plan makes the first one.
	e.storePlan(e.billInstalmentOf("kind-instalment-1"))
	if got := e.count("SELECT count(*) FROM fin_transaction WHERE kind = 'instalment'"); got != 4 {
		t.Errorf("%d instalments stored, want the origin and the 3 periods of a bill instalment", got)
	}
}

func TestAPlanRoundTripsThroughTheTableAndGetPlanReadsItInOrder(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	plan := e.storePlan(e.planOf("plan-round-00001"))
	other := e.storePlan(e.planOf("plan-round-00002"))
	bill := e.storePlan(e.billInstalmentOf("plan-round-00003"))

	e.wantSame("origin read back", e.get(plan.Origin.ID), plan.Origin)
	for _, period := range plan.Periods {
		e.wantSame(fmt.Sprintf("period %d read back", *period.PlanSeq), e.get(period.ID), period)
	}
	if got, want := e.lines(e.get(plan.Origin.ID)), []string{"1 instalment(card) -12000 TWD", "2 expense(food) 12000 TWD base 12000"}; !slices.Equal(got, want) {
		t.Errorf("postings of the origin = %q, want %q", got, want)
	}
	if got, want := e.lines(e.get(plan.Periods[0].ID)),
		[]string{"1 instalment(card) 1000 TWD", "2 account(card) -1030 TWD", "3 expense(interest) 30 TWD base 30"}; !slices.Equal(got, want) {
		t.Errorf("postings of the first period = %q, want %q", got, want)
	}

	for name, want := range map[string]model.InstalmentPlan{"an expense paid in instalments": plan, "another plan": other, "a bill instalment": bill} {
		got, err := e.txns.GetPlan(ctx, want.Origin.ID)
		if err != nil {
			t.Fatalf("GetPlan of %s: %v", name, err)
		}
		e.wantSame(name+": the origin", got.Origin, want.Origin)
		if len(got.Periods) != len(want.Periods) {
			t.Fatalf("%s: %d periods, want %d", name, len(got.Periods), len(want.Periods))
		}
		for i, period := range got.Periods {
			// In the order they fall due, which is the order of the position.
			e.wantSame(fmt.Sprintf("%s: period %d", name, i+1), period, want.Periods[i])
		}
	}
}

func TestGetPlanOfAnIdThatIsNotThatOfAPlanIsNotFound(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	plan := e.storePlan(e.planOf("plan-notfound-01"))
	lunch := e.store(e.expense("lunch-0000000001", "120"))

	for name, id := range map[string]uuid.UUID{
		"a transaction that is in no plan": lunch.ID,
		"a period of a plan":               plan.Periods[0].ID,
		"an id nothing has":                uuid.Must(uuid.NewV7()),
	} {
		if _, err := e.txns.GetPlan(ctx, id); !errors.Is(err, model.ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
}

func TestStoringAPlanIsAllOrNothing(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.planOf("plan-atomic-00001")
	// A period in the place of the first, which the table refuses at the last insert.
	clash := plan.Periods[0]
	clash.ID, clash.IdempotencyKey = uuid.Must(uuid.NewV7()), "plan-atomic-clash"

	err := e.inTx(func(ctx context.Context) error {
		for _, txn := range append(plan.Transactions(), clash) {
			if _, err := e.txns.Insert(ctx, txn); err != nil {
				return err
			}
		}
		return nil
	})

	wantConstraint(t, err, uniqueViolation, "fin_transaction_plan_id_plan_seq_key")
	if transactions, postings := e.count("SELECT count(*) FROM fin_transaction"), e.count("SELECT count(*) FROM fin_posting"); transactions != 0 || postings != 0 {
		t.Errorf("%d transactions and %d postings left, want nothing of a plan that was not stored whole", transactions, postings)
	}
}

func TestAPlanIsDeletedPeriodsFirstAndTheTableRefusesTheOriginWhilePeriodsRemain(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.storePlan(e.planOf("plan-delete-00001"))
	other := e.storePlan(e.planOf("plan-delete-00002"))

	// Not the origin first: a period refers to it.
	err := e.inTx(func(ctx context.Context) error { return e.txns.Delete(ctx, plan.Origin.ID) })
	wantConstraint(t, err, foreignKeyViolation, "fin_transaction_plan_id_fkey")
	if got := e.count("SELECT count(*) FROM fin_transaction WHERE plan_id = $1", plan.Origin.ID); got != 13 {
		t.Fatalf("%d transactions of the plan left, want all 13 after the refused delete", got)
	}

	err = e.inTx(func(ctx context.Context) error {
		for _, period := range plan.Periods {
			if err := e.txns.Delete(ctx, period.ID); err != nil {
				return err
			}
		}
		return e.txns.Delete(ctx, plan.Origin.ID)
	})
	if err != nil {
		t.Fatalf("delete the periods and then the origin: %v", err)
	}
	if got := e.count("SELECT count(*) FROM fin_transaction WHERE plan_id = $1", plan.Origin.ID); got != 0 {
		t.Errorf("%d transactions of the plan left, want none", got)
	}
	if got := e.count("SELECT count(*) FROM fin_posting WHERE transaction_id = $1", plan.Periods[0].ID); got != 0 {
		t.Errorf("%d postings of a period left, want none: they go with it", got)
	}
	if got := e.count("SELECT count(*) FROM fin_transaction WHERE plan_id = $1", other.Origin.ID); got != 13 {
		t.Errorf("%d transactions of the other plan left, want its 13", got)
	}
}

// 12,000 on the card in 12 periods of 1,000 on the 5th of each month from December,
// with 30 of interest with the first: what each day sees of it.
func TestBalancesShowTheUnbilledInstalmentOfAPlanUntilEachPeriodFallsDue(t *testing.T) {
	e := newLedgerEnv(t)
	e.confirmedPlan("plan-balance-0001")

	tests := []struct {
		name string
		day  time.Time
		want []string
	}{
		{"the day before the purchase", on(2026, time.November, 13), nil},
		{"the day of the purchase: all of it unbilled, and the card has been charged nothing", on(2026, time.November, 14), []string{"card TWD 0 unbilled -12000"}},
		{"the day before the first period falls due", on(2026, time.December, 4), []string{"card TWD 0 unbilled -12000"}},
		{"the day the first period falls due, with its interest", on(2026, time.December, 5), []string{"card TWD -1030 unbilled -11000"}},
		{"a day between two periods", on(2026, time.December, 20), []string{"card TWD -1030 unbilled -11000"}},
		{"the day the second falls due", on(2027, time.January, 5), []string{"card TWD -2030 unbilled -10000"}},
		{"the day before the last", on(2027, time.November, 4), []string{"card TWD -11030 unbilled -1000"}},
		{"the day the last falls due: nothing left unbilled, and the balance is still listed", on(2027, time.November, 5), []string{"card TWD -12030 unbilled 0"}},
		{"long after the last", on(2035, time.January, 1), []string{"card TWD -12030 unbilled 0"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.balancesOn(nil, tt.day, true); !slices.Equal(got, tt.want) {
				t.Fatalf("balances on %s = %q, want %q", tt.day.Format(time.DateOnly), got, tt.want)
			}
			if got := e.balancesOn(&e.card.ID, tt.day, true); !slices.Equal(got, tt.want) {
				t.Errorf("balances of the card on %s = %q, want %q", tt.day.Format(time.DateOnly), got, tt.want)
			}
			if got := e.balancesOn(&e.bank.ID, tt.day, true); len(got) != 0 {
				t.Errorf("balances of the bank = %q, want none: the plan is the card's", got)
			}
		})
	}

	// Limited to an instrument, the balances of the others are not there.
	got, err := e.txns.Balances(context.Background(), repo.BalanceFilter{InstrumentID: &e.usd.ID, Day: forever})
	if err != nil || len(got) != 0 {
		t.Errorf("balances in USD = %v, %v, want none", got, err)
	}
	got, err = e.txns.Balances(context.Background(), repo.BalanceFilter{AccountID: &e.card.ID, InstrumentID: &e.twd.ID, Day: on(2026, time.December, 5)})
	if err != nil || len(got) != 1 || !got[0].Quantity.Equal(dec("-1030")) || !got[0].UnbilledInstalment.Equal(dec("-11000")) {
		t.Errorf("balance of the card in TWD = %v, %v, want -1030 held and -11000 unbilled", got, err)
	}
}

func TestADraftPlanAndAVoidPlanCountNowhere(t *testing.T) {
	e := newLedgerEnv(t)
	plan := e.storePlan(e.planOf("plan-nowhere-001"))
	lastDay := on(2030, time.January, 1)

	if got := e.balancesOn(nil, lastDay, true); len(got) != 0 {
		t.Fatalf("balances with only a draft plan = %q, want none", got)
	}
	if got := e.summaries(nil, lastDay); len(got) != 0 {
		t.Fatalf("plans with only a draft plan = %q, want none", got)
	}

	e.setPlanStatus(plan, model.StatusConfirmed)
	if got := e.balancesOn(nil, lastDay, true); !slices.Equal(got, []string{"card TWD -12030 unbilled 0"}) {
		t.Fatalf("balances once the plan is confirmed = %q, want the 12,030 charged", got)
	}
	if got := e.summaries(nil, lastDay); len(got) != 1 {
		t.Fatalf("plans once the plan is confirmed = %q, want the one plan", got)
	}

	// Voided after every period has fallen due: the periods that were charged stop
	// counting with it.
	e.setPlanStatus(plan, model.StatusVoid)
	if got := e.balancesOn(nil, lastDay, true); len(got) != 0 {
		t.Errorf("balances once the plan is void = %q, want none", got)
	}
	if got := e.balancesOn(nil, on(2026, time.December, 5), true); len(got) != 0 {
		t.Errorf("balances on the day of the first period once the plan is void = %q, want none", got)
	}
	if got := e.summaries(nil, lastDay); len(got) != 0 {
		t.Errorf("plans once the plan is void = %q, want none", got)
	}
}

func TestAConfirmedTransactionCountsFromItsDayOnAndNotBefore(t *testing.T) {
	e := newLedgerEnv(t)
	e.storeConfirmed(e.openingOn("opening-cash-01", nov(1), e.cash, e.twd, "1000"))
	e.storeConfirmed(e.expenseOf(e.header("future-spend-001", nov(20)), "120"))
	e.storeConfirmed(e.dealingOf("future-deal-0001", nov(20), e.partyA, e.twd, "5000", model.DealingPaidOut))

	if got := e.balancesOn(nil, nov(19), false); !slices.Equal(got, []string{"cash TWD 1000"}) {
		t.Errorf("balances on the 19th = %q, want only what had happened: the opening balance", got)
	}
	if got := e.balancesOn(nil, nov(20), false); !slices.Equal(got, []string{"bank TWD -5000", "cash TWD 880"}) {
		t.Errorf("balances on the 20th = %q, want the expense and the dealing counted from their day", got)
	}
	if got := e.balancesOn(&e.bank.ID, nov(19), false); len(got) != 0 {
		t.Errorf("balances of the bank on the 19th = %q, want none: nothing has happened to it", got)
	}

	if got := e.dealingsOn(nil, nov(19)); len(got) != 0 {
		t.Errorf("dealings on the 19th = %q, want none: the party owes nothing until the 20th", got)
	}
	if got := e.dealingsOn(nil, nov(20)); !slices.Equal(got, []string{"A TWD 5000"}) {
		t.Errorf("dealings on the 20th = %q, want what A owes from that day", got)
	}
	if got := e.dealingsOn(&e.partyA, nov(19)); len(got) != 0 {
		t.Errorf("dealings of A on the 19th = %q, want none", got)
	}
}

func TestInstalmentPlansAddUpWhatIsChargedByTheDay(t *testing.T) {
	e := newLedgerEnv(t)
	big := e.confirmedPlan("plan-summary-0001")
	bill := e.storePlan(e.billInstalmentOf("plan-summary-0002"))
	e.setPlanStatus(bill, model.StatusConfirmed)
	// What is not there: a plan that is a draft, and one that is void.
	e.storePlan(e.planOf("plan-summary-draft"))
	voided := e.confirmedPlan("plan-summary-void1")
	e.setPlanStatus(voided, model.StatusVoid)
	if big.Origin.ID == bill.Origin.ID {
		t.Fatal("the two plans are one")
	}

	tests := []struct {
		name string
		day  time.Time
		want []string
	}{
		// A plan is there from the day of its origin, the day of the purchase or of the
		// conversion, as it is in the balances. The newest is first: the bill
		// instalment of the 16th, then the purchase of the 14th.
		{"the day before the purchase, which is not made yet", on(2026, time.November, 13), nil},
		{"the day of the purchase, when the bill instalment is not made yet", on(2026, time.November, 14), []string{
			"card TWD principal 12000 charged 0 remaining 12000 next 2026-12-05",
		}},
		{"the day before the bill instalment is made", on(2026, time.November, 15), []string{
			"card TWD principal 12000 charged 0 remaining 12000 next 2026-12-05",
		}},
		{"the day the bill instalment is made, before any period of it has fallen due", on(2026, time.November, 16), []string{
			"bank TWD principal 3000 charged 0 remaining 3000 next 2026-11-20",
			"card TWD principal 12000 charged 0 remaining 12000 next 2026-12-05",
		}},
		{"the day its first period falls due, which is charged by then", on(2026, time.November, 20), []string{
			"bank TWD principal 3000 charged 1000 remaining 2000 next 2026-12-20",
			"card TWD principal 12000 charged 0 remaining 12000 next 2026-12-05",
		}},
		{"the day before the first of the purchase", on(2026, time.December, 4), []string{
			"bank TWD principal 3000 charged 1000 remaining 2000 next 2026-12-20",
			"card TWD principal 12000 charged 0 remaining 12000 next 2026-12-05",
		}},
		{"the day the first of the purchase falls due: its 30 of interest is not principal", on(2026, time.December, 5), []string{
			"bank TWD principal 3000 charged 1000 remaining 2000 next 2026-12-20",
			"card TWD principal 12000 charged 1000 remaining 11000 next 2027-01-05",
		}},
		{"the day the bill instalment is finished", on(2027, time.January, 20), []string{
			"bank TWD principal 3000 charged 3000 remaining 0 next -",
			"card TWD principal 12000 charged 2000 remaining 10000 next 2027-02-05",
		}},
		{"after both are finished", on(2030, time.January, 1), []string{
			"bank TWD principal 3000 charged 3000 remaining 0 next -",
			"card TWD principal 12000 charged 12000 remaining 0 next -",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := e.summaries(nil, tt.day); !slices.Equal(got, tt.want) {
				t.Fatalf("plans on %s =\n  %q\nwant\n  %q", tt.day.Format(time.DateOnly), got, tt.want)
			}
		})
	}

	day := on(2026, time.December, 5)
	if got, want := e.summaries(&e.bank.ID, day), []string{"bank TWD principal 3000 charged 1000 remaining 2000 next 2026-12-20"}; !slices.Equal(got, want) {
		t.Errorf("plans of the bank = %q, want %q", got, want)
	}
	if got, want := e.summaries(&e.card.ID, day), []string{"card TWD principal 12000 charged 1000 remaining 11000 next 2027-01-05"}; !slices.Equal(got, want) {
		t.Errorf("plans of the card = %q, want %q", got, want)
	}
	if got := e.summaries(&e.cash.ID, day); len(got) != 0 {
		t.Errorf("plans of cash = %q, want none", got)
	}

	// What the plans say is what the balances say: what a plan has remaining, as a
	// size, is the unbilled instalment of the balance of its account, with the sign
	// of what is owed.
	if got := e.balancesOn(nil, day, true); !slices.Equal(got, []string{"bank TWD 2000 unbilled -2000", "card TWD -1030 unbilled -11000"}) {
		t.Errorf("balances on the day = %q, want the same figures as the plans, with the sign of what is owed", got)
	}
}

// The plans and the balances count from the same day: on every day, what the
// plans of an account have remaining is, with the opposite sign, the unbilled
// instalment of the balance of the account. A plan whose origin is dated after the
// day is in neither.
func TestInstalmentPlansAndBalancesTellTheSameUnbilledInstalmentOnEveryDay(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	e.confirmedPlan("plan-agree-000001")
	bill := e.storePlan(e.billInstalmentOf("plan-agree-000002"))
	e.setPlanStatus(bill, model.StatusConfirmed)

	for day := on(2026, time.November, 10); !day.After(on(2027, time.November, 6)); day = day.AddDate(0, 0, 1) {
		plans, err := e.txns.InstalmentPlans(ctx, nil, day)
		if err != nil {
			t.Fatalf("InstalmentPlans on %s: %v", day.Format(time.DateOnly), err)
		}
		remaining := map[uuid.UUID]decimal.Decimal{}
		for _, plan := range plans {
			if r := plan.Remaining(); !r.IsZero() {
				remaining[plan.AccountID] = remaining[plan.AccountID].Add(r)
			}
		}

		balances, err := e.txns.Balances(ctx, repo.BalanceFilter{Day: day})
		if err != nil {
			t.Fatalf("Balances on %s: %v", day.Format(time.DateOnly), err)
		}
		unbilled := map[uuid.UUID]decimal.Decimal{}
		for _, balance := range balances {
			if !balance.UnbilledInstalment.IsZero() {
				unbilled[balance.AccountID] = balance.UnbilledInstalment.Neg()
			}
		}

		if len(remaining) != len(unbilled) {
			t.Fatalf("on %s the plans have %v remaining and the balances %v unbilled, want the same accounts",
				day.Format(time.DateOnly), remaining, unbilled)
		}
		for account, want := range unbilled {
			if got, ok := remaining[account]; !ok || !got.Equal(want) {
				t.Fatalf("on %s the plans of %s have %s remaining, and its balance has %s unbilled, want them the same",
					day.Format(time.DateOnly), e.names()[account], got, want)
			}
		}
	}
}

func TestListFiltersByThePlan(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	plan := e.storePlan(e.planOf("plan-list-00000001"))
	other := e.storePlan(e.billInstalmentOf("plan-list-00000002"))
	e.store(e.expense("plan-list-lunch-01", "120"))

	got, err := e.txns.List(ctx, repo.TransactionFilter{PlanID: &plan.Origin.ID, Limit: 100})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	var gotIDs, wantIDs []uuid.UUID
	for _, txn := range got {
		gotIDs = append(gotIDs, txn.ID)
	}
	for _, txn := range plan.Transactions() {
		wantIDs = append(wantIDs, txn.ID)
	}
	byBytes := func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) }
	slices.SortFunc(gotIDs, byBytes)
	slices.SortFunc(wantIDs, byBytes)
	if !slices.Equal(gotIDs, wantIDs) {
		t.Errorf("listed %d transactions, want the 13 of the plan and nothing of the other plan or the lunch", len(gotIDs))
	}
	for _, txn := range got {
		if txn.PlanID == nil || *txn.PlanID != plan.Origin.ID || txn.PlanSeq == nil {
			t.Errorf("listed %s with plan %v, position %v, want the place in the plan read back", txn.ID, txn.PlanID, txn.PlanSeq)
		}
	}
	if got, err := e.txns.List(ctx, repo.TransactionFilter{PlanID: &other.Origin.ID, Status: model.StatusConfirmed, Limit: 100}); err != nil || len(got) != 0 {
		t.Errorf("the confirmed transactions of a plan that is all drafts = %d, %v, want none", len(got), err)
	}
}

// What a plan has not charged to an account yet is not what the account holds, so
// the drafts of a plan are in the way of a reconciliation only from the day of
// their period.
func TestPendingDraftsLeavesOutWhatADraftPlanHasNotChargedToTheAccountByTheDay(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	e.storePlan(e.planOf("plan-pending-0001"))

	tests := []struct {
		name string
		day  time.Time
		want int
	}{
		{"the day of the purchase, whose origin has an instalment posting only", on(2026, time.November, 14), 0},
		{"the day before the first period", on(2026, time.December, 4), 0},
		{"the day the first falls due", on(2026, time.December, 5), 1},
		{"the day the second does", on(2027, time.January, 5), 2},
		{"after the last", on(2030, time.January, 1), 12},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.txns.PendingDrafts(ctx, e.card.ID, e.twd.ID, tt.day)
			if err != nil {
				t.Fatalf("PendingDrafts: %v", err)
			}
			if got != tt.want {
				t.Fatalf("pending drafts on %s = %d, want %d", tt.day.Format(time.DateOnly), got, tt.want)
			}
		})
	}
}

func TestATradeMovesBothInstrumentsAndKeepsTheBalanceGuard(t *testing.T) {
	e := newLedgerEnv(t)
	e.storeConfirmed(e.openingOn("opening-bank-001", nov(1), e.bank, e.twd, "500000"))
	trade := func(key string, day time.Time, direction model.TradeDirection, quantity, amount string) model.Transaction {
		txn, err := model.NewTrade(model.TradeParams{
			Header: e.header(key, day), Direction: direction,
			Account: e.cash, Instrument: e.btc, Quantity: dec(quantity),
			CashAccount: e.bank, Currency: e.twd, Amount: dec(amount), BaseCurrency: e.twd,
		})
		if err != nil {
			e.t.Fatalf("NewTrade: %v", err)
		}
		return txn
	}

	buy := e.store(trade("trade-buy-0000001", nov(10), model.TradeBuy, "0.1", "300000"))
	e.wantSame("trade read back", e.get(buy.ID), buy)
	e.wantBalances(nil, "bank TWD 500000")

	e.setStatus(buy.ID, model.StatusConfirmed)
	e.wantBalances(nil, "bank TWD 200000", "cash BTC 0.1")

	e.storeConfirmed(trade("trade-sell-000001", nov(12), model.TradeSell, "0.04", "130000"))
	e.wantBalances(nil, "bank TWD 330000", "cash BTC 0.06")
	if got := e.balancesOn(nil, nov(11), false); !slices.Equal(got, []string{"bank TWD 200000", "cash BTC 0.1"}) {
		t.Errorf("balances between the two = %q, want the sale not counted before its day", got)
	}

	// The balance guard holds for it: the two instruments each add up to zero.
	unbalanced := trade("trade-bad-00000001", nov(13), model.TradeBuy, "0.1", "300000")
	unbalanced.Postings[3].Quantity = dec("0.2")
	err := e.inTx(func(ctx context.Context) error {
		_, err := e.txns.Insert(ctx, unbalanced)
		return err
	})
	wantConstraint(t, err, checkViolation, "fin_transaction_balanced")
}

// reschedule does what a use case does to put a new schedule in the place of the
// old one, in one database transaction: delete the periods, store the new ones as
// drafts, and confirm them when the plan is confirmed.
func (e *ledgerEnv) reschedule(old, next model.InstalmentPlan, confirm bool) error {
	return e.inTx(func(ctx context.Context) error {
		for _, period := range old.Periods {
			if err := e.txns.Delete(ctx, period.ID); err != nil {
				return fmt.Errorf("delete period %s: %w", period.ID, err)
			}
		}
		for _, period := range next.Periods {
			if _, err := e.txns.Insert(ctx, period); err != nil {
				return fmt.Errorf("insert period %s: %w", period.ID, err)
			}
			if confirm {
				if _, err := e.txns.SetStatus(ctx, period.ID, model.StatusConfirmed); err != nil {
					return fmt.Errorf("confirm period %s: %w", period.ID, err)
				}
			}
		}
		return nil
	})
}

// newSchedule is four periods of 3,000 that fall due on the 5th of each month from
// December 2026, with 90 of interest on the third, which split what planOf does.
func (e *ledgerEnv) newSchedule() model.InstalmentSchedule {
	periods := make([]model.InstalmentPeriod, 4)
	for i := range periods {
		periods[i] = model.InstalmentPeriod{
			ID: uuid.Must(uuid.NewV7()), DueOn: time.Date(2026, time.Month(12+i), 5, 0, 0, 0, 0, time.UTC), Principal: dec("3000"),
		}
	}
	periods[2].Interest = decPtr("90")
	return model.InstalmentSchedule{Periods: periods, InterestCategory: &e.interest}
}

func TestAScheduleIsReplacedInOneDatabaseTransactionWhateverTheStatusOfThePlan(t *testing.T) {
	for _, status := range []model.Status{model.StatusDraft, model.StatusConfirmed} {
		t.Run(string(status), func(t *testing.T) {
			e := newLedgerEnv(t)
			ctx := context.Background()
			plan := e.storePlan(e.planOf("plan-resched-0001"))
			other := e.storePlan(e.planOf("plan-resched-0002"))
			if status == model.StatusConfirmed {
				e.setPlanStatus(plan, model.StatusConfirmed)
			}
			old, err := e.txns.GetPlan(ctx, plan.Origin.ID)
			if err != nil {
				t.Fatalf("GetPlan: %v", err)
			}
			schedule := e.newSchedule()
			next, err := old.Reschedule(e.twd, e.twd, schedule)
			if err != nil {
				t.Fatalf("Reschedule: %v", err)
			}

			if err := e.reschedule(old, next, status == model.StatusConfirmed); err != nil {
				t.Fatalf("replace the schedule: %v", err)
			}

			// The old periods are gone, and what is stored is the origin as it was and the
			// new periods in the order they fall due, of the status of the plan.
			for _, period := range old.Periods {
				if _, err := e.txns.Get(ctx, period.ID); !errors.Is(err, model.ErrNotFound) {
					t.Errorf("old period %s: err = %v, want it gone", period.ID, err)
				}
			}
			got, err := e.txns.GetPlan(ctx, plan.Origin.ID)
			if err != nil {
				t.Fatalf("GetPlan: %v", err)
			}
			e.wantSame("origin", got.Origin, old.Origin)
			if len(got.Periods) != 4 {
				t.Fatalf("%d periods stored, want the 4 of the new schedule", len(got.Periods))
			}
			for i, period := range got.Periods {
				if period.ID != schedule.Periods[i].ID || period.Status != status || (status == model.StatusConfirmed) != (period.ConfirmedAt != nil) || period.VoidedAt != nil {
					t.Errorf("period %d = %s %s confirmed at %v, want the new period %s as %s", i+1, period.ID, period.Status, period.ConfirmedAt, schedule.Periods[i].ID, status)
				}
				want := next.Periods[i]
				want.Status = status
				e.wantSame(fmt.Sprintf("period %d", i+1), period, want)
			}
			// Nothing of the other plan was touched.
			if got := e.count("SELECT count(*) FROM fin_transaction WHERE plan_id = $1", other.Origin.ID); got != 13 {
				t.Errorf("%d transactions of the other plan, want its 13", got)
			}

			// A confirmed plan counts the new schedule from the day of each period, and
			// a draft one counts nowhere.
			if status == model.StatusDraft {
				if got := e.balancesOn(&e.card.ID, forever, true); len(got) != 0 {
					t.Errorf("balances of a draft plan = %q, want none", got)
				}
				return
			}
			for _, tt := range []struct {
				day  time.Time
				want string
			}{
				{on(2026, time.December, 4), "card TWD 0 unbilled -12000"},
				{on(2026, time.December, 5), "card TWD -3000 unbilled -9000"},
				{on(2027, time.February, 5), "card TWD -9090 unbilled -3000"},
				{on(2027, time.March, 5), "card TWD -12090 unbilled 0"},
			} {
				if got := e.balancesOn(&e.card.ID, tt.day, true); len(got) != 1 || got[0] != tt.want {
					t.Errorf("balance of the card on %s = %q, want %q", tt.day.Format(time.DateOnly), got, tt.want)
				}
			}
		})
	}
}

// The balance trigger is what holds it all together: a period that does not add up
// takes the whole replacement with it, and the old schedule is there as it was.
func TestAScheduleWhosePeriodDoesNotBalanceLeavesTheOldScheduleWhole(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	plan := e.storePlan(e.planOf("plan-resched-0003"))
	e.setPlanStatus(plan, model.StatusConfirmed)
	old, err := e.txns.GetPlan(ctx, plan.Origin.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	next, err := old.Reschedule(e.twd, e.twd, e.newSchedule())
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	next.Periods[1].Postings[1].Quantity = dec("-1")

	err = e.reschedule(old, next, true)

	wantConstraint(t, err, checkViolation, "fin_transaction_balanced")
	got, err := e.txns.GetPlan(ctx, plan.Origin.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if len(got.Periods) != 12 {
		t.Fatalf("%d periods stored, want the 12 of the old schedule: the replacement is rolled back whole", len(got.Periods))
	}
	for i, period := range got.Periods {
		e.wantSame(fmt.Sprintf("period %d", i+1), period, old.Periods[i])
	}
}
