-- +goose Up

-- M1b: dealings (往來), rewards (回饋), reconciliation (對帳) and the fees,
-- discounts and shares that go with them.

-- Three more kinds of transaction.
ALTER TABLE fin_transaction DROP CONSTRAINT fin_transaction_kind_check;
ALTER TABLE fin_transaction ADD CONSTRAINT fin_transaction_kind_check
    CHECK (kind IN ('expense', 'income', 'transfer', 'exchange', 'opening', 'reward', 'dealing', 'reconciliation'));

-- Three more books, and the two columns they and the expense need. party_id is
-- the id of a catalog party: a plain uuid column with no foreign key, as the
-- project and area of a transaction are; the use case checks it before it writes.
-- discount is what the merchant took off at checkout. It is only a record of it:
-- the quantity of the expense is what was paid, and no balance or total counts
-- the discount.
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
--
-- A dealing is what a party owes me: quantity is positive when they owe me more.
-- A reward is money a payment instrument gives back, and takes the sign of an
-- income. An adjustment is the counterpart of a reconciliation of an instrument
-- that is not a currency. account_id, category_id and the sign of base_amount
-- need no change: their checks already say the right thing about the new books.
ALTER TABLE fin_posting
    ADD COLUMN party_id uuid,
    ADD COLUMN discount numeric;

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_book_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_book_check
    CHECK (book IN ('account', 'expense', 'income', 'conversion', 'opening', 'dealing', 'reward', 'adjustment'));

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_base_amount_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_base_amount_check
    CHECK ((base_amount IS NOT NULL) = (book IN ('expense', 'income', 'reward')));

ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_party_id_check
    CHECK ((party_id IS NOT NULL) = (book = 'dealing'));

-- Passes when discount is NULL.
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_discount_check
    CHECK (discount IS NULL OR (book = 'expense' AND discount > 0));

CREATE INDEX fin_posting_party_id_idx ON fin_posting (party_id);

-- The two system categories, both called 未記錄 (unrecorded): what a
-- reconciliation books when an account holds less (an expense) or more (an
-- income) of a currency than the ledger says. Their ids are fixed because the
-- application refers to them: model.UnrecordedExpenseCategoryID and
-- model.UnrecordedIncomeCategoryID in app/finance/domain/model must stay equal
-- to them.
--
-- A top-level category name is unique per direction (fin_category_name_key), so
-- this migration stops, and rolls back, in a database where a top-level
-- category of either direction is already called 未記錄: rename it first.
INSERT INTO fin_category (id, direction, parent_id, name) VALUES
    ('00000000-0000-7000-8000-000000000101', 'expense', NULL, '未記錄'),
    ('00000000-0000-7000-8000-000000000102', 'income', NULL, '未記錄');

-- A system category is never archived: a reconciliation must always be able to
-- use it.
ALTER TABLE fin_category ADD CONSTRAINT fin_category_system_not_archived_check
    CHECK (archived_at IS NULL OR id NOT IN (
        '00000000-0000-7000-8000-000000000101',
        '00000000-0000-7000-8000-000000000102'));

-- +goose Down

-- Down restores the schema of 0004 and does not delete ledger data: it stops
-- with an error when the ledger holds anything that needs this migration, and
-- leaves the database as it was. What does: a transaction of the kind reward,
-- dealing or reconciliation; a posting to the book dealing, reward or adjustment;
-- a discount; a posting to a system category; and a fee, which is an expense
-- posting inside a transaction that is not an expense. Delete those first.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM fin_transaction WHERE kind IN ('reward', 'dealing', 'reconciliation'))
        OR EXISTS (SELECT 1 FROM fin_posting WHERE book IN ('dealing', 'reward', 'adjustment') OR discount IS NOT NULL)
        OR EXISTS (SELECT 1 FROM fin_posting WHERE category_id IN (
            '00000000-0000-7000-8000-000000000101',
            '00000000-0000-7000-8000-000000000102'))
        OR EXISTS (SELECT 1 FROM fin_posting p JOIN fin_transaction t ON t.id = p.transaction_id
                   WHERE p.book = 'expense' AND t.kind <> 'expense')
    THEN
        RAISE EXCEPTION 'cannot roll back 0005_fin_dealings: the ledger holds dealings, rewards, reconciliations, discounts or fees'
            USING HINT = 'Delete those transactions first: this migration does not delete ledger data.';
    END IF;
END
$$;
-- +goose StatementEnd

ALTER TABLE fin_category DROP CONSTRAINT fin_category_system_not_archived_check;
DELETE FROM fin_category WHERE id IN (
    '00000000-0000-7000-8000-000000000101',
    '00000000-0000-7000-8000-000000000102');

-- Dropping the columns drops the index and the checks that mention them.
ALTER TABLE fin_posting
    DROP COLUMN discount,
    DROP COLUMN party_id;

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_base_amount_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_base_amount_check
    CHECK ((base_amount IS NOT NULL) = (book IN ('expense', 'income')));

ALTER TABLE fin_posting DROP CONSTRAINT fin_posting_book_check;
ALTER TABLE fin_posting ADD CONSTRAINT fin_posting_book_check
    CHECK (book IN ('account', 'expense', 'income', 'conversion', 'opening'));

ALTER TABLE fin_transaction DROP CONSTRAINT fin_transaction_kind_check;
ALTER TABLE fin_transaction ADD CONSTRAINT fin_transaction_kind_check
    CHECK (kind IN ('expense', 'income', 'transfer', 'exchange', 'opening'));
