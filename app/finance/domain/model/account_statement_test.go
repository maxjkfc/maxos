package model_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// newCard returns a card that closes its statement on the closing day and has it
// paid by the due day, either of which may be nil.
func newCard(t *testing.T, name string, closingDay, dueDay *int) model.Account {
	t.Helper()
	a, err := model.NewAccount(model.NewAccountParams{
		ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCard, ClosingDay: closingDay, PaymentDueDay: dueDay,
	})
	if err != nil {
		t.Fatalf("NewAccount(%q): %v", name, err)
	}
	return a
}

// showDay prints a day of the month that may be nil.
func showDay(d *int) string {
	if d == nil {
		return "none"
	}
	return strconv.Itoa(*d)
}

func wantDays(t *testing.T, a model.Account, closingDay, dueDay *int) {
	t.Helper()
	same := func(got, want *int) bool { return (got == nil) == (want == nil) && (got == nil || *got == *want) }
	if !same(a.ClosingDay, closingDay) || !same(a.PaymentDueDay, dueDay) {
		t.Fatalf("days = closing %s, due %s, want closing %s, due %s",
			showDay(a.ClosingDay), showDay(a.PaymentDueDay), showDay(closingDay), showDay(dueDay))
	}
}

func TestACardMayHaveAClosingDayAndAPaymentDueDayOrOneOrNeither(t *testing.T) {
	tests := []struct {
		name               string
		closingDay, dueDay *int
	}{
		{"both", new(5), new(20)},
		{"a closing day alone", new(5), nil},
		{"neither", nil, nil},
		{"the first day of the month", new(1), new(1)},
		{"the last day a month can have", new(31), new(31)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			card := newCard(t, "卡", tt.closingDay, tt.dueDay)

			wantDays(t, card, tt.closingDay, tt.dueDay)
		})
	}
}

func TestAnAccountDoesNotShareTheDaysTheCallerGave(t *testing.T) {
	closing, due := 5, 20

	card := newCard(t, "卡", &closing, &due)
	closing, due = 6, 21

	wantDays(t, card, new(5), new(20))
}

func TestOnlyACardHasAClosingDayAndAPaymentDueDay(t *testing.T) {
	for _, kind := range []model.AccountKind{
		model.AccountKindCash, model.AccountKindBank, model.AccountKindExchange,
		model.AccountKindBrokerage, model.AccountKindWallet, model.AccountKindLoan,
	} {
		for _, days := range []struct {
			name               string
			closingDay, dueDay *int
		}{
			{"a closing day", new(5), nil},
			{"both", new(5), new(20)},
			{"a payment due day alone", nil, new(20)},
		} {
			_, err := model.NewAccount(model.NewAccountParams{
				ID: uuid.Must(uuid.NewV7()), Name: "x", Kind: kind, ClosingDay: days.closingDay, PaymentDueDay: days.dueDay,
			})
			if !errors.Is(err, model.ErrNotACard) {
				t.Errorf("a %s with %s: err = %v, want ErrNotACard", kind, days.name, err)
			}
		}
		// Nothing to refuse when there are no days.
		if _, err := model.NewAccount(model.NewAccountParams{ID: uuid.Must(uuid.NewV7()), Name: "x", Kind: kind}); err != nil {
			t.Errorf("a %s with no days: %v", kind, err)
		}
	}
}

func TestAPaymentDueDayNeedsAClosingDay(t *testing.T) {
	_, err := model.NewAccount(model.NewAccountParams{
		ID: uuid.Must(uuid.NewV7()), Name: "卡", Kind: model.AccountKindCard, PaymentDueDay: new(20),
	})

	if !errors.Is(err, model.ErrDueDayWithoutClosingDay) {
		t.Fatalf("err = %v, want ErrDueDayWithoutClosingDay", err)
	}
}

