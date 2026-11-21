package service_test

import (
	"reflect"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func TestExpenseIsOneLineAtThePriceWithItsFeeAsAnotherLine(t *testing.T) {
	p := plan(row(model.TypeExpense, "Card", "TWD", "-120",
		main_("Food", "Lunch"), name("Noodles"), merchant("Fake Shop"), detail("◎ noodles ($100) ✕ 1"),
		fee("-2"), project("Daily"), tags("family")))
	u := unitOf(t, p)

	if u.Kind != model.KindExpense || u.ID != p.Rows[0].Key {
		t.Errorf("unit = %s %q, want an expense whose ID is its row", u.Kind, u.ID)
	}
	if u.Description != "Noodles" || u.Merchant != "Fake Shop" || u.Area != "Daily" || u.Project != "" {
		t.Errorf("text = %q, %q, area %q, project %q", u.Description, u.Merchant, u.Area, u.Project)
	}
	if !reflect.DeepEqual(u.Tags, []string{"Family"}) {
		t.Errorf("tags = %v, want the ledger name of the tag", u.Tags)
	}
	want := []model.Line{
		{Category: "Food/Lunch", Amount: d("120"), Memo: "◎ noodles ($100) ✕ 1"},
		{Category: "Fees", Amount: d("2")},
	}
	if !reflect.DeepEqual(u.Lines, want) {
		t.Errorf("lines = %+v, want %+v", u.Lines, want)
	}
	if !isLeg(u.Main, "Card", "TWD", "122") {
		t.Errorf("paid = %+v, want 122 from the card: the lines together", u.Main)
	}
}

func TestExpenseDiscountIsPaidLessAndKeptOnTheLine(t *testing.T) {
	p := plan(row(model.TypeExpense, "Card", "TWD", "-100", main_("Food", "Lunch"), discount("20")))
	u := unitOf(t, p)
	if u.Kind != model.KindExpense || len(u.Lines) != 1 {
		t.Fatalf("unit = %s with %d lines", u.Kind, len(u.Lines))
	}
	l := u.Lines[0]
	if !l.Amount.Equal(d("80")) || l.Discount == nil || !l.Discount.Equal(d("20")) {
		t.Errorf("line = %+v, want 80 paid with a discount of 20", l)
	}
	if !u.Main.Amount.Equal(d("80")) {
		t.Errorf("the account pays %s, want 80", u.Main.Amount)
	}
}

func TestExpenseWhoseDiscountIsTheWholePriceIsTheExpenseAndAReward(t *testing.T) {
	p := plan(row(model.TypeExpense, "Card", "TWD", "-100", main_("Food", "Lunch"), name("Coffee"), discount("100"), fee("-3")))
	u := unitOf(t, p)
	if u.Kind != model.KindExpenseReward {
		t.Fatalf("kind = %s, want expense_reward", u.Kind)
	}
	if got := u.Lines[0]; !got.Amount.Equal(d("100")) || got.Discount != nil {
		t.Errorf("expense line = %+v, want the full price and no discount", got)
	}
	if u.Reward == nil || !u.Reward.Amount.Equal(d("100")) || u.Reward.Description != "Coffee （折抵）" {
		t.Errorf("reward = %+v, want 100", u.Reward)
	}
	if keys := u.Keys(); len(keys) != 2 || keys[1] != u.Key()+model.RewardKeySuffix {
		t.Errorf("keys = %v, want the key and the key of the reward", keys)
	}
	if len(p.Caveats) != 0 {
		t.Errorf("caveats = %+v, want none for a discount that is the price", p.Caveats)
	}
}

func TestExpenseWhoseDiscountIsMoreThanThePriceIsAReward(t *testing.T) {
	p := plan(row(model.TypeExpense, "Card", "TWD", "-99", main_("Food", "Lunch"), discount("100"), fee("-1")))
	u := unitOf(t, p)
	if u.Kind != model.KindExpenseReward || !u.Reward.Amount.Equal(d("100")) || !u.Lines[0].Amount.Equal(d("99")) {
		t.Fatalf("unit = %+v, want the expense of 99 and a reward of 100 (so the account ends where the file says: -99+100-1)", u)
	}
	if len(p.Caveats) != 1 || p.Caveats[0].Kind != model.CaveatDiscountOverPrice {
		t.Errorf("caveats = %+v, want one for the discount over the price", p.Caveats)
	}
}

func TestForeignExpenseTakesItsBaseAmountFromTheRatesOfTheDay(t *testing.T) {
	// 2026-03-10 has no rate of its own: the nearest earlier day, 03-01, is 30.
	p := plan(row(model.TypeExpense, "Bank USD", "USD", "-10.55", main_("Food", "Lunch"), fee("-0.45")))
	u := unitOf(t, p)
	if got := u.Lines[0].Base; got == nil || !got.Equal(d("316.5")) {
		t.Errorf("base of the line = %v, want 316.5 (10.55 at 30)", got)
	}
	if got := u.Lines[1].Base; got == nil || !got.Equal(d("13.5")) {
		t.Errorf("base of the fee = %v, want 13.5", got)
	}
	if len(p.Problems) != 0 {
		t.Errorf("problems = %v, want none", p.Problems)
	}
}

func TestUSDTTakesTheRatesOfUSD(t *testing.T) {
	p := plan(row(model.TypeIncome, "Exchange", "USDT", "10", main_("收入", "薪水")))
	u := unitOf(t, p)
	if got := u.Lines[0].Base; got == nil || !got.Equal(d("300")) {
		t.Errorf("base = %v, want 300: USDT is worth USD", got)
	}
}

func TestRateThatTheRatesLackIsAProblemNotAGuess(t *testing.T) {
	p := plan(row(model.TypeExpense, "Bank USD", "USD", "-10", main_("Food", "Lunch"), on("2026-02-01")))
	want := model.Problem{Code: model.ProblemRateMissing, Subject: "rates.USD", Args: []string{"USD", "2026-02-01"}}
	if len(p.Problems) != 1 || !reflect.DeepEqual(p.Problems[0], want) {
		t.Fatalf("problems = %+v, want %+v: the rates of USD lack the first day they are needed", p.Problems, want)
	}
	if !p.Blocked() {
		t.Error("plan is not blocked, but a base amount is missing")
	}
}

// What was paid for a party is a dealing, and what the party owes is all that left the
// account for it: the price less the discount, and the fee of the row, which is part of
// paying for the party and not an expense of the user's own. A dealing has no column for a
// discount or for a fee, so the description says both. The dealing does to the account what
// the row says, so the plan has nothing to tell of it.
func TestExpenseOfAProjectThatIsPaidForAPartyIsADealingOfWhatLeftTheAccount(t *testing.T) {
	for _, tt := range []struct {
		name                      string
		account, currency, amount string
		opts                      []rowOpt
		from, owed, description   string
	}{
		{"with a fee", "Card", "TWD", "-500", []rowOpt{fee("-5")}, "Card", "505", "Team lunch\n手續費 5 TWD\n〔Food/Lunch〕"},
		{"with no fee", "Card", "TWD", "-500", nil, "Card", "500", "Team lunch\n〔Food/Lunch〕"},
		{"with a discount", "Card", "TWD", "-500", []rowOpt{discount("50")}, "Card", "450", "Team lunch\n折扣 50 TWD\n〔Food/Lunch〕"},
		{"with a discount and a fee", "Card", "TWD", "-500", []rowOpt{discount("50"), fee("-5")}, "Card", "455", "Team lunch\n折扣 50 TWD\n手續費 5 TWD\n〔Food/Lunch〕"},
		// A discount that is the whole price takes nothing but the fee: the dealing is the fee alone.
		{"whose discount is the whole price, with a fee", "Card", "TWD", "-500", []rowOpt{discount("500"), fee("-5")}, "Card", "5", "Team lunch\n折扣 500 TWD\n手續費 5 TWD\n〔Food/Lunch〕"},
		{"whose discount is more than the price, with a fee that is more than the excess", "Card", "TWD", "-100", []rowOpt{discount("120"), fee("-25")}, "Card", "5", "Team lunch\n折扣 120 TWD\n手續費 25 TWD\n〔Food/Lunch〕"},
		// The USD account of MOZE is the account of the bank of the ledger: the dealing is in USD.
		{"with a fee, in the currency of the row", "Bank USD", "USD", "-10", []rowOpt{fee("-0.5")}, "Bank", "10.5", "Team lunch\n手續費 0.5 USD\n〔Food/Lunch〕"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			opts := append([]rowOpt{main_("Food", "Lunch"), project("Corp paid"), name("Team lunch")}, tt.opts...)
			p := plan(row(model.TypeExpense, tt.account, tt.currency, tt.amount, opts...))
			u := unitOf(t, p)
			if u.Kind != model.KindDealing || u.Party != "Corp" || u.Direction != "paid_out" {
				t.Fatalf("unit = %s %q %q, want a dealing paid out to Corp", u.Kind, u.Party, u.Direction)
			}
			if !isLeg(u.Main, tt.from, tt.currency, tt.owed) {
				t.Errorf("amount = %+v, want %s %s from %s: what was paid, less the discount, and the fee", u.Main, tt.owed, tt.currency, tt.from)
			}
			if len(u.Fees) != 0 {
				t.Errorf("fees = %+v, want none: the fee is part of the amount, no expense of its own", u.Fees)
			}
			if u.Description != tt.description {
				t.Errorf("description = %q, want %q (nothing of the row is lost)", u.Description, tt.description)
			}
			if len(p.Differences) != 0 || len(p.Unrecorded) != 0 || p.Blocked() {
				t.Errorf("differences = %+v, unrecorded = %+v, problems = %+v, want a dealing that does what its row says", p.Differences, p.Unrecorded, p.Problems)
			}
		})
	}
}

