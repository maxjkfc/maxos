-- +goose Up
CREATE TABLE inbox_item (
    id              uuid PRIMARY KEY,
    received_at     timestamptz NOT NULL DEFAULT now(),
    source          text NOT NULL,
    actor           text NOT NULL,
    kind            text NOT NULL,
    idempotency_key text NOT NULL UNIQUE,
    payload         jsonb NOT NULL,
    status          text NOT NULL DEFAULT 'received'
        CHECK (status IN ('received', 'processed', 'rejected'))
);

CREATE INDEX inbox_item_received_at_idx ON inbox_item (received_at DESC);

CREATE TABLE catalog_tag (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE catalog_party (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE catalog_area (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE catalog_project (
    id           uuid PRIMARY KEY,
    name         text NOT NULL UNIQUE,
    starts_on    date,
    ends_on      date,
    status       text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'done', 'archived')),
    external_ref text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE catalog_link (
    id         uuid PRIMARY KEY,
    src_kind   text NOT NULL,
    src_id     uuid NOT NULL,
    dst_kind   text NOT NULL,
    dst_id     uuid NOT NULL,
    rel        text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (src_kind, src_id, dst_kind, dst_id, rel)
);

CREATE INDEX catalog_link_dst_idx ON catalog_link (dst_kind, dst_id);

-- +goose Down
DROP TABLE catalog_link;
DROP TABLE catalog_project;
DROP TABLE catalog_area;
DROP TABLE catalog_party;
DROP TABLE catalog_tag;
DROP TABLE inbox_item;
