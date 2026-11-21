package file_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// mozeNames are the names of the columns of an export, in the order MOZE writes them.
var mozeNames = []string{
	"帳戶", "幣種", "記錄類型", "主類別", "子類別", "金額", "手續費", "折扣",
	"名稱", "商家", "日期", "時間", "專案", "描述", "標籤", "對象",
}

// The places of the columns in a rec.
const (
	cAccount = iota
	cCurrency
	cType
	cMain
	cSub
	cAmount
	cFee
	cDiscount
	cName
	cMerchant
	cDate
	cClock
	cProject
	cDesc
	cTags
	cParty
)

// rec is a record of an export: a field for each of the columns, in their order.
type rec [16]string

// lunch is the record the tests change what they are about of.
func lunch() rec {
	return rec{
		"Fake Wallet", "TWD", "支出", "食物", "午餐", "-120", "0", "0",
		"Lunch", "Fake Deli", "2026/03/10", "12:30", "Daily", "", "#家庭", "Zed",
	}
}

// csvField writes a field as CSV has it: in quotes when it has to be.
func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\r\n") || s != strings.TrimSpace(s) {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// csvLine writes the fields as a line of CSV.
func csvLine(fields ...string) string {
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = csvField(f)
	}
	return strings.Join(quoted, ",")
}

// exportOf writes an export that has only the columns of order, in that order.
func exportOf(order []int, recs ...rec) string {
	header := make([]string, len(order))
	for i, c := range order {
		header[i] = mozeNames[c]
	}
	var b strings.Builder
	b.WriteString(csvLine(header...) + "\n")
	for _, r := range recs {
		fields := make([]string, len(order))
		for i, c := range order {
			fields[i] = r[c]
		}
		b.WriteString(csvLine(fields...) + "\n")
	}
	return b.String()
}

// allColumns are the places of all the columns, in the order of MOZE.
func allColumns() []int {
	order := make([]int, len(mozeNames))
	for i := range order {
		order[i] = i
	}
	return order
}

// export writes an export of the records, with every column.
func export(recs ...rec) string { return exportOf(allColumns(), recs...) }

// withoutColumns is the columns but the ones that are left out.
func withoutColumns(left ...int) []int {
	var order []int
	for _, c := range allColumns() {
		if !slices.Contains(left, c) {
			order = append(order, c)
		}
	}
	return order
}

func parse(t *testing.T, text string) []model.Row {
	t.Helper()
	rows, err := file.ParseMozeCSV(strings.NewReader(text))
	if err != nil {
		t.Fatalf("ParseMozeCSV: %v", err)
	}
	return rows
}

// parseFails parses text that must be refused, and returns the failure it is.
func parseFails(t *testing.T, text string) *filefailure.Failure {
	t.Helper()
	rows, err := file.ParseMozeCSV(strings.NewReader(text))
	if err == nil {
		t.Fatalf("ParseMozeCSV read %d rows of text that is wrong:\n%s", len(rows), text)
	}
	return failureOf(t, err)
}

// describe is a row as a line of text, for comparing rows with decimals and times in
// them. It leaves the key out.
func describe(r model.Row) string {
	date := "none"
	if r.HasDate() {
		date = r.Date.Format(time.RFC3339)
	}
	return fmt.Sprintf("pos=%d line=%d account=%q currency=%q type=%q main=%q sub=%q "+
		"amount=%s fee=%s discount=%s name=%q merchant=%q date=%s clock=%q project=%q desc=%q tags=%q party=%q",
		r.Pos, r.Line, r.Account, r.Currency, r.Type, r.Main, r.Sub,
		r.Amount, r.Fee, r.Discount, r.Name, r.Merchant, date, r.Clock, r.Project, r.Description, r.Tags, r.Party)
}

func describeAll(rows []model.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = describe(r)
	}
	return out
}

func TestParseMozeCSVReadsEveryField(t *testing.T) {
	r := lunch()
	r[cAmount], r[cFee], r[cDiscount] = "-100.5", "-15", "20"
	r[cDesc] = "no onions"
	rows := parse(t, export(r))
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	want := model.Row{
		Pos: 0, Line: 2,
		Account: "Fake Wallet", Currency: "TWD", Type: model.TypeExpense, Main: "食物", Sub: "午餐",
		Amount: d("-100.5"), Fee: d("-15"), Discount: d("20"),
		Name: "Lunch", Merchant: "Fake Deli",
		Date: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC), Clock: "12:30",
		Project: "Daily", Description: "no onions", Tags: []string{"家庭"}, Party: "Zed",
	}
	if got, w := describe(rows[0]), describe(want); got != w {
		t.Errorf("row\n got %s\nwant %s", got, w)
	}
	if rows[0].Key == "" {
		t.Error("the row has no key")
	}
	// The signs are the file's: the effect on the account is what they add up to.
	if got := rows[0].Effect().String(); got != "-95.5" {
		t.Errorf("Effect() = %s, want -95.5", got)
	}
}

