-- name: InsertInboxItem :one
-- Returns no row (pgx.ErrNoRows) when idempotency_key already exists.
INSERT INTO inbox_item (id, source, actor, kind, idempotency_key, payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetInboxItemByKey :one
SELECT * FROM inbox_item WHERE idempotency_key = $1;

-- name: ListRecentInboxItems :many
SELECT * FROM inbox_item ORDER BY received_at DESC, id DESC LIMIT $1;
