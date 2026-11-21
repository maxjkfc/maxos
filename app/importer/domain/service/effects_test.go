package service_test

import (
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func TestEveryRecordTypeWithItsFeeAndDiscountIsRecordedAsItsRowsSayIt(t *testing.T) {
	p := plan(everyRecordType()...)
	if len(p.Skipped) != 0 || len(p.Problems) != 0 {
		t.Fatalf("the file is made to be recorded whole: skipped %+v, problems %+v", p.Skipped, p.Problems)
	}
	kinds := map[model.UnitKind]bool{}
	for _, u := range p.Units {
		kinds[u.Kind] = true
	}
	for _, k := range []model.UnitKind{
		model.KindExpense, model.KindExpenseReward, model.KindIncome, model.KindReward, model.KindDealing,
		model.KindTransfer, model.KindExchange, model.KindTrade, model.KindOpening, model.KindBillInstalment,
	} {
		if !kinds[k] {
			t.Errorf("the file has no unit of the kind %s", k)
		}
	}

	if len(p.Differences) != 0 {
		t.Errorf("differences = %+v, want none: the ledger has a place for every fee and discount of this file", p.Differences)
	}
	if got := differing(p); len(got) != 0 {
		t.Errorf("units that do not do what their rows say: %v", got)
	}
	if len(p.Unrecorded) != 0 {
		t.Errorf("unrecorded = %+v, want none", p.Unrecorded)
	}
	assertConfirmWillAgree(t, p)
}

func TestWhatTheLedgerHasNoPlaceForIsADifferenceWithItsReason(t *testing.T) {
	rows := append(instalmentRows("Bank", "Bank USD"),
		row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), discount("20"), on("2026-03-01")),
		row(model.TypeIncome, "Bank", "TWD", "1000", main_("收入", "薪水"), discount("30"), on("2026-03-02")),
		row(model.TypeBonus, "Card", "TWD", "50", main_("紅利回饋", "紅利回饋"), discount("5"), on("2026-03-03")),
	)
	withFee(t, rows, model.TypeInstalmentPayment, "2026-02-15", "-3")
	p := plan(rows...)

	// One difference for each, in the order of the units: a payment of a plan has no
	// place for a fee, nor has a dealing, an income or a reward for a discount.
	for i, w := range []struct {
		kind     model.UnitKind
		reason   model.DifferenceReason
		at       slot
		quantity string
	}{
		{model.KindBillInstalment, model.DifferenceFee, slot{"Bank", "TWD", "2026-02-15"}, "-3"},
		{model.KindDealing, model.DifferenceDiscount, slot{"Cash", "TWD", "2026-03-01"}, "20"},
		{model.KindIncome, model.DifferenceDiscount, slot{"Bank", "TWD", "2026-03-02"}, "30"},
		{model.KindReward, model.DifferenceDiscount, slot{"Card", "TWD", "2026-03-03"}, "5"},
	} {
		if len(p.Differences) != 4 || len(p.Units) != 4 {
			t.Fatalf("units = %d, differences = %+v, want 4 of each", len(p.Units), p.Differences)
		}
		u, diff := p.Units[i], p.Differences[i]
		if u.Kind != w.kind || diff.Unit != u.ID || !reflect.DeepEqual(diff.Rows, u.Rows) {
			t.Errorf("difference %d is of the unit %q with the rows %v, want the %s %q with %v", i, diff.Unit, diff.Rows, w.kind, u.ID, u.Rows)
		}
		if !reflect.DeepEqual(diff.Reasons, []model.DifferenceReason{w.reason}) {
			t.Errorf("%s: reasons = %v, want %s", w.kind, diff.Reasons, w.reason)
		}
		if len(diff.Effects) != 1 || slotOf(diff.Effects[0]) != w.at || !diff.Effects[0].Quantity.Equal(d(w.quantity)) || diff.Effects[0].Row != u.Rows[0] {
			t.Errorf("%s: effects = %+v, want %s on %v from the first row of the unit", w.kind, diff.Effects, w.quantity, w.at)
		}
	}

	// What the ledger lacks is what these differences come to, and so a confirmation
	// has nothing unexplained.
	end := day("2100-01-01")
	for pair, want := range map[model.Pair]string{
		{Account: "Bank", Instrument: "TWD"}: "27",
		{Account: "Cash", Instrument: "TWD"}: "20",
		{Account: "Card", Instrument: "TWD"}: "5",
	} {
		if got := p.UnrecordedAt(end)[pair]; !got.Equal(d(want)) {
			t.Errorf("unrecorded %s %s = %s, want %s", pair.Account, pair.Instrument, got, want)
		}
	}
	assertConfirmWillAgree(t, p)
}

