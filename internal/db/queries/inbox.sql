-- name: InsertInboxEntry :one
-- Returns no row (pgx.ErrNoRows) when idempotency_key already exists.
INSERT INTO core_inbox (id, source, actor, kind, idempotency_key, payload)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: GetInboxEntryByKey :one
SELECT * FROM core_inbox WHERE idempotency_key = $1;

-- name: ListRecentInboxEntries :many
SELECT * FROM core_inbox ORDER BY received_at DESC, id DESC LIMIT $1;
