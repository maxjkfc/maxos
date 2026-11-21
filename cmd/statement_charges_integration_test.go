package cmd_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// What a card was charged for on a statement, through the real use cases on a real
// PostgreSQL, against what GetSpending says was spent on the same things: a fee, the
// interest of an instalment, an expense of a subcategory are for the same top-level
// category on the statement as they are in the spending of the month.

func TestTheChargesOfAStatementAreForTheCategoriesThatTheMonthSpentOn(t *testing.T) {
	ctx := context.Background()
	w := newSpendingWorld(t) // it is the 14th of November 2026
	w.cardDays(new(5), new(20))
	alice, err := w.CreateParty.CreateParty(ctx, catalogusecase.CreatePartyInput{Name: "Alice"})
	if err != nil {
		t.Fatalf("CreateParty: %v", err)
	}
	w.names[alice.ID] = "Alice"

	// On the statement of the 6th of October to the 5th of November:
	// a lunch, which is of Lunch, a subcategory of Food;
	w.spend(dateOn(2026, time.October, 10), nil, nil, w.card, w.lunch, "120")
	// a dinner of Food, of which 200 was for Alice;
	d := w.details(w.key())
	d.OccurredOn = dateOn(2026, time.October, 12)
	dinner, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:  []usecase.CategoryLine{{CategoryID: w.food, Amount: dec("300")}},
		Shares: []usecase.Share{{PartyID: alice.ID, Amount: dec("200")}},
	})
	if err != nil {
		t.Fatalf("RecordExpense: %v", err)
	}
	w.confirm(t, dinner.Transaction)
	// 1,000 taken out of the card as cash, and the fee of 15 for it;
	d = w.details(w.key())
	d.OccurredOn = dateOn(2026, time.October, 15)
	advance, err := w.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.card, ToAccountID: w.cash, InstrumentID: model.BaseCurrencyID, Amount: dec("1000"),
		Fees: []usecase.Fee{{AccountID: w.card, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec("15")}},
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	w.confirm(t, advance.Transaction)
	// the first period of a purchase in instalments, bought in September: 1,000 of principal
	// and 50 of interest, which fell due on the 20th;
	d = w.details(w.key())
	d.OccurredOn = dateOn(2026, time.September, 20)
	var periods []usecase.InstalmentPeriod
	for i := range 3 {
		periods = append(periods, usecase.InstalmentPeriod{DueOn: dateOn(2026, time.Month(10+i), 20), Principal: dec("1000"), Interest: new(dec("50"))})
	}
	plan, err := w.RecordExpense.RecordExpense(ctx, usecase.RecordExpenseInput{
		RecordDetails: d, AccountID: &w.card, InstrumentID: model.BaseCurrencyID,
		Lines:       []usecase.CategoryLine{{CategoryID: w.rent, Amount: dec("3000")}},
		Instalments: &usecase.InstalmentSchedule{Periods: periods, InterestCategoryID: &w.interest},
	})
	if err != nil {
		t.Fatalf("RecordExpense in instalments: %v", err)
	}
	w.confirm(t, plan.Transaction)
	// and what is not charged on it: a payment into the card, a draft, and a purchase
	// that belongs to the next statement.
	d = w.details(w.key())
	d.OccurredOn = dateOn(2026, time.October, 22)
	payment, err := w.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.cash, ToAccountID: w.card, InstrumentID: model.BaseCurrencyID, Amount: dec("400"),
	})
	if err != nil {
		t.Fatalf("RecordTransfer: %v", err)
	}
	w.confirm(t, payment.Transaction)
	w.expense(dateOn(2026, time.October, 11), nil, nil, w.card, w.rent, "999") // a draft
	w.spend(dateOn(2026, time.November, 6), nil, nil, w.card, w.fun, "777")

	statement := w.readStatement(new(dateOn(2026, time.October, 25)))
	spent := w.spendingOf(dateOn(2026, time.October, 1))

	// What the statement says the card was charged for, in the order it says it.
	var lines []string
	if len(statement.Charges) != 1 || statement.Charges[0].InstrumentID != model.BaseCurrencyID {
		t.Fatalf("charges = %+v, want those of the base currency only", statement.Charges)
	}
	c := statement.Charges[0]
	for _, category := range c.Categories {
		lines = append(lines, fmt.Sprintf("%s %s", w.names[category.CategoryID], category.Amount))
	}
	want := []string{"Food 420", "Interest 50", "Fees 15"}
	if strings.Join(lines, ", ") != strings.Join(want, ", ") || !c.Shares.Equal(dec("200")) || !c.Other.Equal(dec("2000")) {
		t.Errorf("charges = %v, shares %s, other %s; want %v, shares 200, other 2000 (the 1,000 taken out, and the 1,000 of principal)", lines, c.Shares, c.Other, want)
	}

	// They are the charges of the figures, divided up and nothing over.
	total := c.Shares.Add(c.Other)
	byCategory := map[uuid.UUID]decimal.Decimal{}
	for _, category := range c.Categories {
		total = total.Add(category.Amount)
		byCategory[category.CategoryID] = category.Amount
	}
	if len(statement.Figures) != 1 || !total.Equal(statement.Figures[0].Charges) || !total.Equal(dec("2685")) {
		t.Errorf("the charges add up to %s, the figures say %+v, want both 2,685", total, statement.Figures)
	}

	// The same things are the same categories as in the spending of the month: the fee, the
	// interest and the food of the lunch and of the dinner, which is what the card was
	// charged for them, whatever the instrument paid.
	for _, top := range spent.TopLevel {
		if got, ok := byCategory[top.CategoryID]; !ok || !got.Equal(top.Total) {
			t.Errorf("%s: the statement charged %s for it, and the month spent %s on it", w.names[top.CategoryID], got, top.Total)
		}
	}
	if len(spent.TopLevel) != len(byCategory) {
		t.Errorf("the month spent on %d categories (%s), the statement was charged for %d", len(spent.TopLevel), strings.Join(w.categoryLines(spent), "; "), len(byCategory))
	}
}
