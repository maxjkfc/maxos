package cmd_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/fx"

	"github.com/maxjkfc/maxos/cmd"
	"github.com/maxjkfc/maxos/internal/testpg"
	"github.com/maxjkfc/maxos/internal/webtest"
	"github.com/maxjkfc/maxos/pkg/cfaccess"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// mcpApp serves what `serve` serves for MCP, on a real PostgreSQL: the inbox and its
// tools, behind the verifier and the headers, in the server that serve runs. It
// returns the address. The verifier is the stand-in that trusts a key of the test.
func mcpApp(t *testing.T, access *webtest.Access) string {
	t.Helper()
	var handler http.Handler
	app := fx.New(
		fx.NopLogger,
		fx.Supply(testpg.New(t)),
		fx.Provide(
			func() *cfaccess.Verifier { return access.Verifier() },
			func() *webserver.Registry { return webserver.NewRegistry(slog.New(slog.DiscardHandler), nil) },
		),
		cmd.InboxModule(),
		cmd.MCPModule(),
		cmd.HTTPModule(),
		fx.Populate(&handler),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("assemble the modules: %v", err)
	}
	return serveOn(t, handler)
}

// captured is what the capture tool gives back.
type captured struct {
	ID        string `json:"id"`
	Duplicate bool   `json:"duplicate"`
	Text      string `json:"text"`
}

// TestMCPCaptureThroughTheRealHandler has a client of the MCP SDK sign in, do the
// handshake of the protocol and call the capture tool of the inbox, in the server and
// the handler that serve runs, on a real PostgreSQL. The note is stored for who is
// signed in, what the client gets is the result of the tool as the tool made it, and
// every answer of the endpoint, the event stream of a call included, carries the five
// headers, with a Cache-Control that no cache may keep.
func TestMCPCaptureThroughTheRealHandler(t *testing.T) {
	access := webtest.NewAccess(t)
	addr := mcpApp(t, access)

	tests := []struct {
		name      string
		version   string // of the protocol; the newest of the SDK when empty
		handshake string // the message that opens the session
		note      string
	}{
		{"initialize and initialized, as the clients of today do (2025-06-18)", "2025-06-18", "initialize", "午餐 120，麵店"},
		{"server/discover, as the newest protocol of the SDK does", "", "server/discover", "晚餐 380，拉麵"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			session, transport := connectMCP(ctx, t, access, addr, tt.version, nil)
			note := tt.note
			key := "mcp-headers-" + uuid.NewString()

			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      "capture",
				Arguments: map[string]any{"text": note, "idempotency_key": key},
			})

			if err != nil {
				t.Fatalf("call capture: %v", err)
			}
			if res.IsError {
				t.Fatalf("capture failed: %s", toolText(res))
			}
			var fromStructured, fromContent captured
			remarshal(t, "the structured content", res.StructuredContent, &fromStructured)
			if err := json.Unmarshal([]byte(toolText(res)), &fromContent); err != nil {
				t.Fatalf("the text content of the result is not the output of the tool: %v\n%s", err, toolText(res))
			}
			if fromStructured != fromContent {
				t.Errorf("structured content %+v and text content %+v of one result differ", fromStructured, fromContent)
			}
			if _, err := uuid.Parse(fromStructured.ID); err != nil || fromStructured.Text != note || fromStructured.Duplicate {
				t.Errorf("capture = %+v, want a new note %q with an id", fromStructured, note)
			}

			// What was stored is what a later call shows, for the signed-in user.
			recent, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "inbox_recent", Arguments: map[string]any{"limit": 5}})
			if err != nil || recent.IsError {
				t.Fatalf("call inbox_recent: %v %s", err, toolText(recent))
			}
			var entries struct {
				Entries []struct {
					ID, Source, Actor, Kind, Text string
				}
			}
			remarshal(t, "the entries of inbox_recent", recent.StructuredContent, &entries)
			var stored bool
			for _, e := range entries.Entries {
				if e.ID == fromStructured.ID {
					stored = true
					if e.Text != note || e.Source != "mcp-http" || e.Actor != "max@example.com" || e.Kind != "note" {
						t.Errorf("stored entry = %+v, want the note %q of max@example.com from mcp-http", e, note)
					}
				}
			}
			if !stored {
				t.Errorf("the note %s is not among the recent entries: %+v", fromStructured.ID, entries.Entries)
			}

			// The handshake, the calls and the rest were all answered with the headers.
			if got := len(transport.answersTo(tt.handshake)); got != 1 {
				t.Errorf("the server answered %d %s, want 1", got, tt.handshake)
			}
			if got := len(transport.answersTo("tools/call")); got != 2 {
				t.Errorf("the server answered %d calls, want 2", got)
			}
			wantEveryAnswerHeld(t, transport)
		})
	}

	// The same call with no client but net/http, to see the bytes: one event, whole.
	t.Run("the bytes of the answer", func(t *testing.T) {
		note := "房租 15000"
		call := map[string]any{
			"jsonrpc": "2.0", "id": 7, "method": "tools/call",
			"params": map[string]any{"name": "capture", "arguments": map[string]any{"text": note, "idempotency_key": "mcp-bytes-" + uuid.NewString()}},
		}
		body, err := json.Marshal(call)
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/mcp", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set(webtest.Header, access.Token(t, "max@example.com"))

		res, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			t.Fatalf("POST /mcp: %v", err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatalf("read the answer: %v", err)
		}

		if res.StatusCode != http.StatusOK {
			t.Fatalf("POST /mcp = %d, want 200:\n%s", res.StatusCode, raw)
		}
		wantHeaders(t, "POST /mcp tools/call", res.Header, mcpCacheControl)
		if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
			t.Errorf("Content-Type = %q, want text/event-stream", ct)
		}
		data, ok := strings.CutPrefix(strings.TrimSuffix(string(raw), "\n\n"), "event: message\ndata: ")
		if !ok || strings.Contains(data, "\n") {
			t.Fatalf("the answer is not one event of a message, whole:\n%q", raw)
		}
		var message struct {
			ID     int `json:"id"`
			Result struct {
				IsError           bool     `json:"isError"`
				StructuredContent captured `json:"structuredContent"`
				Content           []struct{ Type, Text string }
			} `json:"result"`
			Error *struct{ Message string } `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &message); err != nil {
			t.Fatalf("the event is not a JSON-RPC message: %v\n%s", err, data)
		}
		if message.Error != nil || message.ID != 7 || message.Result.IsError {
			t.Fatalf("the message = %s, want the result of call 7", data)
		}
		if got := message.Result.StructuredContent; got.Text != note || got.Duplicate {
			t.Errorf("structured content = %+v, want a new note %q", got, note)
		}
		if len(message.Result.Content) != 1 || message.Result.Content[0].Type != "text" {
			t.Fatalf("content = %+v, want one text", message.Result.Content)
		}
		var fromContent captured
		if err := json.Unmarshal([]byte(message.Result.Content[0].Text), &fromContent); err != nil || fromContent != message.Result.StructuredContent {
			t.Errorf("text content = %q (%v), want the output of the tool: %+v", message.Result.Content[0].Text, err, message.Result.StructuredContent)
		}
	})
}

// remarshal decodes into v what a result holds as structured content, which the client
// gives as the generic form of its JSON.
func remarshal(t *testing.T, what string, from any, v any) {
	t.Helper()
	b, err := json.Marshal(from)
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s is not what the tool gives: %v\n%s", what, err, b)
	}
}
