package service

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// effectDay is the day a row has its effect on the balance of its account: its own,
// and for an opening balance the day it is dated.
func (b *builder) effectDay(r model.Row) time.Time {
	if r.Type == model.TypeInitial {
		return b.openings[acctCurrency{r.Account, r.Currency}].day
	}
	return r.Date
}

// effectOf is what a row does to the balance of its account in its instrument, as the
// plan counts it: its amount, the fee that is charged beside it and the discount that
// was not paid. The amount of the row of a trade whose quantity the export has as
// nothing is the quantity the user gave.
func (b *builder) effectOf(r model.Row) model.Effect {
	amount := r.Amount
	if given, ok := b.quantities[r.Key]; ok {
		amount = given
	}
	return model.Effect{
		Account:    b.ledger(r.Account),
		Instrument: r.Currency,
		Date:       b.effectDay(r),
		Quantity:   amount.Add(r.Discount).Add(r.Fee),
		Row:        r.Key,
	}
}

// effects works out what every row does to a balance, and which of it the ledger will
// not have because the row is left out.
func (b *builder) effects(skipped map[string]bool) {
	p := &b.plan
	for _, r := range b.rows {
		e := b.effectOf(r)
		if e.Quantity.IsZero() || e.Date.IsZero() {
			continue
		}
		p.Effects = append(p.Effects, e)
		if skipped[r.Key] {
			p.Unrecorded = append(p.Unrecorded, e)
		}
	}
}

// differences compares, for every unit, what its rows do to the balances with what
// the unit will do once it is recorded (model.Unit.Effects), by account, instrument
// and day. A unit that does something else is a difference, and what the ledger will
// lack of its rows is unrecorded: that is how a fee or a discount that the ledger has
// no place for is told, and why a plan with no difference is one whose confirmation
// has nothing unexplained.
func (b *builder) differences() {
	p := &b.plan
	byRow := map[string][]model.Effect{}
	for _, e := range p.Effects {
		byRow[e.Row] = append(byRow[e.Row], e)
	}
	rows := p.RowIndex()
	for _, u := range p.Units {
		var says []model.Effect
		for _, k := range u.Rows {
			says = append(says, byRow[k]...)
		}
		lacks := net(append(says, negated(u.Effects())...))
		if len(lacks) == 0 {
			continue
		}
		for i := range lacks {
			lacks[i].Row = u.Rows[0]
		}
		p.Differences = append(p.Differences, model.Difference{
			Unit:    u.ID,
			Rows:    slices.Clone(u.Rows),
			Reasons: b.reasonsOf(u, rows, lacks),
			Effects: lacks,
		})
		p.Unrecorded = append(p.Unrecorded, lacks...)
	}
}

// reasonsOf says why a unit lacks what it does, in the order fee, discount, other: a
// lack that is the fee columns of its rows where it falls is a fee, one that is their
// discount columns is a discount, and one that is both together is both. Any other is
// something the plan does not know.
func (b *builder) reasonsOf(u model.Unit, rows map[string]model.Row, lacks []model.Effect) []model.DifferenceReason {
	var fees, discounts []model.Effect
	for _, k := range u.Rows {
		fee := b.effectOf(rows[k])
		discount := fee
		fee.Quantity, discount.Quantity = rows[k].Fee, rows[k].Discount
		fees, discounts = append(fees, fee), append(discounts, discount)
	}
	fees, discounts = net(fees), net(discounts)

	var isFee, isDiscount, isOther bool
	for _, e := range lacks {
		fee, discount := quantityAt(fees, e), quantityAt(discounts, e)
		switch {
		case e.Quantity.Equal(fee):
			isFee = true
		case e.Quantity.Equal(discount):
			isDiscount = true
		case e.Quantity.Equal(fee.Add(discount)):
			isFee, isDiscount = true, true
		default:
			isOther = true
		}
	}
	var reasons []model.DifferenceReason
	for _, r := range []struct {
		is     bool
		reason model.DifferenceReason
	}{
		{isFee, model.DifferenceFee},
		{isDiscount, model.DifferenceDiscount},
		{isOther, model.DifferenceOther},
	} {
		if r.is {
			reasons = append(reasons, r.reason)
		}
	}
	return reasons
}

// net adds up the effects that fall on one day in one account and one instrument, and
// leaves out what comes to nothing. The result is in the order of the day, the account
// and the instrument, and its effects are no row's.
func net(effects []model.Effect) []model.Effect {
	sorted := slices.Clone(effects)
	slices.SortStableFunc(sorted, func(a, b model.Effect) int {
		return cmp.Or(a.Date.Compare(b.Date), strings.Compare(a.Account, b.Account), strings.Compare(a.Instrument, b.Instrument))
	})
	var out []model.Effect
	for _, e := range sorted {
		if n := len(out); n > 0 && sameSlot(out[n-1], e) {
			out[n-1].Quantity = out[n-1].Quantity.Add(e.Quantity)
			continue
		}
		e.Row = ""
		out = append(out, e)
	}
	return slices.DeleteFunc(out, func(e model.Effect) bool { return e.Quantity.IsZero() })
}

// negated is the effects the other way round.
func negated(effects []model.Effect) []model.Effect {
	out := make([]model.Effect, len(effects))
	for i, e := range effects {
		e.Quantity = e.Quantity.Neg()
		out[i] = e
	}
	return out
}

// quantityAt is the quantity of the effect that falls where the given one does.
func quantityAt(effects []model.Effect, at model.Effect) decimal.Decimal {
	for _, e := range effects {
		if sameSlot(e, at) {
			return e.Quantity
		}
	}
	return decimal.Zero
}

func sameSlot(a, b model.Effect) bool {
	return a.Date.Equal(b.Date) && a.Account == b.Account && a.Instrument == b.Instrument
}