// A row paid for a party that took nothing from the account has no dealing: its discount is
// the whole price and it has no fee, or it is more than the price and the fee does not make
// up for the excess. It is left out as a row of no amount.
func TestRowPaidForAPartyThatTookNothingFromTheAccountIsLeftOut(t *testing.T) {
	p := plan(
		row(model.TypeExpense, "Card", "TWD", "-500", main_("Food", "Lunch"), project("Corp paid"), discount("500")),
		row(model.TypeExpense, "Card", "TWD", "-100", main_("Food", "Lunch"), project("Corp paid"), discount("120"), fee("-15")),
	)
	if got := skippedReasons(p); !reflect.DeepEqual(got, []model.Reason{model.ReasonZeroAmount, model.ReasonZeroAmount}) {
		t.Errorf("reasons = %v, want both rows left out as rows of no amount", got)
	}
	if len(p.Units) != 0 {
		t.Errorf("units = %+v, want none: a dealing has a positive amount", p.Units)
	}
}

// What a party owes is what the rows paid for it take from the accounts: their effects, with
// the fee and the discount that are in them. A dealing says no less and no more, for every
// row that is paid for the party, and a row that is not is not in it.
func TestWhatAPartyOwesIsWhatTheRowsPaidForItTakeFromTheAccounts(t *testing.T) {
	p := plan(
		row(model.TypeExpense, "Card", "TWD", "-500", main_("Food", "Lunch"), project("Corp paid"), fee("-5"), discount("50"), on("2026-03-04")),
		row(model.TypeExpense, "Cash", "TWD", "-200", main_("Food", "Lunch"), project("Corp paid"), fee("-3"), on("2026-03-05")),
		row(model.TypeExpense, "Card", "TWD", "-300", main_("Food", "Lunch"), project("Corp paid"), discount("300"), fee("-4"), on("2026-03-05")),
		row(model.TypeExpense, "Card", "TWD", "-120", main_("Food", "Lunch"), fee("-2"), on("2026-03-06")),
	)
	byRow := map[string]decimal.Decimal{}
	for _, e := range p.Effects {
		byRow[e.Row] = byRow[e.Row].Add(e.Quantity)
	}
	owed, left, dealings := decimal.Zero, decimal.Zero, 0
	for _, u := range p.Units {
		if u.Kind != model.KindDealing || u.Party != "Corp" {
			continue
		}
		dealings++
		owed = owed.Add(u.Main.Amount)
		for _, key := range u.Rows {
			left = left.Sub(byRow[key])
		}
	}
	if dealings != 3 || !owed.Equal(left) || !owed.Equal(d("662")) {
		t.Errorf("Corp owes %s by %d dealings, the rows paid for it take %s from the accounts, want both to be 662 (455, 203 and the 4 of the row that its discount paid)", owed, dealings, left)
	}
}

