package model

import (
	"github.com/shopspring/decimal"
)

// RewardParams is the input of NewReward.
type RewardParams struct {
	Header
	// Account is the account the reward is paid into, and must be in use.
	Account Account
	// Instrument is what the reward is paid in, and must be in use.
	Instrument Instrument
	// BaseCurrency is the base currency, which the base amounts are checked
	// against. It is only used for an instrument other than the base currency.
	BaseCurrency Instrument
	// Amount is greater than zero, and no finer than the instrument.
	Amount decimal.Decimal
	// BaseAmount is Amount in the base currency, at the rate of the day: greater
	// than zero, and no finer than the base currency. It is required when the
	// instrument is not the base currency; when it is, it may be left out, and
	// must equal Amount if given.
	BaseAmount *decimal.Decimal
	// Fees are charges that came with the reward, each an expense of its own.
	Fees []Fee
}

// NewReward returns the draft of a reward: money or credit that a payment
// instrument gives back after the fact. A reward is not an income, and it does
// not reduce the expense it came from. Its postings are
//
//	account   +amount
//	reward    -amount, base amount
//
// followed, for each fee, by the two postings of a fee (see Fee). Points are not
// tracked: the points that are used to pay at checkout are a reward of the
// amount they paid, recorded next to the expense at full price.
func NewReward(p RewardParams) (Transaction, error) {
	h, err := normalizeHeader(p.Header)
	if err != nil {
		return Transaction{}, err
	}
	if err := checkAccount(p.Account); err != nil {
		return Transaction{}, err
	}
	if err := checkInstrument(p.Instrument); err != nil {
		return Transaction{}, err
	}
	if err := checkAmount(p.Instrument, p.Amount); err != nil {
		return Transaction{}, err
	}
	baseAmount, err := baseAmountOf(p.Instrument, p.BaseCurrency, p.Amount, p.BaseAmount)
	if err != nil {
		return Transaction{}, err
	}
	fees, err := feePostings(p.BaseCurrency, p.Fees)
	if err != nil {
		return Transaction{}, err
	}

	accountID := p.Account.ID
	rewardBase := baseAmount.Neg()
	postings := make([]Posting, 0, 2+len(fees))
	postings = append(postings,
		Posting{Book: BookAccount, AccountID: &accountID, InstrumentID: p.Instrument.ID, Quantity: p.Amount},
		Posting{Book: BookReward, InstrumentID: p.Instrument.ID, Quantity: p.Amount.Neg(), BaseAmount: &rewardBase},
	)
	postings = append(postings, fees...)
	return newTransaction(KindReward, h, postings)
}
