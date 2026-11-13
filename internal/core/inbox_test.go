package core_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/maxjkfc/maxos/internal/core"
	"github.com/maxjkfc/maxos/internal/db/dbtest"
)

func TestCaptureIsIdempotent(t *testing.T) {
	ctx := context.Background()
	inbox := core.NewInbox(dbtest.Tx(t))

	first, dup, err := inbox.Capture(ctx, core.CaptureInput{
		Source: "mcp-http", Actor: "a@example.com", Kind: "note",
		IdempotencyKey: "key-1", Payload: json.RawMessage(`{"text":"original"}`),
	})
	if err != nil || dup {
		t.Fatalf("first capture: dup=%v err=%v", dup, err)
	}

	second, dup, err := inbox.Capture(ctx, core.CaptureInput{
		Source: "mcp-stdio", Actor: "other", Kind: "note",
		IdempotencyKey: "key-1", Payload: json.RawMessage(`{"text":"changed"}`),
	})
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}
	if !dup {
		t.Fatal("second capture: duplicate=false, want true")
	}
	if second.ID != first.ID || second.Actor != "a@example.com" {
		t.Fatalf("second capture returned %+v, want original %+v", second, first)
	}
	var payload map[string]string
	if err := json.Unmarshal(second.Payload, &payload); err != nil || payload["text"] != "original" {
		t.Fatalf("payload = %s (err %v), want original", second.Payload, err)
	}

	all, err := inbox.Recent(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d rows, want 1", len(all))
	}
}

func TestRecentIsNewestFirstAndHonoursLimit(t *testing.T) {
	ctx := context.Background()
	inbox := core.NewInbox(dbtest.Tx(t))

	keys := []string{"k1", "k2", "k3"}
	for _, k := range keys {
		if _, _, err := inbox.Capture(ctx, core.CaptureInput{
			Source: "mcp-http", Actor: "a", Kind: "note",
			IdempotencyKey: k, Payload: json.RawMessage(`{}`),
		}); err != nil {
			t.Fatalf("capture %s: %v", k, err)
		}
	}

	got, err := inbox.Recent(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].IdempotencyKey != "k3" || got[1].IdempotencyKey != "k2" {
		t.Fatalf("Recent(2) = %+v, want [k3 k2]", got)
	}
}
