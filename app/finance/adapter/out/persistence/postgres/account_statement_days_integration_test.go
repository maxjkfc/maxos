package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/internal/testpg"
)

// insertCard stores a card with the days, which the model checks first, and
// returns it as stored.
func insertCard(t *testing.T, r *postgres.AccountRepo, name string, closingDay, dueDay *int) model.Account {
	t.Helper()
	card, err := model.NewAccount(model.NewAccountParams{
		ID: uuid.Must(uuid.NewV7()), Name: name, Kind: model.AccountKindCard, ClosingDay: closingDay, PaymentDueDay: dueDay,
	})
	if err != nil {
		t.Fatalf("NewAccount: %v", err)
	}
	stored, err := r.Insert(context.Background(), card)
	if err != nil {
		t.Fatalf("Insert card %q: %v", name, err)
	}
	return stored
}

func dayString(d *int) string {
	if d == nil {
		return "none"
	}
	return fmt.Sprint(*d)
}

func wantAccountDays(t *testing.T, what string, a model.Account, closingDay, dueDay *int) {
	t.Helper()
	same := func(got, want *int) bool { return (got == nil) == (want == nil) && (got == nil || *got == *want) }
	if !same(a.ClosingDay, closingDay) || !same(a.PaymentDueDay, dueDay) {
		t.Errorf("%s: closing day %s, payment due day %s, want %s and %s", what,
			dayString(a.ClosingDay), dayString(a.PaymentDueDay), dayString(closingDay), dayString(dueDay))
	}
}

func TestAccountRoundTripsItsClosingDayAndPaymentDueDay(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	both := insertCard(t, r, "卡甲", new(5), new(20))
	closingOnly := insertCard(t, r, "卡乙", new(31), nil)
	neither := insertCard(t, r, "卡丙", nil, nil)
	cash := insertAccount(t, r, "錢包")

	for _, tt := range []struct {
		account            model.Account
		closingDay, dueDay *int
	}{
		{both, new(5), new(20)},
		{closingOnly, new(31), nil},
		{neither, nil, nil},
		{cash, nil, nil},
	} {
		wantAccountDays(t, "Insert of "+tt.account.Name, tt.account, tt.closingDay, tt.dueDay)
		got, err := r.Get(ctx, tt.account.ID)
		if err != nil {
			t.Fatalf("Get %s: %v", tt.account.Name, err)
		}
		wantAccountDays(t, "Get of "+tt.account.Name, got, tt.closingDay, tt.dueDay)
	}
	listed, err := r.List(ctx, false)
	if err != nil || len(listed) != 4 {
		t.Fatalf("List = %d accounts, %v, want the 4", len(listed), err)
	}
	for _, a := range listed {
		switch a.ID {
		case both.ID:
			wantAccountDays(t, "List of "+a.Name, a, new(5), new(20))
		case closingOnly.ID:
			wantAccountDays(t, "List of "+a.Name, a, new(31), nil)
		default:
			wantAccountDays(t, "List of "+a.Name, a, nil, nil)
		}
	}
	archived, err := r.Archive(ctx, both.ID)
	if err != nil {
		t.Fatalf("Archive: %v", err)
	}
	wantAccountDays(t, "Archive", archived, new(5), new(20))
}

func TestSetStatementDaysReplacesBothDaysAndAClearedDayIsNull(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	card := insertCard(t, r, "卡", nil, nil)

	for _, step := range []struct {
		name               string
		closingDay, dueDay *int
	}{
		{"set", new(5), new(20)},
		{"changed", new(31), new(1)},
		{"a closing day alone", new(10), nil},
		{"cleared", nil, nil},
		{"set again", new(1), new(31)},
	} {
		got, err := r.SetStatementDays(ctx, card.ID, step.closingDay, step.dueDay)
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		wantAccountDays(t, step.name+" (returned)", got, step.closingDay, step.dueDay)
		if got.ID != card.ID || got.Name != "卡" || got.Kind != model.AccountKindCard {
			t.Errorf("%s: account = %+v, want the same card", step.name, got)
		}
		read, err := r.Get(ctx, card.ID)
		if err != nil {
			t.Fatalf("Get after %s: %v", step.name, err)
		}
		wantAccountDays(t, step.name+" (read back)", read, step.closingDay, step.dueDay)
	}

	if _, err := r.SetStatementDays(ctx, uuid.Must(uuid.NewV7()), new(5), nil); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("SetStatementDays of an id that is not there: err = %v, want ErrNotFound", err)
	}
}