func TestAFeeAndADiscountOnOneRowAreBothTheReason(t *testing.T) {
	rows := instalmentRows("Bank", "Bank USD")
	withFee(t, rows, model.TypeInstalmentPayment, "2026-02-15", "-3")
	for i, r := range rows {
		if r.Type == model.TypeInstalmentPayment && r.Date.Equal(day("2026-02-15")) {
			rows[i].Discount = d("2")
		}
	}
	p := plan(rows...)
	if len(p.Differences) != 1 {
		t.Fatalf("differences = %+v, want one", p.Differences)
	}
	if got, want := p.Differences[0].Reasons, []model.DifferenceReason{model.DifferenceFee, model.DifferenceDiscount}; !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if e := p.Differences[0].Effects; len(e) != 1 || !e[0].Quantity.Equal(d("-1")) {
		t.Errorf("effects = %+v, want -1: the fee of 3 less the discount of 2", e)
	}
	assertConfirmWillAgree(t, p)
}

func TestADifferenceThatIsNeitherTheFeeNorTheDiscountIsOther(t *testing.T) {
	// A fee with a positive sign is not one the ledger can record as it is: it records a
	// fee of 5 where the row adds 5.
	p := plan(row(model.TypeIncome, "Bank", "TWD", "1000", main_("收入", "薪水"), fee("5")))
	if len(p.Differences) != 1 || !reflect.DeepEqual(p.Differences[0].Reasons, []model.DifferenceReason{model.DifferenceOther}) {
		t.Fatalf("differences = %+v, want one for no reason the plan knows", p.Differences)
	}
	if e := p.Differences[0].Effects; len(e) != 1 || !e[0].Quantity.Equal(d("10")) {
		t.Errorf("effects = %+v, want 10", e)
	}
	assertConfirmWillAgree(t, p)
}

func TestRefundRecordsItsFee(t *testing.T) {
	p := plan(row(model.TypeRefund, "Card", "TWD", "450", main_("Shop", "Books"), fee("-5")))
	u := unitOf(t, p)
	if len(u.Fees) != 1 || u.Fees[0].Account != "Card" || u.Fees[0].Instrument != "TWD" || u.Fees[0].Category != "Fees" || !u.Fees[0].Amount.Equal(d("5")) {
		t.Errorf("fees = %+v, want the fee of 5 from the card, in the category of fees", u.Fees)
	}
	if len(p.Differences) != 0 || len(differing(p)) != 0 {
		t.Errorf("differences = %+v, want none: the ledger has a place for the fee of an income", p.Differences)
	}
	assertConfirmWillAgree(t, p)
}

func TestFeeAndInterestRowsRecordTheirFeeAndDiscountLikeAnExpense(t *testing.T) {
	for _, tt := range []struct {
		name     string
		typ      model.RecordType
		main     string
		category string
	}{
		{"a row that is a fee", model.TypeFee, "手續費", "Fees"},
		{"an interest that no plan has", model.TypeInterest, "利息支出", "Interest"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(row(tt.typ, "Bank", "TWD", "-30", main_(tt.main, tt.main), fee("-2"), discount("5")))
			u := unitOf(t, p)
			if u.Kind != model.KindExpense || len(u.Lines) != 2 {
				t.Fatalf("unit = %s with %d lines, want an expense of the row and of its fee", u.Kind, len(u.Lines))
			}
			if l := u.Lines[0]; l.Category != tt.category || !l.Amount.Equal(d("25")) || l.Discount == nil || !l.Discount.Equal(d("5")) {
				t.Errorf("line of the row = %+v, want 25 of %s with a discount of 5", l, tt.category)
			}
			if l := u.Lines[1]; l.Category != "Fees" || !l.Amount.Equal(d("2")) {
				t.Errorf("line of the fee = %+v, want 2 of Fees", l)
			}
			if !isLeg(u.Main, "Bank", "TWD", "27") {
				t.Errorf("paid = %+v, want 27 from the bank: the lines together", u.Main)
			}
			if len(p.Differences) != 0 {
				t.Errorf("differences = %+v, want none", p.Differences)
			}
			assertConfirmWillAgree(t, p)
		})
	}
}

func TestBillInstalmentRecordsTheFeeOfItsOriginRows(t *testing.T) {
	rows := instalmentRows("Bank", "Bank USD")
	rows[0].Fee, rows[1].Fee = d("-4"), d("-2")
	p := plan(rows...)
	u := unitOf(t, p)
	if u.Kind != model.KindBillInstalment {
		t.Fatalf("kind = %s, want a plan; skipped %+v", u.Kind, p.Skipped)
	}
	if len(u.Fees) != 2 || !u.Fees[0].Amount.Equal(d("4")) || !u.Fees[1].Amount.Equal(d("2")) {
		t.Fatalf("fees = %+v, want the fees of 4 and 2, one for each row that has one", u.Fees)
	}
	for _, f := range u.Fees {
		if f.Account != "Bank" || f.Instrument != "TWD" || f.Category != "Fees" {
			t.Errorf("fee = %+v, want it paid from Bank in TWD in the category of fees: both accounts of MOZE are Bank", f)
		}
	}
	if len(p.Differences) != 0 || len(differing(p)) != 0 {
		t.Errorf("differences = %+v, want none", p.Differences)
	}
	assertConfirmWillAgree(t, p)
}

