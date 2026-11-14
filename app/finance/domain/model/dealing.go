package model

import (
	"fmt"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// DealingDirection is which way a dealing moves money between an account and a
// party.
type DealingDirection string

// The directions of a dealing.
const (
	// DealingPaidOut is money I paid out, to a party or on their behalf, or lent
	// them: the account loses it and what the party owes me grows.
	DealingPaidOut DealingDirection = "paid_out"
	// DealingReceived is money that came in from a party, such as one repaying
	// me: the account gains it and what the party owes me shrinks.
	DealingReceived DealingDirection = "received"
)

// Valid reports whether d is one of the directions above.
func (d DealingDirection) Valid() bool {
	return d == DealingPaidOut || d == DealingReceived
}

// DealingParams is the input of NewDealing.
type DealingParams struct {
	Header
	// Account is the account the money goes out of or comes into, and must be in
	// use.
	Account Account
	// PartyID is the party on the other side, an id of a catalog entry. Whether it
	// exists and is in use is for the use case to check.
	PartyID uuid.UUID
	// Instrument is what moves, and must be in use. It must be a currency: a
	// dealing is counted in the currency both sides settle in.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts of the fees are
	// checked against. It is only used for a fee in an instrument other than the
	// base currency.
	BaseCurrency Instrument
	// Amount is greater than zero, and no finer than the instrument.
	Amount decimal.Decimal
	// Direction is DealingPaidOut or DealingReceived.
	Direction DealingDirection
	// Fees are charges that came with the dealing, each an expense of its own.
	Fees []Fee
}

// NewDealing returns the draft of a dealing: a change in what a party owes me,
// together with the money that moves into or out of an account. A dealing is not
// an expense or an income. When I pay out, which makes what the party owes me
// grow (往來增加), its postings are
//
//	account   -amount
//	dealing   +amount
//
// and when money comes in, which makes it shrink (往來減少), they are
//
//	account   +amount
//	dealing   -amount
//
// followed, for each fee, by the two postings of a fee (see Fee).
func NewDealing(p DealingParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if !p.Direction.Valid() {
		return Transaction{}, fmt.Errorf("%w %q: want paid_out or received", ErrInvalidDealingDirection, p.Direction)
	}
	if err := checkAccount(p.Account); err != nil {
		return Transaction{}, err
	}
	if !p.Instrument.IsCurrency {
		return Transaction{}, fmt.Errorf("%w: a dealing is counted in a currency, and %s is not one", ErrNotCurrency, p.Instrument.Code)
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Transaction{}, err
	}
	if err := checkAmount(p.Instrument, p.Amount); err != nil {
		return Transaction{}, err
	}
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	// Money paid out leaves the account and grows the dealing; money received does
	// the reverse.
	accountQuantity, dealingQuantity := p.Amount.Neg(), p.Amount
	if p.Direction == DealingReceived {
		accountQuantity, dealingQuantity = dealingQuantity, accountQuantity
	}
	accountID, partyID := p.Account.ID, p.PartyID
	postings := make([]Posting, 0, 2+len(fees))
	postings = append(postings,
		Posting{Book: BookAccount, AccountID: &accountID, InstrumentID: p.Instrument.ID, Quantity: accountQuantity},
		Posting{Book: BookDealing, PartyID: &partyID, InstrumentID: p.Instrument.ID, Quantity: dealingQuantity},
	)
	postings = append(postings, fees...)
	return newTransaction(KindDealing, h, postings)
}

// Dealing is what one party owes me in one instrument: the quantities of the
// confirmed dealing postings made to the party, added up. It is worked out when
// asked for and never stored; drafts and void transactions are not in it. A
// positive quantity is what the party owes me, a negative one what I owe them.
type Dealing struct {
	PartyID      uuid.UUID
	InstrumentID uuid.UUID
	Quantity     decimal.Decimal
}
