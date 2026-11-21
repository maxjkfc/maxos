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
	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// failureSamples is a failure of every code the model has, as the code says it is made:
// the file and the line it is about, and its values. The names are invented, and of
// one word or two, so that anything longer in what the user reads is text.
var failureSamples = map[model.FailureCode]*model.Failure{
	model.FailureFileMissing:    {File: "/data/MOZE_2026.csv"},
	model.FailureFileUnreadable: {File: "/data/mapping.yaml", Err: errors.New("permission denied")},
	model.FailureFileNotWritten: {File: "/data/mapping.yaml", Err: errors.New("no space left on device")},
	model.FailureFileEmpty:      {File: "/data/MOZE_2026.csv"},

	model.FailureColumnTwice:    {File: "/data/MOZE_2026.csv", Line: 1, Args: []string{"金額"}},
	model.FailureColumnsMissing: {File: "/data/MOZE_2026.csv", Line: 1, Args: []string{"金額", "日期"}},
	model.FailureFieldCount:     {File: "/data/MOZE_2026.csv", Line: 7, Args: []string{"3", "16"}},
	model.FailureCSVBareQuote:   {File: "/data/MOZE_2026.csv", Line: 5},
	model.FailureCSVQuote:       {File: "/data/MOZE_2026.csv", Line: 9, Args: []string{"5"}},
	model.FailureCSVSyntax:      {File: "/data/MOZE_2026.csv", Line: 5},

	model.FailureValueEmpty:           {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"日期"}},
	model.FailureNotNumber:            {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"金額", "abc"}},
	model.FailureNotWholeNumber:       {File: "/data/mapping.yaml", Line: 3, Args: []string{"closing_day", "five"}},
	model.FailureNotBoolean:           {File: "/data/mapping.yaml", Line: 3, Args: []string{"is_currency", "maybe"}},
	model.FailureNotDay:               {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"日期", "2026/13/45", "2026/03/10"}},
	model.FailureNotTime:              {File: "/data/MOZE_2026.csv", Line: 4, Args: []string{"時間", "noon"}},
	model.FailureRateNotPositive:      {File: "/data/rates.csv", Line: 3, Args: []string{"twd_per_unit", "0"}},
	model.FailureRateTwice:            {File: "/data/rates.csv", Line: 5, Args: []string{"USD", "2026-04-01", "2"}},
	model.FailureYAMLSyntax:           {File: "/data/mapping.yaml", Line: 4},
	model.FailureSecondDocument:       {File: "/data/mapping.yaml", Line: 12},
	model.FailureNotSections:          {File: "/data/mapping.yaml", Line: 1},
	model.FailureMustBeMapping:        {File: "/data/mapping.yaml", Line: 2, Args: []string{"accounts.Zed"}},
	model.FailureKeyNotSingle:         {File: "/data/mapping.yaml", Line: 2, Args: []string{"accounts"}},
	model.FailureValueNotSingle:       {File: "/data/mapping.yaml", Line: 3, Args: []string{"name"}},
	model.FailureKeyTwice:             {File: "/data/mapping.yaml", Line: 6, Args: []string{"accounts", "Zed", "3"}},
	model.FailureKeyUnknown:           {File: "/data/mapping.yaml", Line: 3, Args: []string{"kindd", "accounts.Zed", "name", "kind", "institution"}},
	model.FailureEntryNotWritable:     {File: "/data/mapping.yaml", Args: []string{"accounts", "two\nlines"}},
	model.FailureBraces:               {File: "/data/mapping.yaml", Line: 1, Args: []string{"parties"}},
	model.FailureLayout:               {File: "/data/mapping.yaml", Line: 1, Args: []string{"accounts"}},
	model.FailureWouldReadDifferently: {File: "/data/mapping.yaml", Args: []string{"parties", "Zed"}},

	model.FailureLedger:             {Args: []string{"create", "account", "Wallet"}, Err: errors.New("the ledger says no")},
	model.FailureArchived:           {Args: []string{"area", "Daily"}},
	model.FailureInstrumentDiffers:  {Args: []string{"USD", "fiat", "2", "currency", "fiat", "4", "currency"}},
	model.FailureAccountKindDiffers: {Args: []string{"Cash", "bank", "cash"}},
}

