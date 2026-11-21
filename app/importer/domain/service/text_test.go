package service_test

import (
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// marked gives a row the text that the tests look for: a name, a shop, a description,
// a 專案 and a tag, each ending in the mark.
func marked(mark string) rowOpt {
	return func(r *model.Row) {
		r.Name, r.Merchant, r.Description = "N-name"+mark, "M-shop"+mark, "D-desc"+mark
		r.Project, r.Tags = "P-proj"+mark, []string{"T-tag" + mark}
	}
}

// textOf is every place of a plan where the text of a row can go: the descriptions, the
// merchant, the area, the project and the tags of the units and of the adjustments, the
// memos of the lines and of the fees, and the description of a reward.
func textOf(p model.Plan) string {
	var parts []string
	for _, u := range p.Units {
		parts = append(parts, u.Description, u.Merchant, u.Area, u.Project)
		parts = append(parts, u.Tags...)
		for _, l := range u.Lines {
			parts = append(parts, l.Memo)
		}
		if u.Reward != nil {
			parts = append(parts, u.Reward.Description)
		}
		for _, f := range u.Fees {
			parts = append(parts, f.Memo)
		}
	}
	for _, a := range p.Adjustments {
		parts = append(parts, a.Description, a.Merchant, a.Area, a.Project)
		parts = append(parts, a.Tags...)
	}
	return strings.Join(parts, "\n")
}

func TestNothingOfARowsTextIsDropped(t *testing.T) {
	// The first row of a unit is marked 1, the second 2, and so on: each says its own
	// name, shop, description, 專案 and tag, and every one of them is to be found.
	instalment := instalmentRows("Bank", "Bank USD")
	for i := range instalment {
		marked(strconv.Itoa(i + 1))(&instalment[i])
	}
	for _, tt := range []struct {
		name string
		rows []model.Row
	}{
		{"an expense", []model.Row{row(model.TypeExpense, "Card", "TWD", "-120", main_("Food", "Lunch"), fee("-2"), marked("1"))}},
		{"an expense paid by its discount", []model.Row{row(model.TypeExpense, "Card", "TWD", "-100", main_("Food", "Lunch"), discount("100"), marked("1"))}},
		{"what was paid for a party", []model.Row{row(model.TypeExpense, "Card", "TWD", "-500", main_("Food", "Dinner"), discount("50"), marked("1"))}},
		{"an income", []model.Row{row(model.TypeIncome, "Bank", "TWD", "500", main_("收入", "薪水"), marked("1"))}},
		{"an income that is a reward", []model.Row{row(model.TypeIncome, "Card", "TWD", "30", main_("收入", "信用卡抵扣"), marked("1"))}},
		{"an income that a party gives back", []model.Row{row(model.TypeIncome, "Bank", "TWD", "800", main_("收入", "公司代收款"), marked("1"))}},
		{"a bonus", []model.Row{row(model.TypeBonus, "Bank USD", "USD", "1.5", main_("紅利回饋", "紅利回饋"), marked("1"))}},
		{"a receivable", []model.Row{row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), marked("1"))}},
		{"a refund", []model.Row{row(model.TypeRefund, "Card", "TWD", "450", main_("Shop", "Books"), marked("1"))}},
		{"a row that is a fee", []model.Row{row(model.TypeFee, "Bank", "TWD", "-30", main_("手續費", "手續費"), marked("1"))}},
		{"an interest", []model.Row{row(model.TypeInterest, "Card", "TWD", "-12", main_("利息支出", "利息支出"), marked("1"))}},
		{"an opening balance", []model.Row{
			row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), marked("1")),
			row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch")),
		}},
		{"an adjustment", []model.Row{row(model.TypeAdjustment, "Cash", "TWD", "40", marked("1"))}},
		{"a transfer", []model.Row{out("Bank", "TWD", "-100", marked("1")), in("Card", "TWD", "100", marked("2"))}},
		{"an exchange", []model.Row{out("Bank", "TWD", "-3000", marked("1")), in("Bank USD", "USD", "100", marked("2"))}},
		{"a trade", []model.Row{out("Exchange", "USDT", "-100", marked("1")), in("Exchange BTC", "BTC", "0.002", marked("2"))}},
		{"an instalment plan", instalment},
	} {
		for _, kind := range []string{model.ProjectProject, model.ProjectArea, model.ProjectTag} {
			t.Run(tt.name+" with a 專案 that is a "+kind, func(t *testing.T) {
				m := testMapping()
				m.Expense["Food/Dinner"] = model.CategoryMap{Treatment: model.TreatmentDealing, Party: "Corp"}
				rows := rowsOf(tt.rows...)
				for _, r := range rows {
					if r.Project != "" {
						m.Projects[r.Project] = model.ProjectMap{Kind: kind, Name: r.Project}
					}
				}
				p := serviceBuild(rows, m)
				if len(p.Skipped) != 0 || len(p.Problems) != 0 {
					t.Fatalf("the rows are made to be recorded: skipped %+v, problems %+v", p.Skipped, p.Problems)
				}
				text := textOf(p)
				for _, r := range rows {
					for _, want := range append([]string{r.Name, r.Merchant, r.Description, r.Project}, r.Tags...) {
						if want != "" && !strings.Contains(text, want) {
							t.Errorf("%q is nowhere in what the plan says: %q", want, text)
						}
					}
				}
			})
		}
	}
}

