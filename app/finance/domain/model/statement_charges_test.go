package model_test

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// chargesWorld is the ids the tests of the charges of a card write transactions
// with: a card, two other accounts, two instruments, a party, and top-level
// categories with a subcategory under one of them.
type chargesWorld struct {
	card, bank, cash uuid.UUID
	twd, usd         uuid.UUID
	food, fees, rent uuid.UUID
	lunch            uuid.UUID // a subcategory of food
	interest, salary uuid.UUID
	someones         uuid.UUID
}

func newChargesWorld() chargesWorld {
	id := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	return chargesWorld{
		card: id(), bank: id(), cash: id(), twd: id(), usd: id(),
		food: id(), fees: id(), rent: id(), lunch: id(), interest: id(), salary: id(), someones: id(),
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
		w.twd: "TWD", w.usd: "USD", model.UnrecordedExpenseCategoryID: "unrecorded",
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

// withBase gives a posting of an expense, an income or a reward the base amount that
// it needs: the instruments of these tests are not the base currency, so any amount
// of the sign of the quantity will do, and this is the quantity.
func withBase(p model.Posting) model.Posting {
	base := p.Quantity
	p.BaseAmount = &base
	return p
}

func expenseLine(category uuid.UUID, instrument uuid.UUID, quantity string) model.Posting {
	return withBase(posting(model.BookExpense, nil, &category, instrument, quantity))
}

// owed is a dealing posting: what the party owes me changes by the quantity.
func (w chargesWorld) owed(instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: model.BookDealing, InstrumentID: instrument, Quantity: dec(quantity), PartyID: &w.someones}
}

// share is what I paid for the party in an expense that I paid.
func (w chargesWorld) share(instrument uuid.UUID, quantity string) model.Posting {
	return w.owed(instrument, quantity)
}

func txn(kind model.Kind, postings ...model.Posting) model.Transaction {
	return model.Transaction{ID: uuid.Must(uuid.NewV7()), Kind: kind, Status: model.StatusConfirmed, Postings: postings}
}

// The transactions below are laid out as the constructors of the model lay them out:
// the postings the kind is made of, and then each fee as the account posting that
// pays it followed by its expense posting. valid says that they are ones that the
// ledger holds.

// on is an account posting of any account.
func (w chargesWorld) on(account, instrument uuid.UUID, quantity string) model.Posting {
	return posting(model.BookAccount, &account, nil, instrument, quantity)
}

// feeIn is the two postings of a fee that the account paid, in the instrument.
func (w chargesWorld) feeIn(instrument, account, category uuid.UUID, amount string) []model.Posting {
	return []model.Posting{w.on(account, instrument, "-"+amount), expenseLine(category, instrument, amount)}
}

// fee is the two postings of a fee that the account paid, in TWD.
func (w chargesWorld) fee(account, category uuid.UUID, amount string) []model.Posting {
	return w.feeIn(w.twd, account, category, amount)
}

// laidOut is a transaction of the kind made of its own postings, and then of its fees.
func laidOut(kind model.Kind, own []model.Posting, fees ...[]model.Posting) model.Transaction {
	postings := own
	for _, fee := range fees {
		postings = append(postings, fee...)
	}
	return txn(kind, postings...)
}

// transfer is an amount of TWD that leaves one account and arrives at another.
func (w chargesWorld) transfer(from, to uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	return laidOut(model.KindTransfer, []model.Posting{w.on(from, w.twd, "-"+amount), w.on(to, w.twd, amount)}, fees...)
}

// lend is an amount of TWD that I paid out of the account to the party, so that it
// owes me that much more: a dealing.
func (w chargesWorld) lend(account uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	return laidOut(model.KindDealing, []model.Posting{w.on(account, w.twd, "-"+amount), w.owed(w.twd, amount)}, fees...)
}

// repaid is an amount of TWD that the party paid into the account, so that it owes me
// that much less: a dealing.
func (w chargesWorld) repaid(account uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	return laidOut(model.KindDealing, []model.Posting{w.on(account, w.twd, amount), w.owed(w.twd, "-"+amount)}, fees...)
}

// income is an amount of TWD of salary that came into the account.
func (w chargesWorld) income(account uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	earned := withBase(posting(model.BookIncome, nil, &w.salary, w.twd, "-"+amount))
	return laidOut(model.KindIncome, []model.Posting{w.on(account, w.twd, amount), earned}, fees...)
}

// reward is an amount of TWD that the account was given back.
func (w chargesWorld) reward(account uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	given := withBase(posting(model.BookReward, nil, nil, w.twd, "-"+amount))
	return laidOut(model.KindReward, []model.Posting{w.on(account, w.twd, amount), given}, fees...)
}

// billInstalment is the origin of a bill instalment: what the account owed is paid
// off, and owed again as principal that is not charged yet.
func (w chargesWorld) billInstalment(account uuid.UUID, amount string, fees ...[]model.Posting) model.Transaction {
	origin := laidOut(model.KindInstalment, []model.Posting{
		w.on(account, w.twd, amount),
		posting(model.BookInstalment, &account, nil, w.twd, "-"+amount),
	}, fees...)
	plan := origin.ID
	origin.PlanID, origin.PlanSeq = &plan, new(0)
	return origin
}

// exchange is 100 USD that one account gives up for 3,000 TWD that arrive at another.
func (w chargesWorld) exchange(from, to uuid.UUID, fees ...[]model.Posting) model.Transaction {
	return laidOut(model.KindExchange, []model.Posting{
		w.on(from, w.usd, "-100"),
		w.on(to, w.twd, "3000"),
		posting(model.BookConversion, nil, nil, w.usd, "100"),
		posting(model.BookConversion, nil, nil, w.twd, "-3000"),
	}, fees...)
}

// instalmentPeriod is a period of an instalment plan as it falls due: the principal,
// which stops being unbilled, and the interest are charged to the account.
func (w chargesWorld) instalmentPeriod(account uuid.UUID, principal, interest string) model.Transaction {
	period := txn(model.KindInstalment,
		posting(model.BookInstalment, &account, nil, w.twd, principal),
		w.on(account, w.twd, dec(principal).Add(dec(interest)).Neg().String()),
		expenseLine(w.interest, w.twd, interest),
	)
	plan := uuid.Must(uuid.NewV7())
	period.PlanID, period.PlanSeq = &plan, new(1)
	return period
}

// valid returns the transactions, and fails the test when one of them is not one that
// the ledger holds. What the card was charged for depends on how a fee is paid, so a
// test that writes a transaction by hand has to write what the constructors would.
func valid(t *testing.T, transactions ...model.Transaction) []model.Transaction {
	t.Helper()
	for _, tx := range transactions {
		if err := tx.Validate(); err != nil {
			t.Fatalf("a %s of the test is not a transaction that the ledger holds: %v", tx.Kind, err)
		}
	}
	return transactions
}

// charges prints what the card was charged, one line for each instrument, as
// "TWD: food 40, fees 10 | shares 50 | other 0" ("-" for no category), and checks
// that it adds up to the amount that the postings to the card take.
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
		if len(categories) == 0 {
			categories = []string{"-"}
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
	got := w.charges(t, valid(t, w.transfer(w.card, w.cash, "1000", w.fee(w.card, w.fees, "15")))...)

	wantCharges(t, got, "TWD: fees 15 | shares 0 | other 1000")
}

func TestTheInterestOfAnInstalmentPeriodIsItsOwnCategoryAndTheirPrincipalIsOther(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, valid(t, w.instalmentPeriod(w.card, "1000", "10"))...)

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

func TestAFeeThatTheOtherAccountOfATransferPaidIsNotWhatTheCardWasChargedFor(t *testing.T) {
	w := newChargesWorld()
	// 1,000 from the card to the bank, with a fee of 10 that the card paid and one of 15
	// that the bank paid, both of the category fees. The card was charged 1,010.
	got := w.charges(t, valid(t, w.transfer(w.card, w.bank, "1000",
		w.fee(w.card, w.fees, "10"),
		w.fee(w.bank, w.fees, "15"),
	))...)

	wantCharges(t, got, "TWD: fees 10 | shares 0 | other 1000")
}

func TestAFeeIsTheCardsOnlyWhenAnAccountPostingToTheCardPaysIt(t *testing.T) {
	w := newChargesWorld()
	tests := []struct {
		name string
		txn  model.Transaction
		want []string // one line for each instrument, in no order; none for no charge
	}{
		{
			"the card pays both fees of a transfer out of it",
			w.transfer(w.card, w.bank, "1000", w.fee(w.card, w.fees, "10"), w.fee(w.card, w.fees, "15")),
			[]string{"TWD: fees 25 | shares 0 | other 1000"},
		},
		{
			"the bank pays both",
			w.transfer(w.card, w.bank, "1000", w.fee(w.bank, w.fees, "10"), w.fee(w.bank, w.fees, "15")),
			[]string{"TWD: - | shares 0 | other 1000"},
		},
		{
			"the bank pays the fee that comes first",
			w.transfer(w.card, w.bank, "1000", w.fee(w.bank, w.fees, "15"), w.fee(w.card, w.fees, "10")),
			[]string{"TWD: fees 10 | shares 0 | other 1000"},
		},
		{
			"the card pays the fee of a transfer between other accounts",
			w.transfer(w.bank, w.cash, "1000", w.fee(w.card, w.fees, "15")),
			[]string{"TWD: fees 15 | shares 0 | other 0"},
		},
		{
			"the accounts of a transfer between them pay its fees",
			w.transfer(w.bank, w.cash, "1000", w.fee(w.bank, w.fees, "15"), w.fee(w.cash, w.fees, "10")),
			nil,
		},
		{
			"the card pays the fee of a payment to it",
			w.transfer(w.bank, w.card, "1000", w.fee(w.card, w.fees, "10")),
			[]string{"TWD: fees 10 | shares 0 | other 0"},
		},
		{
			"the bank pays the fee of a payment to the card",
			w.transfer(w.bank, w.card, "1000", w.fee(w.bank, w.fees, "10")),
			nil,
		},
		{
			"a fee as large as the transfer, with another as large paid by the bank",
			w.transfer(w.card, w.bank, "10", w.fee(w.card, w.fees, "10"), w.fee(w.bank, w.interest, "10")),
			[]string{"TWD: fees 10 | shares 0 | other 10"},
		},
		{
			"fees in another instrument than the transfer",
			w.transfer(w.card, w.bank, "1000", w.feeIn(w.usd, w.card, w.fees, "2"), w.feeIn(w.usd, w.bank, w.fees, "3")),
			[]string{"TWD: - | shares 0 | other 1000", "USD: fees 2 | shares 0 | other 0"},
		},
		{
			"an income into the bank, with a fee that the card paid",
			w.income(w.bank, "5000", w.fee(w.card, w.fees, "20")),
			[]string{"TWD: fees 20 | shares 0 | other 0"},
		},
		{
			"an income into the card, with a fee that the card paid",
			w.income(w.card, "5000", w.fee(w.card, w.fees, "20")),
			[]string{"TWD: fees 20 | shares 0 | other 0"},
		},
		{
			"an income into the card, with a fee that the bank paid",
			w.income(w.card, "5000", w.fee(w.bank, w.fees, "20")),
			nil,
		},
		{
			"a reward to the card, with a fee that the card paid",
			w.reward(w.card, "137", w.fee(w.card, w.fees, "5")),
			[]string{"TWD: fees 5 | shares 0 | other 0"},
		},
		{
			"a reward to the card, with a fee that cash paid",
			w.reward(w.card, "137", w.fee(w.cash, w.fees, "5")),
			nil,
		},
		{
			"the origin of a bill instalment, with a fee that the card paid",
			w.billInstalment(w.card, "1000", w.fee(w.card, w.fees, "30")),
			[]string{"TWD: fees 30 | shares 0 | other 0"},
		},
		{
			"the origin of a bill instalment, with a fee that the bank paid",
			w.billInstalment(w.card, "1000", w.fee(w.bank, w.fees, "30")),
			nil,
		},
		{
			"an exchange out of the card, with a fee in the currency given up that the card paid",
			w.exchange(w.card, w.bank, w.feeIn(w.usd, w.card, w.fees, "5")),
			[]string{"USD: fees 5 | shares 0 | other 100"},
		},
		{
			"an exchange out of the card, with a fee in the currency given up that the bank paid",
			w.exchange(w.card, w.bank, w.feeIn(w.usd, w.bank, w.fees, "5")),
			[]string{"USD: - | shares 0 | other 100"},
		},
		{
			"an exchange out of the card, with a fee in the other currency that the card paid",
			w.exchange(w.card, w.bank, w.fee(w.card, w.fees, "5")),
			[]string{"TWD: fees 5 | shares 0 | other 0", "USD: - | shares 0 | other 100"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.charges(t, valid(t, tt.txn)...)

			slices.Sort(got)
			wantCharges(t, got, tt.want...)
		})
	}
}

func TestFeesOfTheSameAmountAreEachPaidByTheAccountPostingThatComesBeforeIt(t *testing.T) {
	w := newChargesWorld()
	// A transfer of 1,000 from the card to cash, with a fee of 10 that the card paid and
	// another of 10 that the bank paid, of different categories. Either account posting
	// of -10 could pay either fee; each fee is paid by the one put before it.
	tests := []struct {
		name string
		txn  model.Transaction
		want string
	}{
		{
			"the card pays fees, then the bank pays interest",
			w.transfer(w.card, w.cash, "1000", w.fee(w.card, w.fees, "10"), w.fee(w.bank, w.interest, "10")),
			"TWD: fees 10 | shares 0 | other 1000",
		},
		{
			"the bank pays interest, then the card pays fees",
			w.transfer(w.card, w.cash, "1000", w.fee(w.bank, w.interest, "10"), w.fee(w.card, w.fees, "10")),
			"TWD: fees 10 | shares 0 | other 1000",
		},
		{
			"the card pays interest, then the bank pays fees",
			w.transfer(w.card, w.cash, "1000", w.fee(w.card, w.interest, "10"), w.fee(w.bank, w.fees, "10")),
			"TWD: interest 10 | shares 0 | other 1000",
		},
		{
			"the bank pays fees, then the card pays interest",
			w.transfer(w.card, w.cash, "1000", w.fee(w.bank, w.fees, "10"), w.fee(w.card, w.interest, "10")),
			"TWD: interest 10 | shares 0 | other 1000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantCharges(t, w.charges(t, valid(t, tt.txn)...), tt.want)
		})
	}
}