func TestIncomeKinds(t *testing.T) {
	for _, tt := range []struct {
		name  string
		row   model.Row
		check func(t *testing.T, p model.Plan, u model.Unit)
	}{
		{
			"an income of its category",
			row(model.TypeIncome, "Bank", "TWD", "50000", main_("收入", "薪水"), project("Daily")),
			func(t *testing.T, _ model.Plan, u model.Unit) {
				if u.Kind != model.KindIncome || u.Lines[0].Category != "Salary" || u.Area != "Daily" {
					t.Errorf("unit = %+v", u)
				}
			},
		},
		{
			"a reward when the category is a credit card offset",
			row(model.TypeIncome, "Card", "TWD", "30", main_("收入", "信用卡抵扣")),
			func(t *testing.T, _ model.Plan, u model.Unit) {
				if u.Kind != model.KindReward || !u.Main.Amount.Equal(d("30")) {
					t.Errorf("unit = %+v", u)
				}
			},
		},
		{
			"what the company gives back is a dealing received",
			row(model.TypeIncome, "Bank", "TWD", "800", main_("收入", "公司代收款"), name("Reimbursement")),
			func(t *testing.T, _ model.Plan, u model.Unit) {
				if u.Kind != model.KindDealing || u.Party != "Corp" || u.Direction != "received" {
					t.Errorf("unit = %+v", u)
				}
			},
		},
		{
			"a 收款 is an income to check, tagged, and a caveat",
			row(model.TypeIncome, "Bank", "TWD", "1200", main_("收入", "收款"), name("Someone paid")),
			func(t *testing.T, p model.Plan, u model.Unit) {
				if u.Kind != model.KindIncome || u.Lines[0].Category != "收款（待確認）" || !reflect.DeepEqual(u.Tags, []string{"待確認"}) {
					t.Errorf("unit = %+v", u)
				}
				if len(p.Caveats) != 1 || p.Caveats[0].Kind != model.CaveatPendingIncome || p.Caveats[0].Row != u.Rows[0] {
					t.Errorf("caveats = %+v, want the row to be listed", p.Caveats)
				}
			},
		},
		{
			"a refund is an income of the refund category with its old category in the memo",
			row(model.TypeRefund, "Card", "TWD", "450", main_("Shop", "Books"), name("Returned"), detail("receipt 1")),
			func(t *testing.T, p model.Plan, u model.Unit) {
				if u.Kind != model.KindIncome || u.Lines[0].Category != "Refunds" || u.Lines[0].Memo != "receipt 1\n〔Shop/Books〕" {
					t.Errorf("unit = %+v", u)
				}
				if len(p.Caveats) != 1 || p.Caveats[0].Kind != model.CaveatRefund {
					t.Errorf("caveats = %+v", p.Caveats)
				}
			},
		},
		{
			"a bonus is a reward in the currency it was given in, with a base amount",
			row(model.TypeBonus, "Bank USD", "USD", "1.5", main_("紅利回饋", "紅利回饋"), name("Cash back")),
			func(t *testing.T, _ model.Plan, u model.Unit) {
				if u.Kind != model.KindReward || u.Base == nil || !u.Base.Equal(d("45")) {
					t.Errorf("unit = %+v, want a reward with base 45", u)
				}
			},
		},
		{
			"the reward of what was paid for the company is a reward with no area of its own",
			row(model.TypeBonus, "Card", "TWD", "5", project("Corp paid")),
			func(t *testing.T, _ model.Plan, u model.Unit) {
				if u.Kind != model.KindReward || u.Area != "" || u.Project != "" {
					t.Errorf("unit = %+v", u)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := plan(tt.row)
			tt.check(t, p, unitOf(t, p))
		})
	}
}

func TestReceivableIsADealingWhoseDirectionIsTheSign(t *testing.T) {
	p := plan(
		row(model.TypeReceivable, "Cash", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), fee("-5")),
		row(model.TypeReceivable, "Cash", "TWD", "100", party("Amy"), main_("應收款項", "借出")),
	)
	if len(p.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(p.Units))
	}
	out, in := p.Units[0], p.Units[1]
	if out.Kind != model.KindDealing || out.Direction != "paid_out" || !out.Main.Amount.Equal(d("300")) || out.Party != "Amy" {
		t.Errorf("negative = %+v, want 300 paid out to Amy", out)
	}
	if len(out.Fees) != 1 || !out.Fees[0].Amount.Equal(d("5")) {
		t.Errorf("fees = %+v, want the fee of 5", out.Fees)
	}
	if in.Direction != "received" || !in.Main.Amount.Equal(d("100")) {
		t.Errorf("positive = %+v, want 100 received", in)
	}
	if want := "〔應收款項/借出〕"; out.Description != want {
		t.Errorf("description = %q, want the category kept: %q", out.Description, want)
	}
}

