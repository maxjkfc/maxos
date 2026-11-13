package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/internal/core"
)

type fakeInbox struct {
	mu      sync.Mutex
	byKey   map[string]core.InboxEntry
	ordered []core.InboxEntry
}

func newFakeInbox() *fakeInbox {
	return &fakeInbox{byKey: map[string]core.InboxEntry{}}
}

func (f *fakeInbox) Capture(_ context.Context, in core.CaptureInput) (core.InboxEntry, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.byKey[in.IdempotencyKey]; ok {
		return e, true, nil
	}
	e := core.InboxEntry{
		ID:             uuid.Must(uuid.NewV7()),
		ReceivedAt:     time.Date(2026, 1, 1, 0, 0, len(f.ordered), 0, time.UTC),
		Source:         in.Source,
		Actor:          in.Actor,
		Kind:           in.Kind,
		IdempotencyKey: in.IdempotencyKey,
		Payload:        in.Payload,
		Status:         "received",
	}
	f.byKey[in.IdempotencyKey] = e
	f.ordered = append(f.ordered, e)
	return e, false, nil
}

func (f *fakeInbox) Recent(_ context.Context, limit int) ([]core.InboxEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []core.InboxEntry
	for i := len(f.ordered) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, f.ordered[i])
	}
	return out, nil
}

func connect(t *testing.T, inbox Inbox) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := New(inbox, "mcp-http", "max@example.com").Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return res
}

func decode[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	if res.IsError {
		t.Fatalf("unexpected tool error: %+v", res.Content)
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, err)
	}
	return v
}

func TestCaptureStoresNote(t *testing.T) {
	inbox := newFakeInbox()
	cs := connect(t, inbox)

	res := call(t, cs, "capture", map[string]any{"text": "  午餐 120 元  ", "idempotency_key": "key-00000001"})
	out := decode[captureOutput](t, res)
	if out.Duplicate {
		t.Fatalf("duplicate = true on first capture")
	}
	if out.Text != "午餐 120 元" {
		t.Fatalf("text = %q", out.Text)
	}
	if _, err := time.Parse(time.RFC3339, out.ReceivedAt); err != nil {
		t.Fatalf("received_at %q: %v", out.ReceivedAt, err)
	}

	if len(inbox.ordered) != 1 {
		t.Fatalf("stored %d entries, want 1", len(inbox.ordered))
	}
	e := inbox.ordered[0]
	if e.Actor != "max@example.com" || e.Source != "mcp-http" || e.Kind != "note" || e.IdempotencyKey != "key-00000001" {
		t.Fatalf("unexpected entry attribution: %+v", e)
	}
	if e.ID.String() != out.ID {
		t.Fatalf("id mismatch: %s vs %s", e.ID, out.ID)
	}
	if got := payloadText(e.Payload); got != "午餐 120 元" {
		t.Fatalf("payload text = %q", got)
	}
}

func TestCaptureDuplicateReturnsOriginal(t *testing.T) {
	inbox := newFakeInbox()
	cs := connect(t, inbox)

	first := decode[captureOutput](t, call(t, cs, "capture", map[string]any{"text": "original", "idempotency_key": "same-key-001"}))
	second := decode[captureOutput](t, call(t, cs, "capture", map[string]any{"text": "different", "idempotency_key": "same-key-001"}))

	if !second.Duplicate {
		t.Fatalf("duplicate = false on repeated key")
	}
	if second.Text != "original" {
		t.Fatalf("text = %q, want original", second.Text)
	}
	if second.ID != first.ID {
		t.Fatalf("id changed: %s vs %s", first.ID, second.ID)
	}
	if len(inbox.ordered) != 1 {
		t.Fatalf("stored %d entries, want 1", len(inbox.ordered))
	}
}

func TestCaptureBlankTextRejected(t *testing.T) {
	inbox := newFakeInbox()
	cs := connect(t, inbox)

	res := call(t, cs, "capture", map[string]any{"text": "   \n\t", "idempotency_key": "key-00000002"})
	if !res.IsError {
		t.Fatalf("blank text was accepted: %+v", res)
	}
	if len(inbox.ordered) != 0 {
		t.Fatalf("stored %d entries, want 0", len(inbox.ordered))
	}
}

func TestInboxRecentNewestFirstDefaultLimit(t *testing.T) {
	inbox := newFakeInbox()
	cs := connect(t, inbox)

	const total = 12
	for i := range total {
		decode[captureOutput](t, call(t, cs, "capture", map[string]any{
			"text":            fmt.Sprintf("note %d", i),
			"idempotency_key": fmt.Sprintf("key-%08d", i),
		}))
	}

	out := decode[recentOutput](t, call(t, cs, "inbox_recent", map[string]any{}))
	if len(out.Entries) != defaultLimit {
		t.Fatalf("got %d entries, want %d", len(out.Entries), defaultLimit)
	}
	for i, e := range out.Entries {
		want := fmt.Sprintf("note %d", total-1-i)
		if e.Text != want {
			t.Fatalf("entries[%d].text = %q, want %q", i, e.Text, want)
		}
		if e.Actor != "max@example.com" || e.Source != "mcp-http" || e.Kind != "note" || e.Status != "received" {
			t.Fatalf("entries[%d] = %+v", i, e)
		}
	}
}
