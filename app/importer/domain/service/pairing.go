package service

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

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
				b.skip(model.ReasonUnpaired, fmt.Sprintf("%d 轉出、%d 轉入", len(os), len(is)), r)
			}
			continue
		}
		pairs, status := matchPairs(os, is)
		switch status {
		case matchNone:
			for _, r := range append(slices.Clone(os), is...) {
				b.skip(model.ReasonUnpaired, "金額對不上任何一種配法", r)
			}
		case matchAmbiguous:
			for _, r := range append(slices.Clone(os), is...) {
				b.skip(model.ReasonAmbiguousPair, fmt.Sprintf("%d 筆轉出、%d 筆轉入，有不只一種配法", len(os), len(is)), r)
			}
		default:
			for i, j := range pairs {
				b.pair(os[i], is[j])
			}
		}
	}
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
		b.skip(model.ReasonSkipped, "", o, i)
		return
	}
	quantity := ovO.Quantity
	if quantity == nil {
		quantity = ovI.Quantity
	}

	fromAccount, toAccount := b.ledger(o.Account), b.ledger(i.Account)
	u := model.Unit{
		Key:         model.KeyPrefix + o.Key + "+" + i.Key,
		Rows:        []string{o.Key, i.Key},
		Date:        o.Date,
		Clock:       o.Clock,
		Order:       min(o.Pos, i.Pos),
		Description: pairDescription(o, i),
	}
	out, in := o.Amount.Neg(), i.Amount

	switch {
	case o.Currency == i.Currency:
		if !out.Equal(in) {
			b.skip(model.ReasonAmountMismatch, "", o, i)
			return
		}
		if !in.IsPositive() {
			b.skip(model.ReasonZeroAmount, "", o, i)
			return
		}
		if fromAccount == toAccount {
			b.skip(model.ReasonSameAccount, fromAccount, o, i)
			return
		}
		if !b.fits(o.Currency, in) {
			b.skip(model.ReasonTooManyDecimals, o.Currency, o, i)
			return
		}
		if o.Sub == exchangeSub {
			b.caveat(model.CaveatSameInstrumentExchange, o, "")
		}
		u.Kind = model.KindTransfer
		u.Main = model.Leg{Account: fromAccount, Instrument: o.Currency, Amount: in}
		u.Other = &model.Leg{Account: toAccount, Instrument: i.Currency, Amount: in}

	case b.isCurrency(o.Currency) && b.isCurrency(i.Currency):
		if !out.IsPositive() || !in.IsPositive() {
			b.skip(model.ReasonZeroAmount, "", o, i)
			return
		}
		if !b.fits(o.Currency, out) || !b.fits(i.Currency, in) {
			b.skip(model.ReasonTooManyDecimals, o.Currency+"/"+i.Currency, o, i)
			return
		}
		u.Kind = model.KindExchange
		u.Main = model.Leg{Account: fromAccount, Instrument: o.Currency, Amount: out}
		u.Other = &model.Leg{Account: toAccount, Instrument: i.Currency, Amount: in}

	case b.isCurrency(o.Currency) != b.isCurrency(i.Currency):
		// One side is a currency and the other is not: money bought a coin, or a coin
		// was sold for money. The side that is not a currency is the quantity.
		asset, cash := i, o
		u.Direction = "buy"
		if b.isCurrency(i.Currency) {
			asset, cash = o, i
			u.Direction = "sell"
		}
		assetAmount, cashAmount := asset.Amount.Abs(), cash.Amount.Abs()
		if assetAmount.IsZero() && quantity != nil {
			assetAmount = *quantity
		}
		if !assetAmount.IsPositive() {
			b.skip(model.ReasonZeroQuantity, asset.Currency, o, i)
			return
		}
		if !cashAmount.IsPositive() {
			b.skip(model.ReasonZeroAmount, "", o, i)
			return
		}
		if !b.fits(asset.Currency, assetAmount) || !b.fits(cash.Currency, cashAmount) {
			b.skip(model.ReasonTooManyDecimals, o.Currency+"/"+i.Currency, o, i)
			return
		}
		u.Kind = model.KindTrade
		u.Main = model.Leg{Account: b.ledger(asset.Account), Instrument: asset.Currency, Amount: assetAmount}
		u.Other = &model.Leg{Account: b.ledger(cash.Account), Instrument: cash.Currency, Amount: cashAmount}

	default:
		b.skip(model.ReasonNotCurrencies, o.Currency+"/"+i.Currency, o, i)
		return
	}
	u.Fees = append(b.feeOf(o), b.feeOf(i)...)
	b.add(u)
}

// pairDescription is what the two halves say of themselves: the name of each, what
// they say, and the category they have.
func pairDescription(o, i model.Row) string {
	name := o.Name
	if i.Name != "" && i.Name != o.Name {
		name = joinNonEmpty(" / ", o.Name, i.Name)
	}
	details := []string{name, o.Description}
	if i.Description != "" && i.Description != o.Description {
		details = append(details, i.Description)
	}
	details = append(details, categoryNote(o))
	return joinNonEmpty("\n", details...)
}