func TestWhatTheCardPaidToAPartyIsOtherAndNotShares(t *testing.T) {
	w := newChargesWorld()
	tests := []struct {
		name string
		txn  model.Transaction
		want []string
	}{
		{"a loan paid with the card", w.lend(w.card, "700"), []string{"TWD: - | shares 0 | other 700"}},
		{
			"a loan with a fee that the card paid",
			w.lend(w.card, "700", w.fee(w.card, w.fees, "10")),
			[]string{"TWD: fees 10 | shares 0 | other 700"},
		},
		{
			"a loan with a fee that the bank paid",
			w.lend(w.card, "700", w.fee(w.bank, w.fees, "10")),
			[]string{"TWD: - | shares 0 | other 700"},
		},
		{"a repayment into the card", w.repaid(w.card, "700"), nil},
		{"a loan paid with the bank", w.lend(w.bank, "700"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantCharges(t, w.charges(t, valid(t, tt.txn)...), tt.want...)
		})
	}
}

func TestAShareOfAnExpenseIsSharesAndWhatTheCardPaidToAPartyNextToItIsNot(t *testing.T) {
	w := newChargesWorld()
	got := w.charges(t, valid(t,
		txn(model.KindExpense, w.onCard(w.twd, "-500"), expenseLine(w.food, w.twd, "300"), w.share(w.twd, "200")),
		w.lend(w.card, "700"),
	)...)

	wantCharges(t, got, "TWD: food 300 | shares 200 | other 700")
}