func TestPairTakesTheMerchantAndProjectOfTheRowThatLeftAndSaysTheOthers(t *testing.T) {
	m := testMapping()
	m.Projects["P-proj1"] = model.ProjectMap{Kind: model.ProjectProject, Name: "P-proj1"}
	m.Projects["P-proj2"] = model.ProjectMap{Kind: model.ProjectProject, Name: "P-proj2"}

	// Each row has its own: the first is the unit's, the other is a line of its description.
	p := serviceBuild(rowsOf(out("Bank", "TWD", "-100", marked("1")), in("Card", "TWD", "100", marked("2"))), m)
	u := unitOf(t, p)
	if u.Merchant != "M-shop1" || u.Project != "P-proj1" || u.Area != "" {
		t.Errorf("merchant = %q, project = %q, area = %q, want those of the 轉出", u.Merchant, u.Project, u.Area)
	}
	if !reflect.DeepEqual(u.Tags, []string{"T-tag1", "T-tag2"}) {
		t.Errorf("tags = %v, want the tags of both rows", u.Tags)
	}
	for _, line := range []string{"商家：M-shop2", "專案：P-proj2"} {
		if !slices.Contains(strings.Split(u.Description, "\n"), line) {
			t.Errorf("description = %q, want the line %q: the 轉入 says another", u.Description, line)
		}
	}

	// Only the 轉入 has them: they are the unit's, and there is nothing to say.
	rows := rowsOf(out("Bank", "TWD", "-100"), in("Card", "TWD", "100", marked("2")))
	p = serviceBuild(rows, m)
	u = unitOf(t, p)
	if u.Merchant != "M-shop2" || u.Project != "P-proj2" {
		t.Errorf("merchant = %q, project = %q, want those of the 轉入 when the 轉出 has none", u.Merchant, u.Project)
	}
	if strings.Contains(u.Description, "商家：") || strings.Contains(u.Description, "專案：") {
		t.Errorf("description = %q, want no line for what the rows do not say differently", u.Description)
	}

	// Both say the same: there is nothing to say either.
	same := marked("1")
	o, i := out("Bank", "TWD", "-100", same), in("Card", "TWD", "100", same)
	p = serviceBuild(rowsOf(o, i), m)
	if u = unitOf(t, p); strings.Contains(u.Description, "商家：") || strings.Contains(u.Description, "專案：") {
		t.Errorf("description = %q, want no line for what both rows say alike", u.Description)
	}
}

func TestInstalmentPlanKeepsTheTextOfAllItsRowsOnceEach(t *testing.T) {
	rows := instalmentRows("Bank", "Bank USD")
	var pay, interest int
	for i, r := range rows {
		switch r.Type {
		case model.TypeInstalmentPayment:
			pay++
			rows[i].Name = "Pay " + strconv.Itoa(pay)
		case model.TypeInterest:
			interest++
			rows[i].Name = "Int " + strconv.Itoa(interest)
			if interest == 1 {
				rows[i].Description = "D-note"
				rows[i].Merchant = "M-shop"
			}
		}
	}
	for i := range rows[:3] {
		rows[i].Project, rows[i].Tags = "Trip", []string{"family"}
	}
	p := plan(rows...)
	u := unitOf(t, p)
	// The name the three rows of the conversion have is said once, then the rows of the
	// periods, and the category last.
	if want := "Bill instalment\nPay 1\nPay 2\nPay 3\nInt 1\nD-note\nInt 2\nInt 3\n〔帳單分期〕"; u.Description != want {
		t.Errorf("description = %q, want %q", u.Description, want)
	}
	if u.Merchant != "M-shop" {
		t.Errorf("merchant = %q, want the first that any row has", u.Merchant)
	}
	if u.Project != "Trip" || !reflect.DeepEqual(u.Tags, []string{"Family"}) {
		t.Errorf("project = %q, tags = %v, want those of the rows of the conversion", u.Project, u.Tags)
	}
}

