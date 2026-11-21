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
// category on the statement as they are in the spending of the month. What is not
// spent is not for a category: money lent with the card is Other, and so is a
// transfer out of the card, whose fee is the card's only when the card paid it.

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
	// 700 lent to Alice with the card, which is a dealing and no share of what was paid for her;
	d = w.details(w.key())
	d.OccurredOn = dateOn(2026, time.October, 18)
	loan, err := w.RecordDealing.RecordDealing(ctx, usecase.RecordDealingInput{
		RecordDetails: d, AccountID: w.card, PartyID: alice.ID, InstrumentID: model.BaseCurrencyID, Amount: dec("700"), Direction: "paid_out",
	})
	if err != nil {
		t.Fatalf("RecordDealing: %v", err)
	}
	w.confirm(t, loan.Transaction)
	// 500 from the card to the bank, with a fee of 10 that the card paid and another of 10 that the
	// bank paid, of a category of its own: the same amount, so that only the order of the postings
	// says which fee is whose;
	bankFees, err := w.CreateCategory.CreateCategory(ctx, usecase.CreateCategoryInput{Direction: "expense", Name: "Bank fees"})
	if err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	w.names[bankFees.ID] = "Bank fees"
	d = w.details(w.key())
	d.OccurredOn = dateOn(2026, time.October, 19)
	toBank, err := w.RecordTransfer.RecordTransfer(ctx, usecase.RecordTransferInput{
		RecordDetails: d, FromAccountID: w.card, ToAccountID: w.bank, InstrumentID: model.BaseCurrencyID, Amount: dec("500"),
		Fees: []usecase.Fee{
			{AccountID: w.card, InstrumentID: model.BaseCurrencyID, CategoryID: w.fees, Amount: dec("10")},
			{AccountID: w.bank, InstrumentID: model.BaseCurrencyID, CategoryID: bankFees.ID, Amount: dec("10")},
		},
	})
	if err != nil {
		t.Fatalf("RecordTransfer to the bank: %v", err)
	}
	w.confirm(t, toBank.Transaction)
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
	want := []string{"Food 420", "Interest 50", "Fees 25"}
	if strings.Join(lines, ", ") != strings.Join(want, ", ") || !c.Shares.Equal(dec("200")) || !c.Other.Equal(dec("3200")) {
		t.Errorf("charges = %v, shares %s, other %s; want %v, shares 200, other 3200 (the 1,000 taken out, the 1,000 of principal, the 700 lent and the 500 sent to the bank)",
			lines, c.Shares, c.Other, want)
	}

	// They are the charges of the figures, divided up and nothing over.
	total := c.Shares.Add(c.Other)
	byCategory := map[uuid.UUID]decimal.Decimal{}
	for _, category := range c.Categories {
		total = total.Add(category.Amount)
		byCategory[category.CategoryID] = category.Amount
	}
	if len(statement.Figures) != 1 || !total.Equal(statement.Figures[0].Charges) || !total.Equal(dec("3895")) {
		t.Errorf("the charges add up to %s, the figures say %+v, want both 3,895", total, statement.Figures)
	}

	// The same things are the same categories as in the spending of the month: the fee, the
	// interest and the food of the lunch and of the dinner, which is what the card was
	// charged for them, whatever the instrument paid. The month spent on one thing more,
	// which the card was not charged for: the fee that the bank paid.
	for _, top := range spent.TopLevel {
		got, onStatement := byCategory[top.CategoryID]
		switch {
		case top.CategoryID == bankFees.ID:
			if onStatement || !top.Total.Equal(dec("10")) {
				t.Errorf("Bank fees: the statement charged %s for it (%t), and the month spent %s on it, want nothing charged and 10 spent", got, onStatement, top.Total)
			}
		case !onStatement || !got.Equal(top.Total):
			t.Errorf("%s: the statement charged %s for it, and the month spent %s on it", w.names[top.CategoryID], got, top.Total)
		}
	}
	if len(spent.TopLevel) != len(byCategory)+1 {
		t.Errorf("the month spent on %d categories (%s), the statement was charged for %d and the bank's fee is the one more", len(spent.TopLevel), strings.Join(w.categoryLines(spent), "; "), len(byCategory))
	}
}
