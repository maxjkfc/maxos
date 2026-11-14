package model_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
)

func newNote(text, key string) (model.InboxItem, error) {
	return model.NewNote(model.NewNoteParams{
		ID:             uuid.Must(uuid.NewV7()),
		Source:         "mcp-http",
		Actor:          "max@example.com",
		IdempotencyKey: key,
		Text:           text,
	})
}

func TestNewNoteKeepsTrimmedTextAndKey(t *testing.T) {
	item, err := newNote("  午餐 120 元 \n", "  key-00000001 ")
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	if got := item.Text(); got != "午餐 120 元" {
		t.Errorf("Text() = %q, want the trimmed text", got)
	}
	if item.IdempotencyKey != "key-00000001" {
		t.Errorf("IdempotencyKey = %q, want the trimmed key", item.IdempotencyKey)
	}
	// The stored payload is a persisted format: other code reads it back.
	if got, want := string(item.Payload), `{"text":"午餐 120 元"}`; got != want {
		t.Errorf("Payload = %s, want %s", got, want)
	}
}

func TestNewNoteRejectsBlankText(t *testing.T) {
	for _, text := range []string{"", "   ", " \n\t "} {
		if _, err := newNote(text, "key-00000001"); !errors.Is(err, model.ErrTextEmpty) {
			t.Errorf("text %q: err = %v, want ErrTextEmpty", text, err)
		}
	}
}

func TestNewNoteReportsTextBeforeKey(t *testing.T) {
	if _, err := newNote("", "x"); !errors.Is(err, model.ErrTextEmpty) {
		t.Fatalf("err = %v, want ErrTextEmpty", err)
	}
}

func TestNewNoteTextLimitCountsCharactersNotBytes(t *testing.T) {
	atLimit := strings.Repeat("好", model.MaxTextRunes) // 4000 characters, 12000 bytes

	// Whitespace around the text does not count toward the limit.
	if _, err := newNote("  "+atLimit+"  ", "key-00000001"); err != nil {
		t.Fatalf("text at the limit rejected: %v", err)
	}

	_, err := newNote(atLimit+"好", "key-00000001")
	var tooLong *model.TextTooLongError
	if !errors.As(err, &tooLong) {
		t.Fatalf("err = %v, want a TextTooLongError", err)
	}
	if tooLong.Runes != model.MaxTextRunes+1 || tooLong.Max != model.MaxTextRunes {
		t.Errorf("error = %+v, want %d characters against a max of %d", tooLong, model.MaxTextRunes+1, model.MaxTextRunes)
	}
}

func TestNewNoteIdempotencyKeyLength(t *testing.T) {
	tests := []struct {
		name      string
		key       string
		wantRunes int // 0 when the key must be accepted
	}{
		{name: "one short of the minimum", key: "key-007", wantRunes: 7},
		{name: "minimum", key: strings.Repeat("k", model.MinKeyRunes)},
		{name: "maximum", key: strings.Repeat("k", model.MaxKeyRunes)},
		{name: "one over the maximum", key: strings.Repeat("k", model.MaxKeyRunes+1), wantRunes: model.MaxKeyRunes + 1},
		{name: "length is counted after trimming", key: "  short  ", wantRunes: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := newNote("hello", tt.key)
			if tt.wantRunes == 0 {
				if err != nil {
					t.Fatalf("key rejected: %v", err)
				}
				return
			}
			var badKey *model.IdempotencyKeyLengthError
			if !errors.As(err, &badKey) {
				t.Fatalf("err = %v, want an IdempotencyKeyLengthError", err)
			}
			if badKey.Runes != tt.wantRunes || badKey.Min != model.MinKeyRunes || badKey.Max != model.MaxKeyRunes {
				t.Errorf("error = %+v, want %d characters against %d to %d", badKey, tt.wantRunes, model.MinKeyRunes, model.MaxKeyRunes)
			}
		})
	}
}

func TestTextIsEmptyWhenPayloadHasNoNoteText(t *testing.T) {
	for _, payload := range []string{``, `not json`, `{}`, `{"other":"x"}`, `{"text":42}`} {
		item := model.InboxItem{Payload: json.RawMessage(payload)}
		if got := item.Text(); got != "" {
			t.Errorf("payload %q: Text() = %q, want empty", payload, got)
		}
	}
}
