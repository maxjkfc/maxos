package postgres_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// nov is a day of November 2026.
func nov(day int) time.Time {
	return time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC)
}

// dealingOf makes the draft of a dealing with a party, through the bank, in the
// instrument.
func (e *ledgerEnv) dealingOf(key string, day time.Time, party uuid.UUID, instrument model.Instrument, amount string, direction model.DealingDirection) model.Transaction {
	e.t.Helper()
	txn, err := model.NewDealing(model.DealingParams{
		Header: e.header(key, day), Account: e.bank, PartyID: party, Instrument: instrument,
		Amount: dec(amount), Direction: direction,
	})
	if err != nil {
		e.t.Fatalf("NewDealing: %v", err)
	}
	return txn
}

// openingOn makes the draft of the balance an account holds of an instrument from
// the day.
func (e *ledgerEnv) openingOn(key string, day time.Time, account model.Account, instrument model.Instrument, amount string) model.Transaction {
	e.t.Helper()
	txn, err := model.NewOpeningBalance(model.OpeningBalanceParams{
		Header: e.header(key, day), Account: account, Instrument: instrument, Amount: dec(amount),
	})
	if err != nil {
		e.t.Fatalf("NewOpeningBalance: %v", err)
	}
	return txn
}

// splitOf makes the draft of an expense of 300 TWD on food, paid from cash, for
// which the party owes 200 more.
func (e *ledgerEnv) splitOf(key string, day time.Time, party uuid.UUID) model.Transaction {
	e.t.Helper()
	txn, err := model.NewExpense(model.ExpenseParams{
		Header: e.header(key, day), Account: &e.cash, Instrument: e.twd,
		Lines:  []model.CategoryLine{{Category: e.food, Amount: dec("300")}},
		Shares: []model.Share{{PartyID: party, Amount: dec("200")}},
	})
	if err != nil {
		e.t.Fatalf("NewExpense: %v", err)
	}
	return txn
}

// dealings returns what Dealings returns on a day after all that is stored, one to a line.
func (e *ledgerEnv) dealings(partyID *uuid.UUID) []string {
	e.t.Helper()
	return e.dealingsOn(partyID, forever)
}

// dealingsOn returns what Dealings returns on the day, one to a line.
func (e *ledgerEnv) dealingsOn(partyID *uuid.UUID, day time.Time) []string {
	e.t.Helper()
	got, err := e.txns.Dealings(context.Background(), partyID, day)
	if err != nil {
		e.t.Fatalf("Dealings: %v", err)
	}
	names := e.names()
	lines := make([]string, len(got))
	for i, d := range got {
		lines[i] = fmt.Sprintf("%s %s %s", names[d.PartyID], names[d.InstrumentID], d.Quantity)
	}
	return lines
}

