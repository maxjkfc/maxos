package model

import (
	"fmt"

	"github.com/shopspring/decimal"
)

// TradeDirection is whether a trade buys or sells.
type TradeDirection string

// The directions of a trade.
const (
	// TradeBuy is buying an instrument with a currency: the currency leaves its
	// account and the instrument arrives at its account.
	TradeBuy TradeDirection = "buy"
	// TradeSell is selling an instrument for a currency: the instrument leaves its
	// account and the currency arrives at its account.
	TradeSell TradeDirection = "sell"
)

// Valid reports whether d is one of the directions above.
func (d TradeDirection) Valid() bool {
	return d == TradeBuy || d == TradeSell
}

// TradeParams is the input of NewTrade.
type TradeParams struct {
	Header
	// Direction is TradeBuy or TradeSell.
	Direction TradeDirection
	// Account is the account that holds the instrument: it receives the quantity in
	// a buy, and gives it up in a sell. It must be in use.
	Account Account
	// Instrument is what is bought or sold, and must be in use. It must not be a
	// currency: changing one currency into another is an exchange.
	Instrument Instrument
	// Quantity is how much of the instrument is bought or sold: greater than zero,
	// and no finer than the instrument.
	Quantity decimal.Decimal
	// CashAccount is the account the currency is paid from in a buy, and comes into
	// in a sell. It must be in use, and may be Account: an exchange holds both.
	CashAccount Account
	// Currency is what the instrument is paid in, and must be a currency in use.
	Currency Instrument
	// Amount is what was paid or received, in the currency: greater than zero, and no
	// finer than the currency. The price is what Amount and Quantity make.
	Amount decimal.Decimal
	// BaseCurrency is the base currency, which the base amounts of the fees are
	// checked against. It is only used for a fee in an instrument other than the
	// base currency.
	BaseCurrency Instrument
	// Fees are charges that came with the trade, each an expense of its own.
	Fees []Fee
}

// NewTrade returns the draft of a trade: an instrument that is not a currency
// bought or sold for a currency, such as bitcoin for new Taiwan dollars. A
// conversion posting for each of the two keeps each of them balanced by itself, as
// in an exchange. The postings of a buy are
//
//	account(cash account)      -amount     (currency)
//	conversion                 +amount     (currency)
//	conversion                 -quantity   (instrument)
//	account(account)           +quantity   (instrument)
//
// and a sell is the mirror image:
//
//	account(account)           -quantity   (instrument)
//	conversion                 +quantity   (instrument)
//	conversion                 -amount     (currency)
//	account(cash account)      +amount     (currency)
//
// each followed, for each fee, by the two postings of a fee (see Fee). A trade
// records what changed hands and nothing else: it is neither an expense nor an
// income, and the cost of the instrument and the gain or loss of selling it are
// not worked out here.
func NewTrade(p TradeParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if !p.Direction.Valid() {
		return Transaction{}, fmt.Errorf("%w %q: want buy or sell", ErrInvalidTradeDirection, p.Direction)
	}
	if p.Instrument.IsCurrency {
		return Transaction{}, fmt.Errorf("%w: %s is a currency, and changing one currency into another is an exchange", ErrIsCurrency, p.Instrument.Code)
	}
	if !p.Currency.IsCurrency {
		return Transaction{}, fmt.Errorf("%w: a trade is paid in a currency, and %s is not one", ErrNotCurrency, p.Currency.Code)
	}
	for _, a := range []Account{p.Account, p.CashAccount} {
		if err := checkAccount(a); err != nil {
			return Transaction{}, err
		}
	}
	for _, i := range []Instrument{p.Instrument, p.Currency} {
		if err := checkInstrument(i); err != nil {
			return Transaction{}, err
		}
	}
	if err := checkAmount(p.Instrument, p.Quantity); err != nil {
		return Transaction{}, fmt.Errorf("quantity: %w", err)
	}
	if err := checkAmount(p.Currency, p.Amount); err != nil {
		return Transaction{}, fmt.Errorf("amount: %w", err)
	}
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	var postings []Posting
	if p.Direction == TradeBuy {
		postings = []Posting{
			{Book: BookAccount, AccountID: new(p.CashAccount.ID), InstrumentID: p.Currency.ID, Quantity: p.Amount.Neg()},
			{Book: BookConversion, InstrumentID: p.Currency.ID, Quantity: p.Amount},
			{Book: BookConversion, InstrumentID: p.Instrument.ID, Quantity: p.Quantity.Neg()},
			{Book: BookAccount, AccountID: new(p.Account.ID), InstrumentID: p.Instrument.ID, Quantity: p.Quantity},
		}
	} else {
		postings = []Posting{
			{Book: BookAccount, AccountID: new(p.Account.ID), InstrumentID: p.Instrument.ID, Quantity: p.Quantity.Neg()},
			{Book: BookConversion, InstrumentID: p.Instrument.ID, Quantity: p.Quantity},
			{Book: BookConversion, InstrumentID: p.Currency.ID, Quantity: p.Amount.Neg()},
			{Book: BookAccount, AccountID: new(p.CashAccount.ID), InstrumentID: p.Currency.ID, Quantity: p.Amount},
		}
	}
	postings = append(postings, fees...)
	return newTransaction(KindTrade, h, postings)
}
