package model

import (
	"slices"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// BaseDecimals is how many decimal places the base currency has.
const BaseDecimals = 2

// Rate is what one unit of a currency was worth in the base currency on a day.
type Rate struct {
	Date    time.Time
	PerUnit decimal.Decimal
}

// Rates are the daily rates of the currencies that are not the base currency, by
// code, as the file the user keeps has them. They give the amount in the base
// currency (TWD) that the ledger asks of a foreign expense, income or reward, so
// they are an approximation of what the day's money was worth, not what the bank
// charged; the amount is not changed afterwards.
type Rates struct {
	byCode map[string][]Rate
}

// NewRates makes rates of the entries of each code, in any order.
func NewRates(entries map[string][]Rate) Rates {
	byCode := make(map[string][]Rate, len(entries))
	for code, list := range entries {
		sorted := slices.Clone(list)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Date.Before(sorted[j].Date) })
		byCode[code] = sorted
	}
	return Rates{byCode: byCode}
}

// Lookup returns the rate of the code on the day, or when there is none that day,
// the rate of the nearest earlier day that has one. It returns false when the code
// has no rate on or before the day.
func (r Rates) Lookup(code string, day time.Time) (Rate, bool) {
	list := r.byCode[code]
	i := sort.Search(len(list), func(i int) bool { return list[i].Date.After(day) })
	if i == 0 {
		return Rate{}, false
	}
	return list[i-1], true
}

// BaseAmountOf is the amount of a quantity in the base currency: its product with
// the rate, to the decimals of the base currency, which is never negative.
func BaseAmountOf(rate Rate, quantity decimal.Decimal) decimal.Decimal {
	return quantity.Abs().Mul(rate.PerUnit).Round(BaseDecimals)
}
