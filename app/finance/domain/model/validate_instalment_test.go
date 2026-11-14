package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// The raw postings below skip the constructors on purpose, like those of
// transaction_test.go: Validate is the check that holds for a transaction put
// together any other way, such as one read back from the store.

// instalmentPosting is a posting to the instalment book of an account.
func instalmentPosting(account, instrument uuid.UUID, quantity string) model.Posting {
	return model.Posting{Book: model.BookInstalment, AccountID: &account, InstrumentID: instrument, Quantity: dec(quantity)}
}

// originOf puts the transaction in a plan as its origin: position 0, and the
// plan is its own id.
func originOf(txn model.Transaction) model.Transaction {
	txn.PlanID, txn.PlanSeq = new(txn.ID), new(0)
	return txn
}

// periodOf puts the transaction in a plan, which is another transaction's, at the
// position.
func periodOf(txn model.Transaction, seq int) model.Transaction {
	txn.PlanID, txn.PlanSeq = new(uuid.Must(uuid.NewV7())), new(seq)
	return txn
}

// planWith returns the transaction with the plan id and the position as given,
// each of which may be left out.
func planWith(txn model.Transaction, plan *uuid.UUID, seq *int) model.Transaction {
	txn.PlanID, txn.PlanSeq = plan, seq
	return txn
}

func TestValidateAcceptsInstalmentsAndTrades(t *testing.T) {
	twd := model.BaseCurrencyID
	tests := []struct {
		name string
		txn  model.Transaction
	}{
		{"the expense that is paid in instalments, as the origin of its plan", originOf(raw(model.KindExpense,
			instalmentPosting(acctA, twd, "-12000"),
			categoryPosting(model.BookExpense, twd, "12000", "12000")))},
		{"the expense that is paid in instalments, in a foreign currency", originOf(raw(model.KindExpense,
			instalmentPosting(acctA, usdID, "-100"),
			categoryPosting(model.BookExpense, usdID, "100", "3200")))},
		{"the expense that is paid in instalments, with a share for another", originOf(raw(model.KindExpense,
			instalmentPosting(acctA, twd, "-600"),
			categoryPosting(model.BookExpense, twd, "300", "300"),
			dealingPosting(partyP, twd, "300")))},
		{"a bill instalment", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctA, twd, "-12000")))},
		{"a bill instalment with a fee paid from another account", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctA, twd, "-12000"),
			accountPosting(acctB, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "100", "100")))},
		{"a bill instalment with a fee as big as the debt, paid by its own account", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "100"),
			instalmentPosting(acctA, twd, "-100"),
			accountPosting(acctA, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "100", "100")))},
		{"a period", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1000")), 1)},
		{"a period with interest", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1030"),
			categoryPosting(model.BookExpense, twd, "30", "30")), 7)},
		{"a period with interest as big as its principal", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "30"),
			accountPosting(acctA, twd, "-60"),
			categoryPosting(model.BookExpense, twd, "30", "30")), 2)},
		{"a period in a foreign currency with interest", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, usdID, "25"),
			accountPosting(acctA, usdID, "-26.5"),
			categoryPosting(model.BookExpense, usdID, "1.5", "48")), 1)},
		{"a buy", raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "-0.1"),
			accountPosting(acctB, usdID, "0.1"))},
		{"a sell", raw(model.KindTrade,
			accountPosting(acctB, usdID, "-0.1"),
			bookPosting(model.BookConversion, usdID, "0.1"),
			bookPosting(model.BookConversion, twd, "-300000"),
			accountPosting(acctA, twd, "300000"))},
		{"a buy with a fee", raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "-0.1"),
			accountPosting(acctB, usdID, "0.1"),
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