// byPartyID returns the lines of each party, the parties in the order of their
// ids: the order Dealings lists them in.
func byPartyID(perParty map[uuid.UUID][]string) []string {
	parties := make([]uuid.UUID, 0, len(perParty))
	for party := range perParty {
		parties = append(parties, party)
	}
	slices.SortFunc(parties, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	var lines []string
	for _, party := range parties {
		lines = append(lines, perParty[party]...)
	}
	return lines
}

func TestThePostingTableChecksWhatDealingsRewardsAndAdjustmentsNeed(t *testing.T) {
	e := newLedgerEnv(t)
	cash, food, salary, party := e.cash.ID, e.food.ID, e.salary.ID, e.partyA
	twd := e.twd.ID

	account := model.Posting{Seq: 1, Book: model.BookAccount, AccountID: &cash, InstrumentID: twd, Quantity: dec("1")}
	expense := model.Posting{Seq: 1, Book: model.BookExpense, CategoryID: &food, InstrumentID: twd, Quantity: dec("1"), BaseAmount: decPtr("1")}
	income := model.Posting{Seq: 1, Book: model.BookIncome, CategoryID: &salary, InstrumentID: twd, Quantity: dec("-1"), BaseAmount: decPtr("-1")}
	dealing := model.Posting{Seq: 1, Book: model.BookDealing, PartyID: &party, InstrumentID: twd, Quantity: dec("1")}
	reward := model.Posting{Seq: 1, Book: model.BookReward, InstrumentID: twd, Quantity: dec("-1"), BaseAmount: decPtr("-1")}
	adjustment := model.Posting{Seq: 1, Book: model.BookAdjustment, InstrumentID: twd, Quantity: dec("-1")}
	with := func(p model.Posting, change func(p *model.Posting)) model.Posting {
		change(&p)
		return p
	}

	tests := []struct {
		name       string
		posting    model.Posting
		constraint string
	}{
		{"a dealing posting without a party", with(dealing, func(p *model.Posting) { p.PartyID = nil }), "fin_posting_party_id_check"},
		{"an account posting with a party", with(account, func(p *model.Posting) { p.PartyID = &party }), "fin_posting_party_id_check"},
		{"an expense posting with a party", with(expense, func(p *model.Posting) { p.PartyID = &party }), "fin_posting_party_id_check"},
		{"a reward posting with a party", with(reward, func(p *model.Posting) { p.PartyID = &party }), "fin_posting_party_id_check"},
		{"a dealing posting with an account", with(dealing, func(p *model.Posting) { p.AccountID = &cash }), "fin_posting_account_id_check"},
		{"a dealing posting with a category", with(dealing, func(p *model.Posting) { p.CategoryID = &food }), "fin_posting_category_id_check"},
		{"a dealing posting with a base amount", with(dealing, func(p *model.Posting) { p.BaseAmount = decPtr("1") }), "fin_posting_base_amount_check"},
		{"a reward posting without a base amount", with(reward, func(p *model.Posting) { p.BaseAmount = nil }), "fin_posting_base_amount_check"},
		{"a reward posting with a category", with(reward, func(p *model.Posting) { p.CategoryID = &salary }), "fin_posting_category_id_check"},
		{"a reward posting with an account", with(reward, func(p *model.Posting) { p.AccountID = &cash }), "fin_posting_account_id_check"},
		{"a reward posting with a base amount of the other sign", with(reward, func(p *model.Posting) { p.BaseAmount = decPtr("1") }), "fin_posting_base_amount_sign_check"},
		{"an adjustment posting with a base amount", with(adjustment, func(p *model.Posting) { p.BaseAmount = decPtr("-1") }), "fin_posting_base_amount_check"},
		{"an adjustment posting with a category", with(adjustment, func(p *model.Posting) { p.CategoryID = &food }), "fin_posting_category_id_check"},
		{"an adjustment posting with a party", with(adjustment, func(p *model.Posting) { p.PartyID = &party }), "fin_posting_party_id_check"},
		{"a discount on an income posting", with(income, func(p *model.Posting) { p.Discount = decPtr("1") }), "fin_posting_discount_check"},
		{"a discount on an account posting", with(account, func(p *model.Posting) { p.Discount = decPtr("1") }), "fin_posting_discount_check"},
		{"a discount on a dealing posting", with(dealing, func(p *model.Posting) { p.Discount = decPtr("1") }), "fin_posting_discount_check"},
		{"a discount of zero on an expense posting", with(expense, func(p *model.Posting) { p.Discount = decPtr("0") }), "fin_posting_discount_check"},
		{"a negative discount on an expense posting", with(expense, func(p *model.Posting) { p.Discount = decPtr("-5") }), "fin_posting_discount_check"},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			txn := model.Transaction{
				ID: uuid.Must(uuid.NewV7()), Kind: model.KindExpense, Status: model.StatusDraft,
				OccurredOn: ledgerDay, IdempotencyKey: fmt.Sprintf("check-m1b-post-%02d", i), Postings: []model.Posting{tt.posting},
			}

			// The literals skip the model on purpose: the table is the last guard.
			err := e.inTx(func(ctx context.Context) error {
				_, err := e.txns.Insert(ctx, txn)
				return err
			})
			wantConstraint(t, err, checkViolation, tt.constraint)
		})
	}
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 0 {
		t.Errorf("%d transactions stored, want none: each rolled back with its posting", got)
	}
}

func TestTheTransactionTableTakesTheNewKinds(t *testing.T) {
	e := newLedgerEnv(t)

	for _, kind := range []model.Kind{model.KindReward, model.KindDealing, model.KindReconciliation} {
		txn := e.expense("kind-"+string(kind)+"-01", "120")
		txn.Kind = kind
		err := e.inTx(func(ctx context.Context) error {
			_, err := e.txns.Insert(ctx, txn)
			return err
		})
		if err != nil {
			t.Errorf("kind %q: %v, want it stored: the table does not check the postings against the kind", kind, err)
		}
	}
}