func TestSetStatementDaysKeepsTheArchiveAndTheNameOfAnArchivedCard(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	card := insertCard(t, r, "卡", new(5), new(20))
	archived, err := r.Archive(ctx, card.ID)
	if err != nil || archived.ArchivedAt == nil {
		t.Fatalf("Archive = %+v, %v", archived, err)
	}

	got, err := r.SetStatementDays(ctx, card.ID, new(8), new(25))

	if err != nil {
		t.Fatalf("SetStatementDays: %v", err)
	}
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(*archived.ArchivedAt) {
		t.Errorf("archived at %v, want the time it was archived, %v", got.ArchivedAt, archived.ArchivedAt)
	}
	wantAccountDays(t, "SetStatementDays", got, new(8), new(25))
}

func TestTheTableChecksTheClosingDayAndThePaymentDueDay(t *testing.T) {
	ctx := context.Background()
	pool := testpg.New(t)
	insert := func(kind string, closingDay, dueDay *int) error {
		_, err := pool.Exec(ctx, `INSERT INTO fin_account (id, name, kind, closing_day, payment_due_day) VALUES ($1, $2, $3, $4, $5)`,
			uuid.Must(uuid.NewV7()), fmt.Sprintf("%s %s %s", kind, dayString(closingDay), dayString(dueDay)), kind, closingDay, dueDay)
		return err
	}

	refused := []struct {
		name               string
		kind               string
		closingDay, dueDay *int
		constraint         string
	}{
		{"a closing day of 0", "card", new(0), nil, "fin_account_closing_day_check"},
		{"a closing day of 32", "card", new(32), nil, "fin_account_closing_day_check"},
		{"a negative closing day", "card", new(-1), nil, "fin_account_closing_day_check"},
		{"a payment due day of 0", "card", new(5), new(0), "fin_account_payment_due_day_check"},
		{"a payment due day of 32", "card", new(5), new(32), "fin_account_payment_due_day_check"},
		{"a payment due day with no closing day", "card", nil, new(20), "fin_account_payment_due_day_closing_day_check"},
		{"a closing day on a bank account", "bank", new(5), nil, "fin_account_statement_days_kind_check"},
		{"a closing day on cash", "cash", new(5), nil, "fin_account_statement_days_kind_check"},
		{"a closing day and a payment due day on a wallet", "wallet", new(5), new(20), "fin_account_statement_days_kind_check"},
		{"a closing day on a loan", "loan", new(31), nil, "fin_account_statement_days_kind_check"},
	}
	for _, tt := range refused {
		t.Run(tt.name, func(t *testing.T) {
			wantConstraint(t, insert(tt.kind, tt.closingDay, tt.dueDay), checkViolation, tt.constraint)
		})
	}

	// The ends of the range are accepted, and so is a card with no day.
	for _, tt := range []struct {
		name               string
		kind               string
		closingDay, dueDay *int
	}{
		{"the first day", "card", new(1), new(1)},
		{"the last day a month can have", "card", new(31), new(31)},
		{"a closing day alone", "card", new(15), nil},
		{"a card with no days", "card", nil, nil},
		{"a bank account with no days", "bank", nil, nil},
	} {
		if err := insert(tt.kind, tt.closingDay, tt.dueDay); err != nil {
			t.Errorf("%s: %v, want it stored", tt.name, err)
		}
	}
}

func TestChangingTheDaysOfAnAccountIsCheckedByTheTableToo(t *testing.T) {
	ctx := context.Background()
	r := postgres.NewAccountRepo(testpg.New(t))
	card := insertCard(t, r, "卡", new(5), new(20))
	cash := insertAccount(t, r, "錢包")

	// The calls skip the model on purpose: the table is the last guard.
	_, err := r.SetStatementDays(ctx, cash.ID, new(5), nil)
	wantConstraint(t, err, checkViolation, "fin_account_statement_days_kind_check")
	_, err = r.SetStatementDays(ctx, card.ID, new(32), nil)
	wantConstraint(t, err, checkViolation, "fin_account_closing_day_check")
	_, err = r.SetStatementDays(ctx, card.ID, nil, new(20))
	wantConstraint(t, err, checkViolation, "fin_account_payment_due_day_closing_day_check")

	got, err := r.Get(ctx, card.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	wantAccountDays(t, "the card after the refused changes", got, new(5), new(20))
	if got, err := r.Get(ctx, cash.ID); err != nil || got.ClosingDay != nil {
		t.Errorf("cash = %+v, %v, want no day", got, err)
	}
}