func TestFeeRowAndInterestAreExpensesOfTheirCategories(t *testing.T) {
	p := plan(
		row(model.TypeFee, "Bank", "TWD", "-30", main_("手續費", "手續費"), name("Wire")),
		row(model.TypeInterest, "Card", "TWD", "-12", main_("利息支出", "利息支出"), detail("6% a year")),
	)
	if len(p.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(p.Units))
	}
	if p.Units[0].Lines[0].Category != "Fees" || p.Units[1].Lines[0].Category != "Interest" || p.Units[1].Lines[0].Memo != "6% a year" {
		t.Errorf("units = %+v", p.Units)
	}
}

func TestOpeningBalanceIsDatedTheDayBeforeTheFirstRowOfItsAccount(t *testing.T) {
	p := plan(
		row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), at("")),
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch"), on("2026-02-03")),
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch"), on("2026-01-20")),
	)
	var opening model.Unit
	for _, u := range p.Units {
		if u.Kind == model.KindOpening {
			opening = u
		}
	}
	if !opening.Date.Equal(day("2026-01-19")) || !opening.Main.Amount.Equal(d("1000")) || opening.Clock != "" {
		t.Errorf("opening = %+v, want 1000 on 2026-01-19", opening)
	}
	if p.Units[0].Kind != model.KindOpening {
		t.Errorf("the opening is not the first unit: %v", p.Units[0].Kind)
	}
}

