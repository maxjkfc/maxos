package cli_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/maxjkfc/maxos/app/importer/adapter/in/cli"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// The files that declare the codes of failure, the verbs and the things: the out-port of
// the files and the in-port of the ledger. They are read here so that a code that is
// added is found, and cannot be left without its text.
var (
	fileFailurePath   = filepath.Join("..", "..", "..", "usecase", "port", "out", "filefailure", "filefailure.go")
	ledgerFailurePath = filepath.Join("..", "..", "..", "usecase", "port", "in", "usecase", "failure.go")
)

// fileSamples is a failure of every code of a file that the out-port has, as the code
// says it is made: the file and the line it is about, and its values. The names are
// invented, and of one word or two, so that anything longer in what the user reads is
// text.
var fileSamples = map[filefailure.Code]*filefailure.Failure{
	filefailure.Missing:    {File: "/data/MOZE_2026.csv"},
	filefailure.Unreadable: {File: "/data/mapping.yaml", Err: errors.New("permission denied")},
	filefailure.NotWritten: {File: "/data/mapping.yaml", Err: errors.New("no space left on device")},
	filefailure.Empty:      {File: "/data/MOZE_2026.csv"},

	filefailure.ColumnTwice:    {File: "/data/MOZE_2026.csv", Line: 1, Args: []string{"金額"}},
	filefailure.ColumnsMissing: {File: "/data/MOZE_2026.csv", Line: 1, Args: []string{"金額", "日期"}},
	filefailure.FieldCount:     {File: "/data/MOZE_2026.csv", Line: 7, Args: []string{"3", "16"}},
	filefailure.CSVBareQuote:   {File: "/data/MOZE_2026.csv", Line: 5},
	filefailure.CSVQuote:       {File: "/data/MOZE_2026.csv", Line: 9, Args: []string{"5"}},
	filefailure.CSVSyntax:      {File: "/data/MOZE_2026.csv", Line: 5},

	filefailure.ValueEmpty:           {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"日期"}},
	filefailure.NotNumber:            {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"金額", "abc"}},
	filefailure.NotWholeNumber:       {File: "/data/mapping.yaml", Line: 3, Args: []string{"closing_day", "five"}},
	filefailure.NotBoolean:           {File: "/data/mapping.yaml", Line: 3, Args: []string{"is_currency", "maybe"}},
	filefailure.NotDay:               {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"日期", "2026/13/45", "2026/03/10"}},
	filefailure.NotTime:              {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"時間", "noon"}},
	filefailure.RateNotPositive:      {File: "/data/rates.csv", Line: 3, Args: []string{"twd_per_unit", "0"}},
	filefailure.RateTwice:            {File: "/data/rates.csv", Line: 5, Args: []string{"USD", "2026-04-01", "2"}},
	filefailure.YAMLSyntax:           {File: "/data/mapping.yaml", Line: 4},
	filefailure.SecondDocument:       {File: "/data/mapping.yaml", Line: 12},
	filefailure.NotSections:          {File: "/data/mapping.yaml", Line: 1},
	filefailure.MustBeMapping:        {File: "/data/mapping.yaml", Line: 2, Args: []string{"accounts.Zed"}},
	filefailure.KeyNotSingle:         {File: "/data/mapping.yaml", Line: 2, Args: []string{"accounts"}},
	filefailure.ValueNotSingle:       {File: "/data/mapping.yaml", Line: 3, Args: []string{"name"}},
	filefailure.KeyTwice:             {File: "/data/mapping.yaml", Line: 6, Args: []string{"accounts", "Zed", "3"}},
	filefailure.KeyUnknown:           {File: "/data/mapping.yaml", Line: 3, Args: []string{"kindd", "accounts.Zed", "name", "kind", "institution"}},
	filefailure.EntryNotWritable:     {File: "/data/mapping.yaml", Args: []string{"accounts", "two\nlines"}},
	filefailure.Braces:               {File: "/data/mapping.yaml", Line: 1, Args: []string{"parties"}},
	filefailure.Layout:               {File: "/data/mapping.yaml", Line: 1, Args: []string{"accounts"}},
	filefailure.WouldReadDifferently: {File: "/data/mapping.yaml", Args: []string{"parties", "Zed"}},
}

