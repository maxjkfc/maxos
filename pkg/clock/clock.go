// Package clock tells the time in a timezone.
package clock

import "time"

// System is the wall clock, read in a timezone.
type System struct {
	loc *time.Location
}

// New returns a clock that tells the time in loc.
func New(loc *time.Location) System {
	return System{loc: loc}
}

// Now returns the current time in the timezone of the clock, so that the day it
// falls on, as its own location reads it, is the day it is there.
func (c System) Now() time.Time {
	return time.Now().In(c.loc)
}