func TestParseMozeCSVStripsBOMAndTakesCRLF(t *testing.T) {
	a, b := lunch(), lunch()
	a[cDesc] = "line one\nline two"
	b[cName], b[cMerchant] = "Dinner, late", "Fake Grill"
	lf := export(a, b)

	want := parse(t, lf)
	wantKeys := []string{want[0].Key, want[1].Key}
	for name, text := range map[string]string{
		"BOM":      "\ufeff" + lf,
		"CRLF":     strings.ReplaceAll(lf, "\n", "\r\n"),
		"BOM CRLF": "\ufeff" + strings.ReplaceAll(lf, "\n", "\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			got := parse(t, text)
			if !slices.Equal(describeAll(got), describeAll(want)) {
				t.Errorf("rows\n got %q\nwant %q", describeAll(got), describeAll(want))
			}
			if got[0].Description != "line one\nline two" {
				t.Errorf("Description = %q, want the line break to be \\n", got[0].Description)
			}
			if got[0].Key != wantKeys[0] || got[1].Key != wantKeys[1] {
				t.Errorf("keys %q %q, want %q: the line endings are not part of what a row says", got[0].Key, got[1].Key, wantKeys)
			}
		})
	}
}

func TestParseMozeCSVQuotedFields(t *testing.T) {
	invoice := "發票 AB-12345678\n◎ 品項 ($30) ✕ 2\n◎ 品項B ($45) ✕ 1\n備註: \"加辣\", 不要蔥\n"
	r := lunch()
	r[cMerchant] = "Fake Deli, Branch 2"
	r[cDesc] = invoice
	for name, text := range map[string]string{
		"LF":   export(r, lunch()),
		"CRLF": strings.ReplaceAll(export(r, lunch()), "\n", "\r\n"),
	} {
		t.Run(name, func(t *testing.T) {
			rows := parse(t, text)
			if len(rows) != 2 {
				t.Fatalf("got %d rows, want 2", len(rows))
			}
			if got := rows[0].Merchant; got != "Fake Deli, Branch 2" {
				t.Errorf("Merchant = %q, want the comma to be in it", got)
			}
			// The whitespace around the field goes, the lines inside it stay, the doubled
			// quotes are one.
			if got, want := rows[0].Description, strings.TrimSpace(invoice); got != want {
				t.Errorf("Description = %q, want %q", got, want)
			}
			if rows[1].Description != "" || rows[1].Merchant != "Fake Deli" {
				t.Errorf("the row after it is %q %q, want it as it is", rows[1].Merchant, rows[1].Description)
			}
		})
	}
}

func TestParseMozeCSVLinesAndPositions(t *testing.T) {
	a, b, c := lunch(), lunch(), lunch()
	a[cDesc] = "one\ntwo\nthree" // a record of three lines: 2, 3 and 4
	b[cName] = "Second"
	c[cName] = "Third"
	rows := parse(t, export(a, b, c))
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, wantLine := range []int{2, 5, 6} {
		if rows[i].Pos != i || rows[i].Line != wantLine {
			t.Errorf("row %d has Pos %d, Line %d, want %d, %d", i, rows[i].Pos, rows[i].Line, i, wantLine)
		}
	}
}

func TestParseMozeCSVDay(t *testing.T) {
	tests := []struct {
		name, typ, date string
		want            string // the day, or "none" for a row that has none
		wantCode        filefailure.Code
		wantArgs        []string // what the failure says: the column, the text, a day as it is to be written
	}{
		{"slashes", "支出", "2026/03/10", "2026-03-10", "", nil},
		{"dashes", "支出", "2026-03-10", "2026-03-10", "", nil},
		{"whitespace", "支出", "  2026/03/10 ", "2026-03-10", "", nil},
		{"initial balance with a day", "初始金額", "2026/01/01", "2026-01-01", "", nil},
		{"initial balance without one", "初始金額", "", "none", "", nil},
		{"expense without one", "支出", "", "", filefailure.ValueEmpty, []string{"日期"}},
		{"transfer without one", "轉出", "", "", filefailure.ValueEmpty, []string{"日期"}},
		{"not a day", "支出", "2026/13/45", "", filefailure.NotDay, []string{"日期", "2026/13/45", "2026/03/10"}},
		{"another order", "支出", "10/03/2026", "", filefailure.NotDay, []string{"日期", "10/03/2026", "2026/03/10"}},
		{"no zero padding", "支出", "2026/3/1", "", filefailure.NotDay, []string{"日期", "2026/3/1", "2026/03/10"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := lunch()
			r[cType], r[cDate] = tt.typ, tt.date
			if tt.wantCode != "" {
				wantFailure(t, parseFails(t, export(r)), tt.wantCode, 2, tt.wantArgs...)
				return
			}
			row := parse(t, export(r))[0]
			if tt.want == "none" {
				if row.HasDate() || !row.Date.IsZero() {
					t.Errorf("Date = %v, want none", row.Date)
				}
				return
			}
			if got := row.Date.Format(time.RFC3339); got != tt.want+"T00:00:00Z" {
				t.Errorf("Date = %s, want %s at midnight UTC", got, tt.want)
			}
		})
	}
}

