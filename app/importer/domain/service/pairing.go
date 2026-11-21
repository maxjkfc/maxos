package service

import (
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// maxGroup is the most rows of one side that are matched by trying the pairings: a
// minute with more transfers than that is not one the import will guess at.
const maxGroup = 8

// minute is a day and a time of day: where the two halves of a transfer meet.
type minute struct {
	day   time.Time
	clock string
}

// transfers finds the two halves of the transfers and records each pair as the
// transfer, the exchange or the trade it is. The halves of a transfer are a 轉出 and
// a 轉入 of the same minute. When a minute has several, the amounts say which belong
// together; where they leave more than one way to pair them, none is taken.
func (b *builder) transfers() {
	outs := map[minute][]model.Row{}
	ins := map[minute][]model.Row{}
	var minutes []minute
	seen := map[minute]bool{}
	for _, r := range b.rows {
		if b.handled[r.Pos] || !r.HasDate() {
			continue
		}
		var side map[minute][]model.Row
		switch r.Type {
		case model.TypeTransferOut:
			side = outs
		case model.TypeTransferIn:
			side = ins
		default:
			continue
		}
		m := minute{r.Date, r.Clock}
		side[m] = append(side[m], r)
		if !seen[m] {
			seen[m] = true
			minutes = append(minutes, m)
		}
	}

	for _, m := range minutes {
		os, is := outs[m], ins[m]
		if len(os) != len(is) || len(os) == 0 {
			for _, r := range append(slices.Clone(os), is...) {
				b.skipWith(model.ReasonUnpaired, minuteCounts(os, is), r)
			}
			continue
		}
		pairs, status := matchPairs(os, is)
		switch status {
		case matchNone:
			for _, r := range append(slices.Clone(os), is...) {
				b.skip(model.ReasonNoPairing, r)
			}
		case matchAmbiguous:
			for _, r := range append(slices.Clone(os), is...) {
				b.skipWith(model.ReasonAmbiguousPair, minuteCounts(os, is), r)
			}
		default:
			for i, j := range pairs {
				b.pair(os[i], is[j])
			}
		}
	}
}

// minuteCounts says how many 轉出 and how many 轉入 a minute has, as the arguments of
// the reasons that tell of it.
func minuteCounts(outs, ins []model.Row) []string {
	return []string{strconv.Itoa(len(outs)), strconv.Itoa(len(ins))}
}

type matchStatus int

const (
	matchUnique matchStatus = iota
	matchNone
	matchAmbiguous
)

// matchPairs pairs the 轉出 with the 轉入 of one minute, by their amounts: two of one
// instrument go together when they are the same amount, two of different instruments
// can go together whatever the amounts. It returns, for each 轉出, the 轉入 it goes
// with, if there is one way to pair them, or if the ways there are all give the same
// transfers (rows that are the same are paired in the order of the file).
func matchPairs(outs, ins []model.Row) ([]int, matchStatus) {
	n := len(outs)
	if n == 1 {
		return []int{0}, matchUnique
	}
	if n > maxGroup {
		return nil, matchAmbiguous
	}
	compatible := func(o, i model.Row) bool {
		if o.Currency == i.Currency {
			return o.Amount.Neg().Equal(i.Amount)
		}
		return true
	}

	var first []int
	signatures := map[string]bool{}
	assign := make([]int, n)
	used := make([]bool, n)
	var walk func(k int)
	walk = func(k int) {
		if len(signatures) > 1 {
			return
		}
		if k == n {
			sig := make([]string, n)
			for a := range assign {
				sig[a] = pairSignature(outs[a], ins[assign[a]])
			}
			sort.Strings(sig)
			signatures[strings.Join(sig, "\n")] = true
			if first == nil {
				first = slices.Clone(assign)
			}
			return
		}
		for j := range ins {
			if used[j] || !compatible(outs[k], ins[j]) {
				continue
			}
			used[j], assign[k] = true, j
			walk(k + 1)
			used[j] = false
		}
	}
	walk(0)
	switch len(signatures) {
	case 0:
		return nil, matchNone
	case 1:
		return first, matchUnique
	default:
		return nil, matchAmbiguous
	}
}

// pairSignature is what a pairing of two rows comes to, so that pairings of rows that
// are alike are told to be the same.
func pairSignature(o, i model.Row) string {
	return strings.Join([]string{
		o.Account, i.Account, o.Currency, i.Currency,
		o.Amount.String(), i.Amount.String(), o.Fee.String(), i.Fee.String(),
	}, "|")
}

// pair records the two halves of a transfer as what they are: a transfer when they
// are of one instrument, an exchange when they are of two currencies, a trade when
// one is not a currency.
func (b *builder) pair(o, i model.Row) {
	b.handled[o.Pos], b.handled[i.Pos] = true, true
	ovO, ovI := b.override(o), b.override(i)
	if ovO.Treatment == model.TreatmentSkip || ovI.Treatment == model.TreatmentSkip {
		b.skip(model.ReasonSkipped, o, i)
		return
	}
	quantity := ovO.Quantity
	if quantity == nil {
		quantity = ovI.Quantity
	}

	j := b.join(o, i)
	u := model.Unit{
		ID:          o.Key + "+" + i.Key,
		Rows:        []string{o.Key, i.Key},
		Date:        o.Date,
		Clock:       o.Clock,
		Order:       min(o.Pos, i.Pos),
		Description: pairDescription(o, i, j.notes),
		Merchant:    j.merchant,
		Area:        j.area,
		Project:     j.project,
		Tags:        j.tags,
		Notes:       j.notes,
	}
	var made bool
	switch {
	case o.Currency == i.Currency:
		made = b.transfer(&u, o, i)
	case b.isCurrency(o.Currency) && b.isCurrency(i.Currency):
		made = b.exchange(&u, o, i)
	case b.isCurrency(o.Currency) != b.isCurrency(i.Currency):
		made = b.trade(&u, o, i, quantity)
	default:
		b.skipWith(model.ReasonNotCurrencies, []string{o.Currency, i.Currency}, o, i)
	}
	if !made {
		return
	}
	u.Fees = append(b.feeOf(o), b.feeOf(i)...)
	b.add(u)
}

// transfer makes the unit of two halves of one instrument: money that moved from one
// account to another. It leaves the rows out, and says it did not make it, when they
// cannot be one.
func (b *builder) transfer(u *model.Unit, o, i model.Row) bool {
	out, in := o.Amount.Neg(), i.Amount
	from, to := b.ledger(o.Account), b.ledger(i.Account)
	switch {
	case !out.Equal(in):
		b.skip(model.ReasonAmountMismatch, o, i)
	case !in.IsPositive():
		b.skip(model.ReasonZeroAmount, o, i)
	case from == to:
		b.skipWith(model.ReasonSameAccount, []string{from}, o, i)
	case !b.fits(o.Currency, in, o.Fee, i.Fee):
		b.skipWith(model.ReasonTooManyDecimals, []string{o.Currency}, o, i)
	default:
		if o.Sub == exchangeSub {
			b.caveat(model.CaveatSameInstrumentExchange, o)
		}
		u.Kind = model.KindTransfer
		u.Main = model.Leg{Account: from, Instrument: o.Currency, Amount: in}
		u.Other = &model.Leg{Account: to, Instrument: i.Currency, Amount: in}
		return true
	}
	return false
}

// exchange makes the unit of two halves in two currencies.
func (b *builder) exchange(u *model.Unit, o, i model.Row) bool {
	out, in := o.Amount.Neg(), i.Amount
	switch {
	case !out.IsPositive() || !in.IsPositive():
		b.skip(model.ReasonZeroAmount, o, i)
	case !b.fits(o.Currency, out, o.Fee) || !b.fits(i.Currency, in, i.Fee):
		b.skipWith(model.ReasonTooManyDecimals, []string{o.Currency, i.Currency}, o, i)
	default:
		u.Kind = model.KindExchange
		u.Main = model.Leg{Account: b.ledger(o.Account), Instrument: o.Currency, Amount: out}
		u.Other = &model.Leg{Account: b.ledger(i.Account), Instrument: i.Currency, Amount: in}
		return true
	}
	return false
}

// trade makes the unit of two halves of which one is a currency and the other is not:
// money bought a coin, or a coin was sold for money. The side that is not a currency
// is the quantity, which is the one the user gave when the export has it as zero.
func (b *builder) trade(u *model.Unit, o, i model.Row, given *decimal.Decimal) bool {
	asset, cash := i, o
	u.Direction = "buy"
	if b.isCurrency(i.Currency) {
		asset, cash = o, i
		u.Direction = "sell"
	}
	assetAmount, cashAmount := asset.Amount.Abs(), cash.Amount.Abs()
	standsIn := assetAmount.IsZero() && given != nil
	if standsIn {
		assetAmount = *given
	}
	switch {
	case !assetAmount.IsPositive():
		b.skipWith(model.ReasonZeroQuantity, []string{asset.Currency}, o, i)
	case !cashAmount.IsPositive():
		b.skip(model.ReasonZeroAmount, o, i)
	case !b.fits(asset.Currency, assetAmount, asset.Fee) || !b.fits(cash.Currency, cashAmount, cash.Fee):
		b.skipWith(model.ReasonTooManyDecimals, []string{o.Currency, i.Currency}, o, i)
	default:
		u.Kind = model.KindTrade
		u.Main = model.Leg{Account: b.ledger(asset.Account), Instrument: asset.Currency, Amount: assetAmount}
		u.Other = &model.Leg{Account: b.ledger(cash.Account), Instrument: cash.Currency, Amount: cashAmount}
		if standsIn {
			// The row of the asset moved what the user says, though the export has it as
			// nothing: that is what it does to the balance, as the sign of its side says.
			b.quantities[asset.Key] = assetAmount
			if u.Direction == "sell" {
				b.quantities[asset.Key] = assetAmount.Neg()
			}
		}
		return true
	}
	return false
}

// pairDescription is what the two halves say of themselves: the name of each, what
// they say, what the unit has no place for, and the category each had.
func pairDescription(o, i model.Row, notes []model.Note) string {
	name := o.Name
	if i.Name != "" && i.Name != o.Name {
		name = joinNonEmpty(" / ", o.Name, i.Name)
	}
	details := []string{name, o.Description}
	if i.Description != "" && i.Description != o.Description {
		details = append(details, i.Description)
	}
	details = append(details, noteLines(notes)...)
	details = append(details, categoryNote(o))
	if categoryNote(i) != categoryNote(o) {
		details = append(details, categoryNote(i))
	}
	return joinNonEmpty("\n", details...)
}
