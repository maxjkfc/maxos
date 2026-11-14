package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

func TestReconcileMakesUpForTheDifferenceInTheWayOfTheInstrument(t *testing.T) {
	cash := newCash(t, "現金")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	names := map[uuid.UUID]string{
		cash.ID: "現金", twd.ID: "TWD", usd.ID: "USD", btc.ID: "BTC",
		model.UnrecordedExpenseCategoryID: "未記錄", model.UnrecordedIncomeCategoryID: "未記錄",
	}

	tests := []struct {
		name       string
		instrument model.Instrument
		recorded   string
		actual     string
		base       *decimal.Decimal
		difference string
		postings   []string
	}{
		{
			name: "a currency, below what the ledger says by 120", instrument: twd,
			recorded: "1000", actual: "880", difference: "-120",
			postings: []string{"account(現金) -120 TWD", "expense(未記錄) +120 TWD, base +120"},
		},
		{
			name: "a currency, above what the ledger says by 80", instrument: twd,
			recorded: "1000", actual: "1080", difference: "80",
			postings: []string{"account(現金) +80 TWD", "income(未記錄) -80 TWD, base -80"},
		},
		{
			name: "an instrument that is not a currency, above by 1.5", instrument: btc,
			recorded: "1", actual: "2.5", difference: "1.5",
			postings: []string{"account(現金) +1.5 BTC", "adjustment -1.5 BTC"},
		},
		{
			name: "an instrument that is not a currency, below by 0.75", instrument: btc,
			recorded: "1", actual: "0.25", difference: "-0.75",
			postings: []string{"account(現金) -0.75 BTC", "adjustment +0.75 BTC"},
		},
		{
			name: "a foreign currency, below, with the base amount of the day", instrument: usd, base: decPtr("160"),
			recorded: "100", actual: "95", difference: "-5",
			postings: []string{"account(現金) -5 USD", "expense(未記錄) +5 USD, base +160"},
		},
		{
			name: "a foreign currency, above, with the base amount of the day", instrument: usd, base: decPtr("160"),
			recorded: "100", actual: "105", difference: "5",
			postings: []string{"account(現金) +5 USD", "income(未記錄) -5 USD, base -160"},
		},
		{
			name: "an account that owes more than the ledger says", instrument: twd,
			recorded: "-5000", actual: "-5200", difference: "-200",
			postings: []string{"account(現金) -200 TWD", "expense(未記錄) +200 TWD, base +200"},
		},
		{
			name: "an account that owes less than the ledger says, past zero", instrument: twd,
			recorded: "-100", actual: "50", difference: "150",
			postings: []string{"account(現金) +150 TWD", "income(未記錄) -150 TWD, base -150"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.Reconcile(model.ReconcileParams{
				Header: header(testKey), Account: cash, Instrument: tt.instrument, BaseCurrency: twd,
				RecordedBalance: dec(tt.recorded), ActualBalance: dec(tt.actual), DifferenceBaseAmount: tt.base,
			})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			if !got.Difference.Equal(dec(tt.difference)) {
				t.Errorf("difference = %s, want %s: the actual balance minus the recorded one", got.Difference, tt.difference)
			}
			if got.Transaction == nil {
				t.Fatal("Transaction is nil, want the draft that makes up for the difference")
			}
			wantPostings(t, *got.Transaction, names, tt.postings...)
			if got.Transaction.Kind != model.KindReconciliation || got.Transaction.Status != model.StatusDraft {
				t.Errorf("kind %q, status %q, want a reconciliation that is a draft", got.Transaction.Kind, got.Transaction.Status)
			}
		})
	}
}

func TestReconcileMakesNothingWhenThereIsNoDifference(t *testing.T) {
	cash := newCash(t, "現金")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)

	for _, tt := range []struct {
		name       string
		instrument model.Instrument
		base       *decimal.Decimal
	}{
		{"the base currency", twd, nil},
		{"a foreign currency, which needs no base amount for nothing", usd, nil},
		{"a base amount that is not looked at", twd, decPtr("12345")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.Reconcile(model.ReconcileParams{
				Header: header(testKey), Account: cash, Instrument: tt.instrument, BaseCurrency: twd,
				RecordedBalance: dec("1000.50"), ActualBalance: dec("1000.5"), DifferenceBaseAmount: tt.base,
			})
			if err != nil {
				t.Fatalf("Reconcile: %v", err)
			}

			if got.Transaction != nil || !got.Difference.IsZero() {
				t.Errorf("reconciliation = %+v, want no difference and no transaction", got)
			}
		})
	}
}

