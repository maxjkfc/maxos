package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/domain/service"
)

func TestGuessAccountFromItsName(t *testing.T) {
	for _, tt := range []struct {
		name        string
		kind        string
		institution string
	}{
		{"甲Cube 信用卡", "card", "甲"},
		{"乙J卡", "card", "乙"},
		{"Acme Bank 主帳戶", "bank", "Acme"},
		{"丙銀行活期帳戶", "bank", "丙"},
		{"丁證券戶", "brokerage", "丁"},
		{"示範現金", "cash", ""},
		{"備用錢包", "cash", ""},
		{"FakeWallet USDT", "wallet", ""},
		{"Something Odd", "bank", "Something"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := service.GuessAccount(tt.name)
			if got.Kind != tt.kind || got.Institution != tt.institution || got.Name != tt.name {
				t.Errorf("GuessAccount(%q) = %+v, want kind %s, institution %q, and its own name", tt.name, got, tt.kind, tt.institution)
			}
		})
	}
}

func TestGuessInstrument(t *testing.T) {
	for code, want := range map[string]model.InstrumentMap{
		"USD":  {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
		"JPY":  {Name: "日圓", Kind: "fiat", IsCurrency: true, Decimals: 0},
		"USDT": {Name: "Tether USDT", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
		"ETH":  {Name: "ETH", Kind: "crypto", IsCurrency: false, Decimals: 8},
	} {
		if got := service.GuessInstrument(code); got != want {
			t.Errorf("GuessInstrument(%q) = %+v, want %+v", code, got, want)
		}
	}
}

func TestCleanNameDropsDatesSymbolsAndTheWordProject(t *testing.T) {
	for in, want := range map[string]string{
		"2025-04  甲地之旅":   "甲地之旅",
		"2026-09-21 乙地之旅": "乙地之旅",
		"2025丙地生日之旅":      "丙地生日之旅",
		"夥伴💙":             "夥伴",
		"家人手術專案":          "家人手術",
		"生活開銷":            "生活開銷",
		"2025":            "2025",
	} {
		if got := service.CleanName(in); got != want {
			t.Errorf("CleanName(%q) = %q, want %q", in, got, want)
		}
	}
}

// guessedProjects builds a plan of an empty mapping for the rows of the projects and
// returns what the guess made of each.
func guessedProjects(t *testing.T, byProject map[string][]string) map[string]model.ProjectMap {
	t.Helper()
	var rows []model.Row
	for value, days := range byProject {
		for _, dd := range days {
			rows = append(rows, row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch"), project(value), on(dd)))
		}
	}
	return serviceBuild(rowsOf(rows...), model.Mapping{}).Guessed.Projects
}

func TestGuessProjects(t *testing.T) {
	trip := []string{"2026-04-13", "2026-04-15", "2026-04-16", "2026-04-17", "2026-04-17", "2026-04-18", "2026-04-19", "2026-04-20", "2026-04-21", "2026-04-22"}
	got := guessedProjects(t, map[string][]string{
		"2025-04  甲地之旅": append([]string{"2026-03-20"}, trip...), // 9 of its 11 rows are in the run; one is days before it and one long before
		"生活開銷":          {"2026-01-01", "2026-05-01"},
		"公司代付":          {"2026-02-02"},
		"募資商品":          {"2026-02-02"},
		"家人手術專案":        {"2026-02-14", "2026-02-26", "2026-03-02", "2026-03-04", "2026-03-05"},
		"玩樂":            {"2026-01-01"},
		"旅遊":            {"2026-02-02", "2026-08-07"},
	})

	if p := got["2025-04  甲地之旅"]; p.Kind != model.ProjectProject || p.Name != "甲地之旅" ||
		!p.ActiveFrom.Equal(day("2026-04-15")) || !p.ActiveTo.Equal(day("2026-04-22")) {
		t.Errorf("trip = %+v, want a project with the run of days as its active period", p)
	}
	if p := got["家人手術專案"]; p.Kind != model.ProjectProject || !p.ActiveFrom.IsZero() {
		t.Errorf("surgery = %+v, want a project with no active period: its rows are not a run of days", p)
	}
	if p := got["生活開銷"]; p.Kind != model.ProjectArea || p.Name != "生活開銷" {
		t.Errorf("daily = %+v, want an area", p)
	}
	if p := got["公司代付"]; p.Kind != model.ProjectDealings || p.Party != "公司" {
		t.Errorf("company paid = %+v, want a dealings project of 公司", p)
	}
	if p := got["募資商品"]; p.Kind != model.ProjectTag {
		t.Errorf("crowdfunding = %+v, want a tag", p)
	}
	if p := got["旅遊"]; p.Kind != model.ProjectArea {
		t.Errorf("travel = %+v, want an area: it is no trip", p)
	}
}

func TestGuessDefaultAreaOnlyWhenTheMappingHasTheArea(t *testing.T) {
	days := []string{"2026-04-15", "2026-04-16", "2026-04-17"}
	with := guessedProjects(t, map[string][]string{"2025-04 甲地之旅": days, "玩樂": {"2026-01-01"}})
	if got := with["2025-04 甲地之旅"].DefaultArea; got != "玩樂" {
		t.Errorf("default area = %q, want 玩樂", got)
	}
	without := guessedProjects(t, map[string][]string{"2025-04 甲地之旅": days})
	if got := without["2025-04 甲地之旅"].DefaultArea; got != "" {
		t.Errorf("default area = %q, want none for an area the mapping does not have", got)
	}
}

func TestGuessCategories(t *testing.T) {
	p := serviceBuild(rowsOf(
		row(model.TypeExpense, "Cash", "TWD", "-1", main_("飲食", "早餐")),
		row(model.TypeExpense, "Cash", "TWD", "-1", main_("其他", "其他")),
		row(model.TypeIncome, "Cash", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "Cash", "TWD", "1", main_("收入", "收款")),
		row(model.TypeIncome, "Cash", "TWD", "1", main_("收入", "信用卡抵扣")),
		row(model.TypeIncome, "Cash", "TWD", "1", main_("收入", "公司代收款")),
		row(model.TypeIncome, "Cash", "TWD", "1", main_("副業", "接案")),
		row(model.TypeInterest, "Cash", "TWD", "-1", main_("利息支出", "利息支出")),
	), model.Mapping{})

	if got := p.Guessed.Expense["飲食/早餐"].Category; got != "飲食/早餐" {
		t.Errorf("expense = %q, want two levels", got)
	}
	if got := p.Guessed.Expense["其他/其他"].Category; got != "其他" {
		t.Errorf("expense of one word = %q, want one level", got)
	}
	wantIncome := map[string]model.CategoryMap{
		"收入/薪水":    {Category: "薪水"},
		"收入/收款":    {Category: "收款（待確認）", Tag: "待確認"},
		"收入/信用卡抵扣": {Treatment: model.TreatmentReward},
		"收入/公司代收款": {Treatment: model.TreatmentDealing, Party: "公司"},
		"副業/接案":    {Category: "副業/接案"},
	}
	if !reflect.DeepEqual(p.Guessed.Income, wantIncome) {
		t.Errorf("income = %+v, want %+v", p.Guessed.Income, wantIncome)
	}
	if got := p.Guessed.Defaults; got != (model.Defaults{FeeCategory: "手續費", InterestCategory: "利息支出", RefundCategory: "退款"}) {
		t.Errorf("defaults = %+v", got)
	}
}

func TestGuessKeepsToWhatTheMappingLacks(t *testing.T) {
	user := model.Mapping{
		Accounts: map[string]model.AccountMap{"Cash": {Name: "Wallet", Kind: "cash"}},
		Defaults: model.Defaults{FeeCategory: "My fees"},
	}
	p := serviceBuild(rowsOf(row(model.TypeExpense, "Cash", "TWD", "-1", main_("Food", "Lunch"))), user)
	if _, guessedAgain := p.Guessed.Accounts["Cash"]; guessedAgain {
		t.Error("an account of the user's mapping is guessed")
	}
	if p.Mapping.Accounts["Cash"].Name != "Wallet" || p.Mapping.Defaults.FeeCategory != "My fees" {
		t.Errorf("mapping = %+v, want the user's entries to stand", p.Mapping)
	}
	if p.Guessed.Defaults.FeeCategory != "" || p.Guessed.Defaults.RefundCategory == "" {
		t.Errorf("guessed defaults = %+v, want only the ones the user has not set", p.Guessed.Defaults)
	}
}

func TestGuessAccountThatHoldsOnlyCoinsIsAnExchange(t *testing.T) {
	rows := rowsOf(
		row(model.TypeIncome, "測試ETH", "ETH", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "測試USDT", "USDT", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "測試現金", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "測試美金", "USD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "混合ETH", "ETH", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "混合ETH", "TWD", "1", main_("收入", "薪水")),
	)
	got := serviceBuild(rows, model.Mapping{}).Guessed.Accounts
	for name, want := range map[string]string{
		"測試ETH": "exchange", "測試USDT": "exchange", "測試現金": "cash", "測試美金": "bank", "混合ETH": "bank",
	} {
		if got[name].Kind != want {
			t.Errorf("%s = %s, want %s: an account of coins alone, with nothing in its name, is an exchange", name, got[name].Kind, want)
		}
	}
}

func TestGuessInstitutionIsSharedByAccountsThatStartAlike(t *testing.T) {
	rows := rowsOf(
		row(model.TypeIncome, "測試銀行活期帳戶", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "測試Cube 信用卡", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "測試大戶信用卡", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "獨立銀行", "TWD", "1", main_("收入", "薪水")),
		row(model.TypeIncome, "現金", "TWD", "1", main_("收入", "薪水")),
	)
	got := serviceBuild(rows, model.Mapping{}).Guessed.Accounts
	for _, name := range []string{"測試銀行活期帳戶", "測試Cube 信用卡", "測試大戶信用卡"} {
		if got[name].Institution != "測試" {
			t.Errorf("%s institution = %q, want 測試: its accounts begin alike", name, got[name].Institution)
		}
	}
	if got["獨立銀行"].Institution != "獨立" || got["現金"].Institution != "" {
		t.Errorf("a lone account keeps its own guess: %+v, %+v", got["獨立銀行"], got["現金"])
	}
}
