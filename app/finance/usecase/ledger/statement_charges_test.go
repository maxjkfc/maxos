package ledger_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// List returns every category there is, archived or not, whatever it is asked: a
// statement needs the parents of the categories to find the top-level one of a line.
func (f *fakeCategories) List(context.Context, model.Direction, bool) ([]model.Category, error) {
	categories := make([]model.Category, 0, len(f.categories))
	for _, c := range f.categories {
		categories = append(categories, c)
	}
	return categories, nil
}

// subcategory adds a category of the direction expense under the top-level one.
func (e *env) subcategory(name string, parent model.Category) model.Category {
	c := model.Category{ID: uuid.Must(uuid.NewV7()), Direction: model.DirectionExpense, Name: name, ParentID: &parent.ID}
	e.categories.categories[c.ID] = c
	return c
}

// recordExpense records the expense, a draft, and fails the test when that is
// refused.
func (e *env) recordExpense(in usecase.RecordExpenseInput) usecase.Transaction {
	e.t.Helper()
	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", in.IdempotencyKey, err)
	}
	return out.Transaction
}

// chargesLines prints what the statement says the card was charged for, one line for
// each instrument, as "TWD: food 420, fees 15 | shares 200 | other 1000".
func (e *env) chargesLines(s usecase.CardStatement) []string {
	names := e.names()
	var lines []string
	for _, c := range s.Charges {
		var categories []string
		for _, category := range c.Categories {
			categories = append(categories, fmt.Sprintf("%s %s", names[category.CategoryID], category.Amount))
		}
		lines = append(lines, fmt.Sprintf("%s: %s | shares %s | other %s", names[c.InstrumentID], strings.Join(categories, ", "), c.Shares, c.Other))
	}
	return lines
}

func TestTheStatementSaysWhatTheCardWasChargedForByTopLevelCategory(t *testing.T) {
	e := newEnv(t)
	e.at(on(2026, time.November, 14))
	lunch := e.subcategory("lunch", e.food)
	bob := e.refs.addParty("Bob", false)

	// A lunch on a subcategory of food, and a dinner of food of which 200 was for Bob.
	e.confirm(e.recordExpense(usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "charges-lunch-01", OccurredOn: on(2026, time.October, 10)},
		AccountID:     &e.card.ID, InstrumentID: e.twd.ID,
		Lines: []usecase.CategoryLine{{CategoryID: lunch.ID, Amount: dec("120")}},
	}))
	e.confirm(e.recordExpense(usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "charges-dinner-01", OccurredOn: on(2026, time.October, 12)},
		AccountID:     &e.card.ID, InstrumentID: e.twd.ID,
		Lines:  []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("300")}},
		Shares: []usecase.Share{{PartyID: bob, Amount: dec("200")}},
	}))
	// 1,000 taken out of the card as cash, with a fee that the card paid too.
	advance, err := e.svc.RecordTransfer(context.Background(), usecase.RecordTransferInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "charges-advance-01", OccurredOn: on(2026, time.October, 15)},
		FromAccountID: e.card.ID, ToAccountID: e.cash.ID, InstrumentID: e.twd.ID, Amount: dec("1000"),
		Fees: []usecase.Fee{{AccountID: e.card.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	e.confirm(advance.Transaction)
	// What is not charged to this statement: a payment to the card, a draft, and a
	// purchase of the next statement.
	e.payCard("charges-payment-01", on(2026, time.October, 20), "400")
	e.cardDraft("charges-draft-0001", on(2026, time.October, 11), "999")
	e.spendOnCard("charges-next-0001", on(2026, time.November, 6), "777")

	got := e.statement(new(on(2026, time.November, 5)))

	want := "TWD: food 420, fees 15 | shares 200 | other 1000"
	if lines := e.chargesLines(got); len(lines) != 1 || lines[0] != want {
		t.Errorf("charges = %q, want %q", lines, want)
	}
	// They are the Charges of the figures, divided up.
	if len(got.Figures) != 1 || len(got.Charges) != 1 {
		t.Fatalf("figures %+v and charges %+v, want one of each: the card was charged in one instrument", got.Figures, got.Charges)
	}
	c := got.Charges[0]
	total := c.Shares.Add(c.Other)
	for _, category := range c.Categories {
		total = total.Add(category.Amount)
	}
	if !total.Equal(got.Figures[0].Charges) || !total.Equal(decimal.RequireFromString("1635")) {
		t.Errorf("the charges add up to %s and the figures say %s, want both 1635", total, got.Figures[0].Charges)
	}
}

func TestAStatementWithNothingChargedHasNoCharges(t *testing.T) {
	e := newEnv(t)
	e.at(on(2026, time.November, 14))
	e.payCard("charges-payment-01", on(2026, time.October, 20), "400")

	got := e.statement(new(on(2026, time.November, 5)))

	if len(got.Charges) != 0 || len(got.Figures) != 1 {
		t.Errorf("charges = %+v with figures %+v, want no charges for a card that was only paid", got.Charges, got.Figures)
	}
}

// chargesAddUp is what the categories, the shares and the rest of the charges of an
// instrument add up to.
func chargesAddUp(c usecase.StatementCharges) decimal.Decimal {
	total := c.Shares.Add(c.Other)
	for _, category := range c.Categories {
		total = total.Add(category.Amount)
	}
	return total
}

func TestWhatTheCardPaidToAPartyIsOtherOnTheStatementAndNotShares(t *testing.T) {
	e := newEnv(t)
	e.at(on(2026, time.November, 14))
	bob := e.refs.addParty("Bob", false)

	// A dinner of food of which 200 was for Bob, which is a share; and 700 lent to Bob
	// with the card, which is a dealing and a share of nothing.
	e.confirm(e.recordExpense(usecase.RecordExpenseInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "charges-dinner-01", OccurredOn: on(2026, time.October, 12)},
		AccountID:     &e.card.ID, InstrumentID: e.twd.ID,
		Lines:  []usecase.CategoryLine{{CategoryID: e.food.ID, Amount: dec("300")}},
		Shares: []usecase.Share{{PartyID: bob, Amount: dec("200")}},
	}))
	loan, err := e.svc.RecordDealing(context.Background(), usecase.RecordDealingInput{
		RecordDetails: usecase.RecordDetails{IdempotencyKey: "charges-loan-0001", OccurredOn: on(2026, time.October, 14)},
		AccountID:     e.card.ID, PartyID: bob, InstrumentID: e.twd.ID, Amount: dec("700"), Direction: "paid_out",
	})
	if err != nil {
		t.Fatalf("RecordDealing: %v", err)
	}
	e.confirm(loan.Transaction)

	got := e.statement(new(on(2026, time.November, 5)))

	want := "TWD: food 300 | shares 200 | other 700"
	if lines := e.chargesLines(got); len(lines) != 1 || lines[0] != want {
		t.Errorf("charges = %q, want %q", lines, want)
	}
	if len(got.Figures) != 1 || len(got.Charges) != 1 {
		t.Fatalf("figures %+v and charges %+v, want one of each: the card was charged in one instrument", got.Figures, got.Charges)
	}
	if total := chargesAddUp(got.Charges[0]); !total.Equal(got.Figures[0].Charges) || !total.Equal(dec("1200")) {
		t.Errorf("the charges add up to %s and the figures say %s, want both 1200", total, got.Figures[0].Charges)
	}
}

