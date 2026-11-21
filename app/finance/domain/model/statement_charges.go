package model

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// CardCharges is what a card was charged in one instrument on a statement, and what
// it was charged for: the Charges of the figures of the instrument (see
// StatementFigures) divided up. What each transaction charged goes to what it was
// for and no more than what it charged, so that Categories, Shares and Other always
// add up to it.
type CardCharges struct {
	InstrumentID uuid.UUID
	// Categories are the top-level categories the charges were for, the largest
	// amount first and, for the same amount, by category id.
	Categories []CategoryAmount
	// Shares is what was charged for others: the dealing lines of what I paid for
	// them, which they owe me.
	Shares decimal.Decimal
	// Other is what no expense or dealing line explains: a transfer out of the card,
	// the principal of an instalment period.
	Other decimal.Decimal
}

// CategoryAmount is an amount that is for a category.
type CategoryAmount struct {
	CategoryID uuid.UUID
	Amount     decimal.Decimal
}

// Total returns what the instrument was charged: the categories, the shares and the
// rest added up.
func (c CardCharges) Total() decimal.Decimal {
	total := c.Shares.Add(c.Other)
	for _, category := range c.Categories {
		total = total.Add(category.Amount)
	}
	return total
}

// ChargesOfCard divides up what the transactions charged the card, for each
// instrument it was charged in, by what the charge was for. topLevel says which
// top-level category a category is, itself for a top-level one.
//
// The charge of a transaction in an instrument is what its account postings to the
// card in it take, the negative ones added up. It is spread over the lines of the
// transaction in that instrument, in the order of its postings: an expense line,
// which is one of any kind of transaction (the fee of a transfer, the interest of an
// instalment period are lines like those of an expense), goes to the top-level
// category of its own category, and a dealing line that grows what a party owes me,
// what I paid for them, to Shares. A line is taken for no more than what is left of
// the charge, so a transaction that was paid with the card and something else
// charges the card only its part. What is left of the charge when the lines are done
// is Other. A transaction that charged the card nothing, because it only credited
// it, is for nothing.
//
// The result is in no order that matters but one that is the same every time: by
// the id of the instrument. Only the instruments that were charged are in it.
func ChargesOfCard(cardID uuid.UUID, transactions []Transaction, topLevel func(categoryID uuid.UUID) uuid.UUID) []CardCharges {
	var (
		charged = make(map[uuid.UUID]*CardCharges)
		byTop   = make(map[uuid.UUID]map[uuid.UUID]decimal.Decimal) // instrument -> top-level category -> amount
	)
	for _, txn := range transactions {
		for instrument, charge := range chargedToCard(txn, cardID) {
			c, ok := charged[instrument]
			if !ok {
				c = &CardCharges{InstrumentID: instrument}
				charged[instrument] = c
				byTop[instrument] = make(map[uuid.UUID]decimal.Decimal)
			}
			left := charge
			for _, p := range txn.Postings {
				if left.IsZero() {
					break
				}
				if p.InstrumentID != instrument || !p.Quantity.IsPositive() {
					continue
				}
				switch {
				case p.Book == BookExpense && p.CategoryID != nil:
					take := decimal.Min(p.Quantity, left)
					top := topLevel(*p.CategoryID)
					byTop[instrument][top] = byTop[instrument][top].Add(take)
					left = left.Sub(take)
				case p.Book == BookDealing:
					take := decimal.Min(p.Quantity, left)
					c.Shares = c.Shares.Add(take)
					left = left.Sub(take)
				}
			}
			c.Other = c.Other.Add(left)
		}
	}

	out := make([]CardCharges, 0, len(charged))
	for instrument, c := range charged {
		for category, amount := range byTop[instrument] {
			c.Categories = append(c.Categories, CategoryAmount{CategoryID: category, Amount: amount})
		}
		slices.SortFunc(c.Categories, func(a, b CategoryAmount) int {
			return cmp.Or(b.Amount.Cmp(a.Amount), bytes.Compare(a.CategoryID[:], b.CategoryID[:]))
		})
		out = append(out, *c)
	}
	slices.SortFunc(out, func(a, b CardCharges) int { return bytes.Compare(a.InstrumentID[:], b.InstrumentID[:]) })
	return out
}

// chargedToCard returns what the transaction charged the card, for each instrument
// it charged it in, as a positive number: the account postings to the card that take
// from it, added up. An instalment posting to the card is no charge, and a posting
// that credits the card is no charge either.
func chargedToCard(txn Transaction, cardID uuid.UUID) map[uuid.UUID]decimal.Decimal {
	var charges map[uuid.UUID]decimal.Decimal
	for _, p := range txn.Postings {
		if p.Book != BookAccount || p.AccountID == nil || *p.AccountID != cardID || !p.Quantity.IsNegative() {
			continue
		}
		if charges == nil {
			charges = make(map[uuid.UUID]decimal.Decimal)
		}
		charges[p.InstrumentID] = charges[p.InstrumentID].Add(p.Quantity.Neg())
	}
	return charges
}
