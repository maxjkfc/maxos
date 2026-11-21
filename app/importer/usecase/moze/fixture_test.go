package moze_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
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

type opt func(*model.Row)

func cat(main, sub string) opt { return func(r *model.Row) { r.Main, r.Sub = main, sub } }
func fee(s string) opt         { return func(r *model.Row) { r.Fee = d(s) } }
func discount(s string) opt    { return func(r *model.Row) { r.Discount = d(s) } }
func name(s string) opt        { return func(r *model.Row) { r.Name = s } }
func merchant(s string) opt    { return func(r *model.Row) { r.Merchant = s } }
func detail(s string) opt      { return func(r *model.Row) { r.Description = s } }
func project(s string) opt     { return func(r *model.Row) { r.Project = s } }
func party(s string) opt       { return func(r *model.Row) { r.Party = s } }
func tags(s ...string) opt     { return func(r *model.Row) { r.Tags = s } }

// row makes a row at the date and clock ("2026-01-05 10:00"), or with no date for "".
func row(when string, typ model.RecordType, account, currency, amount string, opts ...opt) model.Row {
	r := model.Row{Type: typ, Account: account, Currency: currency, Amount: d(amount)}
	if when != "" {
		r.Date, r.Clock = day(when[:10]), when[11:]
	}
	for _, o := range opts {
		o(&r)
	}
	return r
}

func out(when, account, currency, amount string, opts ...opt) model.Row {
	return row(when, model.TypeTransferOut, account, currency, amount, append([]opt{cat("轉帳", "轉帳")}, opts...)...)
}

func in(when, account, currency, amount string, opts ...opt) model.Row {
	return row(when, model.TypeTransferIn, account, currency, amount, append([]opt{cat("轉帳", "轉帳")}, opts...)...)
}

