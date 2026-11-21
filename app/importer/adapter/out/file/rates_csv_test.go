package file_test

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func parseRates(t *testing.T, text string) model.Rates {
	t.Helper()
	r, err := file.ParseRates(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseRates: %v", err)
	}
	return r
}

// ratesFail parses a rates file that must be refused, and returns what it says.
func ratesFail(t *testing.T, text string) string {
	t.Helper()
	r, err := file.ParseRates(strings.NewReader(text))
	if err == nil {
		t.Fatalf("ParseRates read rates of %v from text that is wrong:\n%s", r.Codes(), text)
	}
	return err.Error()
}

// wantRate checks the rate a lookup finds: the one with the given per unit, or none.
func wantRate(t *testing.T, r model.Rates, code, on, perUnit string) {
	t.Helper()
	got, ok := r.Lookup(code, day(on))
	if perUnit == "" {
		if ok {
			t.Errorf("Lookup(%s, %s) = %s on %s, want none", code, on, got.PerUnit, got.Date.Format("2006-01-02"))
		}
		return
	}
	if !ok || !got.PerUnit.Equal(d(perUnit)) {
		t.Errorf("Lookup(%s, %s) = %s, %v, want %s", code, on, got.PerUnit, ok, perUnit)
	}
}

func TestParseRatesLookup(t *testing.T) {
	r := parseRates(t, `# Rates from a public source (these numbers are invented).
date,instrument,twd_per_unit
2026-04-01,USD,31.50
2026-04-03,USD,31.80
2026-04-01,jpy,0.2100
`)
	wantRate(t, r, "USD", "2026-04-01", "31.50") // the day itself
	wantRate(t, r, "USD", "2026-04-02", "31.50") // the nearest day before it
	wantRate(t, r, "USD", "2026-04-03", "31.80")
	wantRate(t, r, "USD", "2026-06-01", "31.80") // long after the last
	wantRate(t, r, "USD", "2026-03-31", "")      // before the first
	wantRate(t, r, "JPY", "2026-04-05", "0.21")  // a code is read in upper case
	wantRate(t, r, "EUR", "2026-04-05", "")      // no rates at all
	if got := r.Codes(); !slices.Equal(got, []string{"JPY", "USD"}) {
		t.Errorf("Codes() = %v, want JPY and USD", got)
	}
	first, last, ok := r.Range("USD")
	if !ok || first.Format("2006-01-02") != "2026-04-01" || last.Format("2006-01-02") != "2026-04-03" {
		t.Errorf("Range(USD) = %v, %v, %v, want 2026-04-01 to 2026-04-03", first, last, ok)
	}
}

func TestParseRatesCommentsAndBlankLines(t *testing.T) {
	r := parseRates(t, `
# first comment

# second comment
date,instrument,twd_per_unit

2026-04-01,USD,31.50
# a rate that is not to be used:
# 2026-04-02,USD,99

`)
	wantRate(t, r, "USD", "2026-04-02", "31.50")
	if got := r.Codes(); !slices.Equal(got, []string{"USD"}) {
		t.Errorf("Codes() = %v, want USD alone", got)
	}
}

func TestParseRatesAnyOrder(t *testing.T) {
	t.Run("columns", func(t *testing.T) {
		r := parseRates(t, "twd_per_unit,date,instrument\n31.5,2026-04-01,USD\n0.21,2026-04-01,JPY\n")
		wantRate(t, r, "USD", "2026-04-01", "31.5")
		wantRate(t, r, "JPY", "2026-04-01", "0.21")
	})
	t.Run("rows", func(t *testing.T) {
		r := parseRates(t, `date,instrument,twd_per_unit
2026-04-03,USD,31.80
2026-04-01,USD,31.50
2026-04-02,USD,31.60
`)
		wantRate(t, r, "USD", "2026-04-01", "31.50")
		wantRate(t, r, "USD", "2026-04-02", "31.60")
		wantRate(t, r, "USD", "2026-04-04", "31.80")
	})
	t.Run("a column that is not used", func(t *testing.T) {
		r := parseRates(t, "date,source,instrument,twd_per_unit\n2026-04-01,a public bank,USD,31.5\n")
		wantRate(t, r, "USD", "2026-04-01", "31.5")
	})
}

func TestParseRatesBOM(t *testing.T) {
	r := parseRates(t, "\ufeffdate,instrument,twd_per_unit\r\n2026-04-01,USD,31.5\r\n")
	wantRate(t, r, "USD", "2026-04-01", "31.5")
}

