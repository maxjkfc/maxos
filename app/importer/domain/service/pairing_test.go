package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func out(account, currency, amount string, opts ...rowOpt) model.Row {
	return row(model.TypeTransferOut, account, currency, amount, append([]rowOpt{main_("轉帳", "轉帳")}, opts...)...)
}

func in(account, currency, amount string, opts ...rowOpt) model.Row {
	return row(model.TypeTransferIn, account, currency, amount, append([]rowOpt{main_("轉帳", "轉帳")}, opts...)...)
}

func TestTransferIsAPairOfTheSameMinuteAndInstrument(t *testing.T) {
	p := plan(
		out("Bank", "TWD", "-1000", fee("-15"), name("Pay the card"), detail("auto")),
		in("Card", "TWD", "1000", name("Pay the card")),
	)
	u := unitOf(t, p)
	if u.Kind != model.KindTransfer {
		t.Fatalf("kind = %s, want transfer", u.Kind)
	}
	if !isLeg(u.Main, "Bank", "TWD", "1000") || u.Other == nil || !isLeg(*u.Other, "Card", "TWD", "1000") {
		t.Errorf("legs = %+v, %+v", u.Main, u.Other)
	}
	if len(u.Fees) != 1 || u.Fees[0].Account != "Bank" || !u.Fees[0].Amount.Equal(d("15")) {
		t.Errorf("fees = %+v, want the fee from the account that left", u.Fees)
	}
	if want := p.Rows[0].Key + "+" + p.Rows[1].Key; u.ID != want {
		t.Errorf("id = %q, want one made of both rows: %q", u.ID, want)
	}
	if want := "Pay the card\nauto\n〔轉帳〕"; u.Description != want {
		t.Errorf("description = %q, want %q", u.Description, want)
	}
}

func TestFeeOnTheReceivingSideIsPaidFromTheReceivingAccount(t *testing.T) {
	p := plan(
		out("Bank USD", "USD", "-100"),
		in("Exchange", "USD", "100", fee("-1")),
	)
	u := unitOf(t, p)
	if len(u.Fees) != 1 || u.Fees[0].Account != "Exchange" || u.Fees[0].Instrument != "USD" {
		t.Fatalf("fees = %+v, want the fee of the account that received", u.Fees)
	}
	if got := u.Fees[0].Base; got == nil || !got.Equal(d("30")) {
		t.Errorf("fee base = %v, want 30 (1 USD at 30)", got)
	}

	// Fees on both sides are two fees, the sender's first.
	p = plan(
		out("Bank", "TWD", "-100", fee("-2")),
		in("Card", "TWD", "100", fee("-3")),
	)
	u = unitOf(t, p)
	if len(u.Fees) != 2 || u.Fees[0].Account != "Bank" || u.Fees[1].Account != "Card" {
		t.Errorf("fees = %+v, want one from each side", u.Fees)
	}
}

