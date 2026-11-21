package service_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// everyRecordType is a file with every record type the import records, a fee on each
// of them that can carry one, and a discount on the expenses. Everything in it is
// recorded: no row is left out, and nothing in it is wrong.
func everyRecordType() []model.Row {
	rows := []model.Row{
		// 初始金額, dated by the first row of their accounts.
		row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), at("")),
		row(model.TypeInitial, "Card", "TWD", "-800", on("0001-01-01"), at("")),

		// 支出: with a discount, paid in full by the discount, more than paid by it, and
		// for a party.
		row(model.TypeExpense, "Card", "TWD", "-120", main_("Food", "Lunch"), fee("-2"), discount("10"), on("2026-03-01")),
		row(model.TypeExpense, "Card", "TWD", "-100", main_("Food", "Lunch"), fee("-3"), discount("100"), on("2026-03-02")),
		row(model.TypeExpense, "Card", "TWD", "-99", main_("Food", "Lunch"), fee("-1"), discount("100"), on("2026-03-03")),
		row(model.TypeExpense, "Card", "TWD", "-500", main_("Food", "Lunch"), project("Corp paid"), fee("-5"), discount("50"), on("2026-03-04")),

		// 收入: an income, a reward, and what a party gives back; 紅利回饋.
		row(model.TypeIncome, "Bank", "TWD", "50000", main_("收入", "薪水"), fee("-15"), on("2026-03-05")),
		row(model.TypeIncome, "Card", "TWD", "30", main_("收入", "信用卡抵扣"), fee("-1"), on("2026-03-05")),
		row(model.TypeIncome, "Bank", "TWD", "800", main_("收入", "公司代收款"), fee("-2"), on("2026-03-06")),
		row(model.TypeBonus, "Bank USD", "USD", "1.5", main_("紅利回饋", "紅利回饋"), fee("-0.1"), on("2026-03-06")),

		// 應收款項 paid out and received, 退款, and the rows that are fees and interest.
		row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), fee("-5"), on("2026-03-07")),
		row(model.TypeReceivable, "Cash", "TWD", "100", party("Amy"), main_("應收款項", "借出"), fee("-1"), on("2026-03-07"), at("13:00")),
		row(model.TypeRefund, "Card", "TWD", "450", main_("Shop", "Books"), fee("-5"), on("2026-03-08")),
		row(model.TypeFee, "Bank", "TWD", "-30", main_("手續費", "手續費"), fee("-1"), discount("2"), on("2026-03-08")),
		row(model.TypeInterest, "Card", "TWD", "-12", main_("利息支出", "利息支出"), fee("-1"), on("2026-03-08")),

		// 轉出 and 轉入: a transfer, an exchange, a buy and a sell, each with fees.
		out("Bank", "TWD", "-1000", fee("-15"), on("2026-03-09"), at("09:00")),
		in("Card", "TWD", "1000", fee("-1"), on("2026-03-09"), at("09:00")),
		out("Bank", "TWD", "-3000", fee("-10"), on("2026-03-09"), at("10:00")),
		in("Bank USD", "USD", "100", fee("-0.5"), on("2026-03-09"), at("10:00")),
		out("Exchange", "USDT", "-100", fee("-0.1"), on("2026-03-09"), at("11:00")),
		in("Exchange BTC", "BTC", "0.002", on("2026-03-09"), at("11:00")),
		out("Exchange BTC", "BTC", "-0.001", on("2026-03-09"), at("14:00")),
		in("Bank USD", "USD", "50", fee("-0.2"), on("2026-03-09"), at("14:00")),

		// 餘額調整, and one that adds nothing.
		row(model.TypeAdjustment, "Bank USD", "USD", "7", on("2026-03-12")),
		row(model.TypeAdjustment, "Cash", "TWD", "0", on("2026-03-12")),
	}
	// 帳單分期: a conversion with a fee on both of the rows that make it.
	conversion := instalmentRows("Bank", "Bank USD")
	conversion[0].Fee, conversion[1].Fee = d("-4"), d("-2")
	return append(rows, conversion...)
}

