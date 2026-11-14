package model

import (
	"fmt"
	"time"
)

// DateOf returns the calendar day t falls on, as t's own location reads it, as
// midnight UTC. Every date in the model has this form, so dates compare with
// Before, After and Equal, and are exactly what the database date type holds.
func DateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Period is a run of calendar days, both ends included: a one-day period has
// the same From and To.
type Period struct {
	// From and To are dates as DateOf returns them, and From is not after To.
	From, To time.Time
}

// NewPeriod returns the period from the day of from to the day of to. It
// returns ErrPeriodReversed when to is on an earlier day than from.
func NewPeriod(from, to time.Time) (Period, error) {
	p := Period{From: DateOf(from), To: DateOf(to)}
	if p.To.Before(p.From) {
		return Period{}, fmt.Errorf("%w: %s", ErrPeriodReversed, p)
	}
	return p, nil
}

// Covers reports whether every day of other is a day of p.
func (p Period) Covers(other Period) bool {
	return !other.From.Before(p.From) && !other.To.After(p.To)
}

// String returns the period as "2026-12-01 to 2026-12-08".
func (p Period) String() string {
	return p.From.Format(time.DateOnly) + " to " + p.To.Format(time.DateOnly)
}