func TestOpeningBalanceOfNothingNeedsNothing(t *testing.T) {
	p := plan(row(model.TypeInitial, "Cash", "TWD", "0", on("0001-01-01")), row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch")))
	if len(p.NoOps) != 1 || p.NoOps[0].Reason != model.NoOpZeroOpening {
		t.Errorf("noops = %+v, want the zero opening", p.NoOps)
	}
	if len(p.Units) != 1 {
		t.Errorf("units = %d, want only the expense", len(p.Units))
	}
}

func TestOpeningBalanceOfAnAccountWithNoRowFallsBackToItsLedgerAccountThenTheFile(t *testing.T) {
	// "Bank USD" has only an opening: the account of the ledger it is, "Bank", has a row.
	p := plan(
		row(model.TypeInitial, "Bank USD", "USD", "5", on("0001-01-01")),
		row(model.TypeExpense, "Bank", "TWD", "-10", main_("Food", "Lunch"), on("2026-02-03")),
	)
	var opening model.Unit
	for _, u := range p.Units {
		if u.Kind == model.KindOpening {
			opening = u
		}
	}
	if !opening.Date.Equal(day("2026-02-02")) {
		t.Errorf("opening dated %s, want the day before the row of its account of the ledger", opening.Date.Format("2006-01-02"))
	}
	if len(p.Caveats) != 1 || p.Caveats[0].Kind != model.CaveatOpeningDate {
		t.Errorf("caveats = %+v, want the fallback to be told", p.Caveats)
	}

	// An account that has nothing else, and whose ledger account has nothing, goes to
	// the first row of the file.
	p = plan(
		row(model.TypeInitial, "Cash", "TWD", "7", on("0001-01-01")),
		row(model.TypeExpense, "Bank", "TWD", "-10", main_("Food", "Lunch"), on("2026-02-03")),
	)
	if got := p.Units[0].Date; !got.Equal(day("2026-02-02")) {
		t.Errorf("opening dated %s, want the day before the first row of the file", got.Format("2006-01-02"))
	}
}

func TestOpeningDateOfTheMappingIsTheDayOfEveryOpeningBalanceOfItsAccount(t *testing.T) {
	backdated := row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch"), on("2025-12-01"))
	for _, tt := range []struct {
		name    string
		fixed   bool
		extra   []model.Row
		want    string
		caveats []model.Caveat
	}{
		// Without the mapping, the day is by the rows, and moves when a row is dated back.
		{"by the rows", false, nil, "2026-01-19", []model.Caveat{{Kind: model.CaveatOpeningDate, Args: []string{model.OpeningFromLedgerAccount}}}},
		{"by the rows, one of them dated back", false, []model.Row{backdated}, "2025-11-30", []model.Caveat{{Kind: model.CaveatOpeningDate, Args: []string{model.OpeningFromLedgerAccount}}}},
		// With it, the day is the mapping's, however the rows are dated, and nothing is told.
		{"by the mapping", true, nil, "2026-01-01", nil},
		{"by the mapping, one of the rows dated back", true, []model.Row{backdated}, "2026-01-01", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m := testMapping()
			if tt.fixed {
				cash := m.Accounts["Cash"]
				cash.OpeningDate = day("2026-01-01")
				m.Accounts["Cash"] = cash
			}
			// The account has an opening balance in two currencies, and rows in one.
			rows := append([]model.Row{
				row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), at("")),
				row(model.TypeInitial, "Cash", "USD", "5", on("0001-01-01"), at("")),
				row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch"), on("2026-01-20")),
			}, tt.extra...)
			p := serviceBuild(rowsOf(rows...), m)

			var days []string
			for _, u := range p.Units {
				if u.Kind == model.KindOpening {
					days = append(days, u.Date.Format("2006-01-02"))
				}
			}
			if !reflect.DeepEqual(days, []string{tt.want, tt.want}) {
				t.Errorf("openings are dated %v, want both %s", days, tt.want)
			}
			var got []model.Caveat
			for _, c := range p.Caveats {
				if c.Kind == model.CaveatOpeningDate {
					c.Row = ""
					got = append(got, c)
				}
			}
			if !reflect.DeepEqual(got, tt.caveats) {
				t.Errorf("caveats = %+v, want %+v", got, tt.caveats)
			}
		})
	}
}

