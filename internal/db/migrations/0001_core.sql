-- +goose Up
CREATE TABLE core_inbox (
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

CREATE INDEX core_inbox_received_at_idx ON core_inbox (received_at DESC);

CREATE TABLE core_tag (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core_party (
    id         uuid PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core_project (
    id           uuid PRIMARY KEY,
    name         text NOT NULL UNIQUE,
    starts_on    date,
    ends_on      date,
    status       text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'done', 'archived')),
    external_ref text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE core_link (
    id         uuid PRIMARY KEY,
    src_kind   text NOT NULL,
    src_id     uuid NOT NULL,
    dst_kind   text NOT NULL,
    dst_id     uuid NOT NULL,
    rel        text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (src_kind, src_id, dst_kind, dst_id, rel)
);

CREATE INDEX core_link_dst_idx ON core_link (dst_kind, dst_id);

-- +goose Down
DROP TABLE core_link;
DROP TABLE core_project;
DROP TABLE core_party;
DROP TABLE core_tag;
DROP TABLE core_inbox;