// fixture is a small export that has every type of row there is, over three months,
// in the order of a file: the rows with no date first, then by day.
func fixture() []model.Row {
	const exp, inc = model.TypeExpense, model.TypeIncome
	return []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("", model.TypeInitial, "Bank", "TWD", "5000"),
		row("", model.TypeInitial, "Bank USD", "USD", "200"),
		row("", model.TypeInitial, "Card", "TWD", "0"),
		row("", model.TypeInitial, "Exchange BTC", "BTC", "0.5"),
		row("", model.TypeInitial, "Exchange", "USDT", "300"),

		// January
		row("2026-01-05 10:00", exp, "Card", "TWD", "-120", cat("Food", "Lunch"), name("Noodles"), merchant("Fake Shop"),
			detail("◎ noodles ($100) ✕ 1"), fee("-2"), project("Daily"), tags("family")),
		row("2026-01-05 12:30", exp, "Cash", "TWD", "-100", cat("Food", "Lunch"), discount("20"), fee("-1")),
		row("2026-01-06 09:00", exp, "Card", "TWD", "-100", cat("Food", "Lunch"), name("Coffee"), discount("100")),
		row("2026-01-06 18:00", exp, "Bank USD", "USD", "-10", cat("Food", "Lunch")),
		row("2026-01-07 12:00", exp, "Card", "TWD", "-500", cat("Food", "Lunch"), project("Corp paid"), fee("-5"), name("Team lunch")),
		row("2026-01-10 09:00", inc, "Bank", "TWD", "50000", cat("收入", "薪水")),
		row("2026-01-10 09:05", inc, "Bank", "TWD", "1200", cat("收入", "收款"), name("Someone paid")),
		row("2026-01-11 12:00", inc, "Card", "TWD", "30", cat("收入", "信用卡抵扣")),
		row("2026-01-12 12:00", model.TypeBonus, "Bank USD", "USD", "1.5", cat("紅利回饋", "紅利回饋")),
		row("2026-01-13 12:00", model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), cat("應收款項", "借出"), fee("-5")),
		row("2026-01-13 13:00", model.TypeReceivable, "Cash", "TWD", "100", party("Amy"), cat("應收款項", "借出")),
		row("2026-01-14 12:00", model.TypeRefund, "Card", "TWD", "450", cat("Shop", "Books"), name("Returned")),
		row("2026-01-14 13:00", model.TypeFee, "Bank", "TWD", "-30", cat("手續費", "手續費")),
		row("2026-01-15 10:00", exp, "Cash", "TWD", "-40", cat("Food", "Lunch")),
		row("2026-01-15 12:00", model.TypeAdjustment, "Cash", "TWD", "-20", cat("餘額調整", "餘額調整")),
		row("2026-01-15 18:00", exp, "Cash", "TWD", "-10", cat("Food", "Lunch")),
		out("2026-01-20 12:00", "Bank", "TWD", "-1000", fee("-15")),
		in("2026-01-20 12:00", "Card", "TWD", "1000"),
		out("2026-01-21 12:00", "Bank", "TWD", "-3000"),
		in("2026-01-21 12:00", "Bank USD", "USD", "100"),
		out("2026-01-22 12:00", "Exchange", "USDT", "-100"),
		in("2026-01-22 12:00", "Exchange BTC", "BTC", "0.002"),
		out("2026-01-22 13:00", "Exchange", "USDT", "-50"),
		in("2026-01-22 13:00", "Exchange BTC", "BTC", "0"),
		out("2026-01-25 12:00", "Bank", "TWD", "-100"),
		out("2026-01-25 12:00", "Cash", "TWD", "-250"),
		in("2026-01-25 12:00", "Wallet", "TWD", "250"),
		in("2026-01-25 12:00", "Card", "TWD", "100"),
		row("2026-01-26 12:00", model.TypeAdjustment, "Exchange BTC", "BTC", "0.001", cat("餘額調整", "餘額調整")),
		row("2026-01-27 12:00", model.TypeAdjustment, "Bank USD", "USD", "-2.5", cat("餘額調整", "餘額調整")),

		// February
		row("2026-02-05 23:59", model.TypeBillInstalment, "Card", "TWD", "600", name("Bill instalment"), cat("帳單分期", "帳單分期")),
		row("2026-02-05 23:59", model.TypeBillInstalment, "Card instalments", "TWD", "600", cat("帳單分期", "帳單分期")),
		row("2026-02-05 23:59", model.TypeBillInstalment, "Card instalments", "TWD", "-600", cat("帳單分期", "帳單分期")),
		row("2026-02-10 12:00", exp, "Card", "TWD", "-1056", cat("Self", "投資"), project("Daily")),
		row("2026-02-11 12:00", inc, "Broker", "TWD", "24", cat("收入", "投資")),
		row("2026-02-12 12:00", model.TypeAdjustment, "Broker", "TWD", "500", cat("餘額調整", "餘額調整")),
		row("2026-02-14 12:00", exp, "Wallet JPY", "JPY", "-1000", cat("Food", "Lunch")),
		row("2026-02-15 23:59", model.TypeInterest, "Card instalments", "TWD", "-6", cat("利息支出", "利息支出")),
		row("2026-02-15 23:59", model.TypeInstalmentPayment, "Card instalments", "TWD", "-200", cat("帳單分期", "帳單分期")),
		out("2026-02-20 12:00", "Bank", "TWD", "-500", cat("轉帳", "兌換")),
		in("2026-02-20 12:00", "Wallet", "TWD", "500", cat("轉帳", "兌換")),
		out("2026-02-25 12:00", "Cash", "TWD", "-77"),
		row("2026-02-28 12:00", model.TypeAdjustment, "Cash", "TWD", "0", cat("餘額調整", "餘額調整")),

		// March
		row("2026-03-02 12:00", exp, "Card", "TWD", "-300", cat("Shop", "Books")),
		row("2026-03-05 12:00", inc, "Bank", "TWD", "100", cat("收入", "薪水")),
		row("2026-03-10 12:00", model.TypeAdjustment, "Bank", "TWD", "25", cat("餘額調整", "餘額調整")),
		row("2026-03-15 23:59", model.TypeInterest, "Card instalments", "TWD", "-4", cat("利息支出", "利息支出")),
		row("2026-03-15 23:59", model.TypeInstalmentPayment, "Card instalments", "TWD", "-200", cat("帳單分期", "帳單分期")),
		row("2026-04-15 23:59", model.TypeInterest, "Card instalments", "TWD", "-2", cat("利息支出", "利息支出")),
		row("2026-04-15 23:59", model.TypeInstalmentPayment, "Card instalments", "TWD", "-200", cat("帳單分期", "帳單分期")),
	}
}

