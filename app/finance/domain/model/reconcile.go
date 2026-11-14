package model

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// ReconcileParams is the input of Reconcile.
type ReconcileParams struct {
	// Header is the header of the draft that makes up for the difference, when
	// there is one. Its date is the day the balance was observed.
	Header
	// Account and Instrument are what is reconciled, and must be in use.
	Account    Account
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amount of the difference is
	// checked against. It is only used for a currency other than the base
	// currency.
	BaseCurrency Instrument
	// RecordedBalance is what the ledger says the account holds of the instrument
	// on the day: its confirmed postings up to and including that day, added up.
	RecordedBalance decimal.Decimal
	// ActualBalance is what the account really holds of the instrument on the day,
	// no finer than the instrument. It may be negative, for an account that owes.
	ActualBalance decimal.Decimal
	// DifferenceBaseAmount is the difference in the base currency, at the rate of
	// the day, as a positive amount no finer than the base currency, whichever way
	// the difference goes. For a currency other than the base currency it is
	// required when there is a difference. For the base currency it may be left
	// out, and must equal the difference if given. For an instrument that is not a
	// currency it must be left out (ErrBaseAmountNotAllowed). It is not looked at
	// when there is no difference.
	DifferenceBaseAmount *decimal.Decimal
}

// Reconciliation is what comes of reconciling an account.
type Reconciliation struct {
	// Difference is the actual balance minus the recorded one: negative when the
	// account holds less than the ledger says, positive when it holds more.
	Difference decimal.Decimal
	// Transaction is the draft that makes the ledger say what the account holds, and
	// is nil when there is no difference: nothing is recorded then.
	Transaction *Transaction
}

// Reconcile compares what an account really holds of an instrument with what the
// ledger says, and makes up for the difference with a draft of the kind
// reconciliation. The account posting is the difference itself, so that the
// account holds what it really does once the draft is confirmed. For a currency,
// the posting against it is an expense of the system category 未記錄
// (unrecorded) when the account holds less, and an income of the system category
// of the same name when it holds more:
//
//	account   -120
//	expense   +120, base amount     (the account holds 120 less than the ledger says)
//
//	account   +80
//	income    -80, base amount      (the account holds 80 more)
//
// An instrument that is not a currency has no expense or income: its difference
// only corrects the quantity, against an adjustment:
//
//	account      +1.5
//	adjustment   -1.5
//
// When there is no difference nothing is made, and the Transaction returned is
// nil. A currency other than the base currency that has a difference but no base
// amount for it returns a *DifferenceBaseAmountRequiredError.
func Reconcile(p ReconcileParams) (Reconciliation, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Reconciliation{}, err
	}
	if err := checkAccount(p.Account); err != nil {
		return Reconciliation{}, err
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Reconciliation{}, err
	}
	if err := p.Instrument.CheckQuantity(p.ActualBalance); err != nil {
		return Reconciliation{}, fmt.Errorf("actual balance: %w", err)
	}

	difference := p.ActualBalance.Sub(p.RecordedBalance)
	if difference.IsZero() {
		return Reconciliation{Difference: difference}, nil
	}

	accountID := p.Account.ID
	postings := []Posting{
		{Book: BookAccount, AccountID: &accountID, InstrumentID: p.Instrument.ID, Quantity: difference},
	}
	if p.Instrument.IsCurrency {
		counterpart, err := unrecordedPosting(p, difference)
		if err != nil {
			return Reconciliation{}, err
		}
		postings = append(postings, counterpart)
	} else {
		if p.DifferenceBaseAmount != nil {
			return Reconciliation{}, fmt.Errorf("%w: %s is not a currency", ErrBaseAmountNotAllowed, p.Instrument.Code)
		}
		postings = append(postings, Posting{Book: BookAdjustment, InstrumentID: p.Instrument.ID, Quantity: difference.Neg()})
	}

	txn, err := newTransaction(KindReconciliation, h, postings)
	if err != nil {
		return Reconciliation{}, err
	}
	return Reconciliation{Difference: difference, Transaction: &txn}, nil
}

// unrecordedPosting returns the posting that makes up for the difference of a
// currency: an expense of the system category of expenses when the account holds
// less, an income of the one of incomes when it holds more.
func unrecordedPosting(p ReconcileParams, difference decimal.Decimal) (Posting, error) {
	if p.Instrument.ID != BaseCurrencyID && p.DifferenceBaseAmount == nil {
		return Posting{}, &DifferenceBaseAmountRequiredError{InstrumentCode: p.Instrument.Code, Difference: difference}
	}
	magnitude := difference.Abs()
	baseAmount, err := baseAmountOf(p.Instrument, p.BaseCurrency, magnitude, p.DifferenceBaseAmount)
	if err != nil {
		return Posting{}, fmt.Errorf("difference: %w", err)
	}

	if difference.IsNegative() {
		categoryID := UnrecordedExpenseCategoryID
		return Posting{
			Book: BookExpense, CategoryID: &categoryID, InstrumentID: p.Instrument.ID, Quantity: magnitude, BaseAmount: &baseAmount,
		}, nil
	}
	categoryID := UnrecordedIncomeCategoryID
	income := baseAmount.Neg()
	return Posting{
		Book: BookIncome, CategoryID: &categoryID, InstrumentID: p.Instrument.ID, Quantity: magnitude.Neg(), BaseAmount: &income,
	}, nil
}
