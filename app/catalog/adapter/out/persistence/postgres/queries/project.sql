-- name: InsertProject :one
-- Fails with a unique violation when the name is taken.
INSERT INTO catalog_project (id, name, starts_on, ends_on, active_from, active_to, default_area_id)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: GetProject :one
SELECT * FROM catalog_project WHERE id = $1;

-- name: GetProjectsByIDs :many
SELECT * FROM catalog_project WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListProjects :many
SELECT * FROM catalog_project
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: RenameProject :one
-- Fails with a unique violation when the name is taken.
UPDATE catalog_project SET name = $2 WHERE id = $1 RETURNING *;

-- name: SetProjectPeriod :one
-- Both ends NULL clears the period. The table's CHECKs keep the period ordered
-- and around the active period.
UPDATE catalog_project SET starts_on = $2, ends_on = $3 WHERE id = $1 RETURNING *;

-- name: SetProjectActivePeriod :one
-- Both ends NULL clears the active period. The table's CHECKs keep it ordered and
-- within the period.
UPDATE catalog_project SET active_from = $2, active_to = $3 WHERE id = $1 RETURNING *;

-- name: SetProjectDefaultArea :one
UPDATE catalog_project SET default_area_id = $2 WHERE id = $1 RETURNING *;

-- name: ArchiveProject :one
-- A project that is already archived keeps the time it was first archived.
UPDATE catalog_project SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;

-- name: ListProjectsActiveOn :many
-- The projects in use whose active period includes the day, both ends included.
SELECT * FROM catalog_project
WHERE archived_at IS NULL
  AND active_from <= sqlc.arg(day)::date
  AND active_to >= sqlc.arg(day)::date
ORDER BY active_from, id;
