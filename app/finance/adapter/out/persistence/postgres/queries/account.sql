-- name: InsertAccount :one
-- Fails with a unique violation when the name is taken, with a foreign key
-- violation when the institution does not exist, and with a check violation when
-- the closing day or the payment due day breaks a rule of fin_account's checks.
INSERT INTO fin_account (id, name, institution_id, kind, closing_day, payment_due_day)
VALUES ($1, $2, $3, $4, $5, $6)
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

-- name: SetAccountStatementDays :one
-- Replaces the closing day and the payment due day, NULL clearing one. Returns no
-- row when there is no such account, and fails with a check violation when the
-- days break a rule of fin_account's checks (a card only, a due day needs a closing
-- day, 1 to 31).
UPDATE fin_account SET closing_day = $2, payment_due_day = $3 WHERE id = $1 RETURNING *;

-- name: LockAccount :one
-- Holds the row until the database transaction ends, and another transaction that
-- locks it waits for that. It is FOR NO KEY UPDATE, not FOR UPDATE: the foreign
-- key of a posting to the account takes only FOR KEY SHARE, which does not
-- conflict with it, so recording to the account is not held up. Returns no row
-- when there is no such account.
SELECT id FROM fin_account WHERE id = $1 FOR NO KEY UPDATE;
