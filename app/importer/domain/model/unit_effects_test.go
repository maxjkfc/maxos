package model_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func TestUnitEffectsAreWhatTheLedgerWillDoToTheBalances(t *testing.T) {
	// landing is where one effect of a unit falls, and how much it comes to.
	type landing struct {
		account, instrument string
		day                 time.Time
		quantity            string
	}
	num := decimal.RequireFromString
	opened := time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	feb15 := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	mar10 := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	mar15 := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)
	feeOf := func(account, instrument, amount string) model.Fee {
		return model.Fee{Account: account, Instrument: instrument, Category: "Fees", Amount: num(amount)}
	}
	leg := func(account, instrument, amount string) model.Leg {
		return model.Leg{Account: account, Instrument: instrument, Amount: num(amount)}
	}
	other := func(account, instrument, amount string) *model.Leg {
		l := leg(account, instrument, amount)
		return &l
	}
	interest := num("6")

	for _, tt := range []struct {
		name string
		unit model.Unit
		want []landing
	}{
		{
			name: "an expense takes what its lines come to, fees among them",
			unit: model.Unit{Kind: model.KindExpense, Date: mar10, Main: leg("Card", "TWD", "122")},
			want: []landing{{"Card", "TWD", mar10, "-122"}},
		},
		{
			name: "an expense paid by the reward of its discount takes the price and gives the discount back",
			unit: model.Unit{
				Kind: model.KindExpenseReward, Date: mar10, Main: leg("Card", "TWD", "103"),
				Reward: &model.RewardPart{Amount: num("100")},
			},
			want: []landing{{"Card", "TWD", mar10, "-103"}, {"Card", "TWD", mar10, "100"}},
		},
		{
			name: "an income brings in its amount and pays its fee",
			unit: model.Unit{Kind: model.KindIncome, Date: mar10, Main: leg("Bank", "TWD", "500"), Fees: []model.Fee{feeOf("Bank", "TWD", "2")}},
			want: []landing{{"Bank", "TWD", mar10, "500"}, {"Bank", "TWD", mar10, "-2"}},
		},
		{
			name: "a reward brings in its amount and pays its fee from another account in another instrument",
			unit: model.Unit{Kind: model.KindReward, Date: mar10, Main: leg("Card", "TWD", "30"), Fees: []model.Fee{feeOf("Exchange", "USDT", "0.5")}},
			want: []landing{{"Card", "TWD", mar10, "30"}, {"Exchange", "USDT", mar10, "-0.5"}},
		},
		{
			name: "a dealing paid out takes its amount",
			unit: model.Unit{Kind: model.KindDealing, Direction: "paid_out", Date: mar10, Main: leg("Cash", "TWD", "300"), Fees: []model.Fee{feeOf("Cash", "TWD", "5")}},
			want: []landing{{"Cash", "TWD", mar10, "-300"}, {"Cash", "TWD", mar10, "-5"}},
		},
		{
			name: "a dealing received brings in its amount",
			unit: model.Unit{Kind: model.KindDealing, Direction: "received", Date: mar10, Main: leg("Cash", "TWD", "100")},
			want: []landing{{"Cash", "TWD", mar10, "100"}},
		},
		{
			name: "a dealing that goes neither way does nothing to its account but pay its fee",
			unit: model.Unit{Kind: model.KindDealing, Date: mar10, Main: leg("Cash", "TWD", "100"), Fees: []model.Fee{feeOf("Cash", "TWD", "5")}},
			want: []landing{{"Cash", "TWD", mar10, "-5"}},
		},
		{
			name: "a transfer takes from one account and gives to the other, each side with its fee",
			unit: model.Unit{
				Kind: model.KindTransfer, Date: mar10, Main: leg("Bank", "TWD", "1000"), Other: other("Card", "TWD", "1000"),
				Fees: []model.Fee{feeOf("Bank", "TWD", "15"), feeOf("Card", "TWD", "3")},
			},
			want: []landing{
				{"Bank", "TWD", mar10, "-1000"}, {"Card", "TWD", mar10, "1000"},
				{"Bank", "TWD", mar10, "-15"}, {"Card", "TWD", mar10, "-3"},
			},
		},
		{
			name: "a transfer with no second leg has only the first",
			unit: model.Unit{Kind: model.KindTransfer, Date: mar10, Main: leg("Bank", "TWD", "1000")},
			want: []landing{{"Bank", "TWD", mar10, "-1000"}},
		},
		{
			name: "an exchange gives up one instrument and takes the other",
			unit: model.Unit{
				Kind: model.KindExchange, Date: mar10, Main: leg("Bank", "TWD", "3000"), Other: other("Bank", "USD", "100"),
				Fees: []model.Fee{feeOf("Bank", "TWD", "10")},
			},
			want: []landing{{"Bank", "TWD", mar10, "-3000"}, {"Bank", "USD", mar10, "100"}, {"Bank", "TWD", mar10, "-10"}},
		},
		{
			name: "a buy puts the asset in its account and takes the cash out of the other",
			unit: model.Unit{
				Kind: model.KindTrade, Direction: "buy", Date: mar10, Main: leg("Exchange", "BTC", "0.002"), Other: other("Exchange", "USDT", "100"),
				Fees: []model.Fee{feeOf("Exchange", "USDT", "0.1")},
			},
			want: []landing{{"Exchange", "BTC", mar10, "0.002"}, {"Exchange", "USDT", mar10, "-100"}, {"Exchange", "USDT", mar10, "-0.1"}},
		},
		{
			name: "a sell takes the asset out of its account and brings the cash into the other",
			unit: model.Unit{Kind: model.KindTrade, Direction: "sell", Date: mar10, Main: leg("Exchange", "BTC", "0.001"), Other: other("Bank", "USD", "50")},
			want: []landing{{"Exchange", "BTC", mar10, "-0.001"}, {"Bank", "USD", mar10, "50"}},
		},
		{
			name: "an opening balance is what the account holds from its day, owing or not",
			unit: model.Unit{Kind: model.KindOpening, Date: opened, Main: leg("Card", "TWD", "-800")},
			want: []landing{{"Card", "TWD", opened, "-800"}},
		},
		{
			name: "a bill instalment pays the debt off on its day and charges each period on the day it falls due",
			unit: model.Unit{
				Kind: model.KindBillInstalment, Date: opened, Main: leg("Bank", "TWD", "600"),
				Periods: []model.Period{
					{DueOn: feb15, Principal: num("200"), Interest: &interest},
					{DueOn: mar15, Principal: num("400")},
				},
				Fees: []model.Fee{feeOf("Bank", "TWD", "5")},
			},
			want: []landing{
				{"Bank", "TWD", opened, "600"},
				{"Bank", "TWD", feb15, "-206"},
				{"Bank", "TWD", mar15, "-400"},
				{"Bank", "TWD", opened, "-5"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.unit.Effects()
			if len(got) != len(tt.want) {
				t.Fatalf("Effects = %+v, want %d effects: %+v", got, len(tt.want), tt.want)
			}
			for i, w := range tt.want {
				e := got[i]
				if e.Account != w.account || e.Instrument != w.instrument || !e.Date.Equal(w.day) || !e.Quantity.Equal(num(w.quantity)) {
					t.Errorf("Effects[%d] = %s %s %s %s, want %s %s %s %s", i,
						e.Account, e.Instrument, e.Date.Format(time.DateOnly), e.Quantity,
						w.account, w.instrument, w.day.Format(time.DateOnly), w.quantity)
				}
				if e.Row != "" {
					t.Errorf("Effects[%d] has the row %q: the effect of a unit is not a row's", i, e.Row)
				}
			}
		})
	}
}