func TestEveryAmountOfAFeeIsCheckedForDecimalsLikeTheRestOfTheRow(t *testing.T) {
	instalment := instalmentRows("Bank", "Bank USD")
	instalment[0].Fee = d("-0.005")
	for _, tt := range []struct {
		name string
		rows []model.Row
	}{
		{"an income", []model.Row{row(model.TypeIncome, "Bank", "TWD", "10", main_("收入", "薪水"), fee("-0.005"))}},
		{"a reward", []model.Row{row(model.TypeBonus, "Bank", "TWD", "10", main_("紅利回饋", "紅利回饋"), fee("-0.005"))}},
		{"a refund", []model.Row{row(model.TypeRefund, "Card", "TWD", "10", main_("Shop", "Books"), fee("-0.005"))}},
		{"a row that is a fee", []model.Row{row(model.TypeFee, "Bank", "TWD", "-10", main_("手續費", "手續費"), fee("-0.005"))}},
		{"an interest", []model.Row{row(model.TypeInterest, "Card", "TWD", "-10", main_("利息支出", "利息支出"), fee("-0.005"))}},
		{"a transfer", []model.Row{out("Bank", "TWD", "-100", fee("-0.005")), in("Card", "TWD", "100")}},
		{"an exchange", []model.Row{out("Bank", "TWD", "-3000"), in("Bank USD", "USD", "100", fee("-0.005"))}},
		{"a trade", []model.Row{out("Exchange", "USDT", "-100"), in("Exchange BTC", "BTC", "0.002", fee("-0.000000001"))}},
		{"an instalment plan", instalment},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(tt.rows...)
			if got := skippedReasons(p); len(got) == 0 || got[0] != model.ReasonTooManyDecimals {
				t.Errorf("skipped = %v, want the rows left out for their decimals: the ledger would refuse the fee", got)
			}
			if len(p.Units) != 0 {
				t.Errorf("units = %+v, want none", p.Units)
			}
		})
	}
}

func TestAdjustmentTakesWhatItsRowAddsToTheAccount(t *testing.T) {
	p := plan(row(model.TypeAdjustment, "Cash", "TWD", "40", fee("-2"), discount("1")))
	if len(p.Adjustments) != 1 || !p.Adjustments[0].Amount.Equal(d("39")) {
		t.Fatalf("adjustments = %+v, want one of 39: the amount, the discount and the fee", p.Adjustments)
	}
	assertConfirmWillAgree(t, p)
}

func TestBuildingAPlanTwiceGivesTheSamePlan(t *testing.T) {
	rows := append(everyRecordType(),
		row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), discount("20"), on("2026-03-01")),
		row(model.TypeIncome, "Bank", "TWD", "1000", main_("收入", "薪水"), discount("30"), on("2026-03-02")),
		row(model.TypeBonus, "Card", "TWD", "50", main_("紅利回饋", "紅利回饋"), discount("5"), on("2026-03-03")),
	)
	first, second := plan(rows...), serviceBuild(rowsOf(rows...), testMapping())
	if len(first.Units) == 0 || len(first.Differences) != 3 {
		t.Fatalf("units = %d, differences = %d, want a plan with something to compare", len(first.Units), len(first.Differences))
	}
	for i := range first.Units {
		if first.Units[i].Key() != second.Units[i].Key() {
			t.Errorf("unit %d has the keys %q and %q", i, first.Units[i].Key(), second.Units[i].Key())
		}
	}
	if !reflect.DeepEqual(first.Differences, second.Differences) || !reflect.DeepEqual(first.Unrecorded, second.Unrecorded) {
		t.Errorf("two builds of one file differ in what is unrecorded: %+v / %+v", first.Differences, second.Differences)
	}
}

func TestUnitsHaveAVersionAndTheirKeysAreUnique(t *testing.T) {
	p := plan(everyRecordType()...)
	keys := map[string]bool{}
	for _, u := range p.Units {
		if len(u.Version) != 12 || u.Version != u.Fingerprint() {
			t.Errorf("%s %s has the version %q, want its 12 character fingerprint", u.Kind, u.ID, u.Version)
		}
		if keys[u.Key()] {
			t.Errorf("the key %q is that of more than one unit", u.Key())
		}
		keys[u.Key()] = true
	}
}
