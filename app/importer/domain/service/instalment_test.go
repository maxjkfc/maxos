package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// instalmentRows are the rows MOZE writes for a bill of 600 converted into 3
// instalments of 200 each, with interest of 6, 4 and 2: three 帳單分期 rows in one
// minute, and for each period a 分期還款 and a 利息 row.
func instalmentRows(origin, periods string) []model.Row {
	bill := func(account, amount string) model.Row {
		return row(model.TypeBillInstalment, account, "TWD", amount, on("2026-01-05"), at("23:59"), name("Bill instalment"), main_("帳單分期", "帳單分期"))
	}
	rows := []model.Row{bill(origin, "600"), bill(periods, "600"), bill(periods, "-600")}
	for _, p := range []struct{ day, interest string }{{"2026-02-15", "-6"}, {"2026-03-15", "-4"}, {"2026-04-15", "-2"}} {
		rows = append(rows,
			row(model.TypeInterest, periods, "TWD", p.interest, on(p.day), at("23:59"), main_("利息支出", "利息支出")),
			row(model.TypeInstalmentPayment, periods, "TWD", "-200", on(p.day), at("23:59"), main_("帳單分期", "帳單分期")),
		)
	}
	return rows
}

func TestBillInstalmentThatReconcilesIsOnePlanOfItsPeriods(t *testing.T) {
	// "Bank" and "Bank USD" are one account of the ledger.
	p := plan(instalmentRows("Bank", "Bank USD")...)
	u := unitOf(t, p)
	if u.Kind != model.KindBillInstalment {
		t.Fatalf("kind = %s, want a plan; skipped %+v", u.Kind, p.Skipped)
	}
	if !isLeg(u.Main, "Bank", "TWD", "600") || !u.Date.Equal(day("2026-01-05")) || u.Clock != "23:59" {
		t.Errorf("plan = %+v, want 600 of Bank on 2026-01-05", u.Main)
	}
	var days []string
	var principals, interests []string
	for _, period := range u.Periods {
		days = append(days, period.DueOn.Format("2006-01-02"))
		principals = append(principals, period.Principal.String())
		interests = append(interests, period.Interest.String())
	}
	if !reflect.DeepEqual(days, []string{"2026-02-15", "2026-03-15", "2026-04-15"}) ||
		!reflect.DeepEqual(principals, []string{"200", "200", "200"}) ||
		!reflect.DeepEqual(interests, []string{"6", "4", "2"}) {
		t.Errorf("periods = %v %v %v", days, principals, interests)
	}
	if u.InterestCategory != "Interest" {
		t.Errorf("interest category = %q", u.InterestCategory)
	}
	if len(u.Rows) != 9 {
		t.Errorf("the plan is made of %d rows, want all 9", len(u.Rows))
	}
	if len(p.Skipped) != 0 {
		t.Errorf("skipped = %+v, want none: every row of the conversion is in the plan", p.Skipped)
	}
}

func TestBillInstalmentWhoseRowsAreInTwoAccountsIsLeftOutAndSaysSo(t *testing.T) {
	p := plan(instalmentRows("Bank", "Card")...)
	if len(p.Units) != 0 {
		t.Fatalf("units = %+v, want none", p.Units)
	}
	if len(p.Skipped) != 1 || p.Skipped[0].Reason != model.ReasonInstalmentAccounts || len(p.Skipped[0].Rows) != 9 {
		t.Fatalf("skipped = %+v, want the 9 rows for their accounts", p.Skipped)
	}
	if got := p.Skipped[0].Args; !reflect.DeepEqual(got, []string{"Bank", "Card"}) {
		t.Errorf("args = %q, want the accounts to be named, one each", got)
	}
}

func TestBillInstalmentThatDoesNotReconcileIsLeftOutAndTheInterestIsAnExpense(t *testing.T) {
	rows := instalmentRows("Bank", "Bank USD")
	// One payment of 200 is missing from the file: 400 is paid back of 600.
	kept := rows[:0:0]
	for _, r := range rows {
		if r.Type == model.TypeInstalmentPayment && r.Date.Equal(day("2026-04-15")) {
			continue
		}
		kept = append(kept, r)
	}
	p := plan(kept...)
	for _, u := range p.Units {
		if u.Kind == model.KindBillInstalment {
			t.Fatalf("a plan was made of rows that do not add up: %+v", u)
		}
	}
	reasons := skippedReasons(p)
	if len(reasons) == 0 || reasons[0] != model.ReasonInstalmentUnmatched {
		t.Errorf("skipped = %v, want the unmatched rows", reasons)
	}
	// The three interest rows are what they are: expenses of the interest category.
	expenses := 0
	for _, u := range p.Units {
		if u.Kind == model.KindExpense && u.Lines[0].Category == "Interest" {
			expenses++
		}
	}
	if expenses != 3 {
		t.Errorf("interest expenses = %d, want 3", expenses)
	}
}

func TestPaymentsNothingConvertedAreLeftOut(t *testing.T) {
	p := plan(row(model.TypeInstalmentPayment, "Card", "TWD", "-200", main_("帳單分期", "帳單分期")))
	if got := skippedReasons(p); !reflect.DeepEqual(got, []model.Reason{model.ReasonInstalmentUnmatched}) {
		t.Errorf("skipped = %v", got)
	}
}

func TestGuessPutsTheRowsOfOneConversionInOneAccount(t *testing.T) {
	// The user has mapped "Bank"; the account of the payments is new, and the
	// guess for it is the account the bill was converted in.
	rows := rowsOf(instalmentRows("Bank", "Pay account")...)
	user := model.Mapping{Accounts: map[string]model.AccountMap{"Bank": {Name: "My bank", Kind: "bank", Institution: "FakeBank"}}}
	p := serviceBuild(rows, user)
	if got := p.Guessed.Accounts["Pay account"]; got.Name != "My bank" {
		t.Errorf("guessed account = %+v, want the one of the conversion", got)
	}
	if _, again := p.Guessed.Accounts["Bank"]; again {
		t.Error("an account the user has mapped is guessed again")
	}
	var plans int
	for _, u := range p.Units {
		if u.Kind == model.KindBillInstalment {
			plans++
		}
	}
	if plans != 1 {
		t.Errorf("plans = %d, want the conversion to be recognised with the guess", plans)
	}
}
