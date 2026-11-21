package file

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/rates"
)

var _ rates.Store = (*RatesCSV)(nil)

// The columns of a rates file, by the names of its header.
const (
	ratesDate       = "date"
	ratesInstrument = "instrument"
	ratesPerUnit    = "twd_per_unit"
)

// RatesCSV reads the rates the user keeps in a CSV file.
type RatesCSV struct{}

// NewRatesCSV returns a reader of rates files.
func NewRatesCSV() *RatesCSV { return &RatesCSV{} }

// Load reads the rates at path, as ParseRates does; an error about its contents
// begins with the path. A file that does not exist is no rates and no error.
func (*RatesCSV) Load(ctx context.Context, path string) (model.Rates, bool, error) {
	if err := ctx.Err(); err != nil {
		return model.Rates{}, false, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return model.Rates{}, false, nil
	}
	if err != nil {
		return model.Rates{}, false, err
	}
	defer func() { _ = f.Close() }()

	r, err := ParseRates(f)
	if err != nil {
		return model.Rates{}, false, fmt.Errorf("%s: %w", path, err)
	}
	return r, true, nil
}

// ParseRates reads a rates file: CSV with the header date,instrument,twd_per_unit,
// whose columns may come in any order and be joined by others that are not used.
// A line that begins with "#" is a comment, and a blank line is nothing. The rows
// may be in any order.
//
//   - date is a day, 2006-01-02.
//   - instrument is the code of the currency, which is read in upper case.
//   - twd_per_unit is what ONE unit of it is worth in TWD, above zero.
//
// A currency has one rate a day: a second is an error that names both lines. Every
// error begins with the line it is about.
func ParseRates(r io.Reader) (model.Rates, error) {
	table, err := openCSV(r, '#', ratesDate, ratesInstrument, ratesPerUnit)
	if err != nil {
		return model.Rates{}, err
	}
	type given struct{ code, day string }
	lines := map[given]int{} // the line each rate is given on
	byCode := map[string][]model.Rate{}
	for {
		rec, err := table.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return model.Rates{}, err
		}

		dayText := rec.text(ratesDate)
		day, err := time.Parse(time.DateOnly, dayText)
		if err != nil {
			return model.Rates{}, fmt.Errorf("line %d: %s %q is not a day like 2026-03-10", rec.line, ratesDate, dayText)
		}
		code := strings.ToUpper(rec.text(ratesInstrument))
		if code == "" {
			return model.Rates{}, fmt.Errorf("line %d: %s is empty", rec.line, ratesInstrument)
		}
		perUnitText := rec.text(ratesPerUnit)
		perUnit, err := decimal.NewFromString(perUnitText)
		if err != nil {
			return model.Rates{}, fmt.Errorf("line %d: %s %q: %w", rec.line, ratesPerUnit, perUnitText, err)
		}
		if !perUnit.IsPositive() {
			return model.Rates{}, fmt.Errorf("line %d: %s %q is not above zero", rec.line, ratesPerUnit, perUnitText)
		}

		key := given{code: code, day: dayText}
		if first, twice := lines[key]; twice {
			return model.Rates{}, fmt.Errorf("line %d: %s has a rate for %s already, on line %d", rec.line, code, dayText, first)
		}
		lines[key] = rec.line
		byCode[code] = append(byCode[code], model.Rate{Date: day, PerUnit: perUnit})
	}
	return model.NewRates(byCode), nil
}
