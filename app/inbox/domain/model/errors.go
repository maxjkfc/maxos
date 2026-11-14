package model

import (
	"errors"
	"fmt"
)

// ErrTextEmpty is returned when a note has no text once the whitespace around
// it is trimmed.
var ErrTextEmpty = errors.New("text is empty")

// TextTooLongError is returned when the text of a note exceeds MaxTextRunes.
type TextTooLongError struct {
	// Runes is the length of the rejected text, in characters.
	Runes int
	// Max is the longest text allowed, in characters.
	Max int
}

func (e *TextTooLongError) Error() string {
	return fmt.Sprintf("text is too long (%d characters, max %d)", e.Runes, e.Max)
}

// IdempotencyKeyLengthError is returned when an idempotency key is shorter than
// MinKeyRunes or longer than MaxKeyRunes.
type IdempotencyKeyLengthError struct {
	// Runes is the length of the rejected key, in characters.
	Runes int
	// Min and Max are the shortest and longest keys allowed, in characters.
	Min, Max int
}

func (e *IdempotencyKeyLengthError) Error() string {
	return fmt.Sprintf("idempotency key must be %d to %d characters (got %d)", e.Min, e.Max, e.Runes)
}
