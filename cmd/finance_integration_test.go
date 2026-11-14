package cmd_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"go.uber.org/fx"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/testpg"
)

func TestMain(m *testing.M) {
	os.Exit(testpg.Run(m))
}

// The ledger as the app runs it: the real use cases of the finance context and of
// the catalog on a real PostgreSQL, put together by the Fx modules of cmd, which
// is the one place that does so. The repos and the use cases each have tests of
// their own; this one follows money through all of them.
type ledgerApp struct {
	fx.In

	CreateParty    catalogusecase.CreateParty
	ArchiveParty   catalogusecase.ArchiveParty
	CreateAccount  usecase.CreateAccount
	CreateCategory usecase.CreateCategory
	ListCategories usecase.ListCategories
	ArchiveCat     usecase.ArchiveCategory

	SetOpeningBalance usecase.SetOpeningBalance
	RecordExpense     usecase.RecordExpense
	RecordDealing     usecase.RecordDealing
	RecordReward      usecase.RecordReward
	Reconcile         usecase.Reconcile
	Confirm           usecase.ConfirmTransactions
	ListTransactions  usecase.ListTransactions
	GetBalances       usecase.GetBalances
	GetDealings       usecase.GetDealings

	CreateInstrument     usecase.CreateInstrument
	RecordBillInstalment usecase.RecordBillInstalment
	RecordTrade          usecase.RecordTrade
	GetInstalmentPlan    usecase.GetInstalmentPlan
	ListInstalmentPlans  usecase.ListInstalmentPlans
	Void                 usecase.VoidTransaction
	Discard              usecase.DiscardDraft
}

func newLedgerApp(t *testing.T) ledgerApp {
	t.Helper()
	return newLedgerAppOn(t, testpg.New(t), newTestClock(day))
}

// newLedgerAppOn puts the app together on a pool and a clock of the caller, for a
// test that needs a say in how the database is reached or in what day it is.
func newLedgerAppOn(t *testing.T, pool *pgxpool.Pool, clk financeclock.Clock) ledgerApp {
	t.Helper()
	var l ledgerApp
	app := fx.New(
		fx.NopLogger,
		fx.Supply(pool),
		fx.Provide(func() financeclock.Clock { return clk }),
		cmd.CatalogModule(),
		cmd.FinanceModule(),
		fx.Populate(&l),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the modules: %v", err)
	}
	return l
}

// userZone is where the user lives, as an offset, so that no test needs a timezone
// database.
var userZone = time.FixedZone("UTC+8", 8*60*60)

// testClock is the time of the app in a test: noon, in the zone of the user, of the
// day it was last set to. It is safe for the calls of a test that run together.
type testClock struct{ now atomic.Pointer[time.Time] }

func newTestClock(on time.Time) *testClock {
	c := &testClock{}
	c.setDay(on)
	return c
}

func (c *testClock) Now() time.Time { return *c.now.Load() }

// setDay moves the clock to noon on the date of on, in whatever zone on is.
func (c *testClock) setDay(on time.Time) {
	c.now.Store(new(time.Date(on.Year(), on.Month(), on.Day(), 12, 0, 0, 0, userZone)))
}

func dec(s string) decimal.Decimal {
	return decimal.RequireFromString(s)
}

// the day of everything below, in November 2026.
var day = time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC)

func (l ledgerApp) details(key string) usecase.RecordDetails {
	return usecase.RecordDetails{IdempotencyKey: key, OccurredOn: day}
}

func (l ledgerApp) confirm(t *testing.T, txns ...usecase.Transaction) {
	t.Helper()
	ids := make([]uuid.UUID, len(txns))
	for i, txn := range txns {
		ids[i] = txn.ID
	}
	if _, err := l.Confirm.ConfirmTransactions(context.Background(), usecase.ConfirmTransactionsInput{IDs: ids}); err != nil {
		t.Fatalf("ConfirmTransactions: %v", err)
	}
}