func TestTransfersBetweenCurrenciesAreExchangesAndWithACoinAreTrades(t *testing.T) {
	for _, tt := range []struct {
		name  string
		rows  []model.Row
		check func(t *testing.T, u model.Unit)
	}{
		{
			"two currencies are an exchange",
			[]model.Row{out("Bank", "TWD", "-3000", fee("-10")), in("Bank USD", "USD", "100")},
			func(t *testing.T, u model.Unit) {
				if u.Kind != model.KindExchange || u.Main.Instrument != "TWD" || !u.Main.Amount.Equal(d("3000")) ||
					u.Other.Instrument != "USD" || !u.Other.Amount.Equal(d("100")) {
					t.Errorf("unit = %+v", u)
				}
			},
		},
		{
			"money for a coin is a buy, whose quantity is the coin side",
			[]model.Row{out("Exchange", "USDT", "-100"), in("Exchange BTC", "BTC", "0.002")},
			func(t *testing.T, u model.Unit) {
				if u.Kind != model.KindTrade || u.Direction != "buy" || u.Main.Instrument != "BTC" || !u.Main.Amount.Equal(d("0.002")) ||
					u.Other.Instrument != "USDT" || !u.Other.Amount.Equal(d("100")) {
					t.Errorf("unit = %+v", u)
				}
			},
		},
		{
			"a coin for money is a sell",
			[]model.Row{out("Exchange BTC", "BTC", "-0.001"), in("Bank", "USD", "50")},
			func(t *testing.T, u model.Unit) {
				if u.Kind != model.KindTrade || u.Direction != "sell" || u.Main.Account != "Exchange" || u.Other.Account != "Bank" {
					t.Errorf("unit = %+v", u)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(tt.rows...)
			tt.check(t, unitOf(t, p))
		})
	}
}

func TestPurchaseOfACoinWhoseQuantityIsZeroIsLeftOutWithTheMoneyThatLeft(t *testing.T) {
	rows := rowsOf(out("Exchange", "USDT", "-100"), in("Exchange BTC", "BTC", "0"))
	p := serviceBuild(rows, testMapping())
	if len(p.Units) != 0 || !reflect.DeepEqual(skippedReasons(p), []model.Reason{model.ReasonZeroQuantity}) {
		t.Fatalf("units = %+v, skipped = %+v, want the pair left out for its quantity", p.Units, p.Skipped)
	}
	// What the ledger will lack is the money that left the account.
	if len(p.Unrecorded) != 1 || p.Unrecorded[0].Instrument != "USDT" || !p.Unrecorded[0].Quantity.Equal(d("-100")) {
		t.Errorf("unrecorded = %+v, want -100 USDT", p.Unrecorded)
	}

	// The user can give the quantity, and the pair is then a buy.
	m := testMapping()
	m.Rows = map[string]model.RowOverride{rows[1].Key: {Quantity: ptr("0.0015")}}
	p = serviceBuild(rows, m)
	u := unitOf(t, p)
	if u.Kind != model.KindTrade || !u.Main.Amount.Equal(d("0.0015")) {
		t.Errorf("unit = %+v, want a buy of 0.0015", u)
	}
	if len(p.Unrecorded) != 0 {
		t.Errorf("unrecorded = %+v, want none once it is recorded", p.Unrecorded)
	}
	if len(p.Differences) != 0 {
		t.Errorf("differences = %+v, want none: the quantity the user gave is what the row moved", p.Differences)
	}
	assertConfirmWillAgree(t, p)

	// So it is for a sale, whose row that left the account moved the quantity.
	sale := rowsOf(out("Exchange BTC", "BTC", "0"), in("Bank", "USD", "50"))
	m.Rows = map[string]model.RowOverride{sale[0].Key: {Quantity: ptr("0.001")}}
	p = serviceBuild(sale, m)
	if u := unitOf(t, p); u.Kind != model.KindTrade || u.Direction != "sell" || !u.Main.Amount.Equal(d("0.001")) {
		t.Errorf("unit = %+v, want a sell of 0.001", u)
	}
	if len(p.Differences) != 0 || len(p.Unrecorded) != 0 {
		t.Errorf("differences = %+v, unrecorded = %+v, want none", p.Differences, p.Unrecorded)
	}
	assertConfirmWillAgree(t, p)
}

func TestPairsThatCannotBeRecordedAreLeftOutWithAReason(t *testing.T) {
	for _, tt := range []struct {
		name string
		rows []model.Row
		want model.Reason
	}{
		{"an amount that differs in one instrument", []model.Row{out("Bank", "TWD", "-100"), in("Card", "TWD", "90")}, model.ReasonAmountMismatch},
		{"one account on both sides", []model.Row{out("Bank", "TWD", "-100"), in("Bank", "TWD", "100")}, model.ReasonSameAccount},
		{"a 轉出 alone", []model.Row{out("Bank", "TWD", "-100")}, model.ReasonUnpaired},
		{"a 轉入 alone", []model.Row{in("Bank", "TWD", "100")}, model.ReasonUnpaired},
		{"two currencies, one amount nothing", []model.Row{out("Bank", "TWD", "-100"), in("Bank USD", "USD", "0")}, model.ReasonZeroAmount},
		{"two coins", []model.Row{out("Exchange BTC", "BTC", "-1"), in("Exchange", "BTC", "1", on("2026-03-10"))}, model.ReasonSameAccount},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(tt.rows...)
			if got := skippedReasons(p); len(got) == 0 || got[0] != tt.want {
				t.Errorf("skipped = %v, want %v", got, tt.want)
			}
			if len(p.Units) != 0 {
				t.Errorf("units = %+v, want none", p.Units)
			}
		})
	}
}

func TestTwoCoinsAreNotATradeAndAreLeftOut(t *testing.T) {
	p := plan(out("Exchange BTC", "BTC", "-0.5"), in("Cash", "ETH", "4"))
	if got := skippedReasons(p); len(got) != 1 || got[0] != model.ReasonNotCurrencies {
		t.Errorf("skipped = %v, want the pair left out: neither side is a currency", got)
	}
	if len(p.Units) != 0 {
		t.Errorf("units = %+v, want none", p.Units)
	}
}

