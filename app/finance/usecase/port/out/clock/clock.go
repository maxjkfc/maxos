// Package clock declares what the finance use cases need to know about time: when
// it is, and where. They have no other source of it, so that a test can set the
// time, and so that what "today" means is decided in one place.
package clock

import "time"

// Clock tells the time as the user's calendar reads it.
type Clock interface {
	// Now returns the current time in the timezone of the application. The day it
	// falls on, as its own location reads it (model.DateOf), is today: the day a
	// balance is counted up to when nobody says another, and the day an instalment
	// plan is read on.
	Now() time.Time
}
