-- +goose Up

-- One money event. Its postings, the actual movements, are in fin_posting.
--
-- project_id, area_id and inbox_item_id are ids of other contexts' entries: plain
-- uuid columns with no foreign key. The use case checks them before it writes.
CREATE TABLE fin_transaction (
    id              uuid PRIMARY KEY,
    kind            text NOT NULL,
    status          text NOT NULL,
    occurred_on     date NOT NULL,
    occurred_at     timestamptz,
    description     text NOT NULL DEFAULT '',
    merchant        text NOT NULL DEFAULT '',
    project_id      uuid,
    area_id         uuid,
    inbox_item_id   uuid,
    idempotency_key text NOT NULL,
    is_trial        boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    confirmed_at    timestamptz,
    voided_at       timestamptz,
    CONSTRAINT fin_transaction_idempotency_key_key
        UNIQUE (idempotency_key),
    CONSTRAINT fin_transaction_kind_check
        CHECK (kind IN ('expense', 'income', 'transfer', 'exchange', 'opening')),
    CONSTRAINT fin_transaction_status_check
        CHECK (status IN ('draft', 'confirmed', 'void')),
    -- What each status has been through. Only a confirmed transaction can be
    -- voided, so a void one has both times.
    CONSTRAINT fin_transaction_status_times_check
        CHECK (CASE status
            WHEN 'draft'     THEN confirmed_at IS NULL AND voided_at IS NULL
            WHEN 'confirmed' THEN confirmed_at IS NOT NULL AND voided_at IS NULL
            WHEN 'void'      THEN confirmed_at IS NOT NULL AND voided_at IS NOT NULL
        END)
);

-- The list of transactions is newest first.
CREATE INDEX fin_transaction_occurred_on_idx ON fin_transaction (occurred_on DESC, id DESC);
CREATE INDEX fin_transaction_project_id_idx ON fin_transaction (project_id);
CREATE INDEX fin_transaction_area_id_idx ON fin_transaction (area_id);

-- One movement of one instrument: the book it goes to and how much. Each book
-- needs the columns that say which one, and no others:
--
--   book        account_id   category_id   base_amount
--   account     required     -             -
--   expense     -            required      required
--   income      -            required      required
--   conversion  -            -             -
--   opening     -            -             -
--
-- A later migration adds the books of the dealings and the rewards (and a party
-- column). quantity is positive when the book grows (an account holds more, an
-- expense happens) and negative when it shrinks (an income happens). base_amount
-- is the same amount in the base currency, with the sign of quantity.
CREATE TABLE fin_posting (
    id             uuid PRIMARY KEY,
    transaction_id uuid NOT NULL REFERENCES fin_transaction (id) ON DELETE CASCADE,
    seq            smallint NOT NULL,
    book           text NOT NULL,
    account_id     uuid REFERENCES fin_account (id),
    category_id    uuid REFERENCES fin_category (id),
    instrument_id  uuid NOT NULL REFERENCES fin_instrument (id),
    quantity       numeric NOT NULL,
    base_amount    numeric,
    memo           text NOT NULL DEFAULT '',
    CONSTRAINT fin_posting_transaction_id_seq_key
        UNIQUE (transaction_id, seq),
    CONSTRAINT fin_posting_book_check
        CHECK (book IN ('account', 'expense', 'income', 'conversion', 'opening')),
    CONSTRAINT fin_posting_account_id_check
        CHECK ((account_id IS NOT NULL) = (book = 'account')),
    CONSTRAINT fin_posting_category_id_check
        CHECK ((category_id IS NOT NULL) = (book IN ('expense', 'income'))),
    CONSTRAINT fin_posting_base_amount_check
        CHECK ((base_amount IS NOT NULL) = (book IN ('expense', 'income'))),
    CONSTRAINT fin_posting_quantity_check
        CHECK (quantity <> 0),
    -- Passes when base_amount is NULL.
    CONSTRAINT fin_posting_base_amount_sign_check
        CHECK (sign(base_amount) = sign(quantity))
);

CREATE INDEX fin_posting_account_id_instrument_id_idx ON fin_posting (account_id, instrument_id);
CREATE INDEX fin_posting_category_id_idx ON fin_posting (category_id);

-- tag_id is the id of a catalog tag: no foreign key.
CREATE TABLE fin_transaction_tag (
    transaction_id uuid NOT NULL REFERENCES fin_transaction (id) ON DELETE CASCADE,
    tag_id         uuid NOT NULL,
    PRIMARY KEY (transaction_id, tag_id)
);

-- The last guard of the first invariant of the ledger: in one transaction, the
-- postings of each instrument add up to zero. The application checks it first;
-- this keeps any other write honest.
--
-- A transaction is written in several statements, its row and then its postings,
-- so the check is a deferred constraint trigger: it runs when the database
-- transaction commits, for every transaction that was inserted or whose postings
-- changed, and aborts the commit when one does not balance or has no postings. A
-- transaction that no longer exists has no postings to balance; this is how
-- deleting a draft, which deletes its postings, passes.
-- +goose StatementBegin
CREATE FUNCTION fin_assert_transaction_balanced(tx_id uuid) RETURNS void
LANGUAGE plpgsql AS $$
DECLARE
    off_instrument uuid;
    off_total      numeric;
BEGIN
    IF NOT EXISTS (SELECT 1 FROM fin_transaction WHERE id = tx_id) THEN
        RETURN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM fin_posting WHERE transaction_id = tx_id) THEN
        RAISE EXCEPTION 'transaction % has no postings', tx_id
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fin_transaction_balanced';
    END IF;
    SELECT instrument_id, sum(quantity) INTO off_instrument, off_total
    FROM fin_posting
    WHERE transaction_id = tx_id
    GROUP BY instrument_id
    HAVING sum(quantity) <> 0
    LIMIT 1;
    IF FOUND THEN
        RAISE EXCEPTION 'transaction % does not balance: its postings of instrument % add up to %',
                tx_id, off_instrument, off_total
            USING ERRCODE = 'check_violation', CONSTRAINT = 'fin_transaction_balanced';
    END IF;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION fin_transaction_balance_trigger() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM fin_assert_transaction_balanced(NEW.id);
    RETURN NULL;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION fin_posting_balance_trigger() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP <> 'INSERT' THEN
        PERFORM fin_assert_transaction_balanced(OLD.transaction_id);
    END IF;
    IF TG_OP <> 'DELETE' THEN
        PERFORM fin_assert_transaction_balanced(NEW.transaction_id);
    END IF;
    RETURN NULL;
END
$$;
-- +goose StatementEnd

CREATE CONSTRAINT TRIGGER fin_transaction_balanced
    AFTER INSERT ON fin_transaction
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION fin_transaction_balance_trigger();

CREATE CONSTRAINT TRIGGER fin_posting_balanced
    AFTER INSERT OR UPDATE OR DELETE ON fin_posting
    DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION fin_posting_balance_trigger();

-- +goose Down

-- Dropping a table drops its triggers, and only then can their functions go.
DROP TABLE fin_transaction_tag;
DROP TABLE fin_posting;
DROP TABLE fin_transaction;
DROP FUNCTION fin_posting_balance_trigger();
DROP FUNCTION fin_transaction_balance_trigger();
DROP FUNCTION fin_assert_transaction_balanced(uuid);
