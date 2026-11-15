-- +goose Up

-- M1c: budgets (預算) and the allocations (分配) that divide them among
-- categories.

-- A budget is a limit to compare spending against, never money set aside: nothing
-- is blocked when it is exceeded. It is one of two kinds, told apart by scope,
-- and each kind needs its own columns and no others:
--
--   scope     area_id    project_id   effective_from
--   area      required   -            required
--   project   -          required     -
--
-- An area has a budget per month, which does not carry over from one month to the
-- next. A row says what the budget is from the month in effective_from on, and
-- the budget of a month is the row with the greatest effective_from that is not
-- after it; an amount of 0 is "no budget from this month on". A project has one
-- total for the whole project, so one row.
--
-- area_id and project_id are ids of catalog entries: plain uuid columns with no
-- foreign key. The use case checks them before it writes.
CREATE TABLE fin_budget (
    id             uuid PRIMARY KEY,
    scope          text NOT NULL,
    area_id        uuid,
    project_id     uuid,
    effective_from date,
    -- In the base currency, with no more decimal places than it has: the use case
    -- checks that, as the base currency is a row of fin_instrument.
    amount         numeric NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT fin_budget_scope_check
        CHECK (scope IN ('area', 'project')),
    CONSTRAINT fin_budget_scope_columns_check
        CHECK (CASE scope
            WHEN 'area'    THEN area_id IS NOT NULL AND project_id IS NULL AND effective_from IS NOT NULL
            WHEN 'project' THEN project_id IS NOT NULL AND area_id IS NULL AND effective_from IS NULL
        END),
    -- The first day of a month. Passes when effective_from is NULL.
    CONSTRAINT fin_budget_effective_from_check
        CHECK (EXTRACT(DAY FROM effective_from) = 1),
    CONSTRAINT fin_budget_amount_check
        CHECK (amount >= 0),
    -- A NULL is distinct from every other value in a unique constraint, so the
    -- rows of the other scope are not in the way of either of these.
    CONSTRAINT fin_budget_area_id_effective_from_key
        UNIQUE (area_id, effective_from),
    CONSTRAINT fin_budget_project_id_key
        UNIQUE (project_id)
);

-- A share of one budget for one category. An expense falls into the allocation of
-- its own category, else of its parent category, by itself: there is nothing to
-- map by hand, and a category has no budget of its own. What ties allocations to
-- their budget, that they are of expense categories that are not system
-- categories, that a category and its parent are not both allocated, and that
-- they add up to no more than the budget, takes more than a CHECK can see: it is
-- the use case's and the domain's.
CREATE TABLE fin_budget_allocation (
    budget_id   uuid NOT NULL REFERENCES fin_budget (id) ON DELETE CASCADE,
    category_id uuid NOT NULL REFERENCES fin_category (id),
    amount      numeric NOT NULL,
    PRIMARY KEY (budget_id, category_id),
    CONSTRAINT fin_budget_allocation_amount_check
        CHECK (amount > 0)
);

-- +goose Down

-- Down restores the schema of 0006 and does not delete data: it stops with an
-- error when there is a budget, and leaves the database as it was. Delete the
-- budgets first (their allocations go with them).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM fin_budget) THEN
        RAISE EXCEPTION 'cannot roll back 0007_fin_budgets: the database holds budgets'
            USING HINT = 'Delete the budgets first: this migration does not delete data.';
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE fin_budget_allocation;
DROP TABLE fin_budget;
