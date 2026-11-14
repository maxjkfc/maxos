package model_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

func newInstrument(kind model.InstrumentKind, isCurrency bool, decimals int) (model.Instrument, error) {
	return model.NewInstrument(model.NewInstrumentParams{
		ID:         uuid.Must(uuid.NewV7()),
		Code:       "USDT",
		Name:       "Tether",
		Kind:       kind,
		IsCurrency: isCurrency,
		Decimals:   decimals,
	})
}

func TestNewInstrumentNormalisesCodeAndName(t *testing.T) {
	tests := []struct {
		code, name         string
		wantCode, wantName string
	}{
		{code: "usdt", name: "Tether", wantCode: "USDT", wantName: "Tether"},
		{code: "  btc\t", name: " Bitcoin \n", wantCode: "BTC", wantName: "Bitcoin"},
		{code: "2330.tw", name: "台積電", wantCode: "2330.TW", wantName: "台積電"},
	}
	for _, tt := range tests {
		got, err := model.NewInstrument(model.NewInstrumentParams{
			ID: uuid.Must(uuid.NewV7()), Code: tt.code, Name: tt.name, Kind: model.InstrumentKindCrypto, Decimals: 8,
		})
		if err != nil {
			t.Fatalf("NewInstrument(%q): %v", tt.code, err)
		}
		if got.Code != tt.wantCode || got.Name != tt.wantName {
			t.Errorf("NewInstrument(%q, %q) = code %q, name %q, want %q, %q", tt.code, tt.name, got.Code, got.Name, tt.wantCode, tt.wantName)
		}
	}
}

func TestNewInstrumentRejectsABlankCodeAndACodeWithSpaces(t *testing.T) {
	tests := []struct {
		code string
		want error
	}{
		{code: "", want: model.ErrCodeEmpty},
		{code: " \t ", want: model.ErrCodeEmpty},
		{code: "BTC USD", want: model.ErrCodeHasSpace},
		{code: "BT\tC", want: model.ErrCodeHasSpace},
	}
	for _, tt := range tests {
		_, err := model.NewInstrument(model.NewInstrumentParams{
			ID: uuid.Must(uuid.NewV7()), Code: tt.code, Name: "x", Kind: model.InstrumentKindCrypto, Decimals: 8,
		})
		if !errors.Is(err, tt.want) {
			t.Errorf("code %q: err = %v, want %v", tt.code, err, tt.want)
		}
	}
}

func TestNewInstrumentRejectsABlankName(t *testing.T) {
	_, err := model.NewInstrument(model.NewInstrumentParams{
		ID: uuid.Must(uuid.NewV7()), Code: "BTC", Name: "  ", Kind: model.InstrumentKindCrypto, Decimals: 8,
	})
	if !errors.Is(err, model.ErrNameEmpty) {
		t.Fatalf("err = %v, want ErrNameEmpty", err)
	}
}

func TestNewInstrumentOnlyTakesTheFixedKinds(t *testing.T) {
	for _, kind := range []model.InstrumentKind{
		model.InstrumentKindFiat, model.InstrumentKindCrypto, model.InstrumentKindStock, model.InstrumentKindFund,
	} {
		if _, err := newInstrument(kind, false, 2); err != nil {
			t.Errorf("kind %q rejected: %v", kind, err)
		}
	}
	// "currency" was the name of the fiat kind. It is no kind now: being a
	// currency is what an instrument is used for (IsCurrency), whatever its kind.
	for _, kind := range []model.InstrumentKind{"", "bond", "Fiat", "crypto ", "currency"} {
		if _, err := newInstrument(kind, false, 2); !errors.Is(err, model.ErrInvalidInstrumentKind) {
			t.Errorf("kind %q: err = %v, want ErrInvalidInstrumentKind", kind, err)
		}
	}
}

func TestNewInstrumentDecimalsAreFromZeroToEighteen(t *testing.T) {
	for _, decimals := range []int{0, 2, 8, 18} {
		if _, err := newInstrument(model.InstrumentKindCrypto, false, decimals); err != nil {
			t.Errorf("decimals %d rejected: %v", decimals, err)
		}
	}
	for _, decimals := range []int{-1, 19, 100} {
		if _, err := newInstrument(model.InstrumentKindCrypto, false, decimals); !errors.Is(err, model.ErrDecimalsOutOfRange) {
			t.Errorf("decimals %d: err = %v, want ErrDecimalsOutOfRange", decimals, err)
		}
	}
}

func TestBeingACurrencyIsIndependentOfTheKind(t *testing.T) {
	for _, kind := range []model.InstrumentKind{
		model.InstrumentKindFiat, model.InstrumentKindCrypto, model.InstrumentKindStock, model.InstrumentKindFund,
	} {
		for _, isCurrency := range []bool{true, false} {
			got, err := newInstrument(kind, isCurrency, 2)
			if err != nil {
				t.Errorf("kind %q, currency %v rejected: %v", kind, isCurrency, err)
				continue
			}
			if got.Kind != kind || got.IsCurrency != isCurrency {
				t.Errorf("kind %q, currency %v came out as kind %q, currency %v", kind, isCurrency, got.Kind, got.IsCurrency)
			}
		}
	}
}

func TestOnlyTheBaseCurrencyCannotBeArchived(t *testing.T) {
	twd := model.Instrument{ID: model.BaseCurrencyID, Code: model.BaseCurrencyCode}
	if err := twd.CheckArchivable(); !errors.Is(err, model.ErrBaseCurrency) {
		t.Fatalf("err = %v, want ErrBaseCurrency", err)
	}

	other := model.Instrument{ID: uuid.Must(uuid.NewV7()), Code: "USD"}
	if err := other.CheckArchivable(); err != nil {
		t.Fatalf("CheckArchivable of another instrument: %v", err)
	}
}

func TestCheckQuantityAllowsNoMoreDecimalPlacesThanTheInstrument(t *testing.T) {
	tests := []struct {
		decimals int
		quantity string
		ok       bool
	}{
		{decimals: 2, quantity: "100", ok: true},
		{decimals: 2, quantity: "1.5", ok: true},
		{decimals: 2, quantity: "1.50", ok: true},
		{decimals: 2, quantity: "1.500", ok: true}, // trailing zeros are not decimal places
		{decimals: 2, quantity: "-3.25", ok: true},
		{decimals: 2, quantity: "0.001", ok: false},
		{decimals: 2, quantity: "1.005", ok: false},
		{decimals: 2, quantity: "-0.001", ok: false},
		{decimals: 0, quantity: "120", ok: true},
		{decimals: 0, quantity: "120.5", ok: false},
		{decimals: 18, quantity: "0.123456789012345678", ok: true},
		{decimals: 18, quantity: "0.1234567890123456789", ok: false},
	}
	for _, tt := range tests {
		i := model.Instrument{Code: "X", Decimals: tt.decimals}
		err := i.CheckQuantity(decimal.RequireFromString(tt.quantity))
		switch {
		case tt.ok && err != nil:
			t.Errorf("decimals %d, quantity %s rejected: %v", tt.decimals, tt.quantity, err)
		case !tt.ok && !errors.Is(err, model.ErrTooManyDecimals):
			t.Errorf("decimals %d, quantity %s: err = %v, want ErrTooManyDecimals", tt.decimals, tt.quantity, err)
		}
	}
}