func TestAdjustmentIsDescribedAsAReconciliationWithWhatItsRowSays(t *testing.T) {
	m := testMapping()
	m.Projects["P-proj1"] = model.ProjectMap{Kind: model.ProjectArea, Name: "P-proj1"}
	p := serviceBuild(rowsOf(
		row(model.TypeAdjustment, "Cash", "TWD", "40", name("fix"), detail("by hand"), merchant("M-shop"), project("P-proj1"), tags("family")),
		row(model.TypeAdjustment, "Cash", "TWD", "10", on("2026-03-11")),
	), m)
	if len(p.Adjustments) != 2 {
		t.Fatalf("adjustments = %+v, want two", p.Adjustments)
	}
	a := p.Adjustments[0]
	if want := "對帳（MOZE 的餘額調整）\nfix\nby hand"; a.Description != want {
		t.Errorf("description = %q, want %q: the source is quoted, then what the row says, a line each", a.Description, want)
	}
	if a.Merchant != "M-shop" || a.Area != "P-proj1" || a.Project != "" || !reflect.DeepEqual(a.Tags, []string{"Family"}) {
		t.Errorf("merchant = %q, area = %q, project = %q, tags = %v", a.Merchant, a.Area, a.Project, a.Tags)
	}
	if want := "對帳（MOZE 的餘額調整）"; p.Adjustments[1].Description != want {
		t.Errorf("description = %q, want the source alone for a row that says nothing: %q", p.Adjustments[1].Description, want)
	}
}

func TestOpeningBalanceKeepsWhatItsRowSays(t *testing.T) {
	m := testMapping()
	m.Projects["P-proj1"] = model.ProjectMap{Kind: model.ProjectProject, Name: "P-proj1"}
	p := serviceBuild(rowsOf(
		row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), name("Start"), detail("counted by hand"), merchant("M-shop"), project("P-proj1"), tags("family")),
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch")),
	), m)
	var opening model.Unit
	for _, u := range p.Units {
		if u.Kind == model.KindOpening {
			opening = u
		}
	}
	if want := "期初金額\nStart\ncounted by hand"; opening.Description != want {
		t.Errorf("description = %q, want %q", opening.Description, want)
	}
	if opening.Merchant != "M-shop" || opening.Project != "P-proj1" || !reflect.DeepEqual(opening.Tags, []string{"Family"}) {
		t.Errorf("merchant = %q, project = %q, tags = %v", opening.Merchant, opening.Project, opening.Tags)
	}
}

func TestMerchantOfAReceivableAndOfWhatAPartyGivesBackIsKept(t *testing.T) {
	p := plan(
		row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), merchant("Fake Shop")),
		row(model.TypeIncome, "Bank", "TWD", "800", main_("收入", "公司代收款"), merchant("Fake Shop"), on("2026-03-11")),
	)
	if len(p.Units) != 2 || p.Units[0].Merchant != "Fake Shop" || p.Units[1].Merchant != "Fake Shop" {
		t.Errorf("units = %+v, want both to have the merchant", p.Units)
	}
}

func TestProjectOfAPartyIsInTheDescriptionWhereTheUnitIsNotADealingOfThatParty(t *testing.T) {
	rows := rowsOf(
		row(model.TypeBonus, "Card", "TWD", "5", project("Corp paid")),
		row(model.TypeExpense, "Card", "TWD", "-20", main_("Food", "Lunch"), project("Corp paid")),
		row(model.TypeExpense, "Card", "TWD", "-30", main_("Food", "Lunch"), project("Corp paid")),
	)
	m := testMapping()
	// The second is an expense that the user says is not paid for the company.
	m.Rows = map[string]model.RowOverride{rows[1].Key: {Treatment: model.TreatmentExpense, Category: "Food/Lunch"}}
	p := serviceBuild(rows, m)
	if len(p.Units) != 3 {
		t.Fatalf("units = %d, skipped = %+v, want 3", len(p.Units), p.Skipped)
	}
	for _, u := range p.Units {
		has := strings.Contains(u.Description, "專案：Corp paid")
		switch {
		case u.Kind == model.KindDealing && has:
			t.Errorf("a dealing of Corp says %q: the party is the 專案", u.Description)
		case u.Kind != model.KindDealing && !has:
			t.Errorf("%s says %q, want the 專案 that is nowhere else", u.Kind, u.Description)
		}
		if u.Version != u.Fingerprint() {
			t.Errorf("%s has the version %q, but its fingerprint is %q: it was made before the text was final", u.Kind, u.Version, u.Fingerprint())
		}
	}
}
