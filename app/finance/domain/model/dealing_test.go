package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// newParty returns the id of a party: the model knows a party by its id only.
func newParty() uuid.UUID {
	return uuid.Must(uuid.NewV7())
}

func TestNewDealingPaidOutMakesWhatThePartyOwesMeGrow(t *testing.T) {
	bank := newCash(t, "銀行")
	twd := newTWD(t)
	company := newParty()
	names := map[uuid.UUID]string{bank.ID: "銀行", twd.ID: "TWD", company: "公司"}

	// I paid 5,000 for the company, or lent it.
	txn, err := model.NewDealing(model.DealingParams{
		Header: header(testKey), Account: bank, PartyID: company, Instrument: twd,
		Amount: dec("5000"), Direction: model.DealingPaidOut,
	})
	if err != nil {
		t.Fatalf("NewDealing: %v", err)
	}

	wantPostings(t, txn, names,
		"account(銀行) -5000 TWD",
		"dealing(公司) +5000 TWD",
	)
	if txn.Kind != model.KindDealing || txn.Status != model.StatusDraft {
		t.Errorf("kind %q, status %q, want a dealing that is a draft", txn.Kind, txn.Status)
	}
}

func TestNewDealingReceivedMakesWhatThePartyOwesMeShrink(t *testing.T) {
	bank := newCash(t, "銀行")
	twd := newTWD(t)
	company := newParty()
	names := map[uuid.UUID]string{bank.ID: "銀行", twd.ID: "TWD", company: "公司"}

	// The company repays 5,000.
	txn, err := model.NewDealing(model.DealingParams{
		Header: header(testKey), Account: bank, PartyID: company, Instrument: twd,
		Amount: dec("5000"), Direction: model.DealingReceived,
	})
	if err != nil {
		t.Fatalf("NewDealing: %v", err)
	}

	wantPostings(t, txn, names,
		"account(銀行) +5000 TWD",
		"dealing(公司) -5000 TWD",
	)
}

func TestNewDealingInAForeignCurrencyHasNoBaseAmount(t *testing.T) {
	wallet := newCash(t, "錢包")
	usdt, err := model.NewInstrument(model.NewInstrumentParams{
		ID: uuid.Must(uuid.NewV7()), Code: "USDT", Name: "Tether", Kind: model.InstrumentKindCrypto, IsCurrency: true, Decimals: 6,
	})
	if err != nil {
		t.Fatalf("NewInstrument: %v", err)
	}
	friend := newParty()
	names := map[uuid.UUID]string{wallet.ID: "錢包", usdt.ID: "USDT", friend: "朋友"}

	txn, err := model.NewDealing(model.DealingParams{
		Header: header(testKey), Account: wallet, PartyID: friend, Instrument: usdt,
		Amount: dec("12.5"), Direction: model.DealingPaidOut,
	})
	if err != nil {
		t.Fatalf("NewDealing: %v", err)
	}

	// A dealing is counted in the currency of the two sides, not in the base
	// currency: neither posting has a base amount.
	wantPostings(t, txn, names,
		"account(錢包) -12.5 USDT",
		"dealing(朋友) +12.5 USDT",
	)
}

func TestNewDealingRefuses(t *testing.T) {
	bank := newCash(t, "銀行")
	twd := newTWD(t)
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	company := newParty()
	valid := func() model.DealingParams {
		return model.DealingParams{
			Header: header(testKey), Account: bank, PartyID: company, Instrument: twd,
			Amount: dec("5000"), Direction: model.DealingPaidOut,
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.DealingParams)
		want   error
	}{
		{"an instrument that is not a currency", func(p *model.DealingParams) { p.Instrument = btc }, model.ErrNotCurrency},
		{"a direction that is neither", func(p *model.DealingParams) { p.Direction = "lend" }, model.ErrInvalidDealingDirection},
		{"no direction", func(p *model.DealingParams) { p.Direction = "" }, model.ErrInvalidDealingDirection},
		{"a zero amount", func(p *model.DealingParams) { p.Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(p *model.DealingParams) { p.Amount = dec("-5000") }, model.ErrAmountNotPositive},
		{"more decimals than the currency has", func(p *model.DealingParams) { p.Amount = dec("5000.001") }, model.ErrTooManyDecimals},
		{"an archived account", func(p *model.DealingParams) { p.Account = archivedAccount(bank) }, model.ErrArchived},
		{"an archived currency", func(p *model.DealingParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			txn, err := model.NewDealing(p)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if txn.ID != uuid.Nil || len(txn.Postings) != 0 {
				t.Errorf("transaction = %+v, want nothing made", txn)
			}
		})
	}
}

func TestNewReward(t *testing.T) {
	card := newCash(t, "信用卡")
	twd := newTWD(t)
	names := map[uuid.UUID]string{card.ID: "卡", twd.ID: "TWD"}

	// The card gives 50 back. It is not an income, and it takes no expense down.
	txn, err := model.NewReward(model.RewardParams{
		Header: header(testKey), Account: card, Instrument: twd, Amount: dec("50"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}

	wantPostings(t, txn, names,
		"account(卡) +50 TWD",
		"reward -50 TWD, base -50",
	)
	if txn.Kind != model.KindReward || txn.Status != model.StatusDraft {
		t.Errorf("kind %q, status %q, want a reward that is a draft", txn.Kind, txn.Status)
	}
}

func TestNewRewardInAForeignCurrencyKeepsTheBaseAmountOfTheCaller(t *testing.T) {
	card := newCash(t, "Ether.fi 卡")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	names := map[uuid.UUID]string{card.ID: "卡", usd.ID: "USD", twd.ID: "TWD"}

	txn, err := model.NewReward(model.RewardParams{
		Header: header(testKey), Account: card, Instrument: usd, BaseCurrency: twd,
		Amount: dec("5"), BaseAmount: decPtr("160"),
	})
	if err != nil {
		t.Fatalf("NewReward: %v", err)
	}

	// The base amount is given as a positive amount and takes the sign of its posting.
	wantPostings(t, txn, names,
		"account(卡) +5 USD",
		"reward -5 USD, base -160",
	)
}

func TestNewRewardRefuses(t *testing.T) {
	card := newCash(t, "信用卡")
	usd := newCurrency(t, "USD", 2)
	twd := newTWD(t)
	valid := func() model.RewardParams {
		return model.RewardParams{Header: header(testKey), Account: card, Instrument: twd, BaseCurrency: twd, Amount: dec("50")}
	}

	tests := []struct {
		name   string
		mutate func(p *model.RewardParams)
		want   error
	}{
		{"a zero amount", func(p *model.RewardParams) { p.Amount = dec("0") }, model.ErrAmountNotPositive},
		{"a negative amount", func(p *model.RewardParams) { p.Amount = dec("-50") }, model.ErrAmountNotPositive},
		{"more decimals than the instrument has", func(p *model.RewardParams) { p.Amount = dec("50.005") }, model.ErrTooManyDecimals},
		{"no base amount for a foreign currency", func(p *model.RewardParams) { p.Instrument = usd }, model.ErrBaseAmountRequired},
		{"a base amount that is not the amount, for the base currency", func(p *model.RewardParams) {
			p.BaseAmount = decPtr("51")
		}, model.ErrBaseAmountMismatch},
		{"a zero base amount", func(p *model.RewardParams) {
			p.Instrument = usd
			p.BaseAmount = decPtr("0")
		}, model.ErrBaseAmountNotPositive},
		{"an archived account", func(p *model.RewardParams) { p.Account = archivedAccount(card) }, model.ErrArchived},
		{"an archived instrument", func(p *model.RewardParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			if _, err := model.NewReward(p); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