// constantsOf lists the values of the constants of a type in the files of the model that
// have the failures, so that a code, a verb or a thing that is added is found here and
// cannot be left without its text.
func constantsOf(t *testing.T, typeName string) []string {
	t.Helper()
	var values []string
	fset := token.NewFileSet()
	for _, name := range []string{"failure.go", "failure_file.go"} {
		file, err := parser.ParseFile(fset, filepath.Join("..", "..", "..", "domain", "model", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
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
					t.Fatalf("constant %s of %s: %v", value.Names[0].Name, name, err)
				}
				values = append(values, text)
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("no constant of the type %s in the model", typeName)
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

// Every code of failure the model has is told in Chinese, with the file it is about and
// the line, and with what to do about it. A code that is added to the model without its
// text, or without a sample here, fails this test.
func TestEveryFailureIsToldInChineseWithItsPlace(t *testing.T) {
	codes := constantsOf(t, "FailureCode")
	for _, code := range codes {
		t.Run(code, func(t *testing.T) {
			sample, ok := failureSamples[model.FailureCode(code)]
			if !ok {
				t.Fatalf("no sample of %q in failureSamples: add one, and the text of the code to the command line", code)
			}
			f := *sample
			f.Code = model.FailureCode(code)

			got := told(&f)
			if got == nil {
				t.Fatal("Refusal gave no error")
			}
			text := got.Error()
			if text == f.Error() || !hasChinese(text) {
				t.Fatalf("told as %q, want text in Chinese and not the code", text)
			}
			if f.File != "" && !strings.HasPrefix(text, f.File) {
				t.Errorf("%q does not begin with the file %q", text, f.File)
			}
			if want := fmt.Sprintf("第 %d 行", f.Line); f.Line > 0 && !strings.Contains(text, want) {
				t.Errorf("%q does not say %q", text, want)
			}
			if strings.Contains(text, "\n") {
				t.Errorf("%q has a line break, which the error of a command does not", text)
			}
			if strings.Contains(strings.ReplaceAll(text, "「幣種」", ""), "幣種") {
				t.Errorf("%q says 幣種: the glossary has 幣別", text)
			}
			checkWording(t, code, text)

			// What is in 「」 is a name, and the path and the reply of the system or the
			// ledger are theirs: the rest is what the import says, and it is Chinese.
			prose := quotedName.ReplaceAllString(text, "")
			prose = strings.ReplaceAll(prose, f.File, "")
			if f.Err != nil {
				prose = strings.ReplaceAll(prose, f.Err.Error(), "")
			}
			if m := asciiSentence.FindString(prose); m != "" {
				t.Errorf("%q has an English sentence: %q", text, m)
			}
		})
	}
	for code := range failureSamples {
		if !slices.Contains(codes, string(code)) {
			t.Errorf("failureSamples has %q, which is not a code of the model", code)
		}
	}
}

// What a failure says, in full, for the codes whose words are to be kept. A name is in
// 「」, the file and the line come first, and the text says what to do.
func TestWhatTheFailuresSay(t *testing.T) {
	tests := []struct {
		code model.FailureCode
		want string
	}{
		{model.FailureFileMissing, "/data/MOZE_2026.csv：找不到這個檔案；--file 要指到 MOZE 匯出的 CSV"},
		{model.FailureFileUnreadable, "/data/mapping.yaml：讀不到這個檔案（系統回報：permission denied）；確認它是一個檔案，而且你有讀取它的權限"},
		{model.FailureFileEmpty, "/data/MOZE_2026.csv：檔案是空的，連標題列都沒有"},
		{model.FailureColumnsMissing, "/data/MOZE_2026.csv 第 1 行：標題列缺少欄位「金額」、「日期」；這些欄位名稱要一字不差地寫在標題列裡"},
		{model.FailureFieldCount, "/data/MOZE_2026.csv 第 7 行：這一筆有 3 個欄位，標題列有 16 個；欄位數要一樣（內容裡有逗號或換行時，整個欄位要用雙引號包起來）"},
		{model.FailureCSVQuote, "/data/MOZE_2026.csv 第 9 行：雙引號沒有成對：開了沒有關，或是關掉之後後面還有字（這一筆從第 5 行開始）"},
		{model.FailureNotDay, "/data/MOZE_2026.csv 第 4 行：「日期」的值「2026/13/45」不是日期，要寫成像「2026/03/10」這樣"},
		{model.FailureNotTime, "/data/MOZE_2026.csv 第 4 行：「時間」的值「noon」不是時間，要寫成像「15:04」這樣"},
		{model.FailureRateTwice, "/data/rates.csv 第 5 行：「USD」在「2026-04-01」已經有一個匯率了（第 2 行）；一天只能有一個，留一個、刪掉另一個"},
		{model.FailureKeyTwice, "/data/mapping.yaml 第 6 行：「accounts」底下有兩個「Zed」（第一個在第 3 行）；每個名稱只能有一個，刪掉或改名其中一個"},
		{model.FailureKeyUnknown, "/data/mapping.yaml 第 3 行：「accounts」裡的「Zed」底下有不認得的鍵「kindd」；可以用的有：「name」、「kind」、「institution」。是不是打錯了？"},
		{model.FailureMustBeMapping, "/data/mapping.yaml 第 2 行：「accounts」裡的「Zed」要是「鍵: 值」的對照（寫成大括號，或逐行縮排），現在不是"},
		{model.FailureEntryNotWritable, "/data/mapping.yaml：「accounts」裡的「\"two\\nlines\"」寫不成對照檔的一行（名稱不能換行、不能超過 128 個字，也要是合法的 UTF-8）。" +
			"什麼都沒有寫入；自己把這個項目寫進對照檔（名稱用雙引號包起來），或在 MOZE 裡改掉這個名稱"},
		{model.FailureBraces, "/data/mapping.yaml 第 1 行：「parties」寫在大括號裡，程式沒辦法在裡面補項目。什麼都沒有寫入；改成逐行縮排的寫法，或自己把缺的項目加進去"},
		{model.FailureLedger, "建立帳戶「Wallet」失敗，帳本的回覆：the ledger says no"},
		{model.FailureArchived, "帳本裡的領域「Daily」已經封存，對照檔卻用到它；在帳本裡把它恢復使用，或讓對照檔改用別的名稱"},
		{model.FailureInstrumentDiffers, "標的「USD」在帳本裡是「fiat」、2 位小數、是幣別，對照檔寫的是「fiat」、4 位小數、是幣別；兩邊要一致，改對照檔或改帳本都可以"},
		{model.FailureAccountKindDiffers, "帳戶「Cash」在帳本裡的種類是「bank」，對照檔寫的是「cash」；兩邊要一致，改對照檔或改帳本都可以"},
	}
	for _, tt := range tests {
		t.Run(string(tt.code), func(t *testing.T) {
			f := *failureSamples[tt.code]
			f.Code = tt.code
			if got := told(&f); got == nil || got.Error() != tt.want {
				t.Errorf("told as %v\nwant %s", got, tt.want)
			}
		})
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
			got := told(&model.Failure{Code: model.FailureKeyUnknown, Line: 2, Args: []string{"colour", tt.where, "name"}})
			if got == nil || !strings.HasPrefix(got.Error(), "第 2 行："+tt.want) {
				t.Errorf("told as %v, want it to say %s", got, tt.want)
			}
		})
	}
}

// A step of the ledger is told as the verb, the thing and its name, and the reply of the
// ledger as it is: every verb and every thing of the model has its word.
func TestAStepOfTheLedgerIsToldWithItsVerbAndItsThing(t *testing.T) {
	verbs := map[string]string{"list": "讀取", "get": "取得", "create": "建立", "confirm": "確認", "discard": "捨棄"}
	things := map[string]string{
		"instrument": "標的", "institution": "機構", "account": "帳戶", "category": "類別", "area": "領域",
		"project": "專案", "tag": "標籤", "party": "對象", "transaction": "交易", "balance": "餘額",
	}
	for _, verb := range constantsOf(t, "Verb") {
		for _, thing := range constantsOf(t, "Thing") {
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
				got := told(model.LedgerFailure(model.Verb(verb), model.Thing(thing), "Zed", reply))
				if want := verbWord + thingWord + "「Zed」失敗，帳本的回覆：the ledger says no"; got == nil || got.Error() != want {
					t.Errorf("told as %v, want %s", got, want)
				}
				got = told(model.LedgerFailure(model.Verb(verb), model.Thing(thing), "", reply))
				if want := verbWord + thingWord + "失敗，帳本的回覆：the ledger says no"; got == nil || got.Error() != want {
					t.Errorf("told as %v, want %s", got, want)
				}
			})
		}
	}
}

