package model_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// chargesWorld is the ids the tests of the charges of a card write transactions
// with: a card, another account, two instruments, and top-level categories with a
// subcategory under one of them.
type chargesWorld struct {
	card, cash         uuid.UUID
	twd, usd           uuid.UUID
	food, fees, rent   uuid.UUID
	lunch              uuid.UUID // a subcategory of food
	interest, someones uuid.UUID
}

func newChargesWorld() chargesWorld {
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	return chargesWorld{
		card: id(), cash: id(), twd: id(), usd: id(),
		food: id(), fees: id(), rent: id(), lunch: id(), interest: id(), someones: id(),
	}
}

// topLevel says that lunch is under food and every other category is top-level.
func (w chargesWorld) topLevel(category uuid.UUID) uuid.UUID {
	if category == w.lunch {
		return w.food
	}
	return category
}

func (w chargesWorld) names() map[uuid.UUID]string {
	return map[uuid.UUID]string{
		w.food: "food", w.fees: "fees", w.rent: "rent", w.lunch: "lunch", w.interest: "interest",
		w.twd: "TWD", w.usd: "USD",
	}
}

func posting(book model.Book, account, category *uuid.UUID, instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: book, AccountID: account, CategoryID: category, InstrumentID: instrument, Quantity: dec(quantity)}
}

func (w chargesWorld) onCard(instrument uuid.UUID, quantity string) model.Posting {
	return posting(model.BookAccount, &w.card, nil, instrument, quantity)
}

func (w chargesWorld) onCash(instrument uuid.UUID, quantity string) model.Posting {
	return posting(model.BookAccount, &w.cash, nil, instrument, quantity)
}

func expenseLine(category uuid.UUID, instrument uuid.UUID, quantity string) model.Posting {
	return posting(model.BookExpense, nil, &category, instrument, quantity)
}

func (w chargesWorld) share(instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: model.BookDealing, InstrumentID: instrument, Quantity: dec(quantity), PartyID: &w.someones}
}

func txn(kind model.Kind, postings ...model.Posting) model.Transaction {
	return model.Transaction{ID: uuid.Must(uuid.NewV7()), Kind: kind, Status: model.StatusConfirmed, Postings: postings}
}

// charges prints what the card was charged, one line for each instrument, as
// "TWD: food 40, fees 10 | shares 50 | other 0", and checks that it adds up to the
// amount that the postings to the card take.
func (w chargesWorld) charges(t *testing.T, transactions ...model.Transaction) []string {
	t.Helper()
	names := w.names()
	var lines []string
	got := model.ChargesOfCard(w.card, transactions, w.topLevel)
	for _, c := range got {
		var categories []string
		for _, category := range c.Categories {
			categories = append(categories, fmt.Sprintf("%s %s", names[category.CategoryID], category.Amount))
		}
		lines = append(lines, fmt.Sprintf("%s: %s | shares %s | other %s", names[c.InstrumentID], strings.Join(categories, ", "), c.Shares, c.Other))

		taken := decimal.Zero
		for _, tx := range transactions {
			for _, p := range tx.Postings {
				if p.Book == model.BookAccount && *p.AccountID == w.card && p.InstrumentID == c.InstrumentID && p.Quantity.IsNegative() {
					taken = taken.Add(p.Quantity.Neg())
				}
			}
		}
		if !c.Total().Equal(taken) {
			t.Errorf("%s: the charges add up to %s, but the card was charged %s", names[c.InstrumentID], c.Total(), taken)
		}
	}
	return lines
}

func wantCharges(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("charges =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestAnExpenseIsChargedToItsCategoriesAndWhatWasPaidForOthersIsShares(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, txn(model.KindExpense,
		w.onCard(w.twd, "-100"),
		expenseLine(w.food, w.twd, "40"),
		expenseLine(w.fees, w.twd, "10"),
		w.share(w.twd, "50"),
	))

	wantCharges(t, got, "TWD: food 40, fees 10 | shares 50 | other 0")
}

func TestASubcategoryIsChargedToItsTopLevelCategoryAndTheLinesOfSeveralTransactionsAddUp(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t,
		txn(model.KindExpense, w.onCard(w.twd, "-120"), expenseLine(w.lunch, w.twd, "120")),
		txn(model.KindExpense, w.onCard(w.twd, "-30"), expenseLine(w.food, w.twd, "30")),
		txn(model.KindExpense, w.onCard(w.twd, "-500"), expenseLine(w.rent, w.twd, "500")),
	)
	wantCharges(t, got, "TWD: rent 500, food 150 | shares 0 | other 0")
}

