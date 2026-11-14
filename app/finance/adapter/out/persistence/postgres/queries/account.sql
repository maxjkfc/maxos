-- name: InsertAccount :one
-- Fails with a unique violation when the name is taken, and with a foreign key
-- violation when the institution does not exist.
INSERT INTO fin_account (id, name, institution_id, kind)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetAccount :one
SELECT * FROM fin_account WHERE id = $1;

-- name: ListAccounts :many
SELECT * FROM fin_account
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: ArchiveAccount :one
-- An account that is already archived keeps the time it was first archived.
UPDATE fin_account SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