// withFee gives the row of the type on the day the fee.
func withFee(t *testing.T, rows []model.Row, typ model.RecordType, onDay, amount string) {
	t.Helper()
	for i, r := range rows {
		if r.Type == typ && r.Date.Equal(day(onDay)) {
			rows[i].Fee = d(amount)
			return
		}
	}
	t.Fatalf("no row of the type %s on %s", typ, onDay)
}

// slot is where an effect falls: an account and an instrument on a day.
type slot struct{ account, instrument, day string }

func slotOf(e model.Effect) slot {
	return slot{e.Account, e.Instrument, e.Date.Format(time.DateOnly)}
}

// differing is what the plan says of the units, worked out again: for every unit, what
// its rows do to the balances less what the unit does, by where it falls, for the units
// where that is not nothing. It is the oracle of the tests that look for a fee or a
// discount that is lost.
func differing(p model.Plan) map[string]map[slot]decimal.Decimal {
	byRow := map[string][]model.Effect{}
	for _, e := range p.Effects {
		byRow[e.Row] = append(byRow[e.Row], e)
	}
	out := map[string]map[slot]decimal.Decimal{}
	for _, u := range p.Units {
		delta := map[slot]decimal.Decimal{}
		for _, k := range u.Rows {
			for _, e := range byRow[k] {
				delta[slotOf(e)] = delta[slotOf(e)].Add(e.Quantity)
			}
		}
		for _, e := range u.Effects() {
			delta[slotOf(e)] = delta[slotOf(e)].Sub(e.Quantity)
		}
		for s, q := range delta {
			if q.IsZero() {
				delete(delta, s)
			}
		}
		if len(delta) > 0 {
			out[u.ID] = delta
		}
	}
	return out
}

// recordedAt is what the ledger will hold at the end of the day when every unit is
// recorded and confirmed and every adjustment is reconciled.
func recordedAt(p model.Plan, end time.Time) model.Balances {
	out := model.Balances{}
	add := func(account, instrument string, on time.Time, quantity decimal.Decimal) {
		if on.After(end) {
			return
		}
		k := model.Pair{Account: account, Instrument: instrument}
		out[k] = out[k].Add(quantity)
	}
	for _, u := range p.Units {
		for _, e := range u.Effects() {
			add(e.Account, e.Instrument, e.Date, e.Quantity)
		}
	}
	for _, a := range p.Adjustments {
		add(a.Account, a.Instrument, a.Date, a.Amount)
	}
	return out
}

// assertConfirmWillAgree checks that what the plan expects, less what it says the
// ledger will lack, is what the ledger will hold, at the end of every month and at
// the end of time, for every account and instrument: a plan with nothing unexplained
// is a confirmation with nothing unexplained.
func assertConfirmWillAgree(t *testing.T, p model.Plan) {
	t.Helper()
	ends := []time.Time{day("2100-01-01")}
	for _, month := range p.Months() {
		end, err := model.MonthEnd(month)
		if err != nil {
			t.Fatal(err)
		}
		ends = append(ends, end)
	}
	for _, end := range ends {
		expected, unrecorded, held := p.ExpectedAt(end), p.UnrecordedAt(end), recordedAt(p, end)
		all := model.Balances{}
		for _, b := range []model.Balances{expected, unrecorded, held} {
			for k := range b {
				all[k] = decimal.Zero
			}
		}
		for _, k := range all.Pairs() {
			if got := expected[k].Sub(unrecorded[k]); !got.Equal(held[k]) {
				t.Errorf("%s %s on %s: the export says %s, less %s that the ledger lacks, is %s; the ledger will hold %s",
					k.Account, k.Instrument, end.Format(time.DateOnly), expected[k], unrecorded[k], got, held[k])
			}
		}
	}
}
