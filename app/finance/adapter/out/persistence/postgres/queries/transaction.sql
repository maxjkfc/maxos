-- name: InsertTransaction :one
-- Returns no row (pgx.ErrNoRows) when the idempotency key is already stored: the
-- statement waits for a concurrent transaction that holds the key to finish, and
-- when that one commits it does nothing. A transaction is inserted as a draft,
-- which has no confirmed time; the table's CHECK constraints refuse any other
-- status. The postings follow in the same database transaction: the deferred
-- balance trigger checks them at its commit. plan_id and plan_seq are both NULL
-- for a transaction that is in no instalment plan. The origin of a plan is
-- inserted before its periods, which refer to it.
INSERT INTO fin_transaction (
    id, kind, status, occurred_on, occurred_at, description, merchant,
    project_id, area_id, idempotency_key, plan_id, plan_seq
)
VALUES (
    @id, @kind, @status, @occurred_on, @occurred_at, @description, @merchant,
    @project_id, @area_id, @idempotency_key, @plan_id, @plan_seq
)
ON CONFLICT (idempotency_key) DO NOTHING
RETURNING *;

-- name: InsertPosting :exec
INSERT INTO fin_posting (id, transaction_id, seq, book, account_id, category_id, party_id, instrument_id, quantity, base_amount, discount, memo)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12);

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

-- name: ListPlanTransactions :many
-- The transactions of an instalment plan: its origin, which has plan_seq 0, and
-- then its periods in the order they fall due.
SELECT * FROM fin_transaction WHERE plan_id = $1 ORDER BY plan_seq;

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
-- What each account holds of each instrument on a day: the quantities of its
-- postings added up, counting only the confirmed transactions that happened on or
-- before the day (the day itself is in). Nothing is stored. quantity is what the
-- account holds, from its account postings. unbilled_instalment is the principal
-- of instalment plans that is not yet due, from its instalment postings: zero or
-- less, as what the account owes. There is a row for each account and instrument
-- with a posting of either book, so an account that only has unbilled instalment
-- is listed. With an account_id it is the balances of that account only, and with
-- an instrument_id of that instrument only.
SELECT a.id AS account_id, i.id AS instrument_id,
       COALESCE(sum(p.quantity) FILTER (WHERE p.book = 'account'), 0)::numeric AS quantity,
       COALESCE(sum(p.quantity) FILTER (WHERE p.book = 'instalment'), 0)::numeric AS unbilled_instalment
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
JOIN fin_account a ON a.id = p.account_id
JOIN fin_instrument i ON i.id = p.instrument_id
WHERE t.status = 'confirmed' AND p.book IN ('account', 'instalment')
  AND t.occurred_on <= @day
  AND (sqlc.narg('account_id')::uuid IS NULL OR p.account_id = sqlc.narg('account_id'))
  AND (sqlc.narg('instrument_id')::uuid IS NULL OR p.instrument_id = sqlc.narg('instrument_id'))
GROUP BY a.id, a.name, i.id, i.code
ORDER BY a.name, a.id, i.code, i.id;

-- name: CountPendingDrafts :one
-- How many drafts happened on or before the day and have a posting to the account
-- in the instrument. A draft counts once, however many such postings it has.
SELECT count(DISTINCT t.id)
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
WHERE t.status = 'draft' AND p.book = 'account'
  AND p.account_id = @account_id AND p.instrument_id = @instrument_id
  AND t.occurred_on <= @day;

-- name: ListDealings :many
-- What each party owes me of each instrument on a day: the quantities of the
-- dealing postings made to it, added up, counting only the confirmed
-- transactions that happened on or before the day (the day itself is in).
-- Nothing is stored. With a party_id it is that party's only. party_id is a plain
-- uuid column, so no row of another table says which parties exist: the use case
-- orders them by name, and this by id and then instrument code.
SELECT p.party_id::uuid AS party_id, i.id AS instrument_id, sum(p.quantity)::numeric AS quantity
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
JOIN fin_instrument i ON i.id = p.instrument_id
WHERE t.status = 'confirmed' AND p.book = 'dealing'
  AND t.occurred_on <= @day
  AND (sqlc.narg('party_id')::uuid IS NULL OR p.party_id = sqlc.narg('party_id'))
GROUP BY p.party_id, i.id, i.code
ORDER BY p.party_id, i.code, i.id;

-- name: ListInstalmentPlans :many
-- Where each confirmed instalment plan stands on a day. The origin of a plan has
-- an instalment posting of the whole principal, which is negative, and each of its
-- periods one of the principal it charges, which is positive. principal is the
-- former without its sign. charged is the principals of the periods that fall due
-- on or before the day (the day itself is in), and next_due_on is the first day
-- after it that a period falls due, NULL when none does. A plan is there from the
-- day of its origin, as its instalment posting is in ListBalances: one whose
-- origin is dated after the day is not listed. A plan is listed whatever it has
-- left to charge: telling the finished ones is for the use case. With an
-- account_id it is the plans that charge that account only. The newest plan is
-- first: by the day of its origin, then the latest made.
SELECT o.id AS plan_id,
       op.account_id::uuid AS account_id,
       op.instrument_id,
       (-op.quantity)::numeric AS principal,
       COALESCE(sum(pp.quantity) FILTER (WHERE pt.occurred_on <= @day), 0)::numeric AS charged,
       min(pt.occurred_on) FILTER (WHERE pt.occurred_on > @day) AS next_due_on
FROM fin_transaction o
JOIN fin_posting op ON op.transaction_id = o.id AND op.book = 'instalment'
LEFT JOIN fin_transaction pt ON pt.plan_id = o.id AND pt.plan_seq > 0 AND pt.status = 'confirmed'
LEFT JOIN fin_posting pp ON pp.transaction_id = pt.id AND pp.book = 'instalment'
WHERE o.status = 'confirmed' AND o.plan_seq = 0 AND o.occurred_on <= @day
  AND (sqlc.narg('account_id')::uuid IS NULL OR op.account_id = sqlc.narg('account_id'))
GROUP BY o.id, o.occurred_on, op.account_id, op.instrument_id, op.quantity
ORDER BY o.occurred_on DESC, o.id DESC;