func TestTheNewBooksKeepTheBalanceGuard(t *testing.T) {
	e := newLedgerEnv(t)
	cash, party := e.cash.ID, e.partyA
	twd, usd := e.twd.ID, e.usd.ID
	const guard = "fin_transaction_balanced"

	account := func(instrument uuid.UUID, quantity string) model.Posting {
		return model.Posting{Seq: 1, Book: model.BookAccount, AccountID: &cash, InstrumentID: instrument, Quantity: dec(quantity)}
	}
	dealing := func(instrument uuid.UUID, quantity string) model.Posting {
		return model.Posting{Seq: 2, Book: model.BookDealing, PartyID: &party, InstrumentID: instrument, Quantity: dec(quantity)}
	}
	reward := func(quantity string) model.Posting {
		return model.Posting{Seq: 2, Book: model.BookReward, InstrumentID: twd, Quantity: dec(quantity), BaseAmount: decPtr(quantity)}
	}
	adjustment := func(quantity string) model.Posting {
		return model.Posting{Seq: 2, Book: model.BookAdjustment, InstrumentID: twd, Quantity: dec(quantity)}
	}
	store := func(key string, kind model.Kind, postings ...model.Posting) error {
		return e.inTx(func(ctx context.Context) error {
			_, err := e.txns.Insert(ctx, model.Transaction{
				ID: uuid.Must(uuid.NewV7()), Kind: kind, Status: model.StatusDraft,
				OccurredOn: ledgerDay, IdempotencyKey: key, Postings: postings,
			})
			return err
		})
	}

	t.Run("each balanced", func(t *testing.T) {
		for _, tt := range []struct {
			key      string
			kind     model.Kind
			postings []model.Posting
		}{
			{"balanced-deal-1", model.KindDealing, []model.Posting{account(twd, "-5000"), dealing(twd, "5000")}},
			{"balanced-deal-2", model.KindDealing, []model.Posting{account(usd, "10"), dealing(usd, "-10")}},
			{"balanced-reward", model.KindReward, []model.Posting{account(twd, "50"), reward("-50")}},
			{"balanced-adjust", model.KindReconciliation, []model.Posting{account(twd, "1.5"), adjustment("-1.5")}},
		} {
			if err := store(tt.key, tt.kind, tt.postings...); err != nil {
				t.Errorf("%s: %v, want a balanced transaction stored", tt.key, err)
			}
		}
	})
	t.Run("a dealing that does not add up", func(t *testing.T) {
		wantConstraint(t, store("unbalanced-deal", model.KindDealing, account(twd, "-5000"), dealing(twd, "4000")), checkViolation, guard)
	})
	t.Run("a dealing in another instrument than the account, which each instrument must balance in", func(t *testing.T) {
		wantConstraint(t, store("unbalanced-inst", model.KindDealing, account(twd, "-5000"), dealing(usd, "5000")), checkViolation, guard)
	})
	t.Run("a reward that does not add up", func(t *testing.T) {
		wantConstraint(t, store("unbalanced-rewrd", model.KindReward, account(twd, "50"), reward("-40")), checkViolation, guard)
	})
	t.Run("an adjustment that does not add up", func(t *testing.T) {
		wantConstraint(t, store("unbalanced-adjst", model.KindReconciliation, account(twd, "1.5"), adjustment("-1")), checkViolation, guard)
	})
	t.Run("a dealing with nothing against it", func(t *testing.T) {
		wantConstraint(t, store("unbalanced-alone", model.KindDealing, dealing(twd, "5000")), checkViolation, guard)
	})
	if got := e.count("SELECT count(*) FROM fin_transaction"); got != 4 {
		t.Errorf("%d transactions stored, want the 4 balanced ones only", got)
	}
}

