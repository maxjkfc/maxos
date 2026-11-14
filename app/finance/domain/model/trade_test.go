package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// tradeFixtures are what the trades below are made of: a bank that holds the
// currency, a wallet that holds the instrument, and the instruments.
type tradeFixtures struct {
	bank, wallet model.Account
	twd, usd     model.Instrument
	btc, stock   model.Instrument
	fees         model.Category
	income       model.Category
	names        map[uuid.UUID]string
	params       func(direction model.TradeDirection) model.TradeParams
}

func newTradeFixtures(t *testing.T) tradeFixtures {
	t.Helper()
	f := tradeFixtures{
		bank:   newCash(t, "銀行"),
		wallet: newCash(t, "錢包"),
		twd:    newTWD(t),
		usd:    newCurrency(t, "USD", 2),
		btc:    newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8),
		stock:  newNonCurrency(t, "2330.TW", model.InstrumentKindStock, 0),
		fees:   topLevel(t, model.DirectionExpense, "手續費"),
		income: topLevel(t, model.DirectionIncome, "薪水"),
	}
	f.names = map[uuid.UUID]string{
		f.bank.ID: "銀行", f.wallet.ID: "錢包", f.twd.ID: "TWD", f.usd.ID: "USD",
		f.btc.ID: "BTC", f.stock.ID: "2330.TW", f.fees.ID: "手續費",
	}
	// 0.1 BTC for 300,000 TWD, bought from the bank into the wallet or sold the other way.
	f.params = func(direction model.TradeDirection) model.TradeParams {
		return model.TradeParams{
			Header: header(testKey), Direction: direction,
			Account: f.wallet, Instrument: f.btc, Quantity: dec("0.1"),
			CashAccount: f.bank, Currency: f.twd, Amount: dec("300000"), BaseCurrency: f.twd,
		}
	}
	return f
}

func TestNewTradeBuyingTakesTheCurrencyOutOfTheCashAccountAndPutsTheInstrumentIntoTheOther(t *testing.T) {
	f := newTradeFixtures(t)

	txn, err := model.NewTrade(f.params(model.TradeBuy))
	if err != nil {
		t.Fatalf("NewTrade: %v", err)
	}

	// A conversion posting for each of the two instruments keeps each of them
	// balanced by itself, as in an exchange.
	wantPostings(t, txn, f.names,
		"account(銀行) -300000 TWD",
		"conversion +300000 TWD",
		"conversion -0.1 BTC",
		"account(錢包) +0.1 BTC",
	)
	if txn.Kind != model.KindTrade || txn.Status != model.StatusDraft {
		t.Errorf("kind %q, status %q, want a draft trade", txn.Kind, txn.Status)
	}
	if txn.PlanID != nil || txn.PlanSeq != nil {
		t.Errorf("plan %v, position %v, want a trade to be in no plan", txn.PlanID, txn.PlanSeq)
	}
}

func TestNewTradeSellingIsTheMirrorImageOfBuying(t *testing.T) {
	f := newTradeFixtures(t)

	txn, err := model.NewTrade(f.params(model.TradeSell))
	if err != nil {
		t.Fatalf("NewTrade: %v", err)
	}

	wantPostings(t, txn, f.names,
		"account(錢包) -0.1 BTC",
		"conversion +0.1 BTC",
		"conversion -300000 TWD",
		"account(銀行) +300000 TWD",
	)
	if txn.Kind != model.KindTrade {
		t.Errorf("kind %q, want a trade: selling is not an income", txn.Kind)
	}
}

func TestNewTradeTakesAStockInWholeSharesAndAnAccountThatHoldsBoth(t *testing.T) {
	f := newTradeFixtures(t)
	p := f.params(model.TradeBuy)
	p.Account, p.CashAccount = f.bank, f.bank // a brokerage account holds the cash and the shares
	p.Instrument, p.Quantity, p.Amount = f.stock, dec("1000"), dec("589000")

	txn, err := model.NewTrade(p)
	if err != nil {
		t.Fatalf("NewTrade: %v", err)
	}

	wantPostings(t, txn, f.names,
		"account(銀行) -589000 TWD",
		"conversion +589000 TWD",
		"conversion -1000 2330.TW",
		"account(銀行) +1000 2330.TW",
	)

	p.Quantity = dec("0.5")
	if _, err := model.NewTrade(p); !errors.Is(err, model.ErrTooManyDecimals) {
		t.Errorf("half a share: err = %v, want ErrTooManyDecimals", err)
	}
}

