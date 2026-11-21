package model_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// postingDateWorld is what the posting date of a transaction is tested on: a card, a
// cash account that is not one, and an expense of 120 on each, dated the 14th of
// November 2026.
type postingDateWorld struct {
	card, cash model.Account
	twd        model.Instrument
	food       model.Category
}

func newPostingDateWorld(t *testing.T) postingDateWorld {
	t.Helper()
	return postingDateWorld{
		card: newCard(t, "卡", new(5), new(20)),
		cash: newCash(t, "現金"),
		twd:  newTWD(t),
		food: topLevel(t, model.DirectionExpense, "飲食"),
	}
}

// expense records an expense of 120 from the account, a draft, with the posting
// date.
func (w postingDateWorld) expense(t *testing.T, key string, account model.Account, postedOn *time.Time) (model.Transaction, error) {
	t.Helper()
	return model.NewExpense(model.ExpenseParams{
		Header:       header(key),
		Account:      &account,
		Instrument:   w.twd,
		BaseCurrency: w.twd,
		Lines:        []model.CategoryLine{{Category: w.food, Amount: dec("120")}},
		PostedOn:     postedOn,
	})
}

// cardExpense is expense on the card, which fails the test when it is refused.
func (w postingDateWorld) cardExpense(t *testing.T, key string) model.Transaction {
	t.Helper()
	txn, err := w.expense(t, key, w.card, nil)
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	return txn
}

func (w postingDateWorld) confirmed(t *testing.T, txn model.Transaction) model.Transaction {
	t.Helper()
	txn, err := txn.Confirm()
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	return txn
}

func (w postingDateWorld) voided(t *testing.T, txn model.Transaction) model.Transaction {
	t.Helper()
	txn, err := w.confirmed(t, txn).Void()
	if err != nil {
		t.Fatalf("Void: %v", err)
	}
	return txn
}

func wantPostedOn(t *testing.T, txn model.Transaction, want *time.Time) {
	t.Helper()
	switch {
	case want == nil && txn.PostedOn != nil:
		t.Fatalf("posted on %s, want no posting date", txn.PostedOn.Format(time.DateOnly))
	case want != nil && txn.PostedOn == nil:
		t.Fatalf("no posting date, want %s", want.Format(time.DateOnly))
	case want != nil && !txn.PostedOn.Equal(*want):
		t.Fatalf("posted on %s, want %s", txn.PostedOn.Format(time.DateOnly), want.Format(time.DateOnly))
	}
}

func TestWithPostedOnGivesChangesAndTakesAwayThePostingDateOfADraftAndOfAConfirmedTransaction(t *testing.T) {
	w := newPostingDateWorld(t)
	draft := w.cardExpense(t, "lunch-0000001")
	tests := []struct {
		name string
		txn  model.Transaction
	}{
		{"a draft", draft},
		{"a confirmed transaction", w.confirmed(t, draft)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantPostedOn(t, tt.txn, nil)
			if got := tt.txn.StatementDate(); !got.Equal(on(2026, time.November, 14)) {
				t.Fatalf("statement date = %s, want the day it happened, the 14th: it has no posting date", got.Format(time.DateOnly))
			}

			given, err := tt.txn.WithPostedOn(new(on(2026, time.November, 20)), []model.Account{w.card})
			if err != nil {
				t.Fatalf("give a posting date: %v", err)
			}
			wantPostedOn(t, given, new(on(2026, time.November, 20)))
			if got := given.StatementDate(); !got.Equal(on(2026, time.November, 20)) {
				t.Errorf("statement date = %s, want the posting date, the 20th", got.Format(time.DateOnly))
			}
			wantPostedOn(t, tt.txn, nil) // the transaction it was called on is as it was

			again, err := given.WithPostedOn(new(on(2026, time.November, 20)), []model.Account{w.card})
			if err != nil || !reflect.DeepEqual(again, given) {
				t.Fatalf("give the same date again: %+v, %v, want the transaction as it was", again, err)
			}

			changed, err := given.WithPostedOn(new(on(2026, time.December, 2)), []model.Account{w.card})
			if err != nil {
				t.Fatalf("change the posting date: %v", err)
			}
			wantPostedOn(t, changed, new(on(2026, time.December, 2)))

			cleared, err := changed.WithPostedOn(nil, []model.Account{w.card})
			if err != nil {
				t.Fatalf("clear the posting date: %v", err)
			}
			wantPostedOn(t, cleared, nil)
			if got := cleared.StatementDate(); !got.Equal(on(2026, time.November, 14)) {
				t.Errorf("statement date = %s, want the day it happened again", got.Format(time.DateOnly))
			}
			if !reflect.DeepEqual(cleared, tt.txn) {
				t.Errorf("cleared = %+v, want the transaction as it was before it had a date", cleared)
			}
		})
	}
}

