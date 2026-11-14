// Package usecase declares what the inbox context offers its callers. It is the
// only part of the inbox that other contexts may depend on.
package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Capture stores a raw note once per idempotency key.
type Capture interface {
	Capture(ctx context.Context, in CaptureInput) (CaptureOutput, error)
}

// CaptureInput is a raw note from outside.
type CaptureInput struct {
	// Source is the channel the note arrived through, e.g. "mcp-http".
	Source string
	// Actor is the identity that sent the note.
	Actor string
	// IdempotencyKey makes the call safe to repeat: a key that was used before
	// returns the note stored under it.
	IdempotencyKey string
	// Text is the note, verbatim.
	Text string
}

// CaptureOutput is the note as stored.
type CaptureOutput struct {
	ID         uuid.UUID
	ReceivedAt time.Time
	// Text is the text actually stored. On a duplicate it is the original
	// text, which may differ from CaptureInput.Text.
	Text string
	// Duplicate is true when the idempotency key was already used: nothing was
	// written.
	Duplicate bool
}