// ledgerSamples is a failure of every code of the ledger that the in-port has, with its
// values as the code says they are.
var ledgerSamples = map[usecase.LedgerFailureCode]*usecase.LedgerFailure{
	usecase.LedgerStepFailed:         {Args: []string{"create", "account", "Wallet"}, Err: errors.New("the ledger says no")},
	usecase.LedgerArchived:           {Args: []string{"area", "Daily"}},
	usecase.LedgerInstrumentDiffers:  {Args: []string{"USD", "fiat", "2", "currency", "fiat", "4", "currency"}},
	usecase.LedgerAccountKindDiffers: {Args: []string{"Cash", "bank", "cash"}},
}

// constantsOf lists the values of the constants of a type that a file declares.
func constantsOf(t *testing.T, path, typeName string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var values []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok || len(value.Values) != 1 {
				continue
			}
			kind, ok := value.Type.(*ast.Ident)
			if !ok || kind.Name != typeName {
				continue
			}
			lit, ok := value.Values[0].(*ast.BasicLit)
			if !ok {
				continue
			}
			text, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("constant %s of %s: %v", value.Names[0].Name, path, err)
			}
			values = append(values, text)
		}
	}
	if len(values) == 0 {
		t.Fatalf("no constant of the type %s in %s", typeName, path)
	}
	return values
}

// told is what the command says of an error: the error that the user reads.
func told(err error) error {
	var b strings.Builder
	return cli.Refusal(&b, err)
}

// quotedName finds what is in 「」, which is a name and is whatever language it is.
var quotedName = regexp.MustCompile(`「[^」]*」`)

func hasChinese(text string) bool {
	return strings.ContainsFunc(text, func(r rune) bool { return unicode.Is(unicode.Han, r) })
}

// wantToldInChinese checks what the user reads of a failure: text in Chinese that is not
// the code as the logs have it, on one line, in the words of the glossary, that begins
// with the file and has the line when the failure is about them. What is in 「」 is a
// name, and the path and the reply of the system or the ledger are theirs; the rest is
// what the import says, and it has no sentence of English.
func wantToldInChinese(t *testing.T, code, text, logged, file string, line int, reply error) {
	t.Helper()
	if text == logged || !hasChinese(text) {
		t.Fatalf("told as %q, want text in Chinese and not the code", text)
	}
	if file != "" && !strings.HasPrefix(text, file) {
		t.Errorf("%q does not begin with the file %q", text, file)
	}
	if want := fmt.Sprintf("第 %d 行", line); line > 0 && !strings.Contains(text, want) {
		t.Errorf("%q does not say %q", text, want)
	}
	if strings.Contains(text, "\n") {
		t.Errorf("%q has a line break, which the error of a command does not", text)
	}
	if strings.Contains(strings.ReplaceAll(text, "「幣種」", ""), "幣種") {
		t.Errorf("%q says 幣種: the glossary has 幣別", text)
	}
	checkWording(t, code, text)

	prose := quotedName.ReplaceAllString(text, "")
	if file != "" {
		prose = strings.ReplaceAll(prose, file, "")
	}
	if reply != nil {
		prose = strings.ReplaceAll(prose, reply.Error(), "")
	}
	if m := asciiSentence.FindString(prose); m != "" {
		t.Errorf("%q has an English sentence: %q", text, m)
	}
}

// Every code of failure of a file that the out-port has is told in Chinese, with the
// file it is about and the line, and with what to do about it. A code that is added to
// the out-port without its text, or without a sample here, fails this test.
func TestEveryFailureOfAFileIsToldInChineseWithItsPlace(t *testing.T) {
	codes := constantsOf(t, fileFailurePath, "Code")
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			sample, ok := fileSamples[filefailure.Code(code)]
			if !ok {
				t.Fatalf("no sample of %q in fileSamples: add one, and the text of the code to the command line", code)
			}
			f := *sample
			f.Code = filefailure.Code(code)
			got := told(&f)
			if got == nil {
				t.Fatal("Refusal gave no error")
			}
			wantToldInChinese(t, code, got.Error(), f.Error(), f.File, f.Line, f.Err)
		})
	}
	for code := range fileSamples {
		if !slices.Contains(codes, string(code)) {
			t.Errorf("fileSamples has %q, which is not a code of the out-port", code)
		}
	}
}

