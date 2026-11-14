-- name: InsertTag :one
-- Fails with a unique violation when the name is taken.
INSERT INTO catalog_tag (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetTag :one
SELECT * FROM catalog_tag WHERE id = $1;

-- name: GetTagsByIDs :many
SELECT * FROM catalog_tag WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListTags :many
SELECT * FROM catalog_tag
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: RenameTag :one
-- Fails with a unique violation when the name is taken.
UPDATE catalog_tag SET name = $2 WHERE id = $1 RETURNING *;

-- name: ArchiveTag :one
-- A tag that is already archived keeps the time it was first archived.
UPDATE catalog_tag SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
