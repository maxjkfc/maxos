-- +goose Up

-- Every catalog entry can be archived: it stays for the transactions that use
-- it, but new ones may no longer refer to it.
ALTER TABLE catalog_area  ADD COLUMN archived_at timestamptz;
ALTER TABLE catalog_party ADD COLUMN archived_at timestamptz;
ALTER TABLE catalog_tag   ADD COLUMN archived_at timestamptz;

-- A project has a period (starts_on .. ends_on) and an active period
-- (active_from .. active_to), the days it is actually under way. A period has
-- both ends or neither. A CHECK passes when its expression is NULL, so each
-- range check below only applies once the ends it compares are set.
ALTER TABLE catalog_project
    ADD COLUMN starts_on       date,
    ADD COLUMN ends_on         date,
    ADD COLUMN active_from     date,
    ADD COLUMN active_to       date,
    ADD COLUMN default_area_id uuid REFERENCES catalog_area (id),
    ADD COLUMN archived_at     timestamptz,
    ADD CONSTRAINT catalog_project_period_ends_check
        CHECK ((starts_on IS NULL) = (ends_on IS NULL)),
    ADD CONSTRAINT catalog_project_period_order_check
        CHECK (ends_on >= starts_on),
    ADD CONSTRAINT catalog_project_active_period_ends_check
        CHECK ((active_from IS NULL) = (active_to IS NULL)),
    ADD CONSTRAINT catalog_project_active_period_order_check
        CHECK (active_to >= active_from),
    ADD CONSTRAINT catalog_project_active_within_period_check
        CHECK (active_from >= starts_on AND active_to <= ends_on);

-- +goose Down

-- Dropping a column drops the constraints that mention it.
ALTER TABLE catalog_project
    DROP COLUMN archived_at,
    DROP COLUMN default_area_id,
    DROP COLUMN active_to,
    DROP COLUMN active_from,
    DROP COLUMN ends_on,
    DROP COLUMN starts_on;
ALTER TABLE catalog_tag   DROP COLUMN archived_at;
ALTER TABLE catalog_party DROP COLUMN archived_at;
ALTER TABLE catalog_area  DROP COLUMN archived_at;
