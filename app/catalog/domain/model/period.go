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
// the same From and To. Both ends are always set; a period that is not set is
// no Period at all (a Project holds a *Period, nil while it has none).
type Period struct {
	// From and To are dates as DateOf returns them, and From is not after To.
	From, To time.Time
}

// NewPeriod returns the period from the day of from to the day of to. Both are
// required: it returns ErrPeriodIncomplete when either is the zero time, which
// DateOf would otherwise make the first day of the year 1. It returns
// ErrPeriodReversed when to is on an earlier day than from.
func NewPeriod(from, to time.Time) (Period, error) {
	switch {
	case from.IsZero():
		return Period{}, fmt.Errorf("%w: the start is missing", ErrPeriodIncomplete)
	case to.IsZero():
		return Period{}, fmt.Errorf("%w: the end is missing", ErrPeriodIncomplete)
	}
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