// What a balance, a budget and a dealing count is the day it happened, the postings,
// the amounts and the base amounts. A posting date changes none of it, which is why
// it may be changed after the transaction is confirmed.
func TestAPostingDateChangesNothingABalanceABudgetOrADealingCounts(t *testing.T) {
	w := newPostingDateWorld(t)
	confirmed := w.confirmed(t, w.cardExpense(t, "lunch-0000001"))

	got, err := confirmed.WithPostedOn(new(on(2026, time.December, 2)), []model.Account{w.card})
	if err != nil {
		t.Fatalf("WithPostedOn: %v", err)
	}

	want := confirmed
	want.PostedOn = got.PostedOn
	if !reflect.DeepEqual(got, want) {
		t.Errorf("transaction = %+v, want it the same as it was with the posting date only: the day it happened, the status, the postings and their amounts and base amounts, the tags, the project and the area", got)
	}
	if !got.OccurredOn.Equal(on(2026, time.November, 14)) || got.Status != model.StatusConfirmed {
		t.Errorf("occurred on %s, status %s, want the 14th and confirmed", got.OccurredOn.Format(time.DateOnly), got.Status)
	}
}

func TestThePostingDateIsTheDayItsOwnLocationReads(t *testing.T) {
	w := newPostingDateWorld(t)
	txn := w.cardExpense(t, "lunch-0000001")
	tests := []struct {
		name     string
		postedOn time.Time
		want     time.Time
	}{
		// The 20th in Taipei, though it is still the 19th in UTC.
		{"just after midnight in Taipei", time.Date(2026, time.November, 20, 0, 30, 0, 0, taipei), on(2026, time.November, 20)},
		{"the end of the day in UTC", time.Date(2026, time.November, 20, 23, 59, 0, 0, time.UTC), on(2026, time.November, 20)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := txn.WithPostedOn(&tt.postedOn, []model.Account{w.card})
			if err != nil {
				t.Fatalf("WithPostedOn: %v", err)
			}

			wantPostedOn(t, got, &tt.want)
			if got.PostedOn.Location() != time.UTC {
				t.Errorf("posting date is in %s, want a date at midnight UTC", got.PostedOn.Location())
			}
		})
	}
}

func TestWithPostedOnDoesNotShareThePostingDateTheCallerGave(t *testing.T) {
	w := newPostingDateWorld(t)
	postedOn := on(2026, time.November, 20)

	got, err := w.cardExpense(t, "lunch-0000001").WithPostedOn(&postedOn, []model.Account{w.card})
	if err != nil {
		t.Fatalf("WithPostedOn: %v", err)
	}
	postedOn = on(2026, time.December, 25)

	wantPostedOn(t, got, new(on(2026, time.November, 20)))
}

func TestAPostingDateCanBeOnTheDayItHappenedButNotBeforeIt(t *testing.T) {
	w := newPostingDateWorld(t)
	txn := w.cardExpense(t, "lunch-0000001") // the 14th

	same, err := txn.WithPostedOn(new(on(2026, time.November, 14)), []model.Account{w.card})
	if err != nil {
		t.Fatalf("the day it happened: %v, want it allowed: the check is not before it", err)
	}
	wantPostedOn(t, same, new(on(2026, time.November, 14)))

	_, err = txn.WithPostedOn(new(on(2026, time.November, 13)), []model.Account{w.card})
	if !errors.Is(err, model.ErrPostedBeforeOccurred) {
		t.Fatalf("the day before it happened: err = %v, want ErrPostedBeforeOccurred", err)
	}

	// The day before in UTC is the day it happened in Taipei: the day is the day in the
	// location of the date.
	justAfterMidnight := time.Date(2026, time.November, 14, 0, 30, 0, 0, taipei)
	if _, err := txn.WithPostedOn(&justAfterMidnight, []model.Account{w.card}); err != nil {
		t.Fatalf("the 14th in Taipei, just after midnight: %v, want it allowed", err)
	}
}

