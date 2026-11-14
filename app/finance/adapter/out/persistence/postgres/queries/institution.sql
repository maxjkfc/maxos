-- name: InsertInstitution :one
-- Fails with a unique violation when the name is taken.
INSERT INTO fin_institution (id, name)
VALUES ($1, $2)
RETURNING *;

-- name: GetInstitution :one
SELECT * FROM fin_institution WHERE id = $1;

-- name: ListInstitutions :many
SELECT * FROM fin_institution
WHERE sqlc.arg(include_archived)::boolean OR archived_at IS NULL
ORDER BY name, id;

-- name: ArchiveInstitution :one
-- An institution that is already archived keeps the time it was first archived.
UPDATE fin_institution SET archived_at = COALESCE(archived_at, now()) WHERE id = $1 RETURNING *;