func TestADayOfTheMonthIsFrom1To31(t *testing.T) {
	for _, day := range []int{0, -1, 32, 100} {
		_, err := model.NewAccount(model.NewAccountParams{
			ID: uuid.Must(uuid.NewV7()), Name: "卡", Kind: model.AccountKindCard, ClosingDay: &day,
		})
		if !errors.Is(err, model.ErrDayOutOfRange) || !strings.Contains(err.Error(), "closing day") {
			t.Errorf("a closing day of %d: err = %v, want ErrDayOutOfRange naming the closing day", day, err)
		}

		_, err = model.NewAccount(model.NewAccountParams{
			ID: uuid.Must(uuid.NewV7()), Name: "卡", Kind: model.AccountKindCard, ClosingDay: new(5), PaymentDueDay: &day,
		})
		if !errors.Is(err, model.ErrDayOutOfRange) || !strings.Contains(err.Error(), "payment due day") {
			t.Errorf("a payment due day of %d: err = %v, want ErrDayOutOfRange naming the payment due day", day, err)
		}
	}
}

func TestWithStatementDaysSetsChangesAndClearsBothDaysTogether(t *testing.T) {
	card := newCard(t, "卡", nil, nil)

	set, err := card.WithStatementDays(new(5), new(20))
	if err != nil {
		t.Fatalf("WithStatementDays: %v", err)
	}
	wantDays(t, set, new(5), new(20))
	wantDays(t, card, nil, nil) // the card it was called on is as it was

	changed, err := set.WithStatementDays(new(10), new(25))
	if err != nil {
		t.Fatalf("WithStatementDays to other days: %v", err)
	}
	wantDays(t, changed, new(10), new(25))

	// The days are replaced as a pair: a nil clears the day it stands for.
	onlyClosing, err := changed.WithStatementDays(new(10), nil)
	if err != nil {
		t.Fatalf("WithStatementDays with a closing day alone: %v", err)
	}
	wantDays(t, onlyClosing, new(10), nil)

	cleared, err := changed.WithStatementDays(nil, nil)
	if err != nil {
		t.Fatalf("WithStatementDays with no days: %v", err)
	}
	wantDays(t, cleared, nil, nil)
	if cleared.ID != card.ID || cleared.Name != card.Name || cleared.Kind != model.AccountKindCard {
		t.Errorf("account = %+v, want the same account", cleared)
	}
}

func TestWithStatementDaysRefusesWhatAnAccountCannotHaveAndLeavesTheAccountAlone(t *testing.T) {
	card := newCard(t, "卡", new(5), new(20))
	cash := newCash(t, "現金")
	tests := []struct {
		name               string
		account            model.Account
		closingDay, dueDay *int
		want               error
	}{
		{"a day on a cash account", cash, new(5), nil, model.ErrNotACard},
		{"a due day on a cash account", cash, nil, new(5), model.ErrNotACard},
		{"a due day without a closing day on a card", card, nil, new(20), model.ErrDueDayWithoutClosingDay},
		{"a closing day of 0", card, new(0), nil, model.ErrDayOutOfRange},
		{"a closing day of 32", card, new(32), nil, model.ErrDayOutOfRange},
		{"a due day of 0", card, new(5), new(0), model.ErrDayOutOfRange},
		{"a due day of 32", card, new(5), new(32), model.ErrDayOutOfRange},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.account

			_, err := tt.account.WithStatementDays(tt.closingDay, tt.dueDay)

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.account.ClosingDay != before.ClosingDay || tt.account.PaymentDueDay != before.PaymentDueDay {
				t.Errorf("the account was changed by a call that was refused")
			}
		})
	}
}

func TestClearingTheDaysOfAnAccountThatCannotHaveThemChangesNothing(t *testing.T) {
	cash := newCash(t, "現金")

	got, err := cash.WithStatementDays(nil, nil)
	if err != nil {
		t.Fatalf("WithStatementDays: %v", err)
	}
	wantDays(t, got, nil, nil)
}

func TestACardHasStatementsWhenItHasAClosingDay(t *testing.T) {
	tests := []struct {
		name    string
		account model.Account
		want    bool
	}{
		{"a card with both days", newCard(t, "卡", new(5), new(20)), true},
		{"a card with a closing day", newCard(t, "卡", new(31), nil), true},
		{"a card with no days", newCard(t, "卡", nil, nil), false},
		{"cash", newCash(t, "現金"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.account.HasStatements(); got != tt.want {
				t.Fatalf("HasStatements() = %v, want %v", got, tt.want)
			}
		})
	}
}