func TestParseRatesNoRates(t *testing.T) {
	if r := parseRates(t, "date,instrument,twd_per_unit\n"); len(r.Codes()) != 0 {
		t.Errorf("Codes() = %v, want none for a header alone", r.Codes())
	}
	mustSay(t, ratesFail(t, ""), "empty")
	mustSay(t, ratesFail(t, "# only a comment\n"), "empty")
}

func TestParseRatesRefusesTwoRatesForADay(t *testing.T) {
	message := ratesFail(t, `date,instrument,twd_per_unit
2026-04-01,USD,31.50
2026-04-01,JPY,0.21
2026-04-02,USD,31.60
2026-04-01,usd,31.55
`)
	mustSay(t, message, "line 5", "USD", "2026-04-01", "line 2")
}

func TestParseRatesRefusesBadRows(t *testing.T) {
	tests := []struct {
		name    string
		row     string
		wantErr []string
	}{
		{"zero", "2026-04-01,USD,0", []string{"line 2", "twd_per_unit", `"0"`}},
		{"negative", "2026-04-01,USD,-31.5", []string{"line 2", "twd_per_unit", "-31.5"}},
		{"text", "2026-04-01,USD,abc", []string{"line 2", "twd_per_unit", "abc"}},
		{"nothing", "2026-04-01,USD,", []string{"line 2", "twd_per_unit"}},
		{"another form of day", "2026/04/01,USD,31.5", []string{"line 2", "date", "2026/04/01"}},
		{"no day", ",USD,31.5", []string{"line 2", "date"}},
		{"no instrument", "2026-04-01, ,31.5", []string{"line 2", "instrument"}},
		{"short", "2026-04-01,USD", []string{"line 2", "2 fields"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustSay(t, ratesFail(t, "date,instrument,twd_per_unit\n"+tt.row+"\n"), tt.wantErr...)
		})
	}
	t.Run("the line is the line of the file", func(t *testing.T) {
		text := "# comment\ndate,instrument,twd_per_unit\n\n2026-04-01,USD,31.5\n# another\n2026-04-02,USD,0\n"
		mustSay(t, ratesFail(t, text), "line 6")
	})
}

func TestParseRatesRefusesAHeaderThatIsWrong(t *testing.T) {
	mustSay(t, ratesFail(t, "date,instrument\n2026-04-01,USD\n"), "line 1", "twd_per_unit")
	mustSay(t, ratesFail(t, "# comment\ninstrument,rate\nUSD,31.5\n"), "line 2", "date", "twd_per_unit")
	mustSay(t, ratesFail(t, "date,date,instrument,twd_per_unit\n"), "date", "twice")
}

func TestRatesCSVLoad(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := file.NewRatesCSV()

	t.Run("reads the file", func(t *testing.T) {
		path := writeFile(t, dir, "rates.csv", "date,instrument,twd_per_unit\n2026-04-01,USD,31.5\n")
		r, exists, err := store.Load(ctx, path)
		if err != nil || !exists {
			t.Fatalf("Load = %v, %v, want the file to be read", exists, err)
		}
		wantRate(t, r, "USD", "2026-04-02", "31.5")
	})
	t.Run("a file that is not there is no rates", func(t *testing.T) {
		r, exists, err := store.Load(ctx, filepath.Join(dir, "none.csv"))
		if err != nil || exists || len(r.Codes()) != 0 {
			t.Errorf("Load = %v, %v, %v, want no rates, no file and no error", r.Codes(), exists, err)
		}
	})
	t.Run("a file that is wrong names the file and the line", func(t *testing.T) {
		path := writeFile(t, dir, "bad.csv", "date,instrument,twd_per_unit\n2026-04-01,USD,0\n")
		_, exists, err := store.Load(ctx, path)
		if err == nil || exists {
			t.Fatalf("Load = %v, %v, want an error", exists, err)
		}
		mustSay(t, err.Error(), path, "line 2")
	})
	t.Run("what cannot be read", func(t *testing.T) {
		// A folder is there, and it is not a file of rates.
		if _, _, err := store.Load(ctx, dir); err == nil || errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Load of a folder = %v, want an error that is not about a file that is missing", err)
		}
	})
	t.Run("a context that is done", func(t *testing.T) {
		done, cancel := context.WithCancel(ctx)
		cancel()
		if _, _, err := store.Load(done, filepath.Join(dir, "rates.csv")); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