func TestReconcileOfAForeignCurrencyAsksForTheBaseAmountAndSaysWhatToAskItFor(t *testing.T) {
	cash := newCash(t, "現金")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)

	_, err := model.Reconcile(model.ReconcileParams{
		Header: header(testKey), Account: cash, Instrument: usd, BaseCurrency: twd,
		RecordedBalance: dec("100"), ActualBalance: dec("87.25"),
	})

	var need *model.DifferenceBaseAmountRequiredError
	if !errors.As(err, &need) {
		t.Fatalf("err = %v, want a *DifferenceBaseAmountRequiredError", err)
	}
	if !need.Difference.Equal(dec("-12.75")) || need.InstrumentCode != "USD" {
		t.Errorf("error = %+v, want the difference -12.75 and the code USD, to ask again with", need)
	}
	if !errors.Is(err, model.ErrBaseAmountRequired) {
		t.Errorf("err = %v, want it to be ErrBaseAmountRequired as well", err)
	}
}

func TestReconcileRefuses(t *testing.T) {
	cash := newCash(t, "現金")
	twd := newTWD(t)
	usd := newCurrency(t, "USD", 2)
	btc := newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8)
	valid := func() model.ReconcileParams {
		return model.ReconcileParams{
			Header: header(testKey), Account: cash, Instrument: twd, BaseCurrency: twd,
			RecordedBalance: dec("1000"), ActualBalance: dec("880"),
		}
	}

	tests := []struct {
		name   string
		mutate func(p *model.ReconcileParams)
		want   error
	}{
		{"a base amount for an instrument that is not a currency", func(p *model.ReconcileParams) {
			p.Instrument = btc
			p.RecordedBalance, p.ActualBalance = dec("1"), dec("2")
			p.DifferenceBaseAmount = decPtr("100")
		}, model.ErrBaseAmountNotAllowed},
		{"a base amount that is not the difference, for the base currency", func(p *model.ReconcileParams) {
			p.DifferenceBaseAmount = decPtr("121")
		}, model.ErrBaseAmountMismatch},
		{"a zero base amount for a foreign currency", func(p *model.ReconcileParams) {
			p.Instrument = usd
			p.DifferenceBaseAmount = decPtr("0")
		}, model.ErrBaseAmountNotPositive},
		{"a negative base amount for a foreign currency", func(p *model.ReconcileParams) {
			p.Instrument = usd
			p.DifferenceBaseAmount = decPtr("-3200")
		}, model.ErrBaseAmountNotPositive},
		{"a base amount finer than the base currency", func(p *model.ReconcileParams) {
			p.Instrument = usd
			p.DifferenceBaseAmount = decPtr("3200.123")
		}, model.ErrTooManyDecimals},
		{"an actual balance finer than the instrument", func(p *model.ReconcileParams) { p.ActualBalance = dec("880.123") }, model.ErrTooManyDecimals},
		{"an archived account", func(p *model.ReconcileParams) { p.Account = archivedAccount(cash) }, model.ErrArchived},
		{"an archived instrument", func(p *model.ReconcileParams) { p.Instrument = archivedInstrument(twd) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid()
			tt.mutate(&p)

			got, err := model.Reconcile(p)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if got.Transaction != nil {
				t.Errorf("transaction = %+v, want nothing made", got.Transaction)
			}
		})
	}
}

func TestReconcileRefusesAnArchivedAccountEvenWithoutADifference(t *testing.T) {
	cash := archivedAccount(newCash(t, "現金"))
	twd := newTWD(t)

	_, err := model.Reconcile(model.ReconcileParams{
		Header: header(testKey), Account: cash, Instrument: twd,
		RecordedBalance: dec("1000"), ActualBalance: dec("1000"),
	})

	if !errors.Is(err, model.ErrArchived) {
		t.Fatalf("err = %v, want ErrArchived: nothing is reconciled on an account that is not in use", err)
	}
}