// Every code of failure of the ledger that the in-port has is told in Chinese, with what
// to do about it. A code that is added to the in-port without its text, or without a
// sample here, fails this test.
func TestEveryFailureOfTheLedgerIsToldInChinese(t *testing.T) {
	codes := constantsOf(t, ledgerFailurePath, "LedgerFailureCode")
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			sample, ok := ledgerSamples[usecase.LedgerFailureCode(code)]
			if !ok {
				t.Fatalf("no sample of %q in ledgerSamples: add one, and the text of the code to the command line", code)
			}
			f := *sample
			f.Code = usecase.LedgerFailureCode(code)
			got := told(&f)
			if got == nil {
				t.Fatal("Refusal gave no error")
			}
			wantToldInChinese(t, code, got.Error(), f.Error(), "", 0, f.Err)
		})
	}
	for code := range ledgerSamples {
		if !slices.Contains(codes, string(code)) {
			t.Errorf("ledgerSamples has %q, which is not a code of the in-port", code)
		}
	}
}

// What a failure of a file says, in full, for the codes whose words are to be kept. A
// name is in 「」, the file and the line come first, and the text says what to do.
func TestWhatTheFailuresOfAFileSay(t *testing.T) {
	tests := []struct {
		code filefailure.Code
		want string
	}{
		{filefailure.Missing, "/data/MOZE_2026.csv：找不到這個檔案；--file 要指到 MOZE 匯出的 CSV"},
		{filefailure.Unreadable, "/data/mapping.yaml：讀不到這個檔案（系統回報：permission denied）；確認它是一個檔案，而且你有讀取它的權限"},
		{filefailure.Empty, "/data/MOZE_2026.csv：檔案是空的，連標題列都沒有"},
		{filefailure.ColumnsMissing, "/data/MOZE_2026.csv 第 1 行：標題列缺少欄位「金額」、「日期」；這些欄位名稱要一字不差地寫在標題列裡"},
		{filefailure.FieldCount, "/data/MOZE_2026.csv 第 7 行：這一筆有 3 個欄位，標題列有 16 個；欄位數要一樣（內容裡有逗號或換行時，整個欄位要用雙引號包起來）"},
		{filefailure.CSVQuote, "/data/MOZE_2026.csv 第 9 行：雙引號沒有成對：開了沒有關，或是關掉之後後面還有字（這一筆從第 5 行開始）"},
		{filefailure.NotDay, "/data/MOZE_2026.csv 第 4 行：「日期」的值「2026/13/45」不是日期，要寫成像「2026/03/10」這樣"},
		{filefailure.NotTime, "/data/MOZE_2026.csv 第 4 行：「時間」的值「noon」不是時間，要寫成像「15:04」這樣"},
		{filefailure.RateTwice, "/data/rates.csv 第 5 行：「USD」在「2026-04-01」已經有一個匯率了（第 2 行）；一天只能有一個，留一個、刪掉另一個"},
		{filefailure.KeyTwice, "/data/mapping.yaml 第 6 行：「accounts」底下有兩個「Zed」（第一個在第 3 行）；每個名稱只能有一個，刪掉或改名其中一個"},
		{filefailure.KeyUnknown, "/data/mapping.yaml 第 3 行：「accounts」裡的「Zed」底下有不認得的鍵「kindd」；可以用的有：「name」、「kind」、「institution」。是不是打錯了？"},
		{filefailure.MustBeMapping, "/data/mapping.yaml 第 2 行：「accounts」裡的「Zed」要是「鍵: 值」的對照（寫成大括號，或逐行縮排），現在不是"},
		{filefailure.EntryNotWritable, "/data/mapping.yaml：「accounts」裡的「\"two\\nlines\"」寫不成對照檔的一行（名稱不能換行、不能超過 128 個字，也要是合法的 UTF-8）。" +
			"什麼都沒有寫入；自己把這個項目寫進對照檔（名稱用雙引號包起來），或在 MOZE 裡改掉這個名稱"},
		{filefailure.Braces, "/data/mapping.yaml 第 1 行：「parties」寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成逐行縮排的寫法，或自己把缺的項目加進去"},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			f := *fileSamples[tt.code]
			f.Code = tt.code
			if got := told(&f); got == nil || got.Error() != tt.want {
				t.Errorf("told as %v\nwant %s", got, tt.want)
			}
		})
	}
}

