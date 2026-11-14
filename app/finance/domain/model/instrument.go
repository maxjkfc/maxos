package model

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// BaseCurrencyID is the id of the base currency, TWD: the currency every
// statistic and comparison is made in. It is the id migration 0003 gives the TWD
// row, and the two must stay equal.
var BaseCurrencyID = uuid.MustParse("00000000-0000-7000-8000-000000000001")

// BaseCurrencyCode is the code of the base currency.
const BaseCurrencyCode = "TWD"

// The range of Instrument.Decimals.
const (
	MinDecimals = 0
	MaxDecimals = 18
)

// InstrumentKind is what sort of thing an instrument is.
type InstrumentKind string

// The kinds of instrument. A kind is not the same as being a currency: USDT is
// crypto and still a currency (see Instrument.IsCurrency). The kind of TWD, USD
// and JPY is fiat, named for what they are so that it is not taken for the
// currency of CONTEXT.md, which includes USDT.
const (
	InstrumentKindFiat   InstrumentKind = "fiat"
	InstrumentKindCrypto InstrumentKind = "crypto"
	InstrumentKindStock  InstrumentKind = "stock"
	InstrumentKindFund   InstrumentKind = "fund"
)

// Valid reports whether k is one of the kinds above.
func (k InstrumentKind) Valid() bool {
	switch k {
	case InstrumentKindFiat, InstrumentKindCrypto, InstrumentKindStock, InstrumentKindFund:
		return true
	}
	return false
}

// Instrument is anything that can be held in a quantity and has a price:
// fiat money, a cryptocurrency, a stock, a fund.
type Instrument struct {
	ID uuid.UUID
	// Code identifies the instrument to people: "TWD", "USDT", "BTC", "2330.TW".
	// It is upper case and unique.
	Code string
	Name string
	Kind InstrumentKind
	// IsCurrency is whether the instrument is used to price and to pay, as TWD,
	// USD, JPY and USDT are. It does not follow from Kind.
	IsCurrency bool
	// Decimals is how many decimal places a quantity of the instrument has, from
	// MinDecimals to MaxDecimals.
	Decimals int
	// CreatedAt is assigned when the instrument is stored; it is zero before then.
	CreatedAt time.Time
	// ArchivedAt is nil while the instrument is in use. An archived instrument
	// stays for the transactions that already hold it, but new ones may not.
	ArchivedAt *time.Time
}

// NewInstrumentParams is the raw input of an instrument.
type NewInstrumentParams struct {
	ID         uuid.UUID
	Code       string
	Name       string
	Kind       InstrumentKind
	IsCurrency bool
	Decimals   int
}

// NewInstrument validates an instrument and returns it. The whitespace around
// the code is trimmed and the code is made upper case, so "usdt " and "USDT" are
// the same instrument; the whitespace around the name is trimmed. A code may
// not be empty or have whitespace inside it.
func NewInstrument(p NewInstrumentParams) (Instrument, error) {
	code := strings.ToUpper(strings.TrimSpace(p.Code))
	if code == "" {
		return Instrument{}, ErrCodeEmpty
	}
	if strings.ContainsFunc(code, unicode.IsSpace) {
		return Instrument{}, fmt.Errorf("%w: %q", ErrCodeHasSpace, code)
	}
	name, err := normalizeName(p.Name)
	if err != nil {
		return Instrument{}, err
	}
	if !p.Kind.Valid() {
		return Instrument{}, fmt.Errorf("%w %q: want one of fiat, crypto, stock, fund", ErrInvalidInstrumentKind, p.Kind)
	}
	if p.Decimals < MinDecimals || p.Decimals > MaxDecimals {
		return Instrument{}, fmt.Errorf("%w: %d, want %d to %d", ErrDecimalsOutOfRange, p.Decimals, MinDecimals, MaxDecimals)
	}
	return Instrument{
		ID:         p.ID,
		Code:       code,
		Name:       name,
		Kind:       p.Kind,
		IsCurrency: p.IsCurrency,
		Decimals:   p.Decimals,
	}, nil
}

// IsArchived reports whether the instrument is archived.
func (i Instrument) IsArchived() bool {
	return i.ArchivedAt != nil
}

// CheckArchivable returns ErrBaseCurrency for the base currency, which is never
// archived, and nil for every other instrument.
func (i Instrument) CheckArchivable() error {
	if i.ID == BaseCurrencyID {
		return fmt.Errorf("%w: %s", ErrBaseCurrency, i.Code)
	}
	return nil
}

// CheckQuantity returns ErrTooManyDecimals when q has more decimal places than
// the instrument has. Trailing zeros do not count: 1.50 fits two decimals and
// so does 1.5.
func (i Instrument) CheckQuantity(q decimal.Decimal) error {
	if !q.Equal(q.Round(int32(i.Decimals))) {
		return fmt.Errorf("%w: %s has more than %d decimal places for %s", ErrTooManyDecimals, q, i.Decimals, i.Code)
	}
	return nil
}
