package usecase

import (
	"context"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// RecordTrade records buying or selling an instrument that is not a currency
// (cryptocurrency, a stock, a fund) for a currency, as a draft. It records what
// changed hands, the quantity and the amount, and nothing more: the cost of the
// instrument and the gain or loss of selling it are not worked out. The accounts,
// the instrument and the currency must exist and not be archived.
//
// Trading one currency for another is an exchange, and one instrument that is not
// a currency for another is not a trade: it is refused.
type RecordTrade interface {
	RecordTrade(ctx context.Context, in RecordTradeInput) (RecordOutput, error)
}

// RecordTradeInput is a trade to record.
type RecordTradeInput struct {
	RecordDetails
	// Direction is "buy" or "sell". A buy takes the amount of the currency out of
	// CashAccountID and puts the quantity of the instrument into AccountID; a sell
	// takes the quantity out of AccountID and puts the amount into CashAccountID.
	Direction string
	// AccountID is the account that holds the instrument.
	AccountID uuid.UUID
	// InstrumentID is what is bought or sold, and must not be a currency
	// (ErrIsCurrency).
	InstrumentID uuid.UUID
	// Quantity is how much of the instrument: greater than zero, with no more
	// decimal places than the instrument.
	Quantity decimal.Decimal
	// CashAccountID is the account the currency is paid from, or comes into. It may
	// be AccountID, for an account that holds both.
	CashAccountID uuid.UUID
	// CurrencyID is what the instrument is paid in, and must be a currency
	// (ErrNotCurrency).
	CurrencyID uuid.UUID
	// Amount is what was paid or received, in the currency: greater than zero, with
	// no more decimal places than the currency. The price is what it and Quantity
	// make.
	Amount decimal.Decimal
	// Fees are charges that came with the trade. Each is paid from its account,
	// besides the amount.
	Fees []Fee
}