func TestAFeeIsChargedToTheCardOnTheStatementOnlyWhenTheCardPaidIt(t *testing.T) {
	tests := []struct {
		name        string
		record      func(e *env) usecase.RecordTransferInput
		want        string
		wantCharged string
	}{
		{
			// 1,000 from the card to the bank: a fee of 10 that the card paid, and one of 15 that
			// the bank paid, both of fees. The card was charged 1,010.
			"a transfer out of the card with a fee that the bank paid",
			func(e *env) usecase.RecordTransferInput {
				return usecase.RecordTransferInput{
					FromAccountID: e.card.ID, ToAccountID: e.bank.ID, InstrumentID: e.twd.ID, Amount: dec("1000"),
					Fees: []usecase.Fee{
						{AccountID: e.card.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("10")},
						{AccountID: e.bank.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")},
					},
				}
			},
			"TWD: fees 10 | shares 0 | other 1000", "1010",
		},
		{
			// A transfer between the bank and cash is on the statement for the fee that the card
			// paid, and is nothing else of the card's.
			"a transfer between other accounts with a fee that the card paid",
			func(e *env) usecase.RecordTransferInput {
				return usecase.RecordTransferInput{
					FromAccountID: e.bank.ID, ToAccountID: e.cash.ID, InstrumentID: e.twd.ID, Amount: dec("1000"),
					Fees: []usecase.Fee{
						{AccountID: e.card.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("15")},
						{AccountID: e.bank.ID, InstrumentID: e.twd.ID, CategoryID: e.fees.ID, Amount: dec("10")},
					},
				}
			},
			"TWD: fees 15 | shares 0 | other 0", "15",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t)
			e.at(on(2026, time.November, 14))
			in := tt.record(e)
			in.RecordDetails = usecase.RecordDetails{IdempotencyKey: "charges-transfer-01", OccurredOn: on(2026, time.October, 15)}
			out, err := e.svc.RecordTransfer(context.Background(), in)
			if err != nil {
				t.Fatalf("RecordTransfer: %v", err)
			}
			e.confirm(out.Transaction)

			got := e.statement(new(on(2026, time.November, 5)))

			if lines := e.chargesLines(got); len(lines) != 1 || lines[0] != tt.want {
				t.Errorf("charges = %q, want %q", lines, tt.want)
			}
			if len(got.Figures) != 1 || len(got.Charges) != 1 {
				t.Fatalf("figures %+v and charges %+v, want one of each: the card was charged in one instrument", got.Figures, got.Charges)
			}
			if total := chargesAddUp(got.Charges[0]); !total.Equal(got.Figures[0].Charges) || !total.Equal(dec(tt.wantCharged)) {
				t.Errorf("the charges add up to %s and the figures say %s, want both %s", total, got.Figures[0].Charges, tt.wantCharged)
			}
		})
	}
}
