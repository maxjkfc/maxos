// Package mcpserver exposes the maxos inbox as an MCP server.
package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/internal/core"
)

const (
	kindNote = "note"

	maxTextRunes   = 4000
	minKeyRunes    = 8
	maxKeyRunes    = 128
	defaultLimit   = 10
	maxRecentLimit = 50
)

// Inbox is the storage surface the MCP tools need.
type Inbox interface {
	Capture(ctx context.Context, in core.CaptureInput) (core.InboxEntry, bool, error)
	Recent(ctx context.Context, limit int) ([]core.InboxEntry, error)
}

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

type recentInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"How many entries to return, 1 to 50. Defaults to 10."`
}

type recentEntry struct {
	ID         string `json:"id" jsonschema:"Unique id of the inbox entry."`
	ReceivedAt string `json:"received_at" jsonschema:"When the entry was stored, RFC 3339."`
	Source     string `json:"source" jsonschema:"Channel the entry arrived through, for example mcp-http."`
	Actor      string `json:"actor" jsonschema:"Identity that captured the entry."`
	Kind       string `json:"kind" jsonschema:"Entry type, for example note."`
	Status     string `json:"status" jsonschema:"Processing state: received, processed, or rejected."`
	Text       string `json:"text" jsonschema:"The note text when the entry has one, otherwise empty."`
}

type recentOutput struct {
	Entries []recentEntry `json:"entries" jsonschema:"Inbox entries, newest first."`
}

// New builds an MCP server whose tool calls are attributed to actor and source.
func New(inbox Inbox, source, actor string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "maxos"}, nil)

	closed := false
	notDestructive := false
	mcp.AddTool(s, &mcp.Tool{
		Name: "capture",
		Description: "Save one raw note from the owner into the maxos inbox for later processing. " +
			"Use it when the owner asks you to remember, record, or jot something down. " +
			"Pass the owner's words verbatim; do not interpret or restructure them. " +
			"Generate a new random idempotency_key for every distinct note, and reuse the same key only when retrying that same note. " +
			"The result shows the text actually stored; if duplicate is true, an earlier note with the same key was kept and nothing new was written.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Capture note",
			ReadOnlyHint:    false,
			IdempotentHint:  true,
			DestructiveHint: &notDestructive,
			OpenWorldHint:   &closed,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in captureInput) (*mcp.CallToolResult, captureOutput, error) {
		text := strings.TrimSpace(in.Text)
		if n := utf8.RuneCountInString(text); n == 0 {
			return nil, captureOutput{}, fmt.Errorf("text is empty: pass the owner's note verbatim in text (1 to %d characters)", maxTextRunes)
		} else if n > maxTextRunes {
			return nil, captureOutput{}, fmt.Errorf("text is too long (%d characters, max %d): ask the owner to shorten it or split it into several notes, each with its own idempotency_key", n, maxTextRunes)
		}
		key := strings.TrimSpace(in.IdempotencyKey)
		if n := utf8.RuneCountInString(key); n < minKeyRunes || n > maxKeyRunes {
			return nil, captureOutput{}, fmt.Errorf("idempotency_key must be %d to %d characters (got %d): generate a random string such as a UUID", minKeyRunes, maxKeyRunes, n)
		}

		payload, err := json.Marshal(struct {
			Text string `json:"text"`
		}{Text: text})
		if err != nil {
			return nil, captureOutput{}, fmt.Errorf("encode note: %w", err)
		}
		entry, dup, err := inbox.Capture(ctx, core.CaptureInput{
			Source:         source,
			Actor:          actor,
			Kind:           kindNote,
			IdempotencyKey: key,
			Payload:        payload,
		})
		if err != nil {
			return nil, captureOutput{}, fmt.Errorf("could not store the note, retrying with the same idempotency_key is safe: %w", err)
		}
		return nil, captureOutput{
			ID:         entry.ID.String(),
			ReceivedAt: entry.ReceivedAt.UTC().Format(time.RFC3339),
			Duplicate:  dup,
			Text:       payloadText(entry.Payload),
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "inbox_recent",
		Description: "List the most recent maxos inbox entries, newest first. " +
			"Use it to check what has already been captured, for example before re-capturing something the owner may have saved earlier.",
		Annotations: &mcp.ToolAnnotations{
			Title:         "Recent inbox entries",
			ReadOnlyHint:  true,
			OpenWorldHint: &closed,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in recentInput) (*mcp.CallToolResult, recentOutput, error) {
		limit := in.Limit
		if limit == 0 {
			limit = defaultLimit
		}
		limit = min(max(limit, 1), maxRecentLimit)

		entries, err := inbox.Recent(ctx, limit)
		if err != nil {
			return nil, recentOutput{}, fmt.Errorf("could not read the inbox: %w", err)
		}
		out := recentOutput{Entries: make([]recentEntry, 0, len(entries))}
		for _, e := range entries {
			out.Entries = append(out.Entries, recentEntry{
				ID:         e.ID.String(),
				ReceivedAt: e.ReceivedAt.UTC().Format(time.RFC3339),
				Source:     e.Source,
				Actor:      e.Actor,
				Kind:       e.Kind,
				Status:     e.Status,
				Text:       payloadText(e.Payload),
			})
		}
		return nil, out, nil
	})

	return s
}

// payloadText returns the "text" field of a note payload, or "" if absent or malformed.
func payloadText(p json.RawMessage) string {
	var v struct {
		Text string `json:"text"`
	}
	if len(p) == 0 || json.Unmarshal(p, &v) != nil {
		return ""
	}
	return v.Text
}
