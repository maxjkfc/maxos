package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
)

type recentInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"How many entries to return, 1 to 50. Defaults to 10."`
}

type recentEntry struct {
	ID         string `json:"id" jsonschema:"Unique id of the inbox entry."`
	ReceivedAt string `json:"received_at" jsonschema:"When the entry was stored, RFC 3339."`
	Source     string `json:"source" jsonschema:"Channel the entry arrived through, for example mcp-http."`
	Actor      string `json:"actor" jsonschema:"Identity that captured the entry."`
	Kind       string `json:"kind" jsonschema:"Entry type, for example note."`
	Text       string `json:"text" jsonschema:"The note text when the entry has one, otherwise empty."`
}

type recentOutput struct {
	Entries []recentEntry `json:"entries" jsonschema:"Inbox entries, newest first."`
}

func addInboxRecentTool(s *sdk.Server, recent usecase.ListRecent) {
	closed := false
	sdk.AddTool(s, &sdk.Tool{
		Name: "inbox_recent",
		Description: "List the most recent maxos inbox entries, newest first. " +
			"Use it to check what has already been captured, for example before re-capturing something the owner may have saved earlier.",
		Annotations: &sdk.ToolAnnotations{
			Title:         "Recent inbox entries",
			ReadOnlyHint:  true,
			OpenWorldHint: &closed,
		},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, in recentInput) (*sdk.CallToolResult, recentOutput, error) {
		out, err := recent.ListRecent(ctx, usecase.ListRecentInput{Limit: in.Limit})
		if err != nil {
			return nil, recentOutput{}, fmt.Errorf("could not read the inbox: %w", err)
		}
		entries := make([]recentEntry, 0, len(out.Items))
		for _, item := range out.Items {
			entries = append(entries, recentEntry{
				ID:         item.ID.String(),
				ReceivedAt: formatTime(item.ReceivedAt),
				Source:     item.Source,
				Actor:      item.Actor,
				Kind:       item.Kind,
				Text:       item.Text,
			})
		}
		return nil, recentOutput{Entries: entries}, nil
	})
}
