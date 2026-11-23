-- name: InsertTransaction :one
-- Returns no row (pgx.ErrNoRows) when the idempotency key is already stored: the
-- statement waits for a concurrent transaction that holds the key to finish, and
-- when that one commits it does nothing. A transaction is inserted as a draft,
-- which has no confirmed time; the table's CHECK constraints refuse any other
-- status. The postings follow in the same database transaction: the deferred
-- balance trigger checks them at its commit. plan_id and plan_seq are both NULL
-- for a transaction that is in no instalment plan. The origin of a plan is
-- inserted before its periods, which refer to it. posted_on is NULL unless the
-- transaction was recorded with a posting date, which the table's CHECKs keep from
-- being before occurred_on and from being on a transaction of an instalment plan.
INSERT INTO fin_transaction (
    id, kind, status, occurred_on, occurred_at, description, merchant,
    project_id, area_id, idempotency_key, plan_id, plan_seq, posted_on
)
VALUES (
    @id, @kind, @status, @occurred_on, @occurred_at, @description, @merchant,
    @project_id, @area_id, @idempotency_key, @plan_id, @plan_seq, @posted_on
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

-- name: ListPreviewBalances :many
-- What each account would hold of each instrument on a day if every draft were
-- confirmed: ListBalances, with the drafts added in beside what is confirmed.
-- confirmed is its quantity, the confirmed account postings added up, and
-- unbilled_instalment is its own, of the confirmed instalment postings: a draft
-- plan is not in a balance until it is confirmed. drafts is what confirming the
-- drafts that happened on or before the day (the day itself is in) would add: the
-- account postings of the transactions in the status draft, added up. The periods
-- of a draft plan are drafts of their own in the table, so one that has fallen due
-- is in and one that has not is not, as it is dated; a draft dated after the day, a
-- void transaction and a posting of any other book are not. It is the sum that
-- SummarizeDrafts makes of the postings of ListDraftPostings in the amounts of an
-- account, of all the months, and the tests hold the two to each other. There is a
-- row for each account and instrument with a confirmed posting of either book or a
-- draft's account posting, however they add up. It is one statement, so confirmed
-- and drafts are of one snapshot.
SELECT a.id AS account_id, i.id AS instrument_id,
       COALESCE(sum(p.quantity) FILTER (WHERE t.status = 'confirmed' AND p.book = 'account'), 0)::numeric AS confirmed,
       COALESCE(sum(p.quantity) FILTER (WHERE t.status = 'draft' AND p.book = 'account'), 0)::numeric AS drafts,
       COALESCE(sum(p.quantity) FILTER (WHERE t.status = 'confirmed' AND p.book = 'instalment'), 0)::numeric AS unbilled_instalment
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
JOIN fin_account a ON a.id = p.account_id
JOIN fin_instrument i ON i.id = p.instrument_id
WHERE t.occurred_on <= @day
  AND ((t.status = 'confirmed' AND p.book IN ('account', 'instalment'))
    OR (t.status = 'draft' AND p.book = 'account'))
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

-- name: SetTransactionPostedOn :one
-- Sets the posting date of the transaction, or clears it with NULL. The table's
-- CHECKs refuse a date before the day the transaction happened, and any date on a
-- transaction of an instalment plan. Whether the transaction may have one is the
-- model's to say: the use case locks the row and asks the model first.
UPDATE fin_transaction SET posted_on = @posted_on WHERE id = @id RETURNING *;

-- name: ListStatementFigures :many
-- What each instrument did on each of several statements of one account. A
-- statement is a range of days, both ends included, given as the day it starts and
-- the day it closes: the two arrays are the statements, in the same order, and idx
-- is the position of one in them, from 1. Which days a statement has follows the
-- closing day of the account, which is the model's to work out.
--
-- A posting counts when it is an account posting to the account, of a confirmed
-- transaction, and on the statement date of that transaction: the day it was posted
-- when it has a posting date, otherwise the day it happened. Only the postings with
-- a statement date on or before the day the statement closes or today, whichever is
-- earlier, count: what is dated after today counts from its day, as it does in
-- ListBalances. previous is what the postings before the statement add up to,
-- charges the negative postings of the statement as a positive number, and credits
-- its positive postings. Closing, which is previous - charges + credits, is the
-- model's to work out. It is all one statement of SQL, so that previous and what
-- the statements did are of one snapshot.
--
-- An instrument is listed for a statement when something was posted to it on the
-- statement or the statement starts with a previous that is not zero; one that did
-- nothing and carries nothing has nothing to say. By statement, then instrument
-- code.
WITH statements AS (
    SELECT f.idx, f.from_day, LEAST(c.closing_day, @today::date) AS counted_to
    FROM unnest(@from_days::date[]) WITH ORDINALITY AS f(from_day, idx)
    JOIN unnest(@closing_days::date[]) WITH ORDINALITY AS c(closing_day, idx) ON c.idx = f.idx
), postings AS (
    SELECT p.instrument_id, p.quantity, COALESCE(t.posted_on, t.occurred_on) AS statement_on
    FROM fin_posting p
    JOIN fin_transaction t ON t.id = p.transaction_id
    WHERE t.status = 'confirmed' AND p.book = 'account' AND p.account_id = @account_id
)
SELECT s.idx::int AS idx, p.instrument_id,
       COALESCE(sum(p.quantity) FILTER (WHERE p.statement_on < s.from_day), 0)::numeric AS previous,
       COALESCE(-sum(p.quantity) FILTER (WHERE p.statement_on >= s.from_day AND p.quantity < 0), 0)::numeric AS charges,
       COALESCE(sum(p.quantity) FILTER (WHERE p.statement_on >= s.from_day AND p.quantity > 0), 0)::numeric AS credits
FROM statements s
JOIN postings p ON p.statement_on <= s.counted_to
JOIN fin_instrument i ON i.id = p.instrument_id
GROUP BY s.idx, p.instrument_id, i.code
HAVING count(*) FILTER (WHERE p.statement_on >= s.from_day) > 0
    OR COALESCE(sum(p.quantity) FILTER (WHERE p.statement_on < s.from_day), 0) <> 0
ORDER BY s.idx, i.code, p.instrument_id;

-- name: ListStatementTransactionIDs :many
-- The transactions on a statement of one account: the confirmed ones with an
-- account posting to the account whose statement date, the day it was posted when
-- it has a posting date and otherwise the day it happened, is from from_day to
-- to_day, both ends included. The caller gives as to_day the day the statement
-- closes, or today when that is earlier, as ListStatementFigures counts. Oldest
-- statement date first, the way the lines of a bank's statement run, then the day it
-- happened, oldest first, and for the same day the one made first (the ids are
-- UUIDv7). That is the opposite of ListTransactions, which lists the newest first.
SELECT t.id
FROM fin_transaction t
WHERE t.status = 'confirmed'
  AND COALESCE(t.posted_on, t.occurred_on) BETWEEN @from_day::date AND @to_day::date
  AND EXISTS (
      SELECT 1 FROM fin_posting p
      WHERE p.transaction_id = t.id AND p.book = 'account' AND p.account_id = @account_id)
ORDER BY COALESCE(t.posted_on, t.occurred_on), t.occurred_on, t.id;

-- name: CountStatementDrafts :one
-- How many drafts with an account posting to the account have a statement date from
-- from_day to closing_day, both ends included: the drafts that would be on the
-- statement once confirmed, which the figures of the statement do not count. A draft
-- counts once, however many postings it has to the account. It does not stop at
-- today: a draft that is dated after it would be on the statement from its day.
SELECT count(*)
FROM fin_transaction t
WHERE t.status = 'draft'
  AND COALESCE(t.posted_on, t.occurred_on) BETWEEN @from_day::date AND @closing_day::date
  AND EXISTS (
      SELECT 1 FROM fin_posting p
      WHERE p.transaction_id = t.id AND p.book = 'account' AND p.account_id = @account_id);

-- name: ListDraftPostings :many
-- What the summary of the drafts is worked out from, which is the model's to do
-- (SummarizeDrafts): every transaction whose status is draft, whatever day it
-- happened on, with the postings that have an account (account_id is not NULL: those
-- of the account book and of the instalment book), one row for each, and one row with
-- no posting for a draft that has none, such as an expense that a party paid. The
-- periods of an instalment plan are drafts of their own in the table and are here
-- with the origin of their plan, as plan_id and plan_seq say: which of them is a draft
-- that counts, in which month, and which postings change a balance today is not
-- decided here. account_name and instrument_code are what the model puts the accounts
-- and the instruments in order by. By transaction, and in it by the position of its
-- postings. It is one statement, so every row is of one snapshot.
SELECT t.id AS transaction_id, t.kind, t.occurred_on, t.plan_id, t.plan_seq,
       p.account_id, a.name AS account_name, p.book,
       p.instrument_id, i.code AS instrument_code, p.quantity
FROM fin_transaction t
LEFT JOIN fin_posting p ON p.transaction_id = t.id AND p.account_id IS NOT NULL
LEFT JOIN fin_account a ON a.id = p.account_id
LEFT JOIN fin_instrument i ON i.id = p.instrument_id
WHERE t.status = 'draft'
ORDER BY t.id, p.seq;