// How an instrument that is not a currency is told, and one the text does not know.
func TestAnInstrumentTheLedgerHasOtherwiseSaysWhetherItIsACurrency(t *testing.T) {
	got := told(&model.Failure{Code: model.FailureInstrumentDiffers, Args: []string{"BTC", "crypto", "8", "not_currency", "fiat", "2", "currency"}})
	want := "標的「BTC」在帳本裡是「crypto」、8 位小數、不是幣別，對照檔寫的是「fiat」、2 位小數、是幣別；兩邊要一致，改對照檔或改帳本都可以"
	if got == nil || got.Error() != want {
		t.Errorf("told as %v\nwant %s", got, want)
	}
}

// What wraps a failure does not change what the user is told of it.
func TestAFailureThatIsWrappedIsToldAsItIs(t *testing.T) {
	f := &model.Failure{Code: model.FailureFileEmpty, File: "export.csv"}
	got := told(fmt.Errorf("apply: %w", f))
	if want := "export.csv：檔案是空的，連標題列都沒有"; got == nil || got.Error() != want {
		t.Errorf("told as %v, want %s", got, want)
	}
}

// What is not a failure, and a failure of a code that has no text, are returned as they
// are, for the command to show.
func TestWhatHasNoTextIsReturnedAsItIs(t *testing.T) {
	plain := errors.New("not a failure")
	if got := told(plain); !errors.Is(got, plain) {
		t.Errorf("told as %v, want the error as it is", got)
	}
	unknown := &model.Failure{Code: "a_code_nobody_wrote", File: "export.csv"}
	if got := told(unknown); !errors.Is(got, unknown) {
		t.Errorf("told as %v, want the failure as it is", got)
	}
}