func TestAdjustmentIsNotRecordedWithTheRestAndKeepsItsBaseAmount(t *testing.T) {
	p := plan(
		row(model.TypeAdjustment, "Bank USD", "USD", "-2.5", name("fix")),
		row(model.TypeAdjustment, "Cash", "TWD", "40"),
		row(model.TypeAdjustment, "Cash", "TWD", "0"),
		row(model.TypeAdjustment, "Exchange BTC", "BTC", "0.5"),
	)
	if len(p.Units) != 0 {
		t.Fatalf("units = %+v, want none: an adjustment is reconciled when its month is confirmed", p.Units)
	}
	if len(p.Adjustments) != 3 || len(p.NoOps) != 1 || p.NoOps[0].Reason != model.NoOpZeroAdjustment {
		t.Fatalf("adjustments = %d, noops = %+v, want 3 and the zero one", len(p.Adjustments), p.NoOps)
	}
	usd := p.Adjustments[0]
	if usd.Account != "Bank" || usd.Instrument != "USD" || !usd.Amount.Equal(d("-2.5")) || usd.Base == nil || !usd.Base.Equal(d("75")) {
		t.Errorf("USD adjustment = %+v, want -2.5 USD with base 75", usd)
	}
	if got := p.Adjustments[1]; got.Base != nil {
		t.Errorf("TWD adjustment has a base %v", got.Base)
	}
	if got := p.Adjustments[2]; got.Base != nil || !got.Amount.Equal(d("0.5")) {
		t.Errorf("BTC adjustment = %+v: an instrument that is not a currency has only a quantity", got)
	}
	if usd.Key != model.KeyPrefix+usd.Row {
		t.Errorf("key = %q", usd.Key)
	}
}