func TestSameInstrumentExchangeIsATransferThatIsTold(t *testing.T) {
	o := out("Bank", "TWD", "-500")
	o.Sub = "兌換"
	i := in("Card", "TWD", "500")
	i.Sub = "兌換"
	p := plan(o, i)
	if u := unitOf(t, p); u.Kind != model.KindTransfer {
		t.Errorf("kind = %s, want transfer: an exchange needs two currencies", u.Kind)
	}
	if len(p.Caveats) != 1 || p.Caveats[0].Kind != model.CaveatSameInstrumentExchange {
		t.Errorf("caveats = %+v", p.Caveats)
	}
}

func TestSeveralTransfersInOneMinuteAreMatchedByTheirAmounts(t *testing.T) {
	// Two payments at the same minute, in the order of the file out-out-in-in, the
	// second received first: only the amounts say which goes with which.
	p := plan(
		out("Bank", "TWD", "-1000"),
		out("Bank", "TWD", "-250"),
		in("Card", "TWD", "250"),
		in("Cash", "TWD", "1000"),
	)
	if len(p.Units) != 2 {
		t.Fatalf("units = %d, skipped = %+v, want 2", len(p.Units), p.Skipped)
	}
	got := map[string]string{}
	for _, u := range p.Units {
		got[u.Main.Amount.String()] = u.Other.Account
	}
	if got["1000"] != "Cash" || got["250"] != "Card" {
		t.Errorf("pairs = %v, want 1000 to Cash and 250 to Card", got)
	}
}

func TestIdenticalTransfersOfOneMinuteArePairedInTheOrderOfTheFile(t *testing.T) {
	p := plan(
		out("Bank", "TWD", "-100"), out("Bank", "TWD", "-100"),
		in("Card", "TWD", "100"), in("Card", "TWD", "100"),
	)
	if len(p.Units) != 2 || p.Units[0].ID == p.Units[1].ID {
		t.Fatalf("units = %d, want 2 with their own IDs (%v)", len(p.Units), skippedReasons(p))
	}
	if p.Units[0].Rows[0] != p.Rows[0].Key || p.Units[0].Rows[1] != p.Rows[2].Key ||
		p.Units[1].Rows[0] != p.Rows[1].Key || p.Units[1].Rows[1] != p.Rows[3].Key {
		t.Errorf("rows = %v, %v, want the first out with the first in, the second with the second", p.Units[0].Rows, p.Units[1].Rows)
	}
}

func TestTransfersOfOneMinuteThatCanBePairedInMoreThanOneWayAreLeftOut(t *testing.T) {
	// Different accounts but the same amount: either pairing is possible.
	p := plan(
		out("Bank", "TWD", "-100"), out("Cash", "TWD", "-100"),
		in("Card", "TWD", "100"), in("Exchange", "TWD", "100"),
	)
	if len(p.Units) != 0 {
		t.Fatalf("units = %+v, want none", p.Units)
	}
	for _, s := range p.Skipped {
		if s.Reason != model.ReasonAmbiguousPair {
			t.Errorf("reason = %s, want ambiguous_pair", s.Reason)
		}
	}
	if len(p.Skipped) != 4 {
		t.Errorf("skipped = %d, want every row of the minute", len(p.Skipped))
	}

	// No pairing at all: the amounts do not fit either.
	p = plan(
		out("Bank", "TWD", "-100"), out("Cash", "TWD", "-300"),
		in("Card", "TWD", "100"), in("Exchange", "TWD", "999"),
	)
	if got := skippedReasons(p); len(got) != 4 || got[0] != model.ReasonNoPairing {
		t.Errorf("skipped = %v, want 4 with no pairing", got)
	}
}

func TestUnequalNumbersOfHalvesAreLeftOut(t *testing.T) {
	p := plan(out("Bank", "TWD", "-100"), out("Cash", "TWD", "-100"), in("Card", "TWD", "100"))
	if got := skippedReasons(p); len(got) != 3 || got[0] != model.ReasonUnpaired {
		t.Errorf("skipped = %v, want 3 unpaired", got)
	}
}

func TestHalvesOfDifferentMinutesAreNotPaired(t *testing.T) {
	p := plan(out("Bank", "TWD", "-100", at("10:00")), in("Card", "TWD", "100", at("10:01")))
	if len(p.Units) != 0 || len(p.Skipped) != 2 {
		t.Errorf("units = %d, skipped = %d, want the two halves left out", len(p.Units), len(p.Skipped))
	}
}