// balance is what the account holds of the instrument.
func (l ledgerApp) balance(t *testing.T, account uuid.UUID, instrument uuid.UUID) decimal.Decimal {
	t.Helper()
	out, err := l.GetBalances.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &account})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	for _, b := range out.Balances {
		if b.InstrumentID == instrument {
			return b.Quantity
		}
	}
	return decimal.Zero
}

// dealings is what the parties owe me, as "name amount", in the order returned.
func (l ledgerApp) dealings(t *testing.T, names map[uuid.UUID]string, party *uuid.UUID) []string {
	t.Helper()
	out, err := l.GetDealings.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: party})
	if err != nil {
		t.Fatalf("GetDealings: %v", err)
	}
	lines := make([]string, len(out.Dealings))
	for i, d := range out.Dealings {
		lines[i] = names[d.PartyID] + " " + d.Quantity.String()
	}
	return lines
}

func TestSplitsDealingsRewardsAndReconciliationsThroughTheRealUseCases(t *testing.T) {
	ctx := context.Background()
	l := newLedgerApp(t)
	twd := model.BaseCurrencyID

	// What the ledger refers to.
	party := func(name string) uuid.UUID {
		p, err := l.CreateParty.CreateParty(ctx, catalogusecase.CreatePartyInput{Name: name})
		if err != nil {
			t.Fatalf("CreateParty(%q): %v", name, err)
		}
		return p.ID
	}
	account := func(name, kind string) uuid.UUID {
		a, err := l.CreateAccount.CreateAccount(ctx, usecase.CreateAccountInput{Name: name, Kind: kind})
		if err != nil {
			t.Fatalf("CreateAccount(%q): %v", name, err)
		}
		return a.ID
	}
	food, err := l.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Food"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	amy, bob, corp := party("Amy"), party("Bob"), party("Corp")
	names := map[uuid.UUID]string{amy: "Amy", bob: "Bob", corp: "Corp"}
	card, cash, bank := account("card", "card"), account("cash", "cash"), account("bank", "bank")

	opening := func(key string, account uuid.UUID, amount string) usecase.Transaction {
		out, err := l.SetOpeningBalance.SetOpeningBalance(ctx, usecase.SetOpeningBalanceInput{
			RecordDetails: l.details(key), AccountID: account, InstrumentID: twd, Amount: dec(amount),
		})
		if err != nil {
			t.Fatalf("SetOpeningBalance(%q): %v", key, err)
		}
		return out.Transaction
	}
	l.confirm(t, opening("opening-cash-001", cash, "2000"), opening("opening-bank-001", bank, "10000"))

	// I pay 900 from the card: 300 is mine, Amy and Bob owe 300 each.
	split, err := l.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: l.details("split-dinner-001"), AccountID: &card, InstrumentID: twd,
		Lines:  []usecase.CategoryLine{{CategoryID: food.ID, Amount: dec("300")}},
		Shares: []usecase.Share{{PartyID: amy, Amount: dec("300")}, {PartyID: bob, Amount: dec("300")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	// I pay 5,000 for the company, and Amy gives me 100 back.
	paidOut, err := l.RecordDealing.RecordDealing(ctx, usecase.RecordDealingInput{
		RecordDetails: l.details("dealing-out-0001"), AccountID: bank, PartyID: corp, InstrumentID: twd,
		Amount: dec("5000"), Direction: "paid_out",
	})
	if err != nil {
		t.Fatalf("RecordDealing paid out: %v", err)
	}
	received, err := l.RecordDealing.RecordDealing(ctx, usecase.RecordDealingInput{
		RecordDetails: l.details("dealing-in-00001"), AccountID: cash, PartyID: amy, InstrumentID: twd,
		Amount: dec("100"), Direction: "received",
	})
	if err != nil {
		t.Fatalf("RecordDealing received: %v", err)
	}

	// All of it is drafts: nothing is owed, and no balance has moved.
	if got := l.dealings(t, names, nil); len(got) != 0 {
		t.Fatalf("dealings before anything is confirmed = %q, want none", got)
	}
	if got := l.balance(t, bank, twd); !got.Equal(dec("10000")) {
		t.Fatalf("bank before the drafts are confirmed = %s, want 10000", got)
	}

	l.confirm(t, split.Transaction, paidOut.Transaction, received.Transaction)

	// What parties owe me, by name, netted: Amy 300 and then 100 back, Bob 300, the
	// company 5,000.
	if got, want := l.dealings(t, names, nil), []string{"Amy 200", "Bob 300", "Corp 5000"}; !slices.Equal(got, want) {
		t.Fatalf("dealings = %q, want %q", got, want)
	}
	if got, want := l.dealings(t, names, &bob), []string{"Bob 300"}; !slices.Equal(got, want) {
		t.Errorf("dealings of Bob = %q, want %q", got, want)
	}

	// A party the catalog does not have is not one that owes nothing: it is not
	// found, in the terms of the ledger, which the catalog's own error is not. One
	// that is archived still shows what it owes, which for this one is nothing.
	stranger := uuid.New()
	if _, err := l.GetDealings.GetDealings(ctx, usecase.GetDealingsInput{PartyID: &stranger}); !errors.Is(err, model.ErrNotFound) {
		t.Errorf("dealings of a party the catalog does not have: err = %v, want ErrNotFound", err)
	}
	old := party("Old")
	if _, err := l.ArchiveParty.ArchiveParty(ctx, old); err != nil {
		t.Fatalf("ArchiveParty: %v", err)
	}
	if got := l.dealings(t, names, &old); len(got) != 0 {
		t.Errorf("dealings of an archived party with none = %q, want none", got)
	}
	for _, tt := range []struct {
		name    string
		account uuid.UUID
		want    string
	}{
		{"the card, which paid all 900", card, "-900"},
		{"the bank, which paid the company", bank, "5000"},
		{"cash, which Amy paid back into", cash, "2100"},
	} {
		if got := l.balance(t, tt.account, twd); !got.Equal(dec(tt.want)) {
			t.Errorf("balance of %s = %s, want %s", tt.name, got, tt.want)
		}
	}
	listed, err := l.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{PartyID: &amy})
	if err != nil || len(listed.Transactions) != 2 {
		t.Errorf("transactions of Amy = %d, %v, want the split and the money she gave back", len(listed.Transactions), err)
	}

	// The card gives 50 back.
	reward, err := l.RecordReward.RecordReward(ctx, usecase.RecordRewardInput{
		RecordDetails: l.details("reward-0000001"), AccountID: card, InstrumentID: twd, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("RecordReward: %v", err)
	}
	l.confirm(t, reward.Transaction)
	if got := l.balance(t, card, twd); !got.Equal(dec("-850")) {
		t.Errorf("card after the reward = %s, want -850", got)
	}

	// Cash holds 120 less than the ledger says: an unrecorded expense, which counts
	// once it is confirmed, and then the ledger says what cash holds.
	below, err := l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
		RecordDetails: l.details("reconcile-cash-01"), AccountID: cash, InstrumentID: twd, ActualBalance: dec("1980"),
	})
	if err != nil {
		t.Fatalf("Reconcile cash: %v", err)
	}
	if below.Transaction == nil || !below.RecordedBalance.Equal(dec("2100")) || !below.Difference.Equal(dec("-120")) {
		t.Fatalf("reconciliation of cash = %+v, want recorded 2100, a difference of -120 and a draft", below)
	}
	// The draft is an expense of the system category 未記錄, as a reconciliation
	// makes up for what an account holds less of: a transaction of its own kind.
	posted := func(txn usecase.Transaction, book string, category uuid.UUID, quantity string) bool {
		for _, p := range txn.Postings {
			if p.Book == book && p.CategoryID != nil && *p.CategoryID == category &&
				p.Quantity.Equal(dec(quantity)) && p.BaseAmount != nil && p.BaseAmount.Equal(dec(quantity)) {
				return true
			}
		}
		return false
	}
	if below.Transaction.Kind != "reconciliation" || below.Transaction.Status != "draft" ||
		!posted(*below.Transaction, "expense", model.UnrecordedExpenseCategoryID, "120") {
		t.Errorf("draft of the cash reconciliation = %+v, want a draft reconciliation with an expense of 120 to the unrecorded category", below.Transaction)
	}
	if got := l.balance(t, cash, twd); !got.Equal(dec("2100")) {
		t.Errorf("cash while the draft waits = %s, want 2100", got)
	}
	// Reconciling the same account again, while its draft waits, would book the
	// difference twice: it is refused, with the number of drafts in the way.
	_, err = l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
		RecordDetails: l.details("reconcile-cash-02"), AccountID: cash, InstrumentID: twd, ActualBalance: dec("1980"),
	})
	var pending *model.PendingDraftsError
	if !errors.As(err, &pending) || pending.Count != 1 {
		t.Errorf("a second reconciliation: err = %v, want a *PendingDraftsError of 1", err)
	}
	l.confirm(t, *below.Transaction)
	if got := l.balance(t, cash, twd); !got.Equal(dec("1980")) {
		t.Errorf("cash once the draft is confirmed = %s, want the actual balance, 1980", got)
	}

	// The bank holds 80 more than the ledger says: an unrecorded income.
	above, err := l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
		RecordDetails: l.details("reconcile-bank-01"), AccountID: bank, InstrumentID: twd, ActualBalance: dec("5080"),
	})
	if err != nil {
		t.Fatalf("Reconcile bank: %v", err)
	}
	if above.Transaction == nil || !above.Difference.Equal(dec("80")) {
		t.Fatalf("reconciliation of the bank = %+v, want a difference of 80 and a draft", above)
	}
	if above.Transaction.Kind != "reconciliation" || above.Transaction.Status != "draft" ||
		!posted(*above.Transaction, "income", model.UnrecordedIncomeCategoryID, "-80") {
		t.Errorf("draft of the bank reconciliation = %+v, want a draft reconciliation with an income of -80 to the unrecorded category", above.Transaction)
	}
	l.confirm(t, *above.Transaction)
	if got := l.balance(t, bank, twd); !got.Equal(dec("5080")) {
		t.Errorf("bank once the draft is confirmed = %s, want the actual balance, 5080", got)
	}

	// Both ledgers now say what the accounts hold, so there is nothing to reconcile.
	for _, tt := range []struct {
		key     string
		account uuid.UUID
		actual  string
	}{{"reconcile-cash-03", cash, "1980"}, {"reconcile-bank-02", bank, "5080"}} {
		out, err := l.Reconcile.Reconcile(ctx, usecase.ReconcileInput{
			RecordDetails: l.details(tt.key), AccountID: tt.account, InstrumentID: twd, ActualBalance: dec(tt.actual),
		})
		if err != nil || out.Transaction != nil || !out.Difference.IsZero() {
			t.Errorf("reconciliation of %s again = %+v, %v, want no difference and nothing recorded", tt.key, out, err)
		}
	}

	// The unrecorded categories are in the list of categories, and stay in use.
	categories, err := l.ListCategories.ListCategories(ctx, usecase.ListCategoriesInput{})
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	system := 0
	for _, c := range categories.Categories {
		if model.IsSystemCategory(c.ID) {
			system++
			if _, err := l.ArchiveCat.ArchiveCategory(ctx, c.ID); !errors.Is(err, model.ErrSystemCategory) {
				t.Errorf("archive %s: err = %v, want ErrSystemCategory", c.ID, err)
			}
		}
	}
	if system != 2 {
		t.Errorf("%d system categories listed, want 2", system)
	}
}