func TestRowsTheLedgerCannotTakeAreSkippedWithTheirReason(t *testing.T) {
	p := plan(
		row(model.TypeExpense, "Cash", "TWD", "10"),                    // wrong sign
		row(model.TypeExpense, "Cash", "TWD", "0"),                     // nothing
		row(model.TypeExpense, "Cash", "TWD", "-1.234"),                // too many decimals for TWD
		row(model.TypeIncome, "Cash", "TWD", "-5", main_("收入", "薪水")),  // wrong sign
		row(model.RecordType("新類型"), "Cash", "TWD", "5"),               // unknown
		row(model.TypeReceivable, "Cash", "JPY", "10.5", party("Amy")), // yen has no decimals
	)
	want := []model.Reason{model.ReasonBadSign, model.ReasonZeroAmount, model.ReasonTooManyDecimals, model.ReasonBadSign,
		model.ReasonUnknownType, model.ReasonTooManyDecimals}
	if got := skippedReasons(p); !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if len(p.Units) != 0 {
		t.Errorf("units = %+v, want none", p.Units)
	}
}

func TestRowsWithNoPartyNoCategoryOrDayAreLeftOutWithTheirReason(t *testing.T) {
	m := testMapping()
	m.Expense["Food/Lunch"] = model.CategoryMap{Treatment: model.TreatmentDealing} // a dealing, and nobody to owe
	m.Expense["Shop/Books"] = model.CategoryMap{}                                  // no category either
	m.Income["收入/薪水"] = model.CategoryMap{}
	m.Income["收入/公司代收款"] = model.CategoryMap{Treatment: model.TreatmentDealing}
	rows := rowsOf(
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch")),
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Shop", "Books")),
		row(model.TypeIncome, "Cash", "TWD", "10", main_("收入", "薪水")),
		row(model.TypeIncome, "Cash", "TWD", "10", main_("收入", "公司代收款")),
		row(model.TypeReceivable, "Cash", "TWD", "10"), // no 對象
	)
	p := serviceBuild(rows, m)
	want := []model.Reason{model.ReasonNoParty, model.ReasonNoCategory, model.ReasonNoCategory, model.ReasonNoParty, model.ReasonNoParty}
	if got := skippedReasons(p); !reflect.DeepEqual(got, want) {
		t.Errorf("reasons = %v, want %v", got, want)
	}
	if len(p.Units) != 0 {
		t.Errorf("units = %+v, want none", p.Units)
	}

	// An opening balance has no day to be put before when the file has no day at all.
	p = plan(row(model.TypeInitial, "Elsewhere", "TWD", "50", on("0001-01-01")))
	if got := skippedReasons(p); !reflect.DeepEqual(got, []model.Reason{model.ReasonNoDate}) {
		t.Errorf("reasons = %v, want the opening left out for its day", got)
	}
}

func TestInvestmentAndBrokerageRowsAreRecordedAsWhatTheyAreAndTold(t *testing.T) {
	p := plan(
		row(model.TypeExpense, "Broker", "TWD", "-2000", main_("Self", "投資")),
		row(model.TypeIncome, "Broker", "TWD", "24", main_("收入", "投資")),
		row(model.TypeExpense, "Cash", "TWD", "-10", main_("Food", "Lunch")),
	)
	if len(p.Units) != 3 {
		t.Fatalf("units = %d, want all three recorded", len(p.Units))
	}
	counts := map[model.CaveatKind]int{}
	for _, c := range p.Caveats {
		counts[c.Kind]++
	}
	if counts[model.CaveatInvestment] != 2 || counts[model.CaveatBrokerage] != 2 {
		t.Errorf("caveats = %v, want two investments and two rows of the brokerage", counts)
	}
}

