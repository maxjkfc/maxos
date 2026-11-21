package service_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/domain/service"
)

// The rows of these tests are invented: no real account, merchant or amount.

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

type rowOpt func(*model.Row)

func main_(main, sub string) rowOpt {
	return func(r *model.Row) { r.Main, r.Sub = main, sub }
}
func fee(s string) rowOpt      { return func(r *model.Row) { r.Fee = d(s) } }
func discount(s string) rowOpt { return func(r *model.Row) { r.Discount = d(s) } }
func name(s string) rowOpt     { return func(r *model.Row) { r.Name = s } }
func merchant(s string) rowOpt { return func(r *model.Row) { r.Merchant = s } }
func detail(s string) rowOpt   { return func(r *model.Row) { r.Description = s } }
func project(s string) rowOpt  { return func(r *model.Row) { r.Project = s } }
func party(s string) rowOpt    { return func(r *model.Row) { r.Party = s } }
func tags(s ...string) rowOpt  { return func(r *model.Row) { r.Tags = s } }
func at(clock string) rowOpt   { return func(r *model.Row) { r.Clock = clock } }
func on(date string) rowOpt    { return func(r *model.Row) { r.Date = day(date) } }

// row makes a row of the type for the account, in the currency, of the amount, on
// 2026-03-10 at 12:00 unless an option says otherwise.
func row(typ model.RecordType, account, currency, amount string, opts ...rowOpt) model.Row {
	r := model.Row{
		Type:     typ,
		Account:  account,
		Currency: currency,
		Amount:   d(amount),
		Date:     day("2026-03-10"),
		Clock:    "12:00",
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

// rowsOf numbers the rows as a file does and gives them their keys.
func rowsOf(rows ...model.Row) []model.Row {
	for i := range rows {
		rows[i].Pos, rows[i].Line = i, i+2
	}
	model.AssignKeys(rows)
	return rows
}

// testMapping is a reviewed mapping for the accounts and the categories of these
// tests: what the tests do not say is guessed, as it is for the user.
func testMapping() model.Mapping {
	return model.Mapping{
		Instruments: map[string]model.InstrumentMap{
			"USD":  {Name: "US dollar", Kind: "fiat", IsCurrency: true, Decimals: 2},
			"JPY":  {Name: "Yen", Kind: "fiat", IsCurrency: true, Decimals: 0},
			"USDT": {Name: "Tether", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
			"BTC":  {Name: "Bitcoin", Kind: "crypto", IsCurrency: false, Decimals: 8},
		},
		Accounts: map[string]model.AccountMap{
			"Bank":         {Name: "Bank", Kind: "bank", Institution: "FakeBank"},
			"Bank USD":     {Name: "Bank", Kind: "bank", Institution: "FakeBank"},
			"Card":         {Name: "Card", Kind: "card", Institution: "FakeBank"},
			"Cash":         {Name: "Cash", Kind: "cash"},
			"Exchange":     {Name: "Exchange", Kind: "exchange"},
			"Exchange BTC": {Name: "Exchange", Kind: "exchange"},
			"Broker":       {Name: "Broker", Kind: "brokerage", Institution: "FakeBroker"},
		},
		Projects: map[string]model.ProjectMap{
			"Daily":     {Kind: model.ProjectArea, Name: "Daily"},
			"Trip":      {Kind: model.ProjectProject, Name: "Trip", DefaultArea: "Daily"},
			"Crowd":     {Kind: model.ProjectTag, Name: "Crowd"},
			"Corp paid": {Kind: model.ProjectDealings, Party: "Corp"},
		},
		Expense: map[string]model.CategoryMap{
			"Food/Lunch": {Category: "Food/Lunch"},
			"Shop/Books": {Category: "Shop/Books"},
			"Self/投資":    {Category: "Self/投資"},
		},
		Income: map[string]model.CategoryMap{
			"收入/薪水":    {Category: "Salary"},
			"收入/收款":    {Category: "收款（待確認）", Tag: "待確認"},
			"收入/信用卡抵扣": {Treatment: model.TreatmentReward},
			"收入/公司代收款": {Treatment: model.TreatmentDealing, Party: "Corp"},
			"收入/投資":    {Category: "Investment"},
		},
		Parties: map[string]string{"Amy": "Amy", "Corp": "Corp"},
		Tags:    map[string]string{"family": "Family"},
		Defaults: model.Defaults{
			FeeCategory:      "Fees",
			InterestCategory: "Interest",
			RefundCategory:   "Refunds",
		},
	}
}

func testRates() model.Rates {
	return model.NewRates(map[string][]model.Rate{
		"USD": {{Date: day("2026-03-01"), PerUnit: d("30")}, {Date: day("2026-03-12"), PerUnit: d("31")}},
		"JPY": {{Date: day("2026-03-01"), PerUnit: d("0.2")}},
	})
}

// plan builds the plan of the rows with the test mapping and rates.
func plan(rows ...model.Row) model.Plan {
	return service.BuildPlan(service.PlanInput{Rows: rowsOf(rows...), Mapping: testMapping(), Rates: testRates()})
}

// unitOf returns the only unit of the plan.
func unitOf(t *testing.T, p model.Plan) model.Unit {
	t.Helper()
	if len(p.Units) != 1 {
		t.Fatalf("plan has %d units, want 1: %+v (skipped %+v)", len(p.Units), p.Units, p.Skipped)
	}
	return p.Units[0]
}

// skippedReasons lists the reasons the plan leaves rows out for.
func skippedReasons(p model.Plan) []model.Reason {
	var out []model.Reason
	for _, s := range p.Skipped {
		out = append(out, s.Reason)
	}
	return out
}

func ptr(s string) *decimal.Decimal {
	v := d(s)
	return &v
}

// serviceBuild builds the plan of rows that already have their keys, with the
// mapping it is given and the test rates.
func serviceBuild(rows []model.Row, m model.Mapping) model.Plan {
	return service.BuildPlan(service.PlanInput{Rows: rows, Mapping: m, Rates: testRates()})
}

// isLeg tells whether the leg is of the account and instrument, for the amount.
func isLeg(l model.Leg, account, instrument, amount string) bool {
	return l.Account == account && l.Instrument == instrument && l.Amount.Equal(d(amount))
}
