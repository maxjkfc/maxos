package model

import "errors"

var (
	// ErrNameEmpty is returned when a name has nothing left once the whitespace
	// around it is trimmed.
	ErrNameEmpty = errors.New("name is empty")

	// ErrPeriodIncomplete is returned when a period lacks its start or its end:
	// a period has both, and one that is not set is no period at all.
	ErrPeriodIncomplete = errors.New("period needs both a start and an end")

	// ErrPeriodReversed is returned when a period ends before it starts.
	ErrPeriodReversed = errors.New("period ends before it starts")

	// ErrActivePeriodOutsidePeriod is returned when the active period of a
	// project would not lie within its period.
	ErrActivePeriodOutsidePeriod = errors.New("active period is not within the project period")

	// ErrNotFound is returned when no entry has the id asked for. Repos return
	// it and use cases pass it on.
	ErrNotFound = errors.New("not found")

	// ErrAlreadyExists is returned when the name of an entry is already taken.
	ErrAlreadyExists = errors.New("already exists")

	// ErrArchived is returned when an archived entry is used where only an entry
	// in use will do.
	ErrArchived = errors.New("archived")
)
