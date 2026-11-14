-- name: InsertCategory :one
-- Lists the columns the application writes: is_top and parent_must_be_top are
-- generated. Fails with a unique violation when a category of the same direction
-- and parent has the name, and with a foreign key violation when the parent does
-- not exist, is a subcategory, or has the other direction.
INSERT INTO fin_category (id, direction, parent_id, name)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetCategory :one
SELECT * FROM fin_category WHERE id = $1;

-- name: ListCategories :many
-- An empty direction lists both.
SELECT * FROM fin_category
WHERE (sqlc.arg(include_archived)::boolean OR archived_at IS NULL)
  AND (sqlc.arg(direction)::text = '' OR direction = sqlc.arg(direction)::text)
ORDER BY direction, name, id;

-- name: ArchiveCategory :many
-- Archives the category and its subcategories in one statement and returns all
-- the rows it touched; the caller picks the one with the id asked for. A category
-- that is already archived keeps the time it was first archived.
UPDATE fin_category SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 OR parent_id = $1 RETURNING *;