func TestParseMozeCSVClock(t *testing.T) {
	tests := []struct {
		clock, want string
		wantCode    filefailure.Code
	}{
		{"12:30", "12:30", ""},
		{"12:30:45", "12:30", ""},
		{"7:05", "07:05", ""},
		{"", "", ""},
		{" 23:59 ", "23:59", ""},
		{"noon", "", filefailure.NotTime},
		{"25:00", "", filefailure.NotTime},
	}
	for _, tt := range tests {
		t.Run(tt.clock, func(t *testing.T) {
			r := lunch()
			r[cClock] = tt.clock
			if tt.wantCode != "" {
				wantFailure(t, parseFails(t, export(r)), tt.wantCode, 2, "時間", tt.clock)
				return
			}
			if got := parse(t, export(r))[0].Clock; got != tt.want {
				t.Errorf("Clock = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMozeCSVAmounts(t *testing.T) {
	tests := []struct {
		name                          string
		amount, fee, discount         string
		wantAmount, wantFee, wantDisc string
	}{
		{"expense with fee and discount", "-100.5", "-15", "20", "-100.5", "-15", "20"},
		{"income", "250", "0", "0", "250", "0", "0"},
		{"decimals", "12.50", "-1.25", "0.50", "12.5", "-1.25", "0.5"},
		{"empty is zero", "", "", "", "0", "0", "0"},
		{"whitespace", " -3 ", "  ", "", "-3", "0", "0"},
		{"explicit plus", "+7", "", "", "7", "0", "0"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := lunch()
			r[cAmount], r[cFee], r[cDiscount] = tt.amount, tt.fee, tt.discount
			row := parse(t, export(r))[0]
			got := [3]string{row.Amount.String(), row.Fee.String(), row.Discount.String()}
			if want := [3]string{tt.wantAmount, tt.wantFee, tt.wantDisc}; got != want {
				t.Errorf("amount, fee, discount = %v, want %v", got, want)
			}
		})
	}
}

func TestParseMozeCSVRefusesBadNumbers(t *testing.T) {
	tests := []struct {
		name   string
		col    int
		text   string
		column string // the column of the export, which the failure names
	}{
		{"amount", cAmount, "abc", "金額"},
		{"fee", cFee, "1,5", "手續費"},
		{"discount", cDiscount, "--2", "折扣"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := lunch()
			r[tt.col] = tt.text
			wantFailure(t, parseFails(t, export(r)), filefailure.NotNumber, 2, tt.column, tt.text)
		})
	}
}

func TestParseMozeCSVRefusesRecordsOfTheWrongSize(t *testing.T) {
	short := "Fake Wallet,TWD,支出\n"
	extra := lunch()
	long := csvLine(append(extra[:], "extra")...) + "\n"
	t.Run("short", func(t *testing.T) {
		wantFailure(t, parseFails(t, export(lunch())+short), filefailure.FieldCount, 3, "3", "16")
	})
	t.Run("long", func(t *testing.T) {
		wantFailure(t, parseFails(t, export(lunch(), lunch())+long), filefailure.FieldCount, 4, "17", "16")
	})
	t.Run("after a record of several lines", func(t *testing.T) {
		r := lunch()
		r[cDesc] = "a\nb"
		wantFailure(t, parseFails(t, export(r)+short), filefailure.FieldCount, 4, "3", "16")
	})
}

// The words of the library are not the user's: what is wrong with the quotes is a
// code, and the line, and the line the record begins on when it is another.
func TestParseMozeCSVRefusesBrokenQuoting(t *testing.T) {
	good := export(lunch())
	t.Run("a quote in a field that has none", func(t *testing.T) {
		wantFailure(t, parseFails(t, good+`Fake Wallet,TWD,支出,食物,午餐,-120,0,0,Lun"ch`+"\n"), filefailure.CSVBareQuote, 3)
	})
	t.Run("a quote that is not closed", func(t *testing.T) {
		wantFailure(t, parseFails(t, good+`Fake Wallet,TWD,支出,食物,午餐,-120,0,0,"Lunch`+"\n"), filefailure.CSVQuote, 3)
	})
	t.Run("a quote that is not closed in a record of several lines", func(t *testing.T) {
		wantFailure(t, parseFails(t, good+`Fake Wallet,TWD,支出,食物,午餐,-120,0,0,"Lunch`+"\nand more\nand more\n"), filefailure.CSVQuote, 5, "3")
	})
	t.Run("text after a quote that is closed", func(t *testing.T) {
		wantFailure(t, parseFails(t, good+`Fake Wallet,TWD,支出,食物,午餐,-120,0,0,"Lunch"x`+"\n"), filefailure.CSVQuote, 3)
	})
}

func TestParseMozeCSVFindsColumnsByName(t *testing.T) {
	a, b := lunch(), lunch()
	a[cDesc] = "multi\nline"
	b[cName] = "Other"
	want := describeAll(parse(t, export(a, b)))

	t.Run("in another order", func(t *testing.T) {
		order := allColumns()
		slices.Reverse(order)
		// The lines count the same, so describe tells everything.
		got := describeAll(parse(t, exportOf(order, a, b)))
		if !slices.Equal(got, want) {
			t.Errorf("rows\n got %q\nwant %q", got, want)
		}
	})
	t.Run("with a column that is not known", func(t *testing.T) {
		lines := strings.Split(strings.TrimSuffix(export(lunch(), lunch()), "\n"), "\n")
		for i := range lines {
			lines[i] = "extra," + lines[i]
		}
		rows := parse(t, strings.Join(lines, "\n")+"\n")
		if len(rows) != 2 || rows[0].Account != "Fake Wallet" || rows[0].Currency != "TWD" {
			t.Errorf("rows = %q, want the unknown column to be left alone", describeAll(rows))
		}
	})
}

func TestParseMozeCSVRefusesAHeaderThatIsWrong(t *testing.T) {
	t.Run("one column missing", func(t *testing.T) {
		wantFailure(t, parseFails(t, exportOf(withoutColumns(cAmount), lunch())), filefailure.ColumnsMissing, 1, "金額")
	})
	t.Run("two columns missing, in the order they are needed", func(t *testing.T) {
		wantFailure(t, parseFails(t, exportOf(withoutColumns(cAmount, cDate), lunch())), filefailure.ColumnsMissing, 1, "金額", "日期")
	})
	t.Run("a column twice", func(t *testing.T) {
		wantFailure(t, parseFails(t, exportOf(append(allColumns(), cAmount), lunch())), filefailure.ColumnTwice, 1, "金額")
	})
}

func TestParseMozeCSVTags(t *testing.T) {
	tests := []struct {
		text string
		want []string
	}{
		{"", nil},
		{"#家庭", []string{"家庭"}},
		{"#家庭;#公司", []string{"家庭", "公司"}},
		{";#a;;#a", []string{"a"}},
		{"#a; #b ;c", []string{"a", "b", "c"}},
		{"#b;#a;#b", []string{"b", "a"}},
		{"#", nil},
		{"##a", []string{"#a"}},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			r := lunch()
			r[cTags] = tt.text
			if got := parse(t, export(r))[0].Tags; !slices.Equal(got, tt.want) {
				t.Errorf("Tags = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseMozeCSVKeepsTheRecordTypesItDoesNotKnow(t *testing.T) {
	r := lunch()
	r[cType] = "奇怪的類型"
	if got := parse(t, export(r))[0].Type; got != "奇怪的類型" {
		t.Errorf("Type = %q, want the one of the file: the plan tells of the ones the import does not know", got)
	}
}

func TestParseMozeCSVNoRows(t *testing.T) {
	t.Run("a header alone", func(t *testing.T) {
		rows := parse(t, export())
		if rows == nil || len(rows) != 0 {
			t.Errorf("rows = %#v, want an empty slice", rows)
		}
	})
	t.Run("a header and blank lines", func(t *testing.T) {
		if rows := parse(t, export()+"\n\n"); len(rows) != 0 {
			t.Errorf("got %d rows, want none", len(rows))
		}
	})
	for name, text := range map[string]string{"an empty file": "", "a byte order mark alone": "\ufeff", "blank lines": "\n\n"} {
		t.Run(name, func(t *testing.T) {
			wantFailure(t, parseFails(t, text), filefailure.Empty, 0)
		})
	}
}

func TestParseMozeCSVKeys(t *testing.T) {
	a, b, c := lunch(), lunch(), lunch()
	b[cName] = "Dinner"
	c[cName] = "Snack"

	t.Run("identical rows are told apart", func(t *testing.T) {
		rows := parse(t, export(a, a, b))
		base := func(key string) string { return key[:strings.LastIndex(key, "-")] }
		if rows[0].Key == rows[1].Key || base(rows[0].Key) != base(rows[1].Key) {
			t.Errorf("keys %q and %q, want the same row twice to be one hash and a count", rows[0].Key, rows[1].Key)
		}
		if !strings.HasSuffix(rows[0].Key, "-1") || !strings.HasSuffix(rows[1].Key, "-2") || !strings.HasSuffix(rows[2].Key, "-1") {
			t.Errorf("keys %q, want the counts -1, -2 and -1", []string{rows[0].Key, rows[1].Key, rows[2].Key})
		}
		if base(rows[2].Key) == base(rows[0].Key) {
			t.Error("a row that is not the same has the same hash")
		}
	})
	t.Run("a row keeps its key when the file has more rows after it", func(t *testing.T) {
		first := parse(t, export(a, a, b))
		second := parse(t, export(a, a, b, c, a))
		for i := range first {
			if first[i].Key != second[i].Key {
				t.Errorf("row %d has the key %q, then %q", i, first[i].Key, second[i].Key)
			}
		}
		if !strings.HasSuffix(second[4].Key, "-3") {
			t.Errorf("the third copy has the key %q, want the count -3", second[4].Key)
		}
	})
	t.Run("whitespace and the form of numbers are not part of it", func(t *testing.T) {
		plain := a
		plain[cAmount], plain[cFee] = "-120", "0"
		spelled := a
		spelled[cAmount], spelled[cFee], spelled[cDiscount] = "-120.00", "", "0.0"
		spelled[cName], spelled[cDate], spelled[cClock] = "  Lunch ", " 2026-03-10 ", "12:30:00"
		if x, y := parse(t, export(plain))[0].Key, parse(t, export(spelled))[0].Key; x != y {
			t.Errorf("keys %q and %q, want the same", x, y)
		}
	})
	t.Run("another amount is another row", func(t *testing.T) {
		other := a
		other[cAmount] = "-121"
		if x, y := parse(t, export(a))[0].Key, parse(t, export(other))[0].Key; x == y {
			t.Errorf("rows that differ have the key %q", x)
		}
	})
}

func TestMozeCSVRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := file.NewMozeCSV()

	t.Run("reads the file", func(t *testing.T) {
		path := writeFile(t, dir, "export.csv", "\ufeff"+export(lunch(), lunch()))
		rows, err := store.Rows(ctx, path)
		if err != nil {
			t.Fatalf("Rows: %v", err)
		}
		if len(rows) != 2 || rows[0].Key == "" || rows[0].Key == rows[1].Key {
			t.Errorf("rows = %q, want two rows with their own keys", describeAll(rows))
		}
	})
	t.Run("a file that is not there", func(t *testing.T) {
		path := filepath.Join(dir, "none.csv")
		_, err := store.Rows(ctx, path)
		f := failureOf(t, err)
		wantFailure(t, f, filefailure.Missing, 0)
		wantFile(t, f, path)
	})
	t.Run("a file that cannot be read gives the reason of the system", func(t *testing.T) {
		_, err := store.Rows(ctx, dir) // a folder opens, and cannot be read
		f := failureOf(t, err)
		wantFailure(t, f, filefailure.Unreadable, 0)
		wantFile(t, f, dir)
		if f.Err == nil {
			t.Error("the failure has no Err: the user is told the reason of the system as it is")
		}
	})
	t.Run("a row that cannot be read names the file and the line", func(t *testing.T) {
		r := lunch()
		r[cDate] = ""
		path := writeFile(t, dir, "broken.csv", export(lunch(), r))
		_, err := store.Rows(ctx, path)
		if err == nil {
			t.Fatal("Rows read a file with a row that has no day")
		}
		f := failureOf(t, err)
		wantFailure(t, f, filefailure.ValueEmpty, 3, "日期")
		wantFile(t, f, path)
	})
	t.Run("a context that is done", func(t *testing.T) {
		done, cancel := context.WithCancel(ctx)
		cancel()
		path := writeFile(t, dir, "again.csv", export(lunch()))
		if _, err := store.Rows(done, path); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}
