-- +goose Up

-- M1c: instalments (分期) and trades (買賣).

-- Two more kinds of transaction. An instalment is a period of an instalment plan
-- being charged to an account, or the conversion of what an account owes into a
-- plan (帳單分期); a trade buys or sells an instrument that is not a currency.
ALTER TABLE fin_transaction DROP CONSTRAINT fin_transaction_kind_check;
ALTER TABLE fin_transaction ADD CONSTRAINT fin_transaction_kind_check
    CHECK (kind IN ('expense', 'income', 'transfer', 'exchange', 'opening', 'reward', 'dealing', 'reconciliation', 'instalment', 'trade'));

-- A plan is the set of transactions that share plan_id; there is no table of
-- plans, the schedule is the period transactions themselves. plan_seq is 0 for
-- the origin of the plan (the purchase that was paid in instalments, or the
-- conversion of a debt), whose id is the plan_id, and 1, 2, ... for the periods,
-- in the order they fall due. A transaction that is in no plan has neither.
ALTER TABLE fin_transaction
    ADD COLUMN plan_id uuid,
    ADD COLUMN plan_seq integer;

ALTER TABLE fin_transaction
    ADD CONSTRAINT fin_transaction_plan_check
        CHECK ((plan_id IS NULL) = (plan_seq IS NULL)),
    -- Passes when plan_seq is NULL.
    ADD CONSTRAINT fin_transaction_plan_seq_check
        CHECK (plan_seq >= 0),
    -- The origin is the transaction the plan is named after, and no period is.
    -- Passes when plan_id is NULL.
    ADD CONSTRAINT fin_transaction_plan_origin_check
        CHECK ((plan_seq = 0) = (plan_id = id)),
    -- An instalment is always part of a plan; so is the expense that is paid in
    -- instalments, as its origin; no other kind is.
    ADD CONSTRAINT fin_transaction_plan_kind_check
        CHECK (CASE kind
            WHEN 'instalment' THEN plan_id IS NOT NULL
            WHEN 'expense'    THEN plan_seq IS NULL OR plan_seq = 0
            ELSE plan_id IS NULL
        END),
    ADD CONSTRAINT fin_transaction_plan_id_plan_seq_key
        UNIQUE (plan_id, plan_seq),
    -- No period without its origin. Deleting a plan deletes its periods first.
    ADD CONSTRAINT fin_transaction_plan_id_fkey
        FOREIGN KEY (plan_id) REFERENCES fin_transaction (id);

-- One more book. An instalment posting is the principal of a plan that has not
-- been charged to the account yet: it is made to the account the plan charges, so
-- account_id is now required for two books. It is negative while the account owes
-- the principal without having been charged it (the origin of the plan makes it
-- so), and each period that is charged takes its share out of it.
--
--   book        account_id   category_id   party_id   base_amount   discount
--   account     required     -             -          -             -
--   expense     -            required      -          required      optional
--   income      -            required      -          required      -
--   conversion  -            -             -          -             -
--   opening     -            -             -          -             -
--   dealing     -            -             required   -             -
--   reward      -            -             -          required      -
--   adjustment  -            -             -          -             -
--   instalment  required     -             -          -             -
--
-- The other checks need no change: the new book has none of the other columns.
ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_book_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_book_check
    CHECK (book IN ('account', 'expense', 'income', 'conversion', 'opening', 'dealing', 'reward', 'adjustment', 'instalment'));

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_account_id_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_account_id_check
    CHECK ((account_id IS NOT NULL) = (book IN ('account', 'instalment')));

-- +goose Down

-- Down restores the schema of 0005 and does not delete ledger data: it stops with
-- an error when the ledger holds anything that needs this migration, and leaves
-- the database as it was. What does: a transaction of the kind instalment or
-- trade, a transaction that is part of a plan (which is the expense that was paid
-- in instalments, as the origin of its plan), and a posting to the book
-- instalment. Delete those first.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM fin_transaction WHERE kind IN ('instalment', 'trade') OR plan_id IS NOT NULL)
        OR EXISTS (SELECT 1 FROM fin_posting WHERE book = 'instalment')
    THEN
        RAISE EXCEPTION 'cannot roll back 0006_fin_instalments: the ledger holds instalment plans or trades'
            USING HINT = 'Delete those transactions first: this migration does not delete ledger data.';
    END IF;
END
$$;
-- +goose StatementEnd

-- Dropping the columns drops the checks, the unique constraint and the foreign
-- key that mention them.
ALTER TABLE fin_transaction
    DROP COLUMN plan_seq,
    DROP COLUMN plan_id;

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_account_id_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_account_id_check
    CHECK ((account_id IS NOT NULL) = (book = 'account'));

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_book_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_book_check
    CHECK (book IN ('account', 'expense', 'income', 'conversion', 'opening', 'dealing', 'reward', 'adjustment'));

ALTER TABLE fin_transaction DROP CONSTRAINT fin_transaction_kind_check;
ALTER TABLE fin_transaction ADD CONSTRAINT fin_transaction_kind_check
    CHECK (kind IN ('expense', 'income', 'transfer', 'exchange', 'opening', 'reward', 'dealing', 'reconciliation'));
