package postgres

import (
	"context"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// The statements written by hand live here, as the conventions allow for a query
// whose conditions are optional. Only this one: it returns the ids and nothing
// else, so no row of the wide transaction table is scanned by hand; the rows are
// read with the generated queries.

// listIDsSQL builds the statement that lists the ids of the transactions matching
// the filter, newest first, with its arguments. Only the conditions the filter
// sets are in it, and every value goes in as an argument, never into the text.
func listIDsSQL(f repo.TransactionFilter) (string, []any) {
	var (
		conditions []string
		args       []any
	)
	// arg adds a value to the arguments and returns the placeholder that stands for
	// it.
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}

	if f.Status != "" {
		conditions = append(conditions, "t.status = "+arg(string(f.Status)))
	}
	if f.From != nil {
		conditions = append(conditions, "t.occurred_on >= "+arg(*f.From))
	}
	if f.To != nil {
		conditions = append(conditions, "t.occurred_on <= "+arg(*f.To))
	}
	if f.ProjectID != nil {
		conditions = append(conditions, "t.project_id = "+arg(*f.ProjectID))
	}
	if f.AreaID != nil {
		conditions = append(conditions, "t.area_id = "+arg(*f.AreaID))
	}
	if f.AccountID != nil {
		conditions = append(conditions,
			"EXISTS (SELECT 1 FROM fin_posting p WHERE p.transaction_id = t.id AND p.account_id = "+arg(*f.AccountID)+")")
	}
	if f.PartyID != nil {
		conditions = append(conditions,
			"EXISTS (SELECT 1 FROM fin_posting p WHERE p.transaction_id = t.id AND p.party_id = "+arg(*f.PartyID)+")")
	}
	if f.PlanID != nil {
		conditions = append(conditions, "t.plan_id = "+arg(*f.PlanID))
	}

	var sb strings.Builder
	sb.WriteString("SELECT t.id FROM fin_transaction t")
	if len(conditions) > 0 {
		sb.WriteString(" WHERE ")
		sb.WriteString(strings.Join(conditions, " AND "))
	}
	// Ordered like fin_transaction_occurred_on_idx. The ids are UUIDv7, which sort
	// by the time they were made at, so the latest made comes first among the
	// transactions of one day.
	sb.WriteString(" ORDER BY t.occurred_on DESC, t.id DESC")
	sb.WriteString(" LIMIT " + arg(f.Limit))
	sb.WriteString(" OFFSET " + arg(f.Offset))
	return sb.String(), args
}

// listIDs runs listIDsSQL.
func (r *TransactionRepo) listIDs(ctx context.Context, f repo.TransactionFilter) ([]uuid.UUID, error) {
	query, args := listIDsSQL(f)
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
