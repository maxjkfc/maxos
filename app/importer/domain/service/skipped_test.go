package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func TestRowsLeftOutSayTheValuesTheirReasonsFillIn(t *testing.T) {
	for _, tt := range []struct {
		name   string
		rows   []model.Row
		reason model.Reason
		args   []string
	}{
		{"a type it does not know", []model.Row{row(model.RecordType("新類型"), "Cash", "TWD", "5")}, model.ReasonUnknownType, []string{"新類型"}},
		{"decimals of one instrument", []model.Row{row(model.TypeExpense, "Cash", "TWD", "-1.234", main_("Food", "Lunch"))}, model.ReasonTooManyDecimals, []string{"TWD"}},
		{"decimals of two", []model.Row{out("Bank", "TWD", "-3000.123"), in("Bank USD", "USD", "100")}, model.ReasonTooManyDecimals, []string{"TWD", "USD"}},
		{"a coin of no quantity", []model.Row{out("Exchange", "USDT", "-100"), in("Exchange BTC", "BTC", "0")}, model.ReasonZeroQuantity, []string{"BTC"}},
		{"two coins", []model.Row{out("Exchange BTC", "BTC", "-0.5"), in("Cash", "ETH", "4")}, model.ReasonNotCurrencies, []string{"BTC", "ETH"}},
		{"a 轉出 alone", []model.Row{out("Bank", "TWD", "-100")}, model.ReasonUnpaired, []string{"1", "0"}},
		{"a 轉入 alone", []model.Row{in("Bank", "TWD", "100")}, model.ReasonUnpaired, []string{"0", "1"}},
		{"more of one than of the other", []model.Row{out("Bank", "TWD", "-100"), out("Cash", "TWD", "-100"), in("Card", "TWD", "100")}, model.ReasonUnpaired, []string{"2", "1"}},
		{"more than one way to pair them", []model.Row{
			out("Bank", "TWD", "-100"), out("Cash", "TWD", "-100"), in("Card", "TWD", "100"), in("Exchange", "TWD", "100"),
		}, model.ReasonAmbiguousPair, []string{"2", "2"}},
		{"no way to pair them", []model.Row{
			out("Bank", "TWD", "-100"), out("Cash", "TWD", "-300"), in("Card", "TWD", "100"), in("Exchange", "TWD", "999"),
		}, model.ReasonNoPairing, nil},
		{"one account on both sides", []model.Row{out("Bank", "TWD", "-100"), in("Bank USD", "TWD", "100")}, model.ReasonSameAccount, []string{"Bank"}},
		{"amounts that differ", []model.Row{out("Bank", "TWD", "-100"), in("Card", "TWD", "90")}, model.ReasonAmountMismatch, nil},
		{"an instalment in two accounts", instalmentRows("Bank", "Card"), model.ReasonInstalmentAccounts, []string{"Bank", "Card"}},
		{"a payment that nothing converted", []model.Row{row(model.TypeInstalmentPayment, "Card", "TWD", "-200", main_("帳單分期", "帳單分期"))}, model.ReasonInstalmentUnmatched, nil},
		{"no day", []model.Row{row(model.TypeExpense, "Cash", "TWD", "-5", main_("Food", "Lunch"), on("0001-01-01"))}, model.ReasonNoDate, nil},
		{"no day for an opening balance", []model.Row{row(model.TypeInitial, "Elsewhere", "TWD", "50", on("0001-01-01"))}, model.ReasonNoDate, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(tt.rows...)
			if len(p.Skipped) == 0 {
				t.Fatalf("nothing is left out; units = %d", len(p.Units))
			}
			if got := p.Skipped[0]; got.Reason != tt.reason || !reflect.DeepEqual(got.Args, tt.args) {
				t.Errorf("skipped = %s %q, want %s %q", got.Reason, got.Args, tt.reason, tt.args)
			}
		})
	}
}

func TestOverridesThatChangeNothingAreWarnedOfWithTheirCode(t *testing.T) {
	rows := rowsOf(instalmentRows("Bank", "Bank USD")...)
	m := testMapping()
	m.Rows = map[string]model.RowOverride{
		rows[3].Key:     {Treatment: model.TreatmentSkip}, // a row of a plan: it takes no override
		"no-such-row-1": {Treatment: model.TreatmentSkip},
	}
	p := serviceBuild(rows, m)
	// In the order of the keys: the key of a row is hexadecimal, so it comes first.
	want := []model.Warning{
		{Code: model.WarningOverrideUnused, Subject: "rows." + rows[3].Key},
		{Code: model.WarningRowUnknown, Subject: "rows.no-such-row-1"},
	}
	if !reflect.DeepEqual(p.Warnings, want) {
		t.Errorf("warnings = %+v, want %+v", p.Warnings, want)
	}
}
