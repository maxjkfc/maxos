package web

import (
	"cmp"
	"html/template"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/svgchart"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// The months the chart of the trend shows.
const trendMonths = 12

// spendingView is what was spent in a month, by category, and in each of the last
// twelve months. Every chart has a table with the same numbers: the table is what is
// read, the chart a picture of it.
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
	Trend           []trendRow
	TrendChart      template.HTML
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

// spending reads what was spent in the month, and in the twelve months of the chart:
// two calls and the list of categories.
func (h *handlers) spending(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	month, err := monthParam(r.URL.Query(), h.today())
	if err != nil {
		return webserver.Page{}, err
	}
	from, to := trendWindow(month, model.MonthOf(h.today()))

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

	v := buildSpending(names, month, spent, trend.Months)
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

// buildSpending lays out the month and the months of the trend. The categories are
// added up under their top-level category (the use case gives each with its parent),
// and the shares are shares of the total of the month.
func buildSpending(r *refs, month time.Time, spent usecase.GetSpendingOutput, months []usecase.MonthSpending) spendingView {
	v := spendingView{Month: webserver.FormatMonth(month), Total: r.base(spent.Total)}

	type group struct {
		id       uuid.UUID
		name     string
		total    decimal.Decimal
		own      decimal.Decimal
		children []usecase.CategorySpending
	}
	groups := make(map[uuid.UUID]*group)
	var order []*group
	for _, c := range spent.Categories {
		top := c.CategoryID
		if c.ParentID != nil {
			top = *c.ParentID
		}
		g, ok := groups[top]
		if !ok {
			g = &group{id: top, name: r.categoryName(top)}
			groups[top] = g
			order = append(order, g)
		}
		g.total = g.total.Add(c.Amount)
		if c.ParentID == nil {
			g.own = g.own.Add(c.Amount)
		} else {
			g.children = append(g.children, c)
		}
	}
	slices.SortFunc(order, func(a, b *group) int {
		return cmp.Or(b.total.Cmp(a.total), cmp.Compare(a.name, b.name))
	})

	bars := make([]svgchart.Bar, 0, len(order))
	for _, g := range order {
		top := topCategoryView{Name: g.name, Total: r.base(g.total), Share: webserver.FormatPercent(g.total, spent.Total)}
		type child struct {
			name   string
			amount decimal.Decimal
		}
		var children []child
		for _, c := range g.children {
			children = append(children, child{r.categoryName(c.CategoryID), c.Amount})
		}
		if len(g.children) > 0 && !g.own.IsZero() {
			children = append(children, child{ownLabel, g.own})
		}
		slices.SortFunc(children, func(a, b child) int {
			return cmp.Or(b.amount.Cmp(a.amount), cmp.Compare(a.name, b.name))
		})
		for _, c := range children {
			top.Children = append(top.Children, childCategoryView{Name: c.name, Amount: r.base(c.amount), Share: webserver.FormatPercent(c.amount, spent.Total)})
		}
		v.Top = append(v.Top, top)
		bars = append(bars, svgchart.Bar{Label: g.name, Value: g.total, Text: top.Total.Text + " " + top.Total.Unit})
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
	v.TrendChart = svgchart.Columns(svgchart.Chart{ID: "trend", Title: "最近十二個月每月的支出合計"}, columns)
	return v
}
