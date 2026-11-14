package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/inbox/adapter/out/persistence/postgres"
	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
	"github.com/maxjkfc/maxos/internal/testpg"
)

func TestMain(m *testing.M) {
	os.Exit(testpg.Run(m))
}

// newRepo returns a repo on its own empty, migrated database.
func newRepo(t *testing.T) *postgres.InboxRepo {
	t.Helper()
	return postgres.NewInboxRepo(testpg.New(t))
}

func note(t *testing.T, key, text string) model.InboxItem {
	t.Helper()
	item, err := model.NewNote(model.NewNoteParams{
		ID:             uuid.Must(uuid.NewV7()),
		Source:         "mcp-http",
		Actor:          "a@example.com",
		IdempotencyKey: key,
		Text:           text,
	})
	if err != nil {
		t.Fatalf("NewNote: %v", err)
	}
	return item
}

func TestInsertReturnsTheStoredItem(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	item := note(t, "key-00000001", "午餐 120 元")

	stored, err := r.Insert(ctx, item)
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if stored.ID != item.ID || stored.Source != item.Source || stored.Actor != item.Actor || stored.Kind != item.Kind || stored.IdempotencyKey != item.IdempotencyKey {
		t.Fatalf("stored = %+v, want %+v", stored, item)
	}
	if stored.ReceivedAt.IsZero() {
		t.Fatal("received_at was not assigned by the database")
	}
	// The payload comes back as jsonb, so compare its content, not its bytes.
	if got := stored.Text(); got != "午餐 120 元" {
		t.Fatalf("stored text = %q", got)
	}
}

func TestInsertRefusesADuplicateKeyAndKeepsTheOriginal(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	first := note(t, "key-00000001", "original")
	if _, err := r.Insert(ctx, first); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	second := note(t, "key-00000001", "changed")
	second.Source, second.Actor = "mcp-stdio", "other"
	if _, err := r.Insert(ctx, second); !errors.Is(err, repo.ErrDuplicateKey) {
		t.Fatalf("second insert: err = %v, want ErrDuplicateKey", err)
	}

	got, err := r.GetByKey(ctx, "key-00000001")
	if err != nil {
		t.Fatalf("GetByKey: %v", err)
	}
	if got.ID != first.ID || got.Actor != "a@example.com" || got.Text() != "original" {
		t.Fatalf("GetByKey = %+v, want the first item", got)
	}

	all, err := r.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d rows, want 1", len(all))
	}
}

func TestGetByKeyReportsAnUnknownKey(t *testing.T) {
	_, err := newRepo(t).GetByKey(context.Background(), "no-such-key")
	if !errors.Is(err, repo.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestListRecentIsNewestFirstAndHonoursLimit(t *testing.T) {
	ctx := context.Background()
	r := newRepo(t)
	for _, key := range []string{"key-00000001", "key-00000002", "key-00000003"} {
		if _, err := r.Insert(ctx, note(t, key, "note "+key)); err != nil {
			t.Fatalf("insert %s: %v", key, err)
		}
	}

	got, err := r.ListRecent(ctx, 2)
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(got) != 2 || got[0].IdempotencyKey != "key-00000003" || got[1].IdempotencyKey != "key-00000002" {
		t.Fatalf("ListRecent(2) = %+v, want [key-00000003 key-00000002]", got)
	}
}
