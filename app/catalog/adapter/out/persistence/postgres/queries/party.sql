-- name: InsertParty :one
-- Fails with a unique violation when the name is taken.
INSERT INTO catalog_party (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetParty :one
SELECT * FROM catalog_party WHERE id = $1;

-- name: GetPartiesByIDs :many
SELECT * FROM catalog_party WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListParties :many
SELECT * FROM catalog_party
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: RenameParty :one
-- Fails with a unique violation when the name is taken.
UPDATE catalog_party SET name = $2 WHERE id = $1 RETURNING *;

-- name: ArchiveParty :one
-- A party that is already archived keeps the time it was first archived.
UPDATE catalog_party SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
