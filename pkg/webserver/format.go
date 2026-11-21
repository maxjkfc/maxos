package webserver

import (
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// minusSign is U+2212, which a reader (and a screen reader) takes for a minus where
// a hyphen could be a dash.
const minusSign = "\u2212"

// FormatAmount formats an amount with exactly decimals decimal places, the number of
// places of its instrument, and a comma between the thousands. A negative amount
// starts with a minus sign, so that the sign tells it apart and not only a colour;
// one that rounds to nothing has no sign.
func FormatAmount(amount decimal.Decimal, decimals int) string {
	places := int32(max(decimals, 0))
	rounded := amount.Round(places)
	text := groupThousands(rounded.Abs().StringFixed(places))
	if rounded.IsNegative() {
		return minusSign + text
	}
	return text
}

// FormatSigned is FormatAmount with a plus sign in front of an amount greater than
// zero: for what moves, which is an increase or a decrease.
func FormatSigned(amount decimal.Decimal, decimals int) string {
	text := FormatAmount(amount, decimals)
	if amount.Round(int32(max(decimals, 0))).IsPositive() {
		return "+" + text
	}
	return text
}

// FormatPercent formats part as a percentage of whole with one decimal place. It is
// a dash when there is no whole to be a part of.
func FormatPercent(part, whole decimal.Decimal) string {
	if whole.IsZero() {
		return "—"
	}
	return part.Div(whole).Mul(decimal.NewFromInt(100)).StringFixed(1) + "%"
}

// FormatDate formats a day, the way a date is kept: at midnight UTC, or in any
// other location whose reading of the day counts. It is 2026-11-14.
func FormatDate(day time.Time) string {
	return day.Format(time.DateOnly)
}

// FormatMonth formats the month a day falls on: 2026-11.
func FormatMonth(day time.Time) string {
	return day.Format("2006-01")
}

// FormatMoment formats a moment as the clock of loc, the timezone of the user, read
// it: 2026-11-14 18:30.
func FormatMoment(moment time.Time, loc *time.Location) string {
	return moment.In(loc).Format("2006-01-02 15:04")
}

// groupThousands puts a comma between the thousands of the whole part of a number
// written as digits, with a decimal point or without one.
func groupThousands(number string) string {
	whole, fraction, hasFraction := strings.Cut(number, ".")
	if len(whole) > 3 {
		var b strings.Builder
		lead := len(whole) % 3
		if lead > 0 {
			b.WriteString(whole[:lead])
		}
		for i := lead; i < len(whole); i += 3 {
			if b.Len() > 0 {
				b.WriteByte(',')
			}
			b.WriteString(whole[i : i+3])
		}
		whole = b.String()
	}
	if hasFraction {
		return whole + "." + fraction
	}
	return whole
}
