package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// The raw postings below skip the constructors on purpose, like those of
// transaction_test.go: Validate is the check that holds for a transaction put
// together any other way.

var (
	partyP = uuid.Must(uuid.NewV7())
	partyQ = uuid.Must(uuid.NewV7())
)

// dealingPosting is a posting to the dealing of a party.
func dealingPosting(party, instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: model.BookDealing, PartyID: &party, InstrumentID: instrument, Quantity: dec(quantity)}
}

// categoryOf returns a posting of the book to a category, with the base amount.
func categoryOf(book model.Book, category, instrument uuid.UUID, quantity, base string) model.Posting {
	return model.Posting{Book: book, CategoryID: &category, InstrumentID: instrument, Quantity: dec(quantity), BaseAmount: decPtr(base)}
}

// rewardPosting is a posting to the rewards, with its base amount and no
// category.
func rewardPosting(instrument uuid.UUID, quantity, base string) model.Posting {
	return model.Posting{Book: model.BookReward, InstrumentID: instrument, Quantity: dec(quantity), BaseAmount: decPtr(base)}
}

// withDiscount returns the posting with a discount.
func withDiscount(p model.Posting, discount string) model.Posting {
	p.Discount = decPtr(discount)
	return p
}

func TestValidateAcceptsDealingsRewardsReconciliationsAndFees(t *testing.T) {
	twd := model.BaseCurrencyID
	unrecordedExpense, unrecordedIncome := model.UnrecordedExpenseCategoryID, model.UnrecordedIncomeCategoryID
	tests := []struct {
		name string
		txn  model.Transaction
	}{
		{"an expense with a share", raw(model.KindExpense,
			accountPosting(acctA, twd, "-500"),
			categoryPosting(model.BookExpense, twd, "300", "300"),
			dealingPosting(partyP, twd, "200"))},
		{"an expense with shares of two parties and a discount", raw(model.KindExpense,
			accountPosting(acctA, twd, "-900"),
			withDiscount(categoryPosting(model.BookExpense, twd, "300", "300"), "50"),
			dealingPosting(partyP, twd, "300"),
			dealingPosting(partyQ, twd, "300"))},
		{"an expense that a party paid", raw(model.KindExpense,
			dealingPosting(partyP, twd, "-300"),
			categoryPosting(model.BookExpense, twd, "300", "300"))},
		{"a dealing that pays out", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			dealingPosting(partyP, twd, "5000"))},
		{"a dealing that receives", raw(model.KindDealing,
			accountPosting(acctA, twd, "5000"),
			dealingPosting(partyP, twd, "-5000"))},
		{"a reward", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			rewardPosting(twd, "-50", "-50"))},
		{"a reconciliation of a currency that is short", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-120"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "120", "120"))},
		{"a reconciliation of a currency that is over", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "80"),
			categoryOf(model.BookIncome, unrecordedIncome, twd, "-80", "-80"))},
		{"a reconciliation of an instrument that is not a currency", raw(model.KindReconciliation,
			accountPosting(acctA, usdID, "1.5"),
			bookPosting(model.BookAdjustment, usdID, "-1.5"))},
		{"an income with a fee", raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			categoryPosting(model.BookIncome, twd, "-50000", "-50000"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
		{"a transfer with a fee paid from another account", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctB, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
		{"a transfer with a fee in another instrument", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, usdID, "-1"),
			categoryPosting(model.BookExpense, usdID, "1", "32"))},
		{"a transfer with a fee as big as what moves, paid by the receiving account", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-15"),
			accountPosting(acctB, twd, "15"),
			accountPosting(acctB, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
		{"an exchange with a fee", raw(model.KindExchange,
			accountPosting(acctA, usdID, "-3000"),
			bookPosting(model.BookConversion, usdID, "3000"),
			bookPosting(model.BookConversion, twd, "-93000"),
			accountPosting(acctB, twd, "93000"),
			accountPosting(acctA, usdID, "-1"),
			categoryPosting(model.BookExpense, usdID, "1", "32"))},
		{"a dealing with a fee", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			dealingPosting(partyP, twd, "5000"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
		{"a reward with a fee", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			rewardPosting(twd, "-50", "-50"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
		{"two fees", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRefusesWhatDealingsRewardsReconciliationsAndFeesGetWrong(t *testing.T) {
	twd := model.BaseCurrencyID
	unrecordedExpense, unrecordedIncome := model.UnrecordedExpenseCategoryID, model.UnrecordedIncomeCategoryID
	tests := []struct {
		name string
		txn  model.Transaction
		want error
	}{
		// What a posting must have by itself.
		{"a dealing posting without a party", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			bookPosting(model.BookDealing, twd, "5000")), model.ErrMalformedPosting},
		{"an account posting with a party", raw(model.KindDealing,
			model.Posting{Book: model.BookAccount, AccountID: &acctA, PartyID: &partyP, InstrumentID: twd, Quantity: dec("-5000")},
			dealingPosting(partyP, twd, "5000")), model.ErrMalformedPosting},
		{"a dealing posting with a base amount", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			model.Posting{Book: model.BookDealing, PartyID: &partyP, InstrumentID: twd, Quantity: dec("5000"), BaseAmount: decPtr("5000")}), model.ErrMalformedPosting},
		{"a dealing posting with a category", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			model.Posting{Book: model.BookDealing, PartyID: &partyP, CategoryID: &catX, InstrumentID: twd, Quantity: dec("5000")}), model.ErrMalformedPosting},
		{"a reward posting without a base amount", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			bookPosting(model.BookReward, twd, "-50")), model.ErrMalformedPosting},
		{"a reward posting with a base amount of the other sign", raw(model.KindReward,
			accountPosting(acctA, usdID, "5"),
			rewardPosting(usdID, "-5", "160")), model.ErrMalformedPosting},
		{"a reward posting with a category", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			model.Posting{Book: model.BookReward, CategoryID: &catX, InstrumentID: twd, Quantity: dec("-50"), BaseAmount: decPtr("-50")}), model.ErrMalformedPosting},
		{"an adjustment posting with a base amount", raw(model.KindReconciliation,
			accountPosting(acctA, usdID, "1.5"),
			model.Posting{Book: model.BookAdjustment, InstrumentID: usdID, Quantity: dec("-1.5"), BaseAmount: decPtr("-48")}), model.ErrMalformedPosting},
		{"a discount on an income posting", raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			withDiscount(categoryPosting(model.BookIncome, twd, "-50000", "-50000"), "5")), model.ErrMalformedPosting},
		{"a discount on an account posting", raw(model.KindTransfer,
			withDiscount(accountPosting(acctA, twd, "-1000"), "5"),
			accountPosting(acctB, twd, "1000")), model.ErrMalformedPosting},
		{"a discount of zero", raw(model.KindExpense,
			accountPosting(acctA, twd, "-450"),
			withDiscount(categoryPosting(model.BookExpense, twd, "450", "450"), "0")), model.ErrMalformedPosting},
		{"a negative discount", raw(model.KindExpense,
			accountPosting(acctA, twd, "-450"),
			withDiscount(categoryPosting(model.BookExpense, twd, "450", "450"), "-50")), model.ErrMalformedPosting},

		// An expense: who paid, and for whom.
		{"an expense with a party in two dealing postings", raw(model.KindExpense,
			accountPosting(acctA, twd, "-500"),
			categoryPosting(model.BookExpense, twd, "200", "200"),
			dealingPosting(partyP, twd, "150"),
			dealingPosting(partyP, twd, "150")), model.ErrDuplicateParty},
		{"an expense paid from an account with a share of the wrong sign", raw(model.KindExpense,
			accountPosting(acctA, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "150", "150"),
			dealingPosting(partyP, twd, "-50")), model.ErrWrongSign},
		{"an expense that a party paid, with the wrong sign", raw(model.KindExpense,
			dealingPosting(partyP, twd, "300"),
			categoryPosting(model.BookExpense, twd, "-300", "-300")), model.ErrWrongSign},
		{"an expense with no account and two dealing postings", raw(model.KindExpense,
			dealingPosting(partyP, twd, "-200"),
			dealingPosting(partyQ, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "300", "300")), model.ErrWrongPostings},
		{"an expense with no account and no dealing posting", raw(model.KindExpense,
			categoryPosting(model.BookExpense, twd, "300", "300")), model.ErrWrongPostings},
		{"an expense with only dealing postings besides the account", raw(model.KindExpense,
			accountPosting(acctA, twd, "-300"),
			dealingPosting(partyP, twd, "300")), model.ErrWrongPostings},
		{"an expense with a reward posting", raw(model.KindExpense,
			accountPosting(acctA, twd, "-50"),
			categoryPosting(model.BookExpense, twd, "100", "100"),
			rewardPosting(twd, "-50", "-50")), model.ErrWrongPostings},

		// A dealing and a reward.
		{"a dealing that takes money out and shrinks the dealing", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			dealingPosting(partyP, twd, "-5000")), model.ErrWrongSign},
		{"a dealing that puts money in and grows the dealing", raw(model.KindDealing,
			accountPosting(acctA, twd, "5000"),
			dealingPosting(partyP, twd, "5000")), model.ErrWrongSign},
		{"a dealing of two accounts", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			accountPosting(acctB, twd, "5000")), model.ErrWrongPostings},
		{"a dealing of two parties", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			dealingPosting(partyP, twd, "2500"),
			dealingPosting(partyQ, twd, "2500")), model.ErrWrongPostings},
		{"a dealing that does not balance", raw(model.KindDealing,
			accountPosting(acctA, twd, "-5000"),
			dealingPosting(partyP, twd, "4000")), model.ErrUnbalanced},
		{"a reward that takes money out of the account", raw(model.KindReward,
			accountPosting(acctA, twd, "-50"),
			rewardPosting(twd, "50", "50")), model.ErrWrongSign},
		{"a reward posting that grows", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			rewardPosting(twd, "50", "50")), model.ErrWrongSign},
		{"a reward with an income posting", raw(model.KindReward,
			accountPosting(acctA, twd, "50"),
			categoryPosting(model.BookIncome, twd, "-50", "-50")), model.ErrWrongPostings},
		{"an income with a reward posting", raw(model.KindIncome,
			accountPosting(acctA, twd, "50"),
			rewardPosting(twd, "-50", "-50")), model.ErrWrongPostings},

		// A reconciliation.
		{"a reconciliation expense of an ordinary category", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-120"),
			categoryPosting(model.BookExpense, twd, "120", "120")), model.ErrWrongPostings},
		{"a reconciliation income of the system category of expenses", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "80"),
			categoryOf(model.BookIncome, unrecordedExpense, twd, "-80", "-80")), model.ErrWrongPostings},
		{"a reconciliation expense of the system category of incomes", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-120"),
			categoryOf(model.BookExpense, unrecordedIncome, twd, "120", "120")), model.ErrWrongPostings},
		{"a reconciliation expense that goes the wrong way", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "120"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "120", "120")), model.ErrWrongSign},
		{"a reconciliation expense that is a reversal of an expense", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "120"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "-120", "-120")), model.ErrWrongSign},
		{"a reconciliation income that is a reversal of an income", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-80"),
			categoryOf(model.BookIncome, unrecordedIncome, twd, "80", "80")), model.ErrWrongSign},
		{"a reconciliation adjustment that goes the way of its account", raw(model.KindReconciliation,
			accountPosting(acctA, usdID, "1.5"),
			bookPosting(model.BookAdjustment, usdID, "1.5")), model.ErrWrongSign},
		{"a reconciliation made up for by a conversion", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-120"),
			bookPosting(model.BookConversion, twd, "120")), model.ErrWrongPostings},
		{"a reconciliation of three postings", raw(model.KindReconciliation,
			accountPosting(acctA, twd, "-120"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "60", "60"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "60", "60")), model.ErrWrongPostings},
		{"an adjustment outside a reconciliation", raw(model.KindOpening,
			accountPosting(acctA, usdID, "1.5"),
			bookPosting(model.BookAdjustment, usdID, "-1.5")), model.ErrWrongPostings},

		// Only a reconciliation uses a system category.
		{"a system category in an expense", raw(model.KindExpense,
			accountPosting(acctA, twd, "-120"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "120", "120")), model.ErrSystemCategory},
		{"a system category in an income", raw(model.KindIncome,
			accountPosting(acctA, twd, "80"),
			categoryOf(model.BookIncome, unrecordedIncome, twd, "-80", "-80")), model.ErrSystemCategory},
		{"a system category as a fee", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, twd, "-15"),
			categoryOf(model.BookExpense, unrecordedExpense, twd, "15", "15")), model.ErrSystemCategory},

		// Fees.
		{"a fee that is paid together with what moves, in one posting of the account", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1015"),
			accountPosting(acctB, twd, "1000"),
			categoryPosting(model.BookExpense, twd, "15", "15")), model.ErrWrongPostings},
		{"a fee that nothing pays", raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			categoryPosting(model.BookIncome, twd, "-50000", "-50000"),
			categoryPosting(model.BookExpense, twd, "15", "15"),
			accountPosting(acctB, twd, "-14")), model.ErrWrongPostings},
		{"a fee that is paid in another instrument than the one that pays it", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, usdID, "-1"),
			categoryPosting(model.BookExpense, twd, "1", "1")), model.ErrWrongPostings},
		{"a fee with a discount", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, twd, "-15"),
			withDiscount(categoryPosting(model.BookExpense, twd, "15", "15"), "5")), model.ErrWrongPostings},
		{"an opening balance with a fee", raw(model.KindOpening,
			accountPosting(acctA, twd, "1000"),
			bookPosting(model.BookOpening, twd, "-1000"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15")), model.ErrWrongPostings},
		{"a transfer with an account posting left over beside the fee", raw(model.KindTransfer,
			accountPosting(acctA, twd, "-1000"),
			accountPosting(acctB, twd, "1000"),
			accountPosting(acctA, twd, "-15"),
			categoryPosting(model.BookExpense, twd, "15", "15"),
			accountPosting(acctA, twd, "-1")), model.ErrWrongPostings},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate: err = %v, want %v", err, tt.want)
			}
		})
	}
}
