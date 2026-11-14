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

-- name: LockAccount :one
-- Holds the row until the database transaction ends, and another transaction that
-- locks it waits for that. It is FOR NO KEY UPDATE, not FOR UPDATE: the foreign
-- key of a posting to the account takes only FOR KEY SHARE, which does not
-- conflict with it, so recording to the account is not held up. Returns no row
-- when there is no such account.
SELECT id FROM fin_account WHERE id = $1 FOR NO KEY UPDATE;
