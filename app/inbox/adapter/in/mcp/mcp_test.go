package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/mcpserver"
)

type fakeCapture struct {
	got usecase.CaptureInput
	out usecase.CaptureOutput
	err error
}

func (f *fakeCapture) Capture(_ context.Context, in usecase.CaptureInput) (usecase.CaptureOutput, error) {
	f.got = in
	return f.out, f.err
}

type fakeRecent struct {
	got usecase.ListRecentInput
	out usecase.ListRecentOutput
	err error
}

func (f *fakeRecent) ListRecent(_ context.Context, in usecase.ListRecentInput) (usecase.ListRecentOutput, error) {
	f.got = in
	return f.out, f.err
}

// connect registers the inbox tools on a server and returns a client talking to
// it over an in-memory transport, as an MCP host would.
func connect(t *testing.T, capture usecase.Capture, recent usecase.ListRecent) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()

	server := sdk.NewServer(&sdk.Implementation{Name: "maxos-test"}, nil)
	NewRegistrar(capture, recent)(server, mcpserver.Caller{Source: "mcp-http", Actor: "max@example.com"})

	serverT, clientT := sdk.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func call(t *testing.T, cs *sdk.ClientSession, tool string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", tool, err)
	}
	return res
}

func decode[T any](t *testing.T, res *sdk.CallToolResult) T {
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

// errorText returns the message of a failed tool call, which is what the model
// reads.
func errorText(t *testing.T, res *sdk.CallToolResult) string {
	t.Helper()
	if !res.IsError {
		t.Fatalf("tool call succeeded, want an error: %+v", res.StructuredContent)
	}
	if len(res.Content) != 1 {
		t.Fatalf("error has %d content blocks, want 1", len(res.Content))
	}
	text, ok := res.Content[0].(*sdk.TextContent)
	if !ok {
		t.Fatalf("error content is %T, want *TextContent", res.Content[0])
	}
	return text.Text
}

func TestCaptureToolHandsNoteAndCallerToUseCase(t *testing.T) {
	// A zone other than UTC proves the time is rendered in UTC.
	receivedAt := time.Date(2026, 1, 1, 8, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	uc := &fakeCapture{out: usecase.CaptureOutput{ID: uuid.Must(uuid.NewV7()), ReceivedAt: receivedAt, Text: "午餐 120 元"}}
	cs := connect(t, uc, &fakeRecent{})

	out := decode[captureOutput](t, call(t, cs, "capture", map[string]any{"text": "  午餐 120 元  ", "idempotency_key": "key-00000001"}))

	want := usecase.CaptureInput{Source: "mcp-http", Actor: "max@example.com", IdempotencyKey: "key-00000001", Text: "  午餐 120 元  "}
	if uc.got != want {
		t.Errorf("use case got %+v, want %+v", uc.got, want)
	}
	if out.ID != uc.out.ID.String() || out.ReceivedAt != "2026-01-01T00:00:00Z" || out.Duplicate || out.Text != "午餐 120 元" {
		t.Errorf("output = %+v", out)
	}
}

func TestCaptureToolReportsDuplicate(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := &fakeCapture{out: usecase.CaptureOutput{ID: id, ReceivedAt: time.Now(), Text: "original", Duplicate: true}}
	cs := connect(t, uc, &fakeRecent{})

	out := decode[captureOutput](t, call(t, cs, "capture", map[string]any{"text": "different", "idempotency_key": "same-key-001"}))

	if !out.Duplicate {
		t.Fatal("duplicate = false, want true")
	}
	if out.Text != "original" || out.ID != id.String() {
		t.Fatalf("output = %+v, want the original note", out)
	}
}

func TestCaptureToolExplainsRejectedNotes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "blank text",
			err:  model.ErrTextEmpty,
			want: "text is empty: pass the owner's note verbatim in text (1 to 4000 characters)",
		},
		{
			name: "text too long",
			err:  &model.TextTooLongError{Runes: 4001, Max: 4000},
			want: "text is too long (4001 characters, max 4000): ask the owner to shorten it or split it into several notes, each with its own idempotency_key",
		},
		{
			name: "idempotency key too short",
			err:  &model.IdempotencyKeyLengthError{Runes: 3, Min: 8, Max: 128},
			want: "idempotency_key must be 8 to 128 characters (got 3): generate a random string such as a UUID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := connect(t, &fakeCapture{err: tt.err}, &fakeRecent{})

			res := call(t, cs, "capture", map[string]any{"text": "hello", "idempotency_key": "key-00000002"})

			if got := errorText(t, res); got != tt.want {
				t.Errorf("message = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaptureToolSaysRetryingIsSafeOnStoreFailure(t *testing.T) {
	cs := connect(t, &fakeCapture{err: errors.New("connection reset")}, &fakeRecent{})

	res := call(t, cs, "capture", map[string]any{"text": "hello", "idempotency_key": "key-00000003"})

	got := errorText(t, res)
	if !strings.HasPrefix(got, "could not store the note, retrying with the same idempotency_key is safe:") || !strings.Contains(got, "connection reset") {
		t.Errorf("message = %q", got)
	}
}

func TestInboxRecentToolListsItemsInUseCaseOrder(t *testing.T) {
	newest := usecase.RecentItem{
		ID:         uuid.Must(uuid.NewV7()),
		ReceivedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Source:     "mcp-http",
		Actor:      "max@example.com",
		Kind:       "note",
		Text:       "newest",
	}
	older := newest
	older.ID = uuid.Must(uuid.NewV7())
	older.ReceivedAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	older.Text = "older"
	cs := connect(t, &fakeCapture{}, &fakeRecent{out: usecase.ListRecentOutput{Items: []usecase.RecentItem{newest, older}}})

	out := decode[recentOutput](t, call(t, cs, "inbox_recent", map[string]any{}))

	want := []recentEntry{
		{ID: newest.ID.String(), ReceivedAt: "2026-01-02T03:04:05Z", Source: "mcp-http", Actor: "max@example.com", Kind: "note", Text: "newest"},
		{ID: older.ID.String(), ReceivedAt: "2026-01-01T00:00:00Z", Source: "mcp-http", Actor: "max@example.com", Kind: "note", Text: "older"},
	}
	if len(out.Entries) != len(want) {
		t.Fatalf("got %d entries, want %d", len(out.Entries), len(want))
	}
	for i := range want {
		if out.Entries[i] != want[i] {
			t.Errorf("entries[%d] = %+v, want %+v", i, out.Entries[i], want[i])
		}
	}
}

func TestInboxRecentToolLeavesTheDefaultLimitToTheUseCase(t *testing.T) {
	tests := []struct {
		name string
		args map[string]any
		want int
	}{
		{name: "limit omitted", args: map[string]any{}, want: 0},
		{name: "limit given", args: map[string]any{"limit": 5}, want: 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uc := &fakeRecent{}
			cs := connect(t, &fakeCapture{}, uc)

			decode[recentOutput](t, call(t, cs, "inbox_recent", tt.args))

			if uc.got.Limit != tt.want {
				t.Errorf("use case got limit %d, want %d", uc.got.Limit, tt.want)
			}
		})
	}
}

func TestInboxRecentToolReturnsAnEmptyListNotNull(t *testing.T) {
	cs := connect(t, &fakeCapture{}, &fakeRecent{})

	res := call(t, cs, "inbox_recent", map[string]any{})

	if res.IsError {
		t.Fatalf("empty inbox failed: %s", errorText(t, res))
	}
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"entries":[]}`; got != want {
		t.Errorf("structured content = %s, want %s", got, want)
	}
}

func TestInboxRecentToolReportsFailure(t *testing.T) {
	cs := connect(t, &fakeCapture{}, &fakeRecent{err: errors.New("connection reset")})

	res := call(t, cs, "inbox_recent", map[string]any{})

	if got := errorText(t, res); got != "could not read the inbox: connection reset" {
		t.Errorf("message = %q", got)
	}
}
