package capture_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/capture"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

// fakeRepo is an in-memory repo.InboxRepo. The method Capture does not call,
// ListRecent, is left to the embedded nil interface.
type fakeRepo struct {
	repo.InboxRepo

	items     []model.InboxItem
	insertErr error
}

func (f *fakeRepo) Insert(_ context.Context, item model.InboxItem) (model.InboxItem, error) {
	if f.insertErr != nil {
		return model.InboxItem{}, f.insertErr
	}
	for _, stored := range f.items {
		if stored.IdempotencyKey == item.IdempotencyKey {
			return model.InboxItem{}, repo.ErrDuplicateKey
		}
	}
	item.ReceivedAt = time.Date(2026, 1, 1, 0, 0, len(f.items), 0, time.UTC)
	f.items = append(f.items, item)
	return item, nil
}

func (f *fakeRepo) GetByKey(_ context.Context, key string) (model.InboxItem, error) {
	for _, stored := range f.items {
		if stored.IdempotencyKey == key {
			return stored, nil
		}
	}
	return model.InboxItem{}, repo.ErrNotFound
}

func input(text, key string) usecase.CaptureInput {
	return usecase.CaptureInput{Source: "mcp-http", Actor: "max@example.com", IdempotencyKey: key, Text: text}
}

func TestCaptureStoresTrimmedNoteWithAttribution(t *testing.T) {
	r := &fakeRepo{}
	svc := capture.New(r)

	out, err := svc.Capture(context.Background(), input("  午餐 120 元  ", "key-00000001"))
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if out.Duplicate {
		t.Fatal("duplicate = true on first capture")
	}
	if out.Text != "午餐 120 元" {
		t.Fatalf("text = %q, want the trimmed text", out.Text)
	}
	if out.ReceivedAt.IsZero() {
		t.Fatal("received_at is zero, want the time the store assigned")
	}

	if len(r.items) != 1 {
		t.Fatalf("stored %d items, want 1", len(r.items))
	}
	item := r.items[0]
	if item.Actor != "max@example.com" || item.Source != "mcp-http" || item.Kind != model.KindNote || item.IdempotencyKey != "key-00000001" {
		t.Fatalf("unexpected attribution: %+v", item)
	}
	if item.ID != out.ID {
		t.Fatalf("id mismatch: stored %s, returned %s", item.ID, out.ID)
	}
	if got := item.Text(); got != "午餐 120 元" {
		t.Fatalf("stored text = %q", got)
	}
}

func TestCaptureDuplicateReturnsOriginal(t *testing.T) {
	r := &fakeRepo{}
	svc := capture.New(r)
	ctx := context.Background()

	first, err := svc.Capture(ctx, input("original", "same-key-001"))
	if err != nil {
		t.Fatalf("first capture: %v", err)
	}
	second, err := svc.Capture(ctx, input("different", "same-key-001"))
	if err != nil {
		t.Fatalf("second capture: %v", err)
	}

	if !second.Duplicate {
		t.Fatal("duplicate = false on repeated key")
	}
	if second.Text != "original" {
		t.Fatalf("text = %q, want original", second.Text)
	}
	if second.ID != first.ID {
		t.Fatalf("id changed: %s vs %s", first.ID, second.ID)
	}
	if len(r.items) != 1 {
		t.Fatalf("stored %d items, want 1", len(r.items))
	}
}

func TestCaptureRejectsInvalidNoteWithoutStoring(t *testing.T) {
	t.Run("blank text", func(t *testing.T) {
		r := &fakeRepo{}
		_, err := capture.New(r).Capture(context.Background(), input("   \n\t", "key-00000002"))
		if !errors.Is(err, model.ErrTextEmpty) {
			t.Fatalf("err = %v, want ErrTextEmpty", err)
		}
		if len(r.items) != 0 {
			t.Fatalf("stored %d items, want 0", len(r.items))
		}
	})

	t.Run("short idempotency key", func(t *testing.T) {
		r := &fakeRepo{}
		_, err := capture.New(r).Capture(context.Background(), input("hello", "short"))
		var badKey *model.IdempotencyKeyLengthError
		if !errors.As(err, &badKey) {
			t.Fatalf("err = %v, want an IdempotencyKeyLengthError", err)
		}
		if len(r.items) != 0 {
			t.Fatalf("stored %d items, want 0", len(r.items))
		}
	})
}

func TestCaptureReportsStoreFailure(t *testing.T) {
	boom := errors.New("connection reset")
	svc := capture.New(&fakeRepo{insertErr: boom})

	_, err := svc.Capture(context.Background(), input("hello", "key-00000003"))
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store failure", err)
	}
}
