-- +goose Up

-- Anything that can be held in some quantity and has a price: fiat money,
-- crypto, stocks, funds. kind says what sort of thing it is; is_currency says
-- whether it is used to price and to pay (a Currency in CONTEXT.md). They are
-- independent: TWD is fiat and a currency, USDT is crypto and still a currency,
-- BTC is crypto and not.
CREATE TABLE fin_instrument (
    id          uuid PRIMARY KEY,
    code        text NOT NULL UNIQUE,
    name        text NOT NULL,
    kind        text NOT NULL,
    is_currency boolean NOT NULL,
    -- Decimal places shown and accepted for quantities of this instrument.
    decimals    smallint NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    CONSTRAINT fin_instrument_kind_check
        CHECK (kind IN ('fiat', 'crypto', 'stock', 'fund')),
    CONSTRAINT fin_instrument_decimals_check
        CHECK (decimals BETWEEN 0 AND 18)
);

-- The base currency. Its id is fixed because the application refers to it:
-- model.BaseCurrencyID in app/finance/domain/model must stay equal to it.
INSERT INTO fin_instrument (id, code, name, kind, is_currency, decimals)
VALUES ('00000000-0000-7000-8000-000000000001', 'TWD', '新台幣', 'fiat', true, 2);

-- The organisation that keeps accounts for me: a bank, a broker, an exchange.
CREATE TABLE fin_institution (
    id          uuid PRIMARY KEY,
    name        text NOT NULL UNIQUE,
    created_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz
);

-- A place that holds instruments. An account is not tied to one instrument,
-- and belongs to at most one institution (cash and self-custody wallets to none).
CREATE TABLE fin_account (
    id             uuid PRIMARY KEY,
    name           text NOT NULL UNIQUE,
    institution_id uuid REFERENCES fin_institution (id),
    kind           text NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    archived_at    timestamptz,
    CONSTRAINT fin_account_kind_check
        CHECK (kind IN ('cash', 'bank', 'card', 'exchange', 'brokerage', 'wallet', 'loan'))
);

-- What an expense bought or where an income came from, in at most two levels.
--
-- The two rules about a parent, "it is a top-level category" and "it has the
-- same direction", are one composite foreign key, so that the database keeps
-- them under every kind of write (a child can not be moved under a child, and a
-- parent that has children can not be turned into one). A foreign key can only
-- compare columns, hence the two generated columns:
--   is_top              true for a top-level category: what a parent must be;
--   parent_must_be_top  true for a child, NULL for a top-level category. A
--                       foreign key is not checked when one of its columns is
--                       NULL, which leaves the top-level categories alone.
CREATE TABLE fin_category (
    id                 uuid PRIMARY KEY,
    direction          text NOT NULL,
    parent_id          uuid,
    name               text NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    archived_at        timestamptz,
    is_top             boolean NOT NULL GENERATED ALWAYS AS (parent_id IS NULL) STORED,
    parent_must_be_top boolean GENERATED ALWAYS AS (CASE WHEN parent_id IS NULL THEN NULL ELSE true END) STORED,
    CONSTRAINT fin_category_direction_check
        CHECK (direction IN ('expense', 'income')),
    -- NULLS NOT DISTINCT: two top-level categories (no parent) may not share a
    -- name either.
    CONSTRAINT fin_category_name_key
        UNIQUE NULLS NOT DISTINCT (direction, parent_id, name),
    CONSTRAINT fin_category_parent_target_key
        UNIQUE (id, direction, is_top),
    CONSTRAINT fin_category_parent_fkey
        FOREIGN KEY (parent_id, direction, parent_must_be_top)
        REFERENCES fin_category (id, direction, is_top)
);

-- +goose Down
DROP TABLE fin_category;
DROP TABLE fin_account;
DROP TABLE fin_institution;
DROP TABLE fin_instrument;
