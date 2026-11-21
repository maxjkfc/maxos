-- name: UpsertAreaBudget :one
-- Sets the budget of an area from a month on: stores it, or replaces the amount of
-- the one that the area has from that month. The row that was there keeps its id
-- and the time it was made, so the id given is only used when there is none. The
-- allocations follow in the same database transaction (DeleteBudgetAllocations,
-- InsertBudgetAllocation).
INSERT INTO fin_budget (id, scope, area_id, effective_from, amount)
VALUES (@id, 'area', @area_id::uuid, @effective_from::date, @amount::numeric)
ON CONFLICT (area_id, effective_from) DO UPDATE SET amount = EXCLUDED.amount
RETURNING *;

-- name: UpsertProjectBudget :one
-- Sets the budget of a project: stores it, or replaces the amount of the one that
-- the project has, which keeps its id and the time it was made.
INSERT INTO fin_budget (id, scope, project_id, amount)
VALUES (@id, 'project', @project_id::uuid, @amount::numeric)
ON CONFLICT (project_id) DO UPDATE SET amount = EXCLUDED.amount
RETURNING *;

-- name: DeleteBudgetAllocations :exec
DELETE FROM fin_budget_allocation WHERE budget_id = $1;

-- name: InsertBudgetAllocation :exec
INSERT INTO fin_budget_allocation (budget_id, category_id, amount) VALUES ($1, $2, $3);

-- name: DeleteProjectBudget :execrows
-- Its allocations go with it.
DELETE FROM fin_budget WHERE scope = 'project' AND project_id = @project_id::uuid;

-- name: ListAreaBudgets :many
-- Every budget of every area, with its allocations: a row for each allocation, and
-- one with no allocation columns for a budget that has none. By area and the month
-- the budget takes effect in; the allocations of a budget by the name of their
-- category (a name is only unique among the categories of one parent) and then id.
SELECT b.id, b.area_id::uuid AS area_id, b.effective_from::date AS effective_from,
       b.amount, b.created_at,
       a.category_id AS allocation_category_id, a.amount AS allocation_amount
FROM fin_budget b
LEFT JOIN fin_budget_allocation a ON a.budget_id = b.id
LEFT JOIN fin_category c ON c.id = a.category_id
WHERE b.scope = 'area'
ORDER BY b.area_id, b.effective_from, c.name, c.id;

-- name: ListProjectBudgets :many
-- The budgets of the projects with their allocations, as ListAreaBudgets lists
-- them. With a project_id it is that project's only.
SELECT b.id, b.project_id::uuid AS project_id, b.amount, b.created_at,
       a.category_id AS allocation_category_id, a.amount AS allocation_amount
FROM fin_budget b
LEFT JOIN fin_budget_allocation a ON a.budget_id = b.id
LEFT JOIN fin_category c ON c.id = a.category_id
WHERE b.scope = 'project'
  AND (sqlc.narg('project_id')::uuid IS NULL OR b.project_id = sqlc.narg('project_id'))
ORDER BY b.project_id, c.name, c.id;

-- name: ListSpending :many
-- What was spent, in one statement: the base amounts of the expense postings of the
-- confirmed transactions that happened on the days from from_day (any day up to
-- to_day when it is NULL) to to_day, both days included, added up for each month (the
-- first day of the month the transaction happened in), area, project and category,
-- and only for the transactions of the project when project_id is given. A transaction
-- that has no area or no project has a row with NULL in its place: it counts as well,
-- so that a month adds up to what the areas spent in it, projects included, and the
-- expenses that have none. What an area, a project or a month has spent is for the
-- model to make of these rows; nothing else decides what counts as spent. Every kind
-- of transaction has expense postings that count (the fee of a transfer, the
-- interest of an instalment period); a draft or a void transaction has none that do;
-- a reward is not an expense posting, and a discount is a column of one, not a
-- posting; a share of what I paid for others is a dealing posting. parent_id is the
-- parent category of the category, NULL for a top-level one: the spending of a
-- subcategory falls into the allocation of its parent when it has none of its own,
-- which is for the model to say. The month is cut from a timestamp, so that no time
-- zone is in it. The rows come in no order that matters.
SELECT date_trunc('month', t.occurred_on::timestamp)::date AS month,
       t.area_id,
       t.project_id,
       p.category_id::uuid AS category_id,
       c.parent_id,
       sum(p.base_amount)::numeric AS amount
FROM fin_posting p
JOIN fin_transaction t ON t.id = p.transaction_id
JOIN fin_category c ON c.id = p.category_id
WHERE t.status = 'confirmed' AND p.book = 'expense'
  AND (sqlc.narg('from_day')::date IS NULL OR t.occurred_on >= sqlc.narg('from_day'))
  AND t.occurred_on <= @to_day
  AND (sqlc.narg('project_id')::uuid IS NULL OR t.project_id = sqlc.narg('project_id'))
GROUP BY date_trunc('month', t.occurred_on::timestamp), t.area_id, t.project_id, p.category_id, c.parent_id;
