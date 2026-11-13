package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/maxjkfc/maxos/internal/db/dbgen"
)

type InboxEntry struct {
	ID             uuid.UUID       `json:"id"`
	ReceivedAt     time.Time       `json:"received_at"`
	Source         string          `json:"source"`
	Actor          string          `json:"actor"`
	Kind           string          `json:"kind"`
	IdempotencyKey string          `json:"idempotency_key"`
	Payload        json.RawMessage `json:"payload"`
	Status         string          `json:"status"`
}

type CaptureInput struct {
	Source, Actor, Kind, IdempotencyKey string
	Payload                             json.RawMessage
}

type Inbox struct {
	q *dbgen.Queries
}

func NewInbox(db dbgen.DBTX) *Inbox {
	return &Inbox{q: dbgen.New(db)}
}

// Capture stores the entry. If idempotency_key already exists it returns the
// existing entry and duplicate=true, writing nothing.
func (i *Inbox) Capture(ctx context.Context, in CaptureInput) (InboxEntry, bool, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return InboxEntry{}, false, fmt.Errorf("generate inbox id: %w", err)
	}

	row, err := i.q.InsertInboxEntry(ctx, dbgen.InsertInboxEntryParams{
		ID:             id,
		Source:         in.Source,
		Actor:          in.Actor,
		Kind:           in.Kind,
		IdempotencyKey: in.IdempotencyKey,
		Payload:        in.Payload,
	})
	if err == nil {
		return toEntry(row), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return InboxEntry{}, false, fmt.Errorf("insert inbox entry: %w", err)
	}

	// ON CONFLICT DO NOTHING returned no row: the key was committed by someone
	// else (a concurrent insert blocks until that commit), so this read sees it.
	row, err = i.q.GetInboxEntryByKey(ctx, in.IdempotencyKey)
	if err != nil {
		return InboxEntry{}, false, fmt.Errorf("get inbox entry by key: %w", err)
	}
	return toEntry(row), true, nil
}

// Recent returns entries newest-first.
func (i *Inbox) Recent(ctx context.Context, limit int) ([]InboxEntry, error) {
	rows, err := i.q.ListRecentInboxEntries(ctx, int32(limit))
	if err != nil {
		return nil, fmt.Errorf("list recent inbox entries: %w", err)
	}
	out := make([]InboxEntry, len(rows))
	for n, r := range rows {
		out[n] = toEntry(r)
	}
	return out, nil
}

func toEntry(r dbgen.CoreInbox) InboxEntry {
	return InboxEntry{
		ID:             r.ID,
		ReceivedAt:     r.ReceivedAt,
		Source:         r.Source,
		Actor:          r.Actor,
		Kind:           r.Kind,
		IdempotencyKey: r.IdempotencyKey,
		Payload:        r.Payload,
		Status:         r.Status,
	}
}
