package budget

import (
	"context"
	"fmt"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/out/repo"
)

// GetSpending returns what was spent in the month the request names, by category,
// with the definition of spent that the budgets have: what the repo adds up for the
// days of the month, up to today and no further (model.SpendingRange). The days of
// a month that has not begun are not asked for at all, and it has nothing; the
// current month has what was made by today. Expenses count whether they belong to
// an area, a project, both or neither, so the total of a month is what the areas
// spent in it, projects included, and what no area has.
//
// The categories are for the domain to make of the rows of the repo
// (model.TopLevelSpendingOfMonth): the top-level ones, the largest first, with the
// subcategories under them, and their total.
func (s *Service) GetSpending(ctx context.Context, in usecase.GetSpendingInput) (usecase.GetSpendingOutput, error) {
	if in.Month.IsZero() {
		return usecase.GetSpendingOutput{}, model.ErrMonthRequired
	}
	month := model.MonthOf(in.Month)

	rows, err := s.spentIn(ctx, month, month)
	if err != nil {
		return usecase.GetSpendingOutput{}, err
	}
	topLevel, total := model.TopLevelSpendingOfMonth(month, rows)

	out := usecase.GetSpendingOutput{Month: month, Total: total, TopLevel: make([]usecase.TopLevelSpending, len(topLevel))}
	for i, t := range topLevel {
		out.TopLevel[i] = usecase.TopLevelSpending{CategoryID: t.CategoryID, Total: t.Total, Own: t.Own}
		for _, c := range t.Children {
			out.TopLevel[i].Children = append(out.TopLevel[i].Children, usecase.CategorySpending{CategoryID: c.CategoryID, Amount: c.Amount})
		}
	}
	return out, nil
}

// GetSpendingTrend returns what was spent in each month of the range the request
// names, as a total for each, with the same definition of spent as GetSpending. It
// asks the repo once for the whole range, from the first day of its first month to
// the last day of its last month or today, whichever is earlier; a range that has
// not begun is not asked for at all. Every month of the range is in the result,
// those that nothing was spent in, and those that have not begun, with a total of
// zero.
//
// The range is checked before anything is read: both ends are required, the last
// month is not before the first, and there are at most usecase.MaxSpendingTrendMonths
// months in it.
func (s *Service) GetSpendingTrend(ctx context.Context, in usecase.GetSpendingTrendInput) (usecase.GetSpendingTrendOutput, error) {
	switch {
	case in.From.IsZero():
		return usecase.GetSpendingTrendOutput{}, fmt.Errorf("%w: the first month of the range is missing", model.ErrMonthRequired)
	case in.To.IsZero():
		return usecase.GetSpendingTrendOutput{}, fmt.Errorf("%w: the last month of the range is missing", model.ErrMonthRequired)
	}
	first, last := model.MonthOf(in.From), model.MonthOf(in.To)
	if last.Before(first) {
		return usecase.GetSpendingTrendOutput{}, fmt.Errorf("%w: from %s, to %s",
			model.ErrDateRangeReversed, first.Format("2006-01"), last.Format("2006-01"))
	}
	if n := model.CountMonths(first, last); n > usecase.MaxSpendingTrendMonths {
		return usecase.GetSpendingTrendOutput{}, fmt.Errorf("%w: %d months from %s to %s, and at most %d are read",
			model.ErrSpendingRangeTooWide, n, first.Format("2006-01"), last.Format("2006-01"), usecase.MaxSpendingTrendMonths)
	}

	rows, err := s.spentIn(ctx, first, last)
	if err != nil {
		return usecase.GetSpendingTrendOutput{}, err
	}
	totals := model.TotalsByMonth(first, last, rows)

	out := usecase.GetSpendingTrendOutput{Months: make([]usecase.MonthSpending, len(totals))}
	for i, t := range totals {
		out.Months[i] = usecase.MonthSpending{Month: t.Month, Total: t.Total}
	}
	return out, nil
}

// spentIn returns what the repo adds up as spent in the months from first to last, up
// to today: for the days of the months that count by today (model.SpendingRange), in
// one call. It asks for nothing when the first month has not begun. It is the one
// place that asks for what was spent by month, for the budgets of the areas, for the
// spending of a month and for the trend.
func (s *Service) spentIn(ctx context.Context, first, last time.Time) ([]model.SpendingRow, error) {
	from, through, begun := model.SpendingRange(first, last, s.today())
	if !begun {
		return nil, nil
	}
	rows, err := s.budgets.Spending(ctx, repo.SpendingFilter{From: &from, To: through})
	if err != nil {
		return nil, fmt.Errorf("get what was spent from %s to %s: %w", from.Format(time.DateOnly), through.Format(time.DateOnly), err)
	}
	return rows, nil
}