func TestWithPostedOnRefusesWhatCannotHaveAPostingDateAndChangesNothing(t *testing.T) {
	w := newPostingDateWorld(t)
	planParams := model.ExpenseParams{
		Header: header("plan-00000001"), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: w.food, Amount: dec("3000")}},
	}
	plan, err := model.NewInstalmentExpense(planParams, model.InstalmentSchedule{Periods: monthly(3, "1000")})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	bill, err := model.NewBillInstalment(model.BillInstalmentParams{
		Header: header("bill-00000001"), Account: w.card, Instrument: w.twd, BaseCurrency: w.twd,
		Principal: dec("3000"), Schedule: model.InstalmentSchedule{Periods: monthly(3, "1000")},
	})
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}
	cashLunch, err := w.expense(t, "lunch-0000002", w.cash, nil)
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	party := uuid.Must(uuid.NewV7())
	paidByParty, err := model.NewExpense(model.ExpenseParams{
		Header: header("lunch-0000003"), PaidBy: &party, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: w.food, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewExpense paid by a party: %v", err)
	}
	cardLunch := w.cardExpense(t, "lunch-0000001")

	tests := []struct {
		name     string
		txn      model.Transaction
		accounts []model.Account
		postedOn *time.Time
		want     error
	}{
		{"an expense from an account that is not a card", cashLunch, []model.Account{w.cash}, new(on(2026, time.November, 20)), model.ErrNoCardPosting},
		{"an expense from a card that is not among the accounts it is given", cardLunch, nil, new(on(2026, time.November, 20)), model.ErrNoCardPosting},
		{"an expense from a card, with another account given", cardLunch, []model.Account{w.cash}, new(on(2026, time.November, 20)), model.ErrNoCardPosting},
		{"an expense a party paid, which has no account posting", paidByParty, []model.Account{w.card}, new(on(2026, time.November, 20)), model.ErrNoCardPosting},
		{"a void transaction", w.voided(t, cardLunch), []model.Account{w.card}, new(on(2026, time.November, 20)), model.ErrTransactionVoid},
		{"the origin of an instalment plan", plan.Origin, []model.Account{w.card}, new(on(2026, time.November, 20)), model.ErrPartOfPlan},
		{"a period of an instalment plan", plan.Periods[0], []model.Account{w.card}, new(on(2026, time.December, 5)), model.ErrPartOfPlan},
		{"the origin of a bill instalment, which does post to the card", bill.Origin, []model.Account{w.card}, new(on(2026, time.November, 20)), model.ErrPartOfPlan},
		{"a date before the day it happened", cardLunch, []model.Account{w.card}, new(on(2026, time.November, 13)), model.ErrPostedBeforeOccurred},
		// Taking the date away is asked about as much as giving one: what cannot have a
		// posting date is not asked about one.
		{"taking away from an account that is not a card", cashLunch, []model.Account{w.cash}, nil, model.ErrNoCardPosting},
		{"taking away from a void transaction", w.voided(t, cardLunch), []model.Account{w.card}, nil, model.ErrTransactionVoid},
		{"taking away from a period of an instalment plan", plan.Periods[1], []model.Account{w.card}, nil, model.ErrPartOfPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.txn

			got, err := tt.txn.WithPostedOn(tt.postedOn, tt.accounts)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !reflect.DeepEqual(got, model.Transaction{}) {
				t.Errorf("transaction = %+v, want none with the error", got)
			}
			if !reflect.DeepEqual(tt.txn, before) {
				t.Errorf("the transaction that was asked about was changed by a call that was refused")
			}
		})
	}
}

func TestAPostingDateIsForATransactionThatPostsToACardWhateverItsKind(t *testing.T) {
	w := newPostingDateWorld(t)
	refunds := topLevel(t, model.DirectionIncome, "退款")
	transfer, err := model.NewTransfer(model.TransferParams{
		Header: header("payment-00001"), From: w.cash, To: w.card, Instrument: w.twd, Amount: dec("1000"), BaseCurrency: w.twd,
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	between, err := model.NewTransfer(model.TransferParams{
		Header: header("payment-00002"), From: w.cash, To: newCash(t, "銀行"), Instrument: w.twd, Amount: dec("1000"), BaseCurrency: w.twd,
	})
	if err != nil {
		t.Fatalf("NewTransfer: %v", err)
	}
	refund, err := model.NewIncome(model.IncomeParams{
		Header: header("refund-000001"), Account: w.card, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: refunds, Amount: dec("120")}},
	})
	if err != nil {
		t.Fatalf("NewIncome: %v", err)
	}

	// A payment to the card, which is a transfer into it, and a refund into it, are
	// on its statement as much as what was charged to it.
	for name, txn := range map[string]model.Transaction{"a payment into the card": transfer, "an income into the card": refund} {
		if _, err := txn.WithPostedOn(new(on(2026, time.November, 20)), []model.Account{w.cash, w.card}); err != nil {
			t.Errorf("%s: %v, want it allowed: it posts to the card", name, err)
		}
	}
	if _, err := between.WithPostedOn(new(on(2026, time.November, 20)), []model.Account{w.cash}); !errors.Is(err, model.ErrNoCardPosting) {
		t.Errorf("a transfer between two accounts that are not cards: err = %v, want ErrNoCardPosting", err)
	}
}

func TestConfirmingAndVoidingKeepThePostingDate(t *testing.T) {
	w := newPostingDateWorld(t)
	draft, err := w.expense(t, "lunch-0000001", w.card, new(on(2026, time.November, 20)))
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	confirmed := w.confirmed(t, draft)
	wantPostedOn(t, confirmed, new(on(2026, time.November, 20)))

	voided, err := confirmed.Void()
	if err != nil {
		t.Fatalf("Void: %v", err)
	}
	wantPostedOn(t, voided, new(on(2026, time.November, 20)))
}

