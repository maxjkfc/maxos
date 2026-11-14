package mcp

import (
	"context"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
)

type captureInput struct {
	Text           string `json:"text" jsonschema:"The owner's words, verbatim, in the language the owner used (usually Traditional Chinese). Do not summarize, translate, or add commentary. 1 to 4000 characters."`
	IdempotencyKey string `json:"idempotency_key" jsonschema:"A fresh random string (8 to 128 characters, for example a UUID) that you generate once per distinct note. If you retry the same note after an error or timeout, reuse the same key so it is not stored twice. Never reuse a key for a different note."`
}

type captureOutput struct {
	ID         string `json:"id" jsonschema:"Unique id of the stored inbox entry."`
	ReceivedAt string `json:"received_at" jsonschema:"When the entry was first stored, RFC 3339."`
	Duplicate  bool   `json:"duplicate" jsonschema:"True if this idempotency_key was already used; nothing new was written and text shows what was saved originally."`
	Text       string `json:"text" jsonschema:"The text that is actually stored. On a duplicate this is the original text, which may differ from what you just sent."`
}

func addCaptureTool(s *sdk.Server, caller mcpserver.Caller, capture usecase.Capture) {
	closed := false
	notDestructive := false
	sdk.AddTool(s, &sdk.Tool{
		Name: "capture",
		Description: "Save one raw note from the owner into the maxos inbox for later processing. " +
			"Use it when the owner asks you to remember, record, or jot something down. " +
			"Pass the owner's words verbatim; do not interpret or restructure them. " +
			"Generate a new random idempotency_key for every distinct note, and reuse the same key only when retrying that same note. " +
			"The result shows the text actually stored; if duplicate is true, an earlier note with the same key was kept and nothing new was written.",
		Annotations: &sdk.ToolAnnotations{
			Title:           "Capture note",
			ReadOnlyHint:    false,
			IdempotentHint:  true,
			DestructiveHint: &notDestructive,
			OpenWorldHint:   &closed,
		},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in captureInput) (*sdk.CallToolResult, captureOutput, error) {
		out, err := capture.Capture(ctx, usecase.CaptureInput{
			Source:         caller.Source,
			Actor:          caller.Actor,
			IdempotencyKey: in.IdempotencyKey,
			Text:           in.Text,
		})
		if err != nil {
			return nil, captureOutput{}, captureError(err)
		}
		return nil, captureOutput{
			ID:         out.ID.String(),
			ReceivedAt: formatTime(out.ReceivedAt),
			Duplicate:  out.Duplicate,
			Text:       out.Text,
		}, nil
	})
}

// captureError turns a Capture failure into the message the model sees. A
// rejected note says how to fix it; anything else says the call is safe to
// retry with the same idempotency_key.
func captureError(err error) error {
	var tooLong *model.TextTooLongError
	var badKey *model.IdempotencyKeyLengthError
	switch {
	case errors.Is(err, model.ErrTextEmpty):
		return fmt.Errorf("text is empty: pass the owner's note verbatim in text (1 to %d characters)", model.MaxTextRunes)
	case errors.As(err, &tooLong):
		return fmt.Errorf("text is too long (%d characters, max %d): ask the owner to shorten it or split it into several notes, each with its own idempotency_key", tooLong.Runes, tooLong.Max)
	case errors.As(err, &badKey):
		return fmt.Errorf("idempotency_key must be %d to %d characters (got %d): generate a random string such as a UUID", badKey.Min, badKey.Max, badKey.Runes)
	default:
		return fmt.Errorf("could not store the note, retrying with the same idempotency_key is safe: %w", err)
	}
}