// What a failure of the ledger says, in full.
func TestWhatTheFailuresOfTheLedgerSay(t *testing.T) {
	tests := []struct {
		code usecase.LedgerFailureCode
		want string
	}{
		{usecase.LedgerStepFailed, "建立帳戶「Wallet」失敗，帳本的回覆：the ledger says no"},
		{usecase.LedgerArchived, "帳本裡的領域「Daily」已經封存，對照檔卻用到它；改對照檔，用別的名稱（帳本裡已封存的不能恢復使用）"},
		{usecase.LedgerInstrumentDiffers, "標的「USD」在帳本裡是「fiat」、2 位小數、是幣別，對照檔寫的是「fiat」、4 位小數、是幣別；兩邊要一致，改對照檔（帳本裡已建立的標的與帳戶不能修改）"},
		{usecase.LedgerAccountKindDiffers, "帳戶「Cash」在帳本裡的種類是「bank」，對照檔寫的是「cash」；兩邊要一致，改對照檔（帳本裡已建立的標的與帳戶不能修改）"},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			f := *ledgerSamples[tt.code]
			f.Code = tt.code
			if got := told(&f); got == nil || got.Error() != tt.want {
				t.Errorf("told as %v\nwant %s", got, tt.want)
			}
		})
	}
}

// The ledger cannot change an instrument it has, nor the kind of an account: instruments
// are created and archived, and an account is created, given the days of its statements
// and archived. So what the user is told to do about the two of them that differ from
// the mapping is to change the mapping, and the ledger is not offered as a way out.
func TestWhatTheLedgerCannotChangeIsNotOfferedAsAWayOut(t *testing.T) {
	const wantEnd = "改對照檔（帳本裡已建立的標的與帳戶不能修改）"
	for _, code := range []usecase.LedgerFailureCode{usecase.LedgerInstrumentDiffers, usecase.LedgerAccountKindDiffers} {
		t.Run(string(code), func(t *testing.T) {
			f := *ledgerSamples[code]
			f.Code = code
			got := told(&f)
			if got == nil {
				t.Fatal("Refusal gave no error")
			}
			if !strings.HasSuffix(got.Error(), wantEnd) {
				t.Errorf("told as %q, want it to end with %q", got, wantEnd)
			}
			if strings.Contains(got.Error(), "改帳本") {
				t.Errorf("told as %q, which offers the ledger as a way out: it cannot be changed", got)
			}
		})
	}
}

// An archived instrument, account or area cannot be brought back either: the ledger can
// archive them and has nothing that takes one out of the archive. So the text says to use
// another name in the mapping, and does not offer to bring the thing back.
func TestWhatTheLedgerCannotBringBackIsNotOfferedAsAWayOut(t *testing.T) {
	const wantEnd = "改對照檔，用別的名稱（帳本裡已封存的不能恢復使用）"
	f := *ledgerSamples[usecase.LedgerArchived]
	f.Code = usecase.LedgerArchived
	got := told(&f)
	if got == nil {
		t.Fatal("Refusal gave no error")
	}
	if !strings.HasSuffix(got.Error(), wantEnd) {
		t.Errorf("told as %q, want it to end with %q", got, wantEnd)
	}
	if strings.Contains(got.Error(), "把它恢復使用") || strings.Contains(got.Error(), "改帳本") {
		t.Errorf("told as %q, which offers the ledger as a way out: it has no way to bring an archived thing back", got)
	}
}

// Where in the mapping file a failure is, is told as the file names it: a section, an
// entry of it, the part of categories that holds the expense, the top of the file.
func TestWhereInTheMappingIsToldAsTheFileNamesIt(t *testing.T) {
	tests := []struct {
		where, want string
	}{
		{"", "最上層有不認得的鍵「colour」"},
		{"accounts", "「accounts」底下有不認得的鍵「colour」"},
		{"accounts.Fake Card", "「accounts」裡的「Fake Card」底下有不認得的鍵「colour」"},
		{"categories", "「categories」底下有不認得的鍵「colour」"},
		{"categories.expense", "「categories」的「expense」底下有不認得的鍵「colour」"},
		{"categories.income.Food/Lunch", "「categories」的「income」裡的「Food/Lunch」底下有不認得的鍵「colour」"},
		{"rows.abc123-1", "「rows」裡的「abc123-1」底下有不認得的鍵「colour」"},
		{"defaults", "「defaults」底下有不認得的鍵「colour」"},
		{"accounts.A.B", "「accounts」裡的「A.B」底下有不認得的鍵「colour」"}, // a name may have a dot in it
	}
	for _, tt := range tests {
		t.Run(tt.where, func(t *testing.T) {
			got := told(&filefailure.Failure{Code: filefailure.KeyUnknown, Line: 2, Args: []string{"colour", tt.where, "name"}})
			if got == nil || !strings.HasPrefix(got.Error(), "第 2 行："+tt.want) {
				t.Errorf("told as %v, want it to say %s", got, tt.want)
			}
		})
	}
}