func TestTheFeeOfATransferOutOfTheCardIsItsOwnCategoryAndTheTransferIsOther(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, txn(model.KindTransfer,
		w.onCard(w.twd, "-1015"),
		w.onCash(w.twd, "1000"),
		expenseLine(w.fees, w.twd, "15"),
	))

	wantCharges(t, got, "TWD: fees 15 | shares 0 | other 1000")
}

func TestTheInterestOfAnInstalmentPeriodIsItsOwnCategoryAndTheirPrincipalIsOther(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, txn(model.KindInstalment,
		w.onCard(w.twd, "-1010"),
		posting(model.BookInstalment, &w.card, nil, w.twd, "1000"),
		expenseLine(w.interest, w.twd, "10"),
	))

	// The instalment posting is to the card too, but it is the principal that falls due
	// and not a charge of its own.
	wantCharges(t, got, "TWD: interest 10 | shares 0 | other 1000")
}

func TestATransactionPaidWithTheCardAndSomethingElseChargesTheCardItsPartOnly(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, txn(model.KindExpense,
		w.onCard(w.twd, "-60"),
		w.onCash(w.twd, "-40"),
		expenseLine(w.food, w.twd, "70"),
		expenseLine(w.fees, w.twd, "30"),
	))

	// The lines are taken in the order of the postings until the charge is used up: no
	// more than the card was charged is for anything, and nothing is left over.
	wantCharges(t, got, "TWD: food 60 | shares 0 | other 0")
}

func TestWhatNoLineExplainsIsOtherAndALineIsNotTakenForMoreThanIsLeft(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t,
		txn(model.KindExpense, w.onCard(w.twd, "-100"), expenseLine(w.food, w.twd, "30")),
		txn(model.KindExpense, w.onCard(w.twd, "-100"), w.share(w.twd, "50"), expenseLine(w.food, w.twd, "60")),
	)

	// The first charge has 70 that no line explains. In the second the share comes first
	// and takes 50, and the 60 of food is taken for the 50 that are left.
	wantCharges(t, got, "TWD: food 80 | shares 50 | other 70")
}

func TestWhatCreditsTheCardIsNoCharge(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t,
		txn(model.KindTransfer, w.onCard(w.twd, "500"), w.onCash(w.twd, "-500")),
		txn(model.KindReward, w.onCard(w.twd, "137")),
		txn(model.KindExpense, w.onCard(w.twd, "-80"), expenseLine(w.food, w.twd, "80")),
	)

	wantCharges(t, got, "TWD: food 80 | shares 0 | other 0")

	if none := model.ChargesOfCard(w.card, []model.Transaction{txn(model.KindReward, w.onCard(w.twd, "137"))}, w.topLevel); len(none) != 0 {
		t.Errorf("a card that was only credited has charges %+v, want none", none)
	}
}

func TestEachInstrumentIsChargedOnItsOwnAndTheTransactionsOfAnotherAccountAreNotCounted(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t,
		txn(model.KindExpense, w.onCard(w.usd, "-20"), expenseLine(w.food, w.usd, "20")),
		txn(model.KindExpense, w.onCard(w.twd, "-100"), expenseLine(w.rent, w.twd, "100")),
		txn(model.KindExpense, w.onCash(w.twd, "-999"), expenseLine(w.food, w.twd, "999")),
	)

	if len(got) != 2 {
		t.Fatalf("charges = %q, want one line for each of the two instruments", got)
	}
	for _, line := range []string{"USD: food 20 | shares 0 | other 0", "TWD: rent 100 | shares 0 | other 0"} {
		if !strings.Contains(strings.Join(got, "\n"), line) {
			t.Errorf("charges = %q, want %q among them", got, line)
		}
	}
}

func TestTheChargesAreTheSameEveryTime(t *testing.T) {
	w := newChargesWorld()
	transactions := []model.Transaction{
		txn(model.KindExpense, w.onCard(w.twd, "-10"), expenseLine(w.food, w.twd, "10")),
		txn(model.KindExpense, w.onCard(w.twd, "-10"), expenseLine(w.rent, w.twd, "10")),
		txn(model.KindExpense, w.onCard(w.twd, "-10"), expenseLine(w.fees, w.twd, "10")),
		txn(model.KindExpense, w.onCard(w.usd, "-1"), expenseLine(w.fees, w.usd, "1")),
	}
	first := fmt.Sprintf("%+v", model.ChargesOfCard(w.card, transactions, w.topLevel))
	for range 20 {
		if again := fmt.Sprintf("%+v", model.ChargesOfCard(w.card, transactions, w.topLevel)); again != first {
			t.Fatalf("charges differ between calls:\n%s\n%s", first, again)
		}
	}
}