func TestValidateRefusesWhatIsNotRightForAPlan(t *testing.T) {
	twd := model.BaseCurrencyID
	other := uuid.Must(uuid.NewV7())
	period := func() model.Transaction {
		return raw(model.KindInstalment, instalmentPosting(acctA, twd, "1000"), accountPosting(acctA, twd, "-1000"))
	}
	bill := func() model.Transaction {
		return raw(model.KindInstalment, accountPosting(acctA, twd, "12000"), instalmentPosting(acctA, twd, "-12000"))
	}
	paidInInstalments := func() model.Transaction {
		return raw(model.KindExpense, instalmentPosting(acctA, twd, "-12000"), categoryPosting(model.BookExpense, twd, "12000", "12000"))
	}
	lunch := func() model.Transaction {
		return raw(model.KindExpense, accountPosting(acctA, twd, "-120"), categoryPosting(model.BookExpense, twd, "120", "120"))
	}

	tests := []struct {
		name string
		txn  model.Transaction
		want error
	}{
		// Where the transaction stands in its plan.
		{"an instalment that is in no plan", period(), model.ErrMalformedPlan},
		{"a plan id without a position", planWith(period(), &other, nil), model.ErrMalformedPlan},
		{"a position without a plan id", planWith(period(), nil, new(1)), model.ErrMalformedPlan},
		{"an origin that is not the transaction its plan names", planWith(bill(), &other, new(0)), model.ErrMalformedPlan},
		{"a period that is the transaction its plan names", func() model.Transaction {
			txn := period()
			return planWith(txn, &txn.ID, new(1))
		}(), model.ErrMalformedPlan},
		{"a position before the origin", periodOf(period(), -1), model.ErrMalformedPlan},
		{"a period that is an expense", periodOf(paidInInstalments(), 1), model.ErrMalformedPlan},
		{"an income in a plan", originOf(raw(model.KindIncome,
			accountPosting(acctA, twd, "50000"),
			categoryPosting(model.BookIncome, twd, "-50000", "-50000"))), model.ErrMalformedPlan},
		{"a transfer in a plan", periodOf(raw(model.KindTransfer,
			accountPosting(acctA, twd, "-3000"),
			accountPosting(acctB, twd, "3000")), 1), model.ErrMalformedPlan},
		{"a trade in a plan", originOf(raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "-0.1"),
			accountPosting(acctB, usdID, "0.1"))), model.ErrMalformedPlan},

		// An expense, which may be paid in instalments, and only as the origin.
		{"an instalment posting of an expense that is in no plan", paidInInstalments(), model.ErrWrongPostings},
		{"the origin of a plan that an account pays at once", originOf(lunch()), model.ErrWrongPostings},
		{"an expense paid by an account and in instalments", originOf(raw(model.KindExpense,
			accountPosting(acctA, twd, "-6000"),
			instalmentPosting(acctA, twd, "-6000"),
			categoryPosting(model.BookExpense, twd, "12000", "12000"))), model.ErrWrongPostings},
		{"an expense that is paid in instalments by a party too", originOf(raw(model.KindExpense,
			instalmentPosting(acctA, twd, "-12000"),
			dealingPosting(partyP, twd, "-100"),
			categoryPosting(model.BookExpense, twd, "12100", "12100"))), model.ErrWrongSign},
		{"an expense whose instalment posting is positive", originOf(raw(model.KindExpense,
			instalmentPosting(acctA, twd, "12000"),
			categoryPosting(model.BookExpense, twd, "-12000", "-12000"))), model.ErrWrongSign},

		// The origin of a bill instalment.
		{"a bill instalment that owes the debt again as the account is paid, the wrong way round", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "-12000"),
			instalmentPosting(acctA, twd, "12000"))), model.ErrWrongSign},
		{"a bill instalment of two accounts", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctB, twd, "-12000"))), model.ErrWrongPostings},
		{"a bill instalment of two instruments", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctA, usdID, "-12000"))), model.ErrWrongPostings},
		{"a bill instalment that owes less than it paid off", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctA, twd, "-11000"))), model.ErrUnbalanced},
		{"a bill instalment with only an account posting", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"))), model.ErrWrongPostings},
		{"a bill instalment with an expense that nothing pays", originOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "12000"),
			instalmentPosting(acctA, twd, "-12000"),
			categoryPosting(model.BookExpense, twd, "100", "100"))), model.ErrWrongPostings},

		// A period.
		{"a period without an instalment posting", periodOf(raw(model.KindInstalment,
			accountPosting(acctA, twd, "-1000"),
			categoryPosting(model.BookExpense, twd, "1000", "1000")), 1), model.ErrWrongPostings},
		{"a period with two expense postings", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1060"),
			categoryPosting(model.BookExpense, twd, "30", "30"),
			categoryPosting(model.BookExpense, twd, "30", "30")), 1), model.ErrWrongPostings},
		{"a period that charges nothing to an account", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			instalmentPosting(acctA, twd, "-1000")), 1), model.ErrWrongPostings},
		{"a period of two accounts", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctB, twd, "-1000")), 1), model.ErrWrongPostings},
		{"a period of two instruments", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, usdID, "-1000")), 1), model.ErrWrongPostings},
		{"a period that puts principal back into what is not charged", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "-1000"),
			accountPosting(acctA, twd, "1000")), 1), model.ErrWrongSign},
		{"a period that does not charge the interest to the account", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1000"),
			categoryPosting(model.BookExpense, twd, "30", "30")), 1), model.ErrUnbalanced},
		{"a period that charges more than principal and interest", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1040"),
			categoryPosting(model.BookExpense, twd, "30", "30")), 1), model.ErrUnbalanced},
		{"a period whose interest is an income", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-970"),
			categoryPosting(model.BookIncome, twd, "-30", "-30")), 1), model.ErrWrongPostings},
		{"interest booked to the system category", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "1000"),
			accountPosting(acctA, twd, "-1030"),
			categoryOf(model.BookExpense, model.UnrecordedExpenseCategoryID, twd, "30", "30")), 1), model.ErrSystemCategory},

		// What each posting of the new book needs by itself.
		{"an instalment posting without an account", periodOf(raw(model.KindInstalment,
			bookPosting(model.BookInstalment, twd, "1000"),
			accountPosting(acctA, twd, "-1000")), 1), model.ErrMalformedPosting},
		{"an instalment posting with a category", periodOf(raw(model.KindInstalment,
			func() model.Posting {
				p := instalmentPosting(acctA, twd, "1000")
				category := catX
				p.CategoryID = &category
				return p
			}(),
			accountPosting(acctA, twd, "-1000")), 1), model.ErrMalformedPosting},
		{"an instalment posting with a base amount", periodOf(raw(model.KindInstalment,
			func() model.Posting {
				p := instalmentPosting(acctA, twd, "1000")
				p.BaseAmount = decPtr("1000")
				return p
			}(),
			accountPosting(acctA, twd, "-1000")), 1), model.ErrMalformedPosting},
		{"an instalment posting of nothing", periodOf(raw(model.KindInstalment,
			instalmentPosting(acctA, twd, "0"),
			accountPosting(acctA, twd, "-1000")), 1), model.ErrMalformedPosting},

		// A trade is made as an exchange is.
		{"a trade of three postings", raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "-0.1")), model.ErrWrongPostings},
		{"a trade that takes out of both accounts", raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "0.1"),
			accountPosting(acctB, usdID, "-0.1")), model.ErrWrongSign},
		{"a trade of one instrument", raw(model.KindTrade,
			accountPosting(acctA, twd, "-3000"),
			bookPosting(model.BookConversion, twd, "3000"),
			bookPosting(model.BookConversion, twd, "-3000"),
			accountPosting(acctB, twd, "3000")), model.ErrWrongPostings},
		{"a trade that does not balance", raw(model.KindTrade,
			accountPosting(acctA, twd, "-300000"),
			bookPosting(model.BookConversion, twd, "300000"),
			bookPosting(model.BookConversion, usdID, "-0.1"),
			accountPosting(acctB, usdID, "0.2")), model.ErrUnbalanced},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.txn.Validate(); !errors.Is(err, tt.want) {
				t.Fatalf("Validate: err = %v, want %v", err, tt.want)
			}
		})
	}
}