func TestCheckPostedOnAllowedNeedsNothingButTheTransaction(t *testing.T) {
	w := newPostingDateWorld(t)
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: header("plan-00000001"), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: w.food, Amount: dec("3000")}},
	}, model.InstalmentSchedule{Periods: monthly(3, "1000")})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	cardLunch := w.cardExpense(t, "lunch-0000001")
	cashLunch, err := w.expense(t, "lunch-0000002", w.cash, nil)
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}

	tests := []struct {
		name string
		txn  model.Transaction
		want error
	}{
		{"a draft", cardLunch, nil},
		{"a confirmed transaction", w.confirmed(t, cardLunch), nil},
		// Which account it posts to is not asked: that is for WithPostedOn.
		{"a transaction on an account that is not a card", cashLunch, nil},
		{"a void transaction", w.voided(t, cardLunch), model.ErrTransactionVoid},
		{"the origin of a plan", plan.Origin, model.ErrPartOfPlan},
		{"a period of a plan", plan.Periods[2], model.ErrPartOfPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.CheckPostedOnAllowed(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestNewExpenseWithAPostingDate(t *testing.T) {
	w := newPostingDateWorld(t)

	got, err := w.expense(t, "lunch-0000001", w.card, new(on(2026, time.November, 20)))
	if err != nil {
		t.Fatalf("NewExpense: %v", err)
	}
	wantPostedOn(t, got, new(on(2026, time.November, 20)))
	if got.Status != model.StatusDraft || !got.OccurredOn.Equal(on(2026, time.November, 14)) {
		t.Errorf("status %s, occurred on %s, want a draft of the 14th", got.Status, got.OccurredOn.Format(time.DateOnly))
	}

	refused := []struct {
		name string
		do   func() error
		want error
	}{
		{"a date before the day it happened", func() error {
			_, err := w.expense(t, "lunch-0000002", w.card, new(on(2026, time.November, 13)))
			return err
		}, model.ErrPostedBeforeOccurred},
		{"an account that is not a card", func() error {
			_, err := w.expense(t, "lunch-0000003", w.cash, new(on(2026, time.November, 20)))
			return err
		}, model.ErrNoCardPosting},
		{"an expense a party paid", func() error {
			party := uuid.Must(uuid.NewV7())
			_, err := model.NewExpense(model.ExpenseParams{
				Header: header("lunch-0000004"), PaidBy: &party, Instrument: w.twd, BaseCurrency: w.twd,
				Lines:    []model.CategoryLine{{Category: w.food, Amount: dec("120")}},
				PostedOn: new(on(2026, time.November, 20)),
			})
			return err
		}, model.ErrNoCardPosting},
		{"an expense paid in instalments", func() error {
			_, err := model.NewInstalmentExpense(model.ExpenseParams{
				Header: header("plan-00000001"), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
				Lines:    []model.CategoryLine{{Category: w.food, Amount: dec("3000")}},
				PostedOn: new(on(2026, time.November, 20)),
			}, model.InstalmentSchedule{Periods: monthly(3, "1000")})
			return err
		}, model.ErrPartOfPlan},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.do(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestValidateRefusesAPostingDateBeforeTheDayItHappenedAndOnAPlan(t *testing.T) {
	w := newPostingDateWorld(t)
	card := w.cardExpense(t, "lunch-0000001")
	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: header("plan-00000001"), Account: &w.card, Instrument: w.twd, BaseCurrency: w.twd,
		Lines: []model.CategoryLine{{Category: w.food, Amount: dec("3000")}},
	}, model.InstalmentSchedule{Periods: monthly(3, "1000")})
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	onTheDay, before, afterwards := card, card, card
	onTheDay.PostedOn = new(on(2026, time.November, 14))
	before.PostedOn = new(on(2026, time.November, 13))
	afterwards.PostedOn = new(on(2027, time.November, 14))
	origin, period := plan.Origin, plan.Periods[0]
	origin.PostedOn, period.PostedOn = new(on(2026, time.November, 20)), new(on(2026, time.December, 20))

	for name, txn := range map[string]model.Transaction{"on the day it happened": onTheDay, "a year later": afterwards} {
		if err := txn.Validate(); err != nil {
			t.Errorf("%s: %v, want it valid", name, err)
		}
	}
	tests := []struct {
		name string
		txn  model.Transaction
		want error
	}{
		{"before the day it happened", before, model.ErrPostedBeforeOccurred},
		{"on the origin of a plan", origin, model.ErrPartOfPlan},
		{"on a period of a plan", period, model.ErrPartOfPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