func TestNewTradePaidInAForeignCurrencyAndWithAFeeKeepsTheFeeOutOfTheTrade(t *testing.T) {
	f := newTradeFixtures(t)
	p := f.params(model.TradeBuy)
	p.Currency, p.Amount = f.usd, dec("9500")
	p.Fees = []model.Fee{
		{Account: f.bank, Instrument: f.usd, Category: f.fees, Amount: dec("9.5"), BaseAmount: decPtr("304")},
		{Account: f.bank, Instrument: f.twd, Category: f.fees, Amount: dec("15")},
	}

	txn, err := model.NewTrade(p)
	if err != nil {
		t.Fatalf("NewTrade: %v", err)
	}

	wantPostings(t, txn, f.names,
		"account(銀行) -9500 USD",
		"conversion +9500 USD",
		"conversion -0.1 BTC",
		"account(錢包) +0.1 BTC",
		"account(銀行) -9.5 USD",
		"expense(手續費) +9.5 USD, base +304",
		"account(銀行) -15 TWD",
		"expense(手續費) +15 TWD, base +15",
	)
	if txn.Kind != model.KindTrade {
		t.Errorf("kind %q, want a trade", txn.Kind)
	}
}

func TestNewTradeRefusesWhatIsNotABuyOrASaleOfSomethingThatIsNotACurrency(t *testing.T) {
	f := newTradeFixtures(t)
	tests := []struct {
		name   string
		change func(p *model.TradeParams)
		want   error
	}{
		{"a currency as what is traded", func(p *model.TradeParams) { p.Instrument, p.Quantity = f.usd, dec("100") }, model.ErrIsCurrency},
		{"the base currency as what is traded", func(p *model.TradeParams) { p.Instrument, p.Quantity = f.twd, dec("100") }, model.ErrIsCurrency},
		{"a payment in something that is not a currency", func(p *model.TradeParams) { p.Currency = f.btc }, model.ErrNotCurrency},
		{"one instrument that is not a currency for another", func(p *model.TradeParams) {
			p.Instrument, p.Quantity = f.stock, dec("10")
			p.Currency = f.btc
		}, model.ErrNotCurrency},
		{"a direction that is neither", func(p *model.TradeParams) { p.Direction = "hold" }, model.ErrInvalidTradeDirection},
		{"no direction", func(p *model.TradeParams) { p.Direction = "" }, model.ErrInvalidTradeDirection},
		{"a quantity of nothing", func(p *model.TradeParams) { p.Quantity = dec("0") }, model.ErrAmountNotPositive},
		{"a negative quantity", func(p *model.TradeParams) { p.Quantity = dec("-0.1") }, model.ErrAmountNotPositive},
		{"an amount of nothing", func(p *model.TradeParams) { p.Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(p *model.TradeParams) { p.Amount = dec("-300000") }, model.ErrAmountNotPositive},
		{"a quantity finer than the instrument", func(p *model.TradeParams) { p.Quantity = dec("0.123456789") }, model.ErrTooManyDecimals},
		{"an amount finer than the currency", func(p *model.TradeParams) { p.Amount = dec("300000.001") }, model.ErrTooManyDecimals},
		{"an archived account", func(p *model.TradeParams) { p.Account = archivedAccount(f.wallet) }, model.ErrArchived},
		{"an archived cash account", func(p *model.TradeParams) { p.CashAccount = archivedAccount(f.bank) }, model.ErrArchived},
		{"an archived instrument", func(p *model.TradeParams) { p.Instrument = archivedInstrument(f.btc) }, model.ErrArchived},
		{"an archived currency", func(p *model.TradeParams) { p.Currency = archivedInstrument(f.twd) }, model.ErrArchived},
		{"a fee booked to an income category", func(p *model.TradeParams) {
			p.Fees = []model.Fee{{Account: f.bank, Instrument: f.twd, Category: f.income, Amount: dec("15")}}
		}, model.ErrWrongCategoryDirection},
	}
	for _, direction := range []model.TradeDirection{model.TradeBuy, model.TradeSell} {
		for _, tt := range tests {
			t.Run(string(direction)+" "+tt.name, func(t *testing.T) {
				p := f.params(direction)
				tt.change(&p)

				txn, err := model.NewTrade(p)

				if !errors.Is(err, tt.want) {
					t.Fatalf("err = %v, want %v", err, tt.want)
				}
				if txn.ID != uuid.Nil || len(txn.Postings) != 0 {
					t.Errorf("transaction = %+v, want nothing made of a trade that is refused", txn)
				}
			})
		}
	}
}
