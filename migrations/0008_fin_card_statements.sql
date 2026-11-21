-- +goose Up

-- M1d: card statements (信用卡帳單).

-- A card closes a statement on one day of the month and has it paid by another.
-- The statement itself is not stored: it is what the card's transactions add up to
-- between two closing days, worked out when it is asked for (so that it can never
-- disagree with the ledger). What is stored is the two days on the account, and,
-- on a transaction, the day the institution put it on a statement when that is not
-- the day it happened.
--
-- A day is 1 to 31. A month that is shorter has no such day, and the last day of
-- the month stands in for it, so 31 is always the last day. That is read where the
-- days are used (model.DateInMonth); here only the range is checked.
--
-- The days are not historised: the statements are cut from the day that is stored
-- now, so changing it cuts the past statements again.
ALTER TABLE fin_account
    ADD COLUMN closing_day smallint,
    ADD COLUMN payment_due_day smallint;

ALTER TABLE fin_account
    -- Each of these passes when the column is NULL.
    ADD CONSTRAINT fin_account_closing_day_check
        CHECK (closing_day BETWEEN 1 AND 31),
    ADD CONSTRAINT fin_account_payment_due_day_check
        CHECK (payment_due_day BETWEEN 1 AND 31),
    -- Only a card has statements.
    ADD CONSTRAINT fin_account_statement_days_kind_check
        CHECK (kind = 'card' OR (closing_day IS NULL AND payment_due_day IS NULL)),
    -- A statement is paid by the first payment due day after it closes, so a due
    -- day needs a closing day. Both may be NULL on a card: it has no statements.
    ADD CONSTRAINT fin_account_payment_due_day_closing_day_check
        CHECK (payment_due_day IS NULL OR closing_day IS NOT NULL);

-- The posting date (入帳日): the day the institution put the transaction on a
-- statement, when that is not the day it happened. NULL means the day it happened,
-- occurred_on. It decides which statement the transaction is on and nothing else:
-- balances, budgets and dealings go by occurred_on, as before. Nothing but the
-- use cases checks that it belongs to a transaction on a card, and that the
-- transaction is not part of an instalment plan: a CHECK cannot see other tables.
ALTER TABLE fin_transaction
    ADD COLUMN posted_on date;

ALTER TABLE fin_transaction
    -- Passes when posted_on is NULL.
    ADD CONSTRAINT fin_transaction_posted_on_check
        CHECK (posted_on >= occurred_on);

-- +goose Down

-- Down restores the schema of 0007 and does not delete data: it stops with an
-- error when an account has a closing day or a payment due day, or a transaction
-- has a posting date, and leaves the database as it was. Clear them first.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM fin_account WHERE closing_day IS NOT NULL OR payment_due_day IS NOT NULL)
        OR EXISTS (SELECT 1 FROM fin_transaction WHERE posted_on IS NOT NULL)
    THEN
        RAISE EXCEPTION 'cannot roll back 0008_fin_card_statements: the database holds closing days, payment due days or posting dates'
            USING HINT = 'Clear them first: this migration does not delete data.';
    END IF;
END
$$;
-- +goose StatementEnd

-- Dropping the columns drops the checks that mention them.
ALTER TABLE fin_transaction
    DROP COLUMN posted_on;

ALTER TABLE fin_account
    DROP COLUMN payment_due_day,
    DROP COLUMN closing_day;