func TestEveryNewShapeOfATransactionRoundTripsThroughTheTable(t *testing.T) {
	e := newLedgerEnv(t)
	feeOf := func(amount string) model.Fee {
		return model.Fee{Account: e.cash, Instrument: e.twd, Category: e.fees, Amount: dec(amount), Memo: "跨行"}
	}

	split, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("round-split-001", ledgerDay), Account: &e.cash, Instrument: e.twd,
		Lines:  []model.CategoryLine{{Category: e.food, Amount: dec("260"), Discount: decPtr("40"), Memo: "晚餐"}},
		Shares: []model.Share{{PartyID: e.partyA, Amount: dec("300"), Memo: "A 的份"}, {PartyID: e.partyB, Amount: dec("300")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	paidBy, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("round-paidby-01", ledgerDay), PaidBy: &e.partyA, Instrument: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("300")}},
	})
	if err != nil {
		t.Fatalf("NewExpense paid by a party: %v", err)
	}
	transfer, err := model.NewTransfer(model.TransferParams{
		Header: e.header("round-fee-00001", ledgerDay), From: e.bank, To: e.cash, Instrument: e.twd, Amount: dec("1000"),
		Fees: []model.Fee{feeOf("15")},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	reward, err := model.NewReward(model.RewardParams{
		Header: e.header("round-reward-01", ledgerDay), Account: e.cash, Instrument: e.usd, BaseCurrency: e.twd,
		Amount: dec("5"), BaseAmount: decPtr("160"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}
	reconcile := func(key string, instrument model.Instrument, recorded, actual string) model.Transaction {
		got, err := model.Reconcile(model.ReconcileParams{
			Header: e.header(key, ledgerDay), Account: e.cash, Instrument: instrument, BaseCurrency: e.twd,
			RecordedBalance: dec(recorded), ActualBalance: dec(actual),
		})
		if err != nil || got.Transaction == nil {
			t.Fatalf("Reconcile %q: %+v, %v", key, got, err)
		}
		return *got.Transaction
	}

	for name, txn := range map[string]model.Transaction{
		"a split expense with a discount": split,
		"an expense a party paid":         paidBy,
		"a transfer with a fee":           transfer,
		"a dealing that pays out":         e.dealingOf("round-dealing-01", ledgerDay, e.partyA, e.twd, "5000", model.DealingPaidOut),
		"a dealing that receives":         e.dealingOf("round-dealing-02", ledgerDay, e.partyB, e.usd, "12.5", model.DealingReceived),
		"a reward":                        reward,
		"a reconciliation, short":         reconcile("round-recon-0001", e.twd, "1000", "880"),
		"a reconciliation, over":          reconcile("round-recon-0002", e.twd, "1000", "1080"),
		"a reconciliation of a quantity":  reconcile("round-recon-0003", e.btc, "1", "2.5"),
	} {
		stored := e.store(txn)
		e.wantSame(name+" as stored", stored, txn)
		e.wantSame(name+" read back", e.get(txn.ID), txn)
	}

	// What the table gives back is what went in, down to the discount and the party.
	got := e.lines(e.get(split.ID))
	want := []string{
		"1 account(cash) -860 TWD",
		`2 expense(food) 260 TWD base 260 discount 40 memo "晚餐"`,
		`3 dealing(A) 300 TWD memo "A 的份"`,
		"4 dealing(B) 300 TWD",
	}
	if !slices.Equal(got, want) {
		t.Errorf("postings of the split = %q, want %q", got, want)
	}
	// The reconciliations booked to the system categories, whose ids the migration
	// seeds: the foreign key of the posting would refuse an id it does not know.
	if got := e.count("SELECT count(*) FROM fin_posting WHERE category_id = $1", model.UnrecordedExpenseCategoryID); got != 1 {
		t.Errorf("%d postings to the unrecorded expense category, want 1", got)
	}
	if got := e.count("SELECT count(*) FROM fin_posting WHERE category_id = $1", model.UnrecordedIncomeCategoryID); got != 1 {
		t.Errorf("%d postings to the unrecorded income category, want 1", got)
	}
}

func TestDealingsCountConfirmedTransactionsOnlyAndNetPaidOutAgainstReceived(t *testing.T) {
	e := newLedgerEnv(t)

	// A paid out 5,000 and received 2,000 back: A owes 3,000. A third, of 100, is
	// still a draft.
	e.storeConfirmed(e.dealingOf("dealing-0000001", nov(10), e.partyA, e.twd, "5000", model.DealingPaidOut))
	e.storeConfirmed(e.dealingOf("dealing-0000002", nov(11), e.partyA, e.twd, "2000", model.DealingReceived))
	e.store(e.dealingOf("dealing-0000003", nov(12), e.partyA, e.twd, "100", model.DealingPaidOut))
	// In USD, A got 10 and gave 10 back: nothing owed, and it is still listed.
	e.storeConfirmed(e.dealingOf("dealing-0000004", nov(10), e.partyA, e.usd, "10", model.DealingPaidOut))
	e.storeConfirmed(e.dealingOf("dealing-0000005", nov(11), e.partyA, e.usd, "10", model.DealingReceived))
	// B owes the 200 of a split, but 999 paid out to B was voided, and B paid 120 of
	// my expense, which I owe B.
	e.storeConfirmed(e.splitOf("split-000000001", nov(12), e.partyB))
	voided := e.storeConfirmed(e.dealingOf("dealing-0000006", nov(13), e.partyB, e.twd, "999", model.DealingPaidOut))
	e.setStatus(voided.ID, model.StatusVoid)
	paidBy, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("paid-by-0000001", nov(14)), PaidBy: &e.partyB, Instrument: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(paidBy)

	// The dealings of each party, by party id and then instrument code.
	e.wantDealings(nil, byPartyID(map[uuid.UUID][]string{
		e.partyA: {"A TWD 3000", "A USD 0"},
		e.partyB: {"B TWD 80"},
	})...)
	e.wantDealings(&e.partyA, "A TWD 3000", "A USD 0")
	e.wantDealings(&e.partyB, "B TWD 80")
	unknown := uuid.Must(uuid.NewV7())
	e.wantDealings(&unknown)

	// Only the dealing postings count: the account postings of the same
	// transactions are balances, and the expense is not a dealing.
	e.wantBalances(&e.bank.ID, "bank TWD -3000", "bank USD 0")
}

func (e *ledgerEnv) wantDealings(partyID *uuid.UUID, want ...string) {
	e.t.Helper()
	if got := e.dealings(partyID); !slices.Equal(got, want) {
		e.t.Fatalf("dealings = %q, want %q", got, want)
	}
}

func TestBalancesOfADayIncludeTheDayItselfAndLeaveOutTheNextOne(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	spend := func(key string, day time.Time, amount string) model.Transaction {
		return e.expenseOf(e.header(key, day), amount)
	}
	e.storeConfirmed(e.openingOn("opening-cash-01", nov(10), e.cash, e.twd, "1000"))
	e.storeConfirmed(e.openingOn("opening-bank-01", nov(10), e.bank, e.twd, "5000"))
	e.storeConfirmed(spend("spend-0000000a", nov(14), "120"))
	e.storeConfirmed(spend("spend-0000000b", nov(15), "50"))
	// What a balance does not count: a draft, a transaction voided, and what is of
	// another instrument.
	e.store(spend("spend-draft-001", nov(12), "999"))
	e.setStatus(e.storeConfirmed(spend("spend-void-001", nov(13), "777")).ID, model.StatusVoid)
	inUSD, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("spend-usd-0001", nov(14)), Account: &e.cash, Instrument: e.usd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("5"), BaseAmount: decPtr("160")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.storeConfirmed(inUSD)

	tests := []struct {
		name       string
		account    uuid.UUID
		instrument uuid.UUID
		day        time.Time
		want       string
	}{
		{"the day before anything, when there is no balance", e.cash.ID, e.twd.ID, nov(9), ""},
		{"the day of the opening balance", e.cash.ID, e.twd.ID, nov(10), "1000"},
		{"a day with nothing of its own", e.cash.ID, e.twd.ID, nov(13), "1000"},
		{"a day with a transaction of its own, which is counted", e.cash.ID, e.twd.ID, nov(14), "880"},
		{"the day after, which is not counted on the day before", e.cash.ID, e.twd.ID, nov(15), "830"},
		{"a day long after", e.cash.ID, e.twd.ID, nov(30), "830"},
		{"another instrument of the account, before its transaction", e.cash.ID, e.usd.ID, nov(13), ""},
		{"another instrument of the account, on the day of its transaction", e.cash.ID, e.usd.ID, nov(14), "-5"},
		{"another account", e.bank.ID, e.twd.ID, nov(14), "5000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.txns.Balances(ctx, repo.BalanceFilter{AccountID: &tt.account, InstrumentID: &tt.instrument, Day: tt.day})
			if err != nil {
				t.Fatalf("Balances: %v", err)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("balances = %v, want none: nothing is posted to them by that day", got)
				}
				return
			}
			if len(got) != 1 || !got[0].Quantity.Equal(decimal.RequireFromString(tt.want)) {
				t.Fatalf("balances = %v, want the one balance of %s", got, tt.want)
			}
		})
	}
}

func TestPendingDraftsCountsTheDraftsThatWouldChangeTheBalanceOnceConfirmed(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	spend := func(key string, day time.Time) model.Transaction {
		return e.expenseOf(e.header(key, day), "10")
	}
	e.store(spend("draft-0000000a", nov(10)))
	e.store(spend("draft-0000000b", nov(14)))
	e.store(spend("draft-0000000c", nov(15)))
	e.storeConfirmed(spend("confirmed-00001", nov(12)))
	e.setStatus(e.storeConfirmed(spend("void-00000001", nov(12))).ID, model.StatusVoid)
	inUSD, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("draft-usd-0001", nov(12)), Account: &e.cash, Instrument: e.usd, BaseCurrency: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("5"), BaseAmount: decPtr("160")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	e.store(inUSD)
	// A transfer out of the bank with a fee from the bank has two postings to the
	// bank in TWD, and is one draft.
	transfer, err := model.NewTransfer(model.TransferParams{
		Header: e.header("draft-transfer1", nov(12)), From: e.bank, To: e.cash, Instrument: e.twd, Amount: dec("3000"),
		Fees: []model.Fee{{Account: e.bank, Instrument: e.twd, Category: e.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	e.store(transfer)

	tests := []struct {
		name       string
		account    uuid.UUID
		instrument uuid.UUID
		day        time.Time
		want       int
	}{
		{"before every draft", e.cash.ID, e.twd.ID, nov(9), 0},
		{"on the day of the first draft", e.cash.ID, e.twd.ID, nov(10), 1},
		{"between drafts, the one of the 12th being the transfer into cash", e.cash.ID, e.twd.ID, nov(13), 2},
		{"on the day of a draft, which is counted", e.cash.ID, e.twd.ID, nov(14), 3},
		{"the day after, where the last draft is", e.cash.ID, e.twd.ID, nov(15), 4},
		{"another instrument of the account", e.cash.ID, e.usd.ID, nov(14), 1},
		{"an account with a draft that has two postings to it", e.bank.ID, e.twd.ID, nov(14), 1},
		{"an account and instrument with no draft", e.bank.ID, e.usd.ID, nov(30), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.txns.PendingDrafts(ctx, tt.account, tt.instrument, tt.day)
			if err != nil {
				t.Fatalf("PendingDrafts: %v", err)
			}
			if got != tt.want {
				t.Fatalf("pending drafts = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestListFiltersByTheParty(t *testing.T) {
	e := newLedgerEnv(t)
	ctx := context.Background()
	paidOut := e.storeConfirmed(e.dealingOf("list-party-0001", nov(10), e.partyA, e.twd, "100", model.DealingPaidOut))
	received := e.store(e.dealingOf("list-party-0002", nov(11), e.partyA, e.twd, "50", model.DealingReceived))
	split := e.store(e.splitOf("list-party-0003", nov(12), e.partyA))
	paidBy, err := model.NewExpense(model.ExpenseParams{
		Header: e.header("list-party-0004", nov(13)), PaidBy: &e.partyA, Instrument: e.twd,
		Lines: []model.CategoryLine{{Category: e.food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	paidByA := e.store(paidBy)
	forB := e.store(e.dealingOf("list-party-0005", nov(14), e.partyB, e.twd, "70", model.DealingPaidOut))
	e.store(e.expense("list-party-0006", "10"))

	list := func(f repo.TransactionFilter) []uuid.UUID {
		t.Helper()
		f.Limit = 100
		got, err := e.txns.List(ctx, f)
		if err != nil {
			t.Fatalf("List(%+v): %v", f, err)
		}
		ids := make([]uuid.UUID, len(got))
		for i, txn := range got {
			ids[i] = txn.ID
		}
		return ids
	}
	ids := func(txns ...model.Transaction) []uuid.UUID {
		out := make([]uuid.UUID, len(txns))
		for i, txn := range txns {
			out[i] = txn.ID
		}
		return out
	}
	day := func(d int) *time.Time {
		v := nov(d)
		return &v
	}

	tests := []struct {
		name   string
		filter repo.TransactionFilter
		want   []uuid.UUID
	}{
		{"every transaction with a dealing of A, newest first", repo.TransactionFilter{PartyID: &e.partyA}, ids(paidByA, split, received, paidOut)},
		{"B", repo.TransactionFilter{PartyID: &e.partyB}, ids(forB)},
		{"A and confirmed", repo.TransactionFilter{PartyID: &e.partyA, Status: model.StatusConfirmed}, ids(paidOut)},
		{"A and the bank, which only the dealings go through", repo.TransactionFilter{PartyID: &e.partyA, AccountID: &e.bank.ID}, ids(received, paidOut)},
		{"A in a range of days", repo.TransactionFilter{PartyID: &e.partyA, From: day(11), To: day(12)}, ids(split, received)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := list(tt.filter); !slices.Equal(got, tt.want) {
				t.Fatalf("List = %v, want %v", got, tt.want)
			}
		})
	}
	unknown := uuid.Must(uuid.NewV7())
	if got := list(repo.TransactionFilter{PartyID: &unknown}); len(got) != 0 {
		t.Errorf("List for a party with no dealing = %v, want none", got)
	}
}
