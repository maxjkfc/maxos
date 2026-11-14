package model

import (
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

// Fee is a charge that comes with a transaction, such as the fee of a transfer
// or of a payment received: an expense of its own, paid from an account. It need
// not be paid from an account of the transaction, or in its instrument. A fee of
// an expense is not one of these: it is just another line.
type Fee struct {
	// Account is the account the fee is paid from, and must be in use.
	Account Account
	// Instrument is what the fee is paid in, and must be in use.
	Instrument Instrument
	// Category must be an expense category in use, and not a system category.
	Category Category
	// Amount is greater than zero, and no finer than the instrument.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency, at the rate of the day: greater
	// than zero, and no finer than the base currency. It is required when the
	// instrument is not the base currency; when it is, it may be left out, and
	// must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Memo is trimmed of the whitespace around it.
	Memo string
}

// feePostings checks the fees of a transaction and returns their postings, the
// two of each fee after the other, in the order the fees are given. A fee is
// always its own pair of postings, and is never added to a posting of the
// transaction that happens to be of the same account and instrument:
//
//	account(fee account)   -amount
//	expense(category)      +amount, base amount
//
// base is the base currency, and is only used for a fee in another instrument.
func feePostings(base Instrument, fees []Fee) ([]Posting, error) {
	postings := make([]Posting, 0, 2*len(fees))
	for i, fee := range fees {
		n := i + 1
		if err := checkAccount(fee.Account); err != nil {
			return nil, fmt.Errorf("fee %d: %w", n, err)
		}
		if err := checkInstrument(fee.Instrument); err != nil {
			return nil, fmt.Errorf("fee %d: %w", n, err)
		}
		if fee.Category.IsArchived() {
			return nil, fmt.Errorf("fee %d: category %q: %w", n, fee.Category.Name, ErrArchived)
		}
		if fee.Category.IsSystem() {
			return nil, fmt.Errorf("fee %d: category %q: %w", n, fee.Category.Name, ErrSystemCategory)
		}
		if fee.Category.Direction != DirectionExpense {
			return nil, fmt.Errorf("fee %d: %w: category %q is for %s, and a fee is an expense",
				n, ErrWrongCategoryDirection, fee.Category.Name, fee.Category.Direction)
		}
		if err := checkAmount(fee.Instrument, fee.Amount); err != nil {
			return nil, fmt.Errorf("fee %d: %w", n, err)
		}
		baseAmount, err := baseAmountOf(fee.Instrument, base, fee.Amount, fee.BaseAmount)
		if err != nil {
			return nil, fmt.Errorf("fee %d: %w", n, err)
		}

		accountID, categoryID := fee.Account.ID, fee.Category.ID
		postings = append(postings,
			Posting{Book: BookAccount, AccountID: &accountID, InstrumentID: fee.Instrument.ID, Quantity: fee.Amount.Neg()},
			Posting{
				Book:         BookExpense,
				CategoryID:   &categoryID,
				InstrumentID: fee.Instrument.ID,
				Quantity:     fee.Amount,
				BaseAmount:   &baseAmount,
				Memo:         strings.TrimSpace(fee.Memo),
			},
		)
	}
	return postings, nil
}
