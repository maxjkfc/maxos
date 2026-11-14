package listrecent_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/listrecent"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

// fakeRepo is a repo.InboxRepo that serves a fixed list and records the limit
// it was asked for. The methods ListRecent does not call are left to the
// embedded nil interface.
type fakeRepo struct {
	repo.InboxRepo

	items    []model.InboxItem // newest first, as a real repo returns them
	gotLimit int
	err      error
}

func (f *fakeRepo) ListRecent(_ context.Context, limit int) ([]model.InboxItem, error) {
	f.gotLimit = limit
	if f.err != nil {
		return nil, f.err
	}
	return f.items[:min(limit, len(f.items))], nil
}

func TestListRecentBringsLimitIntoRange(t *testing.T) {
	tests := []struct {
		limit int
		want  int
	}{
		{limit: 0, want: 10},
		{limit: -5, want: 1},
		{limit: 1, want: 1},
		{limit: 25, want: 25},
		{limit: 50, want: 50},
		{limit: 51, want: 50},
		{limit: 1000, want: 50},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("limit %d", tt.limit), func(t *testing.T) {
			r := &fakeRepo{}
			if _, err := listrecent.New(r).ListRecent(context.Background(), usecase.ListRecentInput{Limit: tt.limit}); err != nil {
				t.Fatalf("ListRecent: %v", err)
			}
			if r.gotLimit != tt.want {
				t.Fatalf("asked the repo for %d items, want %d", r.gotLimit, tt.want)
			}
		})
	}
}

func TestListRecentKeepsRepoOrderAndMapsItems(t *testing.T) {
	var items []model.InboxItem // newest first
	for i := 2; i >= 0; i-- {
		item, err := model.NewNote(model.NewNoteParams{
			ID:             uuid.Must(uuid.NewV7()),
			Source:         "mcp-http",
			Actor:          "max@example.com",
			IdempotencyKey: fmt.Sprintf("key-%08d", i),
			Text:           fmt.Sprintf("note %d", i),
		})
		if err != nil {
			t.Fatalf("NewNote: %v", err)
		}
		item.ReceivedAt = time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC)
		items = append(items, item)
	}

	out, err := listrecent.New(&fakeRepo{items: items}).ListRecent(context.Background(), usecase.ListRecentInput{})
	if err != nil {
		t.Fatalf("ListRecent: %v", err)
	}
	if len(out.Items) != len(items) {
		t.Fatalf("got %d items, want %d", len(out.Items), len(items))
	}
	for i, got := range out.Items {
		want := items[i]
		if got.ID != want.ID || !got.ReceivedAt.Equal(want.ReceivedAt) {
			t.Errorf("items[%d] = %+v, want id %s at %s", i, got, want.ID, want.ReceivedAt)
		}
		if got.Text != want.Text() || got.Source != "mcp-http" || got.Actor != "max@example.com" || got.Kind != "note" {
			t.Errorf("items[%d] = %+v", i, got)
		}
	}
	if out.Items[0].Text != "note 2" {
		t.Errorf("first item text = %q, want the newest note", out.Items[0].Text)
	}
}

func TestListRecentReportsStoreFailure(t *testing.T) {
	boom := errors.New("connection reset")

	_, err := listrecent.New(&fakeRepo{err: boom}).ListRecent(context.Background(), usecase.ListRecentInput{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the store failure", err)
	}
}
