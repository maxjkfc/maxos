package model

import (
	"time"

	"github.com/shopspring/decimal"
)

// The directions of a dealing and of a trade, as Unit.Direction has them.
const (
	directionPaidOut  = "paid_out"
	directionReceived = "received"
	directionBuy      = "buy"
	directionSell     = "sell"
)

// Effects are what recording the unit in the ledger does to the balance of each
// account in each instrument, and on which day: the legs by the kind of the unit,
// each period of an instalment plan on the day it falls due, and every fee, which the
// ledger pays from its own account on the day of the unit. They come in that order,
// not added up, and have no Row: they are the unit's, not a row's.
//
// It is the ledger's side of what the plan compares with what the rows of the unit
// say, to find a fee or a discount that the ledger has no place for (see
// Plan.Differences).
func (u Unit) Effects() []Effect {
	var out []Effect
	add := func(day time.Time, account, instrument string, quantity decimal.Decimal) {
		out = append(out, Effect{Account: account, Instrument: instrument, Date: day, Quantity: quantity})
	}
	gain := func(l Leg) { add(u.Date, l.Account, l.Instrument, l.Amount) }
	lose := func(l Leg) { add(u.Date, l.Account, l.Instrument, l.Amount.Neg()) }
	// A unit with no second leg is not one the ledger would take; it is told by what
	// it has.
	gainOther := func() {
		if u.Other != nil {
			gain(*u.Other)
		}
	}
	loseOther := func() {
		if u.Other != nil {
			lose(*u.Other)
		}
	}

	switch u.Kind {
	case KindExpense:
		lose(u.Main)
	case KindExpenseReward:
		lose(u.Main)
		if u.Reward != nil {
			gain(Leg{Account: u.Main.Account, Instrument: u.Main.Instrument, Amount: u.Reward.Amount})
		}
	case KindIncome, KindReward, KindOpening:
		gain(u.Main)
	case KindDealing:
		switch u.Direction {
		case directionPaidOut:
			lose(u.Main)
		case directionReceived:
			gain(u.Main)
		}
	case KindTransfer, KindExchange:
		lose(u.Main)
		gainOther()
	case KindTrade:
		// Main is what is bought or sold, Other what it is paid in.
		switch u.Direction {
		case directionBuy:
			gain(u.Main)
			loseOther()
		case directionSell:
			lose(u.Main)
			gainOther()
		}
	case KindBillInstalment:
		// The debt is paid off on the day of the conversion, and each period charges
		// the principal and the interest on the day it falls due.
		gain(u.Main)
		for _, p := range u.Periods {
			charged := p.Principal
			if p.Interest != nil {
				charged = charged.Add(*p.Interest)
			}
			add(p.DueOn, u.Main.Account, u.Main.Instrument, charged.Neg())
		}
	}
	for _, f := range u.Fees {
		add(u.Date, f.Account, f.Instrument, f.Amount.Neg())
	}
	return out
}
