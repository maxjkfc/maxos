package file

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"strconv"
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

// ratesDayExample is a day written the way a rates file has it: a failure for a text
// that is not a day shows it, so that the user sees what is meant.
const ratesDayExample = "2026-03-10"

// RatesCSV reads the rates the user keeps in a CSV file.
type RatesCSV struct{}

// NewRatesCSV returns a reader of rates files.
func NewRatesCSV() *RatesCSV { return &RatesCSV{} }

// Load reads the rates at path, as ParseRates does. What is wrong with the file is a
// *model.Failure that names the path. A file that does not exist is no rates and no
// error.
func (*RatesCSV) Load(ctx context.Context, path string) (model.Rates, bool, error) {
	if err := ctx.Err(); err != nil {
		return model.Rates{}, false, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return model.Rates{}, false, nil
	}
	if err != nil {
		return model.Rates{}, false, unreadable(path, err)
	}
	defer func() { _ = f.Close() }()

	r, err := ParseRates(f)
	if err != nil {
		return model.Rates{}, false, inFile(path, err)
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
// A currency has one rate a day: a second is a failure that has both lines. What is
// wrong with the file is a *model.Failure (see failAt) that has the line it is about,
// and the column or the value when it is one.
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
		if dayText == "" {
			return model.Rates{}, failAt(model.FailureValueEmpty, rec.line, ratesDate)
		}
		day, err := time.Parse(time.DateOnly, dayText)
		if err != nil {
			return model.Rates{}, failAt(model.FailureNotDay, rec.line, ratesDate, dayText, ratesDayExample)
		}
		code := strings.ToUpper(rec.text(ratesInstrument))
		if code == "" {
			return model.Rates{}, failAt(model.FailureValueEmpty, rec.line, ratesInstrument)
		}
		perUnitText := rec.text(ratesPerUnit)
		if perUnitText == "" {
			return model.Rates{}, failAt(model.FailureValueEmpty, rec.line, ratesPerUnit)
		}
		perUnit, err := decimal.NewFromString(perUnitText)
		if err != nil {
			return model.Rates{}, failAt(model.FailureNotNumber, rec.line, ratesPerUnit, perUnitText)
		}
		if !perUnit.IsPositive() {
			return model.Rates{}, failAt(model.FailureRateNotPositive, rec.line, ratesPerUnit, perUnitText)
		}

		key := given{code: code, day: dayText}
		if first, twice := lines[key]; twice {
			return model.Rates{}, failAt(model.FailureRateTwice, rec.line, code, dayText, strconv.Itoa(first))
		}
		lines[key] = rec.line
		byCode[code] = append(byCode[code], model.Rate{Date: day, PerUnit: perUnit})
	}
	return model.NewRates(byCode), nil
}