func TestOverridesChangeASingleRow(t *testing.T) {
	rows := rowsOf(
		row(model.TypeIncome, "Bank", "TWD", "1200", main_("收入", "收款")),
		row(model.TypeIncome, "Bank", "TWD", "900", main_("收入", "收款")),
		row(model.TypeIncome, "Bank", "TWD", "700", main_("收入", "收款")),
		row(model.TypeExpense, "Cash", "TWD", "-20", main_("Food", "Lunch")),
	)
	m := testMapping()
	m.Rows = map[string]model.RowOverride{
		rows[0].Key:     {Treatment: model.TreatmentDealing, Party: "Amy"},
		rows[1].Key:     {Treatment: model.TreatmentIncome, Category: "Salary", Tag: "checked"},
		rows[2].Key:     {Treatment: model.TreatmentSkip},
		"no-such-row-1": {Treatment: model.TreatmentSkip},
	}
	p := serviceBuild(rows, m)

	if len(p.Units) != 3 {
		t.Fatalf("units = %d, want the dealing, the income and the expense", len(p.Units))
	}
	if u := p.Units[0]; u.Kind != model.KindDealing || u.Party != "Amy" || u.Direction != "received" {
		t.Errorf("first = %+v, want what Amy gave back", u)
	}
	if u := p.Units[1]; u.Kind != model.KindIncome || u.Lines[0].Category != "Salary" || !reflect.DeepEqual(u.Tags, []string{"待確認", "checked"}) {
		t.Errorf("second = %+v, want an income of Salary with both tags", u)
	}
	if got := skippedReasons(p); !reflect.DeepEqual(got, []model.Reason{model.ReasonSkipped}) {
		t.Errorf("skipped = %v", got)
	}
	wantWarning := model.Warning{Code: model.WarningRowUnknown, Subject: "rows.no-such-row-1"}
	if len(p.Warnings) != 1 || !reflect.DeepEqual(p.Warnings[0], wantWarning) {
		t.Errorf("warnings = %+v, want %+v for the key that no row has", p.Warnings, wantWarning)
	}
	if len(p.Unrecorded) != 1 || !p.Unrecorded[0].Quantity.Equal(d("700")) {
		t.Errorf("unrecorded = %+v, want the effect of the skipped row", p.Unrecorded)
	}
}

func TestOverrideThatDoesNotFitTheRowIsAProblem(t *testing.T) {
	rows := rowsOf(row(model.TypeExpense, "Cash", "TWD", "-20", main_("Food", "Lunch")))
	m := testMapping()
	m.Rows = map[string]model.RowOverride{rows[0].Key: {Treatment: model.TreatmentReward}}
	p := serviceBuild(rows, m)
	want := model.Problem{Code: model.ProblemOverrideNotFit, Subject: "rows." + rows[0].Key, Args: []string{model.TreatmentReward, string(model.TypeExpense)}}
	if len(p.Problems) != 1 || !reflect.DeepEqual(p.Problems[0], want) {
		t.Fatalf("problems = %+v, want %+v: the reward that a 支出 cannot be", p.Problems, want)
	}
}

func TestPlanWithAnEmptyMappingGuessesEverythingAndIsBlocked(t *testing.T) {
	p := serviceBuild(rowsOf(
		row(model.TypeExpense, "Card A 信用卡", "TWD", "-20", main_("Food", "Lunch"), project("Daily")),
	), model.Mapping{})
	if !p.Blocked() || p.Guessed.Empty() {
		t.Fatalf("plan is not blocked: guessed = %+v", p.Guessed)
	}
	if got := p.Guessed.Accounts["Card A 信用卡"]; got.Kind != "card" || got.Name != "Card A 信用卡" {
		t.Errorf("guessed account = %+v", got)
	}
	if got := p.Guessed.Projects["Daily"]; got.Kind != model.ProjectArea {
		t.Errorf("guessed project = %+v", got)
	}
	if got := p.Guessed.Expense["Food/Lunch"].Category; got != "Food/Lunch" {
		t.Errorf("guessed category = %q", got)
	}
	if p.Guessed.Defaults.FeeCategory == "" {
		t.Error("the default categories are not guessed")
	}
	if len(p.Units) != 1 {
		t.Errorf("the plan is still made with the guesses: units = %d", len(p.Units))
	}

	// With the guesses written down there is nothing left to guess.
	again := serviceBuild(p.Rows, p.Mapping)
	if again.Blocked() {
		t.Errorf("plan still blocked once the guesses are the mapping: %+v %v", again.Guessed, again.Problems)
	}
}
