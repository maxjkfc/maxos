-- name: InsertTransaction :one
-- Returns no row (pgx.ErrNoRows) when the idempotency key is already stored: the
-- statement waits for a concurrent transaction that holds the key to finish, and
-- when that one commits it does nothing. A transaction is inserted as a draft,
-- which has no confirmed time; the table's CHECK constraints refuse any other
-- status. The postings follow in the same database transaction: the deferred
-- balance trigger checks them at its commit.
INSERT INTO fin_transaction (
    id, kind, status, occurred_on, occurred_at, description, merchant,
    project_id, area_id, idempotency_key
)
VALUES (
    @id, @kind, @status, @occurred_on, @occurred_at, @description, @merchant,
    @project_id, @area_id, @idempotency_key
)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: InsertPosting :exec
INSERT INTO fin_posting (id, transaction_id, seq, book, account_id, category_id, instrument_id, quantity, base_amount, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10);

-- name: InsertTransactionTag :exec
INSERT INTO fin_transaction_tag (transaction_id, tag_id) VALUES ($1, $2);

-- name: GetTransaction :one
SELECT * FROM fin_transaction WHERE id = $1;

-- name: GetTransactionForUpdate :one
-- Locks the row until the database transaction ends: whoever changes or deletes
-- the transaction meanwhile waits, and then sees what this one left.
SELECT * FROM fin_transaction WHERE id = $1 FOR UPDATE;

-- name: GetTransactionByIdempotencyKey :one
SELECT * FROM fin_transaction WHERE idempotency_key = $1;

-- name: GetTransactionsByIDs :many
-- In no particular order.
SELECT * FROM fin_transaction WHERE id = ANY(@ids::uuid[]);

-- name: ListPostings :many
SELECT * FROM fin_posting
WHERE transaction_id = ANY(@transaction_ids::uuid[])
ORDER BY transaction_id, seq;

-- name: ListTransactionTags :many
SELECT * FROM fin_transaction_tag
WHERE transaction_id = ANY(@transaction_ids::uuid[])
ORDER BY transaction_id, tag_id;

-- name: SetTransactionStatus :one
-- Moves the transaction to the status and stamps the time it got there. The
-- table's CHECK constraints refuse a status that skips a step.
UPDATE fin_transaction
SET status = @status::text,
    confirmed_at = CASE WHEN @status::text = 'confirmed' THEN now() ELSE confirmed_at END,
    voided_at = CASE WHEN @status::text = 'void' THEN now() ELSE voided_at END
WHERE id = @id
RETURNING *;

-- name: DeleteTransaction :execrows
-- Its postings and tags go with it.
DELETE FROM fin_transaction WHERE id = $1;

-- name: ListBalances :many
-- What each account holds of each instrument: the quantities of its postings
-- added up, counting only the confirmed transactions. Nothing is stored. With an
-- account_id it is the balances of that account only.
SELECT a.id AS account_id, i.id AS instrument_id, sum(p.quantity)::numeric AS quantity
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
JOIN fin_account a ON a.id = p.account_id
JOIN fin_instrument i ON i.id = p.instrument_id
WHERE t.status = 'confirmed' AND p.book = 'account'
  AND (sqlc.narg('account_id')::uuid IS NULL OR p.account_id = sqlc.narg('account_id'))
GROUP BY a.id, a.name, i.id, i.code
ORDER BY a.name, a.id, i.code, i.id;
