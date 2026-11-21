package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/svgchart"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// budgetsView is where the areas of a month and the projects stand against their
// budgets.
type budgetsView struct {
	Month    string
	Prev     string
	Next     string
	Current  string
	Areas    []budgetView
	Projects []budgetView
}

// budgetView is an area or a project with what it spent against its budget.
type budgetView struct {
	Name string
	// HasBudget is whether it has a budget to compare with.
	HasBudget bool
	Budget    amountView
	Spent     amountView
	// WithProjects is what an area spent altogether, the projects' spending in it
	// too; it is only an area's.
	WithProjects     amountView
	ShowWithProjects bool
	// Over is whether what was spent is more than the budget; OverBy is by how much,
	// and Left what remains of it when it is not over.
	Over   bool
	OverBy amountView
	Left   amountView
	// Percent is what was spent as a share of the budget.
	Percent     string
	Bar         template.HTML
	Allocations []allocationRow
	Unallocated amountView
}

// allocationRow is an allocation: a share of the budget for a category, with what
// fell into it.
type allocationRow struct {
	Category string
	Amount   amountView
	Spent    amountView
	// Over is whether more fell into it than it is a share of.
	Over bool
}

// budgets reads the budgets of the areas for a month and those of the projects, and
// the lists that name them: a fixed number of calls, whatever number of areas and
// projects there are.
func (h *handlers) budgets(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	q, err := queryOf(r, "month")
	if err != nil {
		return webserver.Page{}, err
	}
	month, err := monthParam(q, h.today())
	if err != nil {
		return webserver.Page{}, err
	}
	areas, err := h.p.GetAreaBudgets.GetAreaBudgets(ctx, usecase.GetAreaBudgetsInput{Month: month})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	projects, err := h.p.GetProjectBudgets.GetProjectBudgets(ctx, usecase.GetProjectBudgetsInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	names, err := h.loadRefs(ctx, needCategories|needAreas|needProjects)
	if err != nil {
		return webserver.Page{}, fail(err)
	}

	v := budgetsView{
		Month:   webserver.FormatMonth(month),
		Prev:    monthLink("/app/budgets", month.AddDate(0, -1, 0)),
		Next:    monthLink("/app/budgets", month.AddDate(0, 1, 0)),
		Current: "/app/budgets",
	}
	for i, a := range areas.Areas {
		v.Areas = append(v.Areas, buildBudget(names, budgetInput{
			ID:           "area-" + strconv.Itoa(i),
			Name:         names.name(a.AreaID),
			Budget:       a.Budget,
			Spent:        a.Spent,
			WithProjects: &a.SpentWithProjects,
			Allocations:  a.Allocations,
			Unallocated:  a.Unallocated,
			Over:         a.Over,
		}))
	}
	for i, p := range projects.Projects {
		v.Projects = append(v.Projects, buildBudget(names, budgetInput{
			ID:          "project-" + strconv.Itoa(i),
			Name:        names.name(p.ProjectID),
			Budget:      p.Budget,
			Spent:       p.Spent,
			Allocations: p.Allocations,
			Unallocated: p.Unallocated,
			Over:        p.Over,
		}))
	}
	return webserver.Page{Name: "budgets", Title: "預算", Nav: navBudgets, Data: v}, nil
}

// monthLink is the page of a month.
func monthLink(path string, month time.Time) string {
	return link(path, url.Values{"month": {webserver.FormatMonth(month)}})
}

// budgetInput is what an area or a project spent, as the use cases say it.
type budgetInput struct {
	// ID tells the chart of this one from the others on the page.
	ID     string
	Name   string
	Budget *decimal.Decimal
	Spent  decimal.Decimal
	// WithProjects is set for an area.
	WithProjects *decimal.Decimal
	Allocations  []usecase.AllocationStatus
	Unallocated  decimal.Decimal
	Over         bool
}

// buildBudget lays out where an area or a project stands. The bar is the share of
// the budget that was spent: when more was spent than the budget it is drawn over
// the limit, and the page says so in words as well as in the colour.
func buildBudget(r *refs, in budgetInput) budgetView {
	v := budgetView{
		Name:        in.Name,
		Spent:       r.base(in.Spent),
		Over:        in.Over,
		Unallocated: r.base(in.Unallocated),
	}
	if in.WithProjects != nil {
		v.WithProjects = r.base(*in.WithProjects)
		v.ShowWithProjects = true
	}
	if in.Budget != nil {
		v.HasBudget = true
		v.Budget = r.base(*in.Budget)
		if !in.Budget.IsZero() {
			v.Percent = webserver.FormatPercent(in.Spent, *in.Budget)
		}
		title := fmt.Sprintf("%s：已花 %s，預算 %s", in.Name, v.Spent.Text, v.Budget.Text)
		if in.Over {
			v.OverBy = r.base(in.Spent.Sub(*in.Budget))
			title += "，超支 " + v.OverBy.Text
		} else {
			v.Left = r.base(in.Budget.Sub(in.Spent))
			title += "，還剩 " + v.Left.Text
		}
		v.Bar = svgchart.Progress(svgchart.Chart{ID: "budget-" + in.ID, Title: title}, in.Spent, *in.Budget)
	}
	for _, a := range in.Allocations {
		v.Allocations = append(v.Allocations, allocationRow{
			Category: r.categoryName(a.CategoryID),
			Amount:   r.base(a.Amount),
			Spent:    r.base(a.Spent),
			Over:     a.Over,
		})
	}
	return v
}
