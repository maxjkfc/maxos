package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func TestExpectedBalancesAreWhatTheFileSaysAtTheEndOfEachMonth(t *testing.T) {
	p := plan(
		row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01")),
		row(model.TypeExpense, "Cash", "TWD", "-100", main_("Food", "Lunch"), on("2026-01-20"), fee("-5"), discount("10")),
		row(model.TypeIncome, "Cash", "TWD", "300", main_("收入", "薪水"), on("2026-02-03")),
		row(model.TypeAdjustment, "Cash", "TWD", "-20", on("2026-02-20")),
		row(model.TypeAdjustment, "Bank USD", "USD", "7", on("2026-03-02")),
		out("Cash", "TWD", "-50", on("2026-03-04")),
		in("Bank", "TWD", "50", on("2026-03-04")),
	)

	if got, want := p.Months(), []string{"2026-01", "2026-02", "2026-03"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("months = %v, want %v", got, want)
	}
	for _, tt := range []struct {
		month string
		want  map[model.Pair]string
	}{
		// The opening is the day before the first row, in January; the expense is
		// -100 + 10 (not paid) - 5 (fee).
		{"2026-01", map[model.Pair]string{{Account: "Cash", Instrument: "TWD"}: "905"}},
		{"2026-02", map[model.Pair]string{{Account: "Cash", Instrument: "TWD"}: "1185"}},
		{"2026-03", map[model.Pair]string{
			{Account: "Cash", Instrument: "TWD"}: "1135",
			{Account: "Bank", Instrument: "TWD"}: "50",
			{Account: "Bank", Instrument: "USD"}: "7",
		}},
	} {
		end, err := model.MonthEnd(tt.month)
		if err != nil {
			t.Fatal(err)
		}
		got := map[model.Pair]string{}
		for pair, q := range p.ExpectedAt(end) {
			got[pair] = q.String()
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: balances = %v, want %v", tt.month, got, tt.want)
		}
	}
}

func TestOpeningBalanceCountsFromItsOwnDayNotFromTheDayOfAnyRow(t *testing.T) {
	p := plan(
		row(model.TypeInitial, "Cash", "TWD", "500", on("0001-01-01")),
		row(model.TypeExpense, "Cash", "TWD", "-100", main_("Food", "Lunch"), on("2026-03-02")),
	)
	if got := p.ExpectedAt(day("2026-03-01"))[model.Pair{Account: "Cash", Instrument: "TWD"}]; !got.Equal(d("500")) {
		t.Errorf("balance on the day of the opening = %s, want 500", got)
	}
	if got := p.ExpectedAt(day("2026-02-28"))[model.Pair{Account: "Cash", Instrument: "TWD"}]; !got.IsZero() {
		t.Errorf("balance before the opening = %s, want nothing", got)
	}
}

func TestUnrecordedAreTheEffectsOfTheRowsThatAreLeftOut(t *testing.T) {
	p := plan(
		out("Bank", "TWD", "-100", fee("-5")),
		row(model.TypeExpense, "Bank", "TWD", "-30", main_("Food", "Lunch")),
	)
	// The 轉出 has no 轉入: it is left out, with its fee, and the expense is recorded.
	pair := model.Pair{Account: "Bank", Instrument: "TWD"}
	end := day("2026-03-31")
	if got := p.ExpectedAt(end)[pair]; !got.Equal(d("-135")) {
		t.Errorf("expected = %s, want -135", got)
	}
	if got := p.UnrecordedAt(end)[pair]; !got.Equal(d("-105")) {
		t.Errorf("unrecorded = %s, want -105: the money and the fee of the left out row", got)
	}
}

func TestRowsOfOneLedgerAccountAddUp(t *testing.T) {
	p := plan(
		row(model.TypeIncome, "Bank", "TWD", "100", main_("收入", "薪水")),
		row(model.TypeIncome, "Bank USD", "TWD", "20", main_("收入", "薪水")),
	)
	if got := p.ExpectedAt(day("2026-03-31"))[model.Pair{Account: "Bank", Instrument: "TWD"}]; !got.Equal(d("120")) {
		t.Errorf("balance = %s, want 120: two accounts of MOZE are one of the ledger", got)
	}
}

func TestMonthsRunFromTheFirstRowToTheLastWithoutGaps(t *testing.T) {
	p := plan(
		row(model.TypeIncome, "Bank", "TWD", "1", main_("收入", "薪水"), on("2025-11-30")),
		row(model.TypeIncome, "Bank", "TWD", "1", main_("收入", "薪水"), on("2026-02-01")),
	)
	if got, want := p.Months(), []string{"2025-11", "2025-12", "2026-01", "2026-02"}; !reflect.DeepEqual(got, want) {
		t.Errorf("months = %v, want %v", got, want)
	}
}
