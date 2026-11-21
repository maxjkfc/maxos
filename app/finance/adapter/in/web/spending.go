package web

import (
	"fmt"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/svgchart"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// The months the chart of the trend shows.
const trendMonths = 12

// spendingView is what was spent in a month, by category, and in each of the
// months of the trend. Every chart has a table with the same numbers: the table is
// what is read, the chart a picture of it.
type spendingView struct {
	Month   string
	Prev    string
	Next    string
	Current string
	Total   amountView
	// Top are the top-level categories, the largest first, each with the
	// subcategories it is made of.
	Top             []topCategoryView
	ByCategoryChart template.HTML
	// TrendTitle says which months the trend is of: the last twelve, or the twelve
	// that end with the month that is looked at when that is not among them.
	TrendTitle string
	Trend      []trendRow
	TrendChart template.HTML
}

type topCategoryView struct {
	Name  string
	Total amountView
	Share string
	// Children are the subcategories, the largest first. When a category has some
	// and was also spent on itself, the rest is a child of its own, called Own.
	Children []childCategoryView
}

type childCategoryView struct {
	Name   string
	Amount amountView
	Share  string
}

type trendRow struct {
	Month    string
	Href     string
	Total    amountView
	Selected bool
}

// ownLabel is the part of a category that was spent on the category itself and on
// none of its subcategories.
const ownLabel = "其他（直接記在這個類別）"

// spending reads what was spent in the month, and in the months of the chart: two
// calls and the list of categories.
func (h *handlers) spending(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	q, err := queryOf(r, "month")
	if err != nil {
		return webserver.Page{}, err
	}
	month, err := monthParam(q, h.today())
	if err != nil {
		return webserver.Page{}, err
	}
	current := model.MonthOf(h.today())
	from, to := trendWindow(month, current)

	spent, err := h.p.GetSpending.GetSpending(ctx, usecase.GetSpendingInput{Month: month})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	trend, err := h.p.GetSpendingTrend.GetSpendingTrend(ctx, usecase.GetSpendingTrendInput{From: from, To: to})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	names, err := h.loadRefs(ctx, needCategories)
	if err != nil {
		return webserver.Page{}, fail(err)
	}

	trendTitle := "最近十二個月每月的支出"
	if !to.Equal(current) {
		trendTitle = fmt.Sprintf("%s 到 %s 每月的支出", webserver.FormatMonth(from), webserver.FormatMonth(to))
	}
	v := buildSpending(names, month, spent, trend.Months, trendTitle)
	v.Prev = monthLink("/app/spending", month.AddDate(0, -1, 0))
	v.Next = monthLink("/app/spending", month.AddDate(0, 1, 0))
	v.Current = "/app/spending"
	return webserver.Page{Name: "spending", Title: "支出", Nav: navSpending, Data: v}, nil
}

// trendWindow is the twelve months the chart shows: those that end with the month
// today is in, which is where the eye looks for the last year, unless the month
// that is looked at is not among them. Then they end with that month, so that it is
// always one of the bars.
func trendWindow(selected, current time.Time) (from, to time.Time) {
	to = current
	if from = to.AddDate(0, -(trendMonths - 1), 0); selected.Before(from) || selected.After(to) {
		to = selected
		from = to.AddDate(0, -(trendMonths - 1), 0)
	}
	return from, to
}

// buildSpending lays out the month and the months of the trend. The use case gives
// each top-level category with its total, what was spent on it itself and its
// subcategories, so the page adds nothing up: the shares are shares of the total of
// the month.
func buildSpending(r *refs, month time.Time, spent usecase.GetSpendingOutput, months []usecase.MonthSpending, trendTitle string) spendingView {
	v := spendingView{Month: webserver.FormatMonth(month), Total: r.base(spent.Total), TrendTitle: trendTitle}

	bars := make([]svgchart.Bar, 0, len(spent.TopLevel))
	for _, t := range spent.TopLevel {
		name := r.categoryName(t.CategoryID)
		top := topCategoryView{Name: name, Total: r.base(t.Total), Share: webserver.FormatPercent(t.Total, spent.Total)}

		// The rows under a category: its subcategories and, when it has some, what was
		// spent on the category itself, the largest first.
		type child struct {
			name   string
			amount decimal.Decimal
		}
		children := make([]child, 0, len(t.Children)+1)
		for _, c := range t.Children {
			children = append(children, child{r.categoryName(c.CategoryID), c.Amount})
		}
		if len(t.Children) > 0 && !t.Own.IsZero() {
			children = append(children, child{ownLabel, t.Own})
		}
		slices.SortStableFunc(children, func(a, b child) int { return b.amount.Cmp(a.amount) })
		for _, c := range children {
			top.Children = append(top.Children, childCategoryView{Name: c.name, Amount: r.base(c.amount), Share: webserver.FormatPercent(c.amount, spent.Total)})
		}

		v.Top = append(v.Top, top)
		bars = append(bars, svgchart.Bar{Label: name, Value: t.Total, Text: top.Total.Text + " " + top.Total.Unit})
	}
	v.ByCategoryChart = svgchart.HBars(svgchart.Chart{ID: "by-category", Title: "這個月的支出（依類別）"}, bars)

	columns := make([]svgchart.Bar, 0, len(months))
	for _, m := range months {
		selected := m.Month.Equal(month)
		total := r.base(m.Total)
		href := monthLink("/app/spending", m.Month)
		v.Trend = append(v.Trend, trendRow{Month: webserver.FormatMonth(m.Month), Href: href, Total: total, Selected: selected})
		columns = append(columns, svgchart.Bar{
			Label:     strconv.Itoa(int(m.Month.Month())) + "月",
			Value:     m.Total,
			Text:      total.Text + " " + total.Unit,
			Href:      href,
			Highlight: selected,
		})
	}
	v.TrendChart = svgchart.Columns(svgchart.Chart{ID: "trend", Title: trendTitle}, columns)
	return v
}