// A step of the ledger is told as the verb, the thing and its name, and the reply of the
// ledger as it is: every verb and every thing of the in-port has its word.
func TestAStepOfTheLedgerIsToldWithItsVerbAndItsThing(t *testing.T) {
	verbs := map[string]string{"list": "讀取", "get": "取得", "create": "建立", "confirm": "確認", "discard": "捨棄"}
	things := map[string]string{
		"instrument": "標的", "institution": "機構", "account": "帳戶", "category": "類別", "area": "領域",
		"project": "專案", "tag": "標籤", "party": "對象", "transaction": "交易", "balance": "餘額",
	}
	for _, verb := range constantsOf(t, ledgerFailurePath, "Verb") {
		for _, thing := range constantsOf(t, ledgerFailurePath, "Thing") {
			t.Run(verb+" "+thing, func(t *testing.T) {
				verbWord, ok := verbs[verb]
				if !ok {
					t.Fatalf("no word for the verb %q: add it here and to the command line", verb)
				}
				thingWord, ok := things[thing]
				if !ok {
					t.Fatalf("no word for the thing %q: add it here and to the command line", thing)
				}
				reply := errors.New("the ledger says no")
				got := told(usecase.StepFailure(usecase.Verb(verb), usecase.Thing(thing), "Zed", reply))
				if want := verbWord + thingWord + "「Zed」失敗，帳本的回覆：the ledger says no"; got == nil || got.Error() != want {
					t.Errorf("told as %v, want %s", got, want)
				}
				got = told(usecase.StepFailure(usecase.Verb(verb), usecase.Thing(thing), "", reply))
				if want := verbWord + thingWord + "失敗，帳本的回覆：the ledger says no"; got == nil || got.Error() != want {
					t.Errorf("told as %v, want %s", got, want)
				}
			})
		}
	}
}

// How an instrument that is not a currency is told.
func TestAnInstrumentTheLedgerHasOtherwiseSaysWhetherItIsACurrency(t *testing.T) {
	got := told(&usecase.LedgerFailure{Code: usecase.LedgerInstrumentDiffers, Args: []string{"BTC", "crypto", "8", "not_currency", "fiat", "2", "currency"}})
	want := "標的「BTC」在帳本裡是「crypto」、8 位小數、不是幣別，對照檔寫的是「fiat」、2 位小數、是幣別；兩邊要一致，改對照檔（帳本裡已建立的標的與帳戶不能修改）"
	if got == nil || got.Error() != want {
		t.Errorf("told as %v\nwant %s", got, want)
	}
}

// What wraps a failure does not change what the user is told of it.
func TestAFailureThatIsWrappedIsToldAsItIs(t *testing.T) {
	t.Run("of a file", func(t *testing.T) {
		f := &filefailure.Failure{Code: filefailure.Empty, File: "export.csv"}
		got := told(fmt.Errorf("apply: %w", f))
		if want := "export.csv：檔案是空的，連標題列都沒有"; got == nil || got.Error() != want {
			t.Errorf("told as %v, want %s", got, want)
		}
	})
	t.Run("of the ledger", func(t *testing.T) {
		f := usecase.StepFailure(usecase.VerbList, usecase.ThingTag, "", errors.New("the ledger says no"))
		got := told(fmt.Errorf("apply: %w", f))
		if want := "讀取標籤失敗，帳本的回覆：the ledger says no"; got == nil || got.Error() != want {
			t.Errorf("told as %v, want %s", got, want)
		}
	})
}

// What is not a failure, and a failure of a code that has no text, are returned as they
// are, for the command to show.
func TestWhatHasNoTextIsReturnedAsItIs(t *testing.T) {
	plain := errors.New("not a failure")
	if got := told(plain); !errors.Is(got, plain) {
		t.Errorf("told as %v, want the error as it is", got)
	}
	file := &filefailure.Failure{Code: "a_code_nobody_wrote", File: "export.csv"}
	if got := told(file); !errors.Is(got, file) {
		t.Errorf("told as %v, want the failure of the file as it is", got)
	}
	ledger := &usecase.LedgerFailure{Code: "a_code_nobody_wrote"}
	if got := told(ledger); !errors.Is(got, ledger) {
		t.Errorf("told as %v, want the failure of the ledger as it is", got)
	}
}
