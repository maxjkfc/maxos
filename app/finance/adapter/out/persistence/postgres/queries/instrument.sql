-- name: InsertInstrument :one
-- Fails with a unique violation when the code is taken.
INSERT INTO fin_instrument (id, code, name, kind, is_currency, decimals)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: GetInstrument :one
SELECT * FROM fin_instrument WHERE id = $1;

-- name: ListInstruments :many
SELECT * FROM fin_instrument
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY code, id;

-- name: ArchiveInstrument :one
-- An instrument that is already archived keeps the time it was first archived.
UPDATE fin_instrument SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