// fixtureMapping is the mapping of the user, who has said which MOZE accounts are one
// account of the ledger and left the rest to the guesses.
func fixtureMapping() model.Mapping {
	return model.Mapping{
		Accounts: map[string]model.AccountMap{
			"Bank USD":         {Name: "Bank", Kind: "bank", Institution: "FakeBank"},
			"Exchange BTC":     {Name: "Exchange", Kind: "exchange"},
			"Wallet JPY":       {Name: "Wallet", Kind: "cash"},
			"Wallet":           {Name: "Wallet", Kind: "cash"},
			"Card instalments": {Name: "Card", Kind: "card", Institution: "FakeBank", ClosingDay: 5, PaymentDueDay: 20},
			"Card":             {Name: "Card", Kind: "card", Institution: "FakeBank", ClosingDay: 5, PaymentDueDay: 20},
			"Bank":             {Name: "Bank", Kind: "bank", Institution: "FakeBank"},
		},
		Projects: map[string]model.ProjectMap{
			"Corp paid": {Kind: model.ProjectDealings, Party: "Corp"},
		},
	}
}

func fixtureRates() model.Rates {
	return model.NewRates(map[string][]model.Rate{
		"USD": {{Date: day("2026-01-01"), PerUnit: d("30")}},
		"JPY": {{Date: day("2026-01-01"), PerUnit: d("0.2")}},
	})
}

// world is the import put together on fakes.
type world struct {
	t       *testing.T
	ledger  *fakeLedger
	mapping *fakeMapping
	planner *moze.Planner
	imp     *moze.Importer
	files   usecase.Files
}

// newWorld makes an import of the rows and the mapping on an empty ledger. A zone
// 8 hours ahead of UTC is where the user lives.
func newWorld(t *testing.T, rows []model.Row, m model.Mapping) *world {
	t.Helper()
	l := newFakeLedger()
	fm := &fakeMapping{loaded: mapping.Loaded{Mapping: m, Exists: true}}
	inputs := moze.Inputs{Source: fakeSource{rows: rows}, Mapping: fm, Rates: fakeRates{fixtureRates()}}
	return &world{
		t:       t,
		ledger:  l,
		mapping: fm,
		planner: moze.NewPlanner(inputs),
		imp:     moze.NewImporter(inputs, l.ledger(), time.FixedZone("UTC+8", 8*3600)),
		files:   usecase.Files{Export: "export.csv", Mapping: "mapping.yaml", Rates: "rates.csv"},
	}
}

// reviewed plans once, so that the mapping has the guesses written in it, as the user
// has when they have looked at it and run plan again.
func (w *world) reviewed() *world {
	w.t.Helper()
	if _, err := w.planner.Plan(context.Background(), usecase.PlanInput{Files: w.files}); err != nil {
		w.t.Fatalf("Plan: %v", err)
	}
	return w
}

func (w *world) apply() usecase.ApplyOutput {
	w.t.Helper()
	out, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files})
	if err != nil {
		w.t.Fatalf("Apply: %v", err)
	}
	return out
}

func (w *world) confirm(month string) (usecase.ConfirmOutput, error) {
	w.t.Helper()
	return w.imp.Confirm(context.Background(), usecase.ConfirmInput{Files: w.files, Month: month})
}

func (w *world) mustConfirm(month string) usecase.ConfirmOutput {
	w.t.Helper()
	out, err := w.confirm(month)
	if err != nil {
		w.t.Fatalf("Confirm(%s): %v", month, err)
	}
	return out
}

func (w *world) plan() model.Plan {
	w.t.Helper()
	out, err := w.planner.Plan(context.Background(), usecase.PlanInput{Files: w.files})
	if err != nil {
		w.t.Fatalf("Plan: %v", err)
	}
	return out.Plan
}
