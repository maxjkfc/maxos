package model

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// The days of the month a closing day and a payment due day can be.
const (
	MinDayOfMonth = 1
	MaxDayOfMonth = 31
)

// A statement (帳單) is what a card posted between two closing days, and what is
// owed at the end of it. It is computed from the transactions of the card when it
// is asked for and never stored, so there is nothing for it to disagree with.
//
// The closing days of a card are one for each month: the day of the month the card
// is set to close on, or the last day of the month when the month is shorter (31
// is always the last day). A statement covers the days after the previous closing
// date up to and including its own, so a day is on exactly one statement. What is
// on it, and the figures it adds up to, are the repo's to read; the days a
// statement covers and when it is due are the arithmetic of this file.
//
// The closing day of a card is not historised: the statements are cut from the day
// the card has now, so changing it cuts the past statements again.

// StatementPeriod is the days a statement covers, both ends included: from the day
// after the previous closing date up to and including the closing date. Both are
// dates at midnight UTC (see DateOf).
type StatementPeriod struct {
	// From is the first day of the statement.
	From time.Time
	// ClosingOn is the closing date: the last day of the statement.
	ClosingOn time.Time
}

// DateInMonth returns the date in the month the time falls on that has the day: the
// last day of the month when the month has fewer days than that, so that 31 is
// always the last day, and 29 to 31 are in February. day is from MinDayOfMonth to
// MaxDayOfMonth. The date is at midnight UTC.
func DateInMonth(month time.Time, day int) time.Time {
	last := LastDayOf(month)
	if day >= last.Day() {
		return last
	}
	return time.Date(last.Year(), last.Month(), day, 0, 0, 0, 0, time.UTC)
}

// StatementContaining returns the statement of a card with the closing day that
// covers the day: the one whose closing date is the first on or after it. The day
// of the closing date is on its statement, and the day after it is on the next.
func StatementContaining(closingDay int, day time.Time) StatementPeriod {
	day = DateOf(day)
	month := MonthOf(day)
	closing := DateInMonth(month, closingDay)
	if day.After(closing) {
		month = month.AddDate(0, 1, 0)
		closing = DateInMonth(month, closingDay)
	}
	previous := DateInMonth(month.AddDate(0, -1, 0), closingDay)
	return StatementPeriod{From: previous.AddDate(0, 0, 1), ClosingOn: closing}
}

// StatementsClosingBetween returns the statements of a card with the closing day
// whose closing date is from from to to, both ends included, the oldest first. It
// is none when no closing date falls in the range.
func StatementsClosingBetween(closingDay int, from, to time.Time) []StatementPeriod {
	from, to = DateOf(from), DateOf(to)
	var periods []StatementPeriod
	for month := MonthOf(from); !month.After(to); month = month.AddDate(0, 1, 0) {
		closing := DateInMonth(month, closingDay)
		if closing.Before(from) || closing.After(to) {
			continue
		}
		previous := DateInMonth(month.AddDate(0, -1, 0), closingDay)
		periods = append(periods, StatementPeriod{From: previous.AddDate(0, 0, 1), ClosingOn: closing})
	}
	return periods
}

// DueDate returns the day the statement is to be paid by: the first date after
// its closing date, strictly, whose day is the payment due day, which in a month
// with fewer days is the last day of the month. A due day that is not after the
// closing day of the month is in the next month, and one that is after it is in the
// same month.
func (p StatementPeriod) DueDate(paymentDueDay int) time.Time {
	month := MonthOf(p.ClosingOn)
	due := DateInMonth(month, paymentDueDay)
	if due.After(p.ClosingOn) {
		return due
	}
	return DateInMonth(month.AddDate(0, 1, 0), paymentDueDay)
}

// Closed reports whether the statement has closed by today: its closing date is
// before today. On the closing date itself it is still open.
func (p StatementPeriod) Closed(today time.Time) bool {
	return p.ClosingOn.Before(DateOf(today))
}

// StatementFigures is what a card did in one instrument on one statement. A card
// can hold several instruments, and each has figures of its own. Previous and
// Closing have the sign of a balance: negative is what the card owes.
type StatementFigures struct {
	InstrumentID uuid.UUID
	// Previous is what the card owed, or held, when the statement began: the
	// postings with a statement date before it, added up.
	Previous decimal.Decimal
	// Charges is the negative postings of the statement added up, as a positive
	// number: what was spent on the card.
	Charges decimal.Decimal
	// Credits is the positive postings of the statement added up: payments, refunds
	// and rewards.
	Credits decimal.Decimal
	// Closing is Previous less Charges plus Credits, what is owed, or held, at the
	// end of the statement. It is Previous of the next statement.
	Closing decimal.Decimal
}

// NewStatementFigures returns the figures of an instrument that carried previous
// into the statement and had charges and credits on it, with the closing balance
// they come to. charges is the size of the negative postings.
func NewStatementFigures(instrumentID uuid.UUID, previous, charges, credits decimal.Decimal) StatementFigures {
	return StatementFigures{
		InstrumentID: instrumentID,
		Previous:     previous,
		Charges:      charges,
		Credits:      credits,
		Closing:      previous.Sub(charges).Add(credits),
	}
}
