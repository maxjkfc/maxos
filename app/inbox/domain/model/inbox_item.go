// Package model holds the inbox entities and the invariants they must keep.
package model

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Limits on a note, counted in characters (runes), not bytes.
const (
	// MaxTextRunes is the longest note text that can be stored.
	MaxTextRunes = 4000

	// MinKeyRunes and MaxKeyRunes bound the idempotency key of a note.
	MinKeyRunes = 8
	MaxKeyRunes = 128
)

// Kind is the type of raw input an inbox item holds.
type Kind string

// KindNote is free text taken down verbatim from the owner.
const KindNote Kind = "note"

// InboxItem is one raw input received from outside, kept for later
// verification.
type InboxItem struct {
	ID uuid.UUID
	// ReceivedAt is assigned when the item is stored; it is zero before then.
	ReceivedAt time.Time
	// Source is the channel the item arrived through, e.g. "mcp-http".
	Source string
	// Actor is the identity that sent the item.
	Actor string
	Kind  Kind
	// IdempotencyKey identifies the input: storing the same key again returns
	// the item that was stored first.
	IdempotencyKey string
	// Payload is the raw content as JSON; its shape depends on Kind.
	Payload json.RawMessage
}

// notePayload is the payload of a KindNote item.
type notePayload struct {
	Text string `json:"text"`
}

// NewNoteParams is the raw input of a note.
type NewNoteParams struct {
	ID             uuid.UUID
	Source         string
	Actor          string
	IdempotencyKey string
	Text           string
}

// NewNote validates a note and returns it, ready to be stored. Whitespace
// around the text and the idempotency key is trimmed before they are checked
// and kept.
func NewNote(p NewNoteParams) (InboxItem, error) {
	text := strings.TrimSpace(p.Text)
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return InboxItem{}, ErrTextEmpty
	case n > MaxTextRunes:
		return InboxItem{}, &TextTooLongError{Runes: n, Max: MaxTextRunes}
	}

	key := strings.TrimSpace(p.IdempotencyKey)
	if n := utf8.RuneCountInString(key); n < MinKeyRunes || n > MaxKeyRunes {
		return InboxItem{}, &IdempotencyKeyLengthError{Runes: n, Min: MinKeyRunes, Max: MaxKeyRunes}
	}

	payload, err := json.Marshal(notePayload{Text: text})
	if err != nil {
		return InboxItem{}, fmt.Errorf("encode note: %w", err)
	}
	return InboxItem{
		ID:             p.ID,
		Source:         p.Source,
		Actor:          p.Actor,
		Kind:           KindNote,
		IdempotencyKey: key,
		Payload:        payload,
	}, nil
}

// Text returns the note text held in the payload, or "" when the payload has
// none or is malformed.
func (i InboxItem) Text() string {
	var p notePayload
	if len(i.Payload) == 0 || json.Unmarshal(i.Payload, &p) != nil {
		return ""
	}
	return p.Text
}