func TestWhatAReconciliationMakesUpForIsAnExpenseLineOfItsOwnAndNotAFee(t *testing.T) {
	w := newChargesWorld()
	// The card was found 80 short, which the ledger makes up for as an expense of the
	// system category of the unrecorded. The 80 is what the card paid, and not a fee
	// that an account posting pays: there is no other posting to say so.
	got := w.charges(t, valid(t, txn(model.KindReconciliation,
		w.onCard(w.twd, "-80"),
		expenseLine(model.UnrecordedExpenseCategoryID, w.twd, "80"),
	))...)

	wantCharges(t, got, "TWD: unrecorded 80 | shares 0 | other 0")
}

func TestTheChargesOfManyTransactionsAreWhatEachOfThemChargedTheCardFor(t *testing.T) {
	w := newChargesWorld()
	r := rand.New(rand.NewPCG(20261121, 7))
	accounts := []uuid.UUID{w.card, w.bank, w.cash}
	pick := func() uuid.UUID { return accounts[r.IntN(len(accounts))] }

	// What the card was charged for is known from how each transaction is made: what
	// it takes out of the card, and which of its fees the card paid.
	var (
		want   = make(map[uuid.UUID]decimal.Decimal) // top-level category -> amount
		shares decimal.Decimal
		other  decimal.Decimal
	)
	fees := func() [][]model.Posting {
		var fees [][]model.Posting
		for range r.IntN(4) {
			payer, category := pick(), []uuid.UUID{w.fees, w.interest}[r.IntN(2)]
			amount := []string{"5", "10", "15"}[r.IntN(3)] // few, so that fees of the same amount are common
			fees = append(fees, w.fee(payer, category, amount))
			if payer == w.card {
				want[category] = want[category].Add(dec(amount))
			}
		}
		return fees
	}

	var transactions []model.Transaction
	for range 3000 {
		amount := []string{"10", "100", "1000"}[r.IntN(3)]
		account := pick()
		switch r.IntN(9) {
		case 0, 1: // a transfer between two accounts
			to := pick()
			for to == account {
				to = pick()
			}
			if account == w.card {
				other = other.Add(dec(amount))
			}
			transactions = append(transactions, w.transfer(account, to, amount, fees()...))
		case 2: // a loan
			if account == w.card {
				other = other.Add(dec(amount))
			}
			transactions = append(transactions, w.lend(account, amount, fees()...))
		case 3: // a repayment
			transactions = append(transactions, w.repaid(account, amount, fees()...))
		case 4:
			transactions = append(transactions, w.income(account, amount, fees()...))
		case 5:
			transactions = append(transactions, w.reward(account, amount, fees()...))
		case 6:
			transactions = append(transactions, w.billInstalment(account, amount, fees()...))
		case 7: // an expense paid with the card, of one or two categories and perhaps with a share
			total := decimal.Zero
			var lines []model.Posting
			for range 1 + r.IntN(2) {
				category := []uuid.UUID{w.food, w.lunch, w.rent}[r.IntN(3)]
				lines = append(lines, expenseLine(category, w.twd, amount))
				want[w.topLevel(category)] = want[w.topLevel(category)].Add(dec(amount))
				total = total.Add(dec(amount))
			}
			if r.IntN(2) == 0 {
				lines = append(lines, w.share(w.twd, "50"))
				shares = shares.Add(dec("50"))
				total = total.Add(dec("50"))
			}
			transactions = append(transactions, txn(model.KindExpense, append([]model.Posting{w.onCard(w.twd, total.Neg().String())}, lines...)...))
		default: // a period of an instalment plan, charged to the card
			other = other.Add(dec(amount))
			want[w.interest] = want[w.interest].Add(dec("10"))
			transactions = append(transactions, w.instalmentPeriod(w.card, amount, "10"))
		}
	}

	got := model.ChargesOfCard(w.card, valid(t, transactions...), w.topLevel)

	if len(got) != 1 || got[0].InstrumentID != w.twd {
		t.Fatalf("charges = %+v, want those of TWD only", got)
	}
	c := got[0]
	names := w.names()
	gotCategories := make(map[uuid.UUID]decimal.Decimal, len(c.Categories))
	for _, category := range c.Categories {
		gotCategories[category.CategoryID] = category.Amount
	}
	if len(gotCategories) != len(want) {
		t.Errorf("charged for %d categories, want %d", len(gotCategories), len(want))
	}
	for category, amount := range want {
		if !gotCategories[category].Equal(amount) {
			t.Errorf("%s: charged %s, want %s", names[category], gotCategories[category], amount)
		}
	}
	if !c.Shares.Equal(shares) || !c.Other.Equal(other) {
		t.Errorf("shares %s and other %s, want %s and %s", c.Shares, c.Other, shares, other)
	}
}
