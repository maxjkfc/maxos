-- name: InsertArea :one
-- Fails with a unique violation when the name is taken.
INSERT INTO catalog_area (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetArea :one
SELECT * FROM catalog_area WHERE id = $1;

-- name: GetAreasByIDs :many
SELECT * FROM catalog_area WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListAreas :many
SELECT * FROM catalog_area
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: RenameArea :one
-- Fails with a unique violation when the name is taken.
UPDATE catalog_area SET name = $2 WHERE id = $1 RETURNING *;

-- name: ArchiveArea :one
-- An area that is already archived keeps the time it was first archived.
UPDATE catalog_area SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
