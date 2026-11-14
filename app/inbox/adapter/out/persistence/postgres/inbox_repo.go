// Package postgres stores inbox items in PostgreSQL. The sqlc-generated code in
// dbgen stays inside this package: nothing generated crosses the repo boundary.
package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/maxjkfc/maxos/app/inbox/adapter/out/persistence/postgres/dbgen"
	"github.com/maxjkfc/maxos/app/inbox/domain/model"
	"github.com/maxjkfc/maxos/app/inbox/usecase/port/out/repo"
)

// InboxRepo implements repo.InboxRepo on the inbox_item table.
type InboxRepo struct {
	q *dbgen.Queries
}

var _ repo.InboxRepo = (*InboxRepo)(nil)

// NewInboxRepo returns a repo that reads and writes through pool.
func NewInboxRepo(pool *pgxpool.Pool) *InboxRepo {
	return &InboxRepo{q: dbgen.New(pool)}
}

// Insert stores item. The INSERT ... ON CONFLICT DO NOTHING returns no row when
// the idempotency key is already stored.
func (r *InboxRepo) Insert(ctx context.Context, item model.InboxItem) (model.InboxItem, error) {
	row, err := r.q.InsertInboxItem(ctx, dbgen.InsertInboxItemParams{
		ID:             item.ID,
		Source:         item.Source,
		Actor:          item.Actor,
		Kind:           string(item.Kind),
		IdempotencyKey: item.IdempotencyKey,
		Payload:        item.Payload,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return model.InboxItem{}, repo.ErrDuplicateKey
	}
	if err != nil {
		return model.InboxItem{}, err
	}
	return toModel(row), nil
}

// GetByKey returns the item stored under key.
func (r *InboxRepo) GetByKey(ctx context.Context, key string) (model.InboxItem, error) {
	row, err := r.q.GetInboxItemByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.InboxItem{}, repo.ErrNotFound
	}
	if err != nil {
		return model.InboxItem{}, err
	}
	return toModel(row), nil
}

// ListRecent returns at most limit items, newest first.
func (r *InboxRepo) ListRecent(ctx context.Context, limit int) ([]model.InboxItem, error) {
	rows, err := r.q.ListRecentInboxItems(ctx, int32(limit))
	if err != nil {
		return nil, err
	}
	items := make([]model.InboxItem, len(rows))
	for i, row := range rows {
		items[i] = toModel(row)
	}
	return items, nil
}

func toModel(row dbgen.InboxItem) model.InboxItem {
	return model.InboxItem{
		ID:             row.ID,
		ReceivedAt:     row.ReceivedAt,
		Source:         row.Source,
		Actor:          row.Actor,
		Kind:           model.Kind(row.Kind),
		IdempotencyKey: row.IdempotencyKey,
		Payload:        row.Payload,
		Status:         model.Status(row.Status),
	}
}
