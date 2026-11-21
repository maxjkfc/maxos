package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.uber.org/fx"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	financeclock "github.com/maxjkfc/maxos/app/finance/usecase/port/out/clock"
	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/pkg/config"
)

// What stops an import is told in Chinese by the real command: the file, the line, the
// name and what to do about it, for what is wrong with the files it reads and for the
// reference data that the ledger has otherwise than the mapping says. The words that
// are not Chinese are names, which are in 「」, or the reason of the system and the reply
// of the ledger, which are shown as they are. The data of these tests is invented.

// asciiSentence finds three or more words of plain ASCII letters in a row: what an
// English sentence looks like. Keys, codes and invented names of one or two words are not.
var asciiSentence = regexp.MustCompile(`[A-Za-z]{2,} [A-Za-z]{2,} [A-Za-z]{2,}`)

// quotedName finds what is in 「」, which is a name, whatever it looks like.
var quotedName = regexp.MustCompile(`「[^」]*」`)

// wantTold checks what a command that failed printed and returned: it failed, it says
// each of the fragments, and outside the names it has no sentence of English.
func wantTold(t *testing.T, out string, err error, fragments ...string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the command did not fail, and printed:\n%s", out)
	}
	all := out + "\n" + err.Error()
	for _, f := range fragments {
		if !strings.Contains(all, f) {
			t.Errorf("the command does not say %q; it printed:\n%s", f, all)
		}
	}
	if m := asciiSentence.FindString(quotedName.ReplaceAllString(all, "")); m != "" {
		t.Errorf("the command says %q, a sentence in English; it printed:\n%s", m, all)
	}
}

// newPlanEnv is a folder for the commands that touch no database, which is plan: the
// rates of the other tests are in it, and the export and the mapping are for the test
// to write. The folder is where the command looks for them, as it does for the user.
func newPlanEnv(t *testing.T) importEnv {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "rates.csv"), []byte(mozeRates), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.MozeDirVariable, dir)
	t.Setenv("MAXOS_TIMEZONE", "Asia/Taipei")
	return importEnv{t: t, dir: dir}
}

// write puts a file in the folder, and returns its path.
func (e importEnv) write(name, text string) string {
	e.t.Helper()
	path := filepath.Join(e.dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return path
}

// exportOf is the text of an export that has the header and the rows, as they are: a
// row that has not the fields of the header is a row of the file that is wrong.
func exportOf(t *testing.T, header []string, rows ...[]string) string {
	t.Helper()
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	for _, row := range append([][]string{header}, rows...) {
		if err := w.Write(row); err != nil {
			t.Fatal(err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// lunch is an export of two rows that the plan has nothing to say against: an opening
// balance and an expense, in the account of a wallet.
func lunch() [][]string {
	return [][]string{
		r("初始金額", "Fake Cash", "TWD", "1000", ""),
		r("支出", "Fake Cash", "TWD", "-100", "2026-01-05 12:30", "主類別", "飲食", "子類別", "午餐"),
	}
}

// withField gives the row of lunch at the index the value in the column.
func withField(t *testing.T, rows [][]string, index int, column, value string) [][]string {
	t.Helper()
	at := -1
	for i, h := range mozeHeader {
		if h == column {
			at = i
		}
	}
	if at < 0 {
		t.Fatalf("no column %q", column)
	}
	rows[index][at] = value
	return rows
}

func TestImportTellsWhatIsWrongWithItsFilesInChinese(t *testing.T) {
	t.Run("an export that is not there", func(t *testing.T) {
		e := newPlanEnv(t)
		path := filepath.Join(e.dir, "none.csv")
		out, err := e.moze("plan", "--file", path)
		wantTold(t, out, err, path+"：找不到這個檔案；--file 要指到 MOZE 匯出的 CSV")
	})

	csvTests := []struct {
		name   string
		header []string
		rows   func(t *testing.T) [][]string
		want   string // after the path of the export
	}{
		{"an amount that is not a number", mozeHeader, func(t *testing.T) [][]string { return withField(t, lunch(), 1, "金額", "abc") },
			" 第 3 行：「金額」的值「abc」不是數字；只能有數字、小數點和正負號"},
		{"a day that is not one", mozeHeader, func(t *testing.T) [][]string { return withField(t, lunch(), 1, "日期", "2026/13/45") },
			" 第 3 行：「日期」的值「2026/13/45」不是日期，要寫成像「2026/03/10」這樣"},
		{"a row that has no day", mozeHeader, func(t *testing.T) [][]string { return withField(t, lunch(), 1, "日期", "") },
			" 第 3 行：「日期」是空的，這裡一定要有值"},
		{"a row that has too few fields", mozeHeader, func(t *testing.T) [][]string { return append(lunch(), []string{"Fake Cash", "TWD", "支出"}) },
			" 第 4 行：這一筆有 3 個欄位，標題列有 16 個；欄位數要一樣（內容裡有逗號或換行時，整個欄位要用雙引號包起來）"},
		{"a header that lacks a column", append(append([]string{}, mozeHeader[:5]...), mozeHeader[6:]...), func(t *testing.T) [][]string { return nil },
			" 第 1 行：標題列缺少欄位「金額」；這些欄位名稱要一字不差地寫在標題列裡"},
	}
	for _, tt := range csvTests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPlanEnv(t)
			path := e.write("MOZE_20260401_000000.csv", exportOf(t, tt.header, tt.rows(t)...))
			out, err := e.moze("plan")
			wantTold(t, out, err, path+tt.want)
		})
	}

	t.Run("a quote that is not closed", func(t *testing.T) {
		e := newPlanEnv(t)
		text := exportOf(t, mozeHeader, lunch()...) + "Fake Cash,TWD,支出,飲食,午餐,-10,0,0,\"Lunch\n"
		path := e.write("MOZE_20260401_000000.csv", text)
		out, err := e.moze("plan")
		wantTold(t, out, err, path+" 第 4 行：雙引號沒有成對：開了沒有關，或是關掉之後後面還有字")
	})

	yamlTests := []struct {
		name    string
		mapping string
		want    string // after the path of the mapping
	}{
		{"a mapping that is not YAML", "accounts: [a\nparties: {}\n", " 第 1 行：不是合法的 YAML（括號、引號或縮排有誤）；改好才能讀"},
		{"a second document", "accounts: {}\n---\nparties: {}\n", " 第 3 行：檔案裡有第二份 YAML 文件（用 --- 隔開的）；對照檔只能有一份，把後面的內容併進前面，或刪掉"},
		{"a key that is not known", "accounts:\n  Zed: {name: Zed, kindd: card}\n",
			" 第 2 行：「accounts」裡的「Zed」底下有不認得的鍵「kindd」；可以用的有：" +
				"「name」、「kind」、「institution」、「closing_day」、「payment_due_day」、「opening_date」。是不是打錯了？"},
		{"a section that is not known", "accountz:\n  Zed: {name: Zed}\n", " 第 1 行：最上層有不認得的鍵「accountz」；可以用的有：「instruments」"},
		{"a key twice", "accounts:\n  Zed: {name: Zed}\n  Zed: {name: Zed}\n", " 第 3 行：「accounts」底下有兩個「Zed」（第一個在第 2 行）；每個名稱只能有一個，刪掉或改名其中一個"},
		{"a section twice", "accounts: {}\nparties: {}\naccounts: {}\n", " 第 3 行：最上層有兩個「accounts」（第一個在第 1 行）；每個名稱只能有一個，刪掉或改名其中一個"},
		{"a number that is not one", "accounts:\n  Zed: {name: Zed, kind: card, closing_day: five}\n", " 第 2 行：「closing_day」的值「five」不是整數"},
		{"a day that is not one", "accounts:\n  Zed: {name: Zed, opening_date: yesterday}\n", " 第 2 行：「opening_date」的值「yesterday」不是日期，要寫成像「2026-04-15」這樣"},
		{"an entry that is not a mapping", "accounts:\n  Zed: card\n", " 第 2 行：「accounts」裡的「Zed」要是「鍵: 值」的對照（寫成大括號，或逐行縮排），現在不是"},
	}
	for _, tt := range yamlTests {
		t.Run(tt.name, func(t *testing.T) {
			e := newPlanEnv(t)
			e.write("MOZE_20260401_000000.csv", exportOf(t, mozeHeader, lunch()...))
			path := e.write("mapping.yaml", tt.mapping)
			out, err := e.moze("plan")
			wantTold(t, out, err, path+tt.want)
		})
	}

	ratesTests := []struct {
		name  string
		rates string
		want  string // after the path of the rates
	}{
		{"a rate that is not a number", "date,instrument,twd_per_unit\n2026-01-01,USD,abc\n", " 第 2 行：「twd_per_unit」的值「abc」不是數字；只能有數字、小數點和正負號"},
		{"a rate that is not above zero", "# a comment\ndate,instrument,twd_per_unit\n2026-01-01,USD,0\n", " 第 3 行：「twd_per_unit」的值「0」要大於 0；它是一單位標的值多少 TWD"},
		{"a day that is not one", "date,instrument,twd_per_unit\n2026/01/01,USD,30\n", " 第 2 行：「date」的值「2026/01/01」不是日期，要寫成像「2026-03-10」這樣"},
		{"two rates for a day", "date,instrument,twd_per_unit\n2026-01-01,USD,30\n2026-01-01,usd,31\n", " 第 3 行：「USD」在「2026-01-01」已經有一個匯率了（第 2 行）；一天只能有一個，留一個、刪掉另一個"},
		{"a header that lacks a column", "date,instrument\n2026-01-01,USD\n", " 第 1 行：標題列缺少欄位「twd_per_unit」；這些欄位名稱要一字不差地寫在標題列裡"},
	}
	for _, tt := range ratesTests {
		t.Run("rates: "+tt.name, func(t *testing.T) {
			e := newPlanEnv(t)
			e.write("MOZE_20260401_000000.csv", exportOf(t, mozeHeader, lunch()...))
			path := e.write("rates.csv", tt.rates)
			out, err := e.moze("plan")
			wantTold(t, out, err, path+tt.want)
		})
	}
}

// A row in a currency that the rates do not have is not a failure of a file but of the
// plan, which says so in its report, in Chinese, with the entry that is to be put right.
func TestImportTellsInChineseThatARateIsMissing(t *testing.T) {
	e := newPlanEnv(t)
	rows := append(lunch(), r("支出", "Fake USD", "USD", "-10", "2026-01-06 18:00", "主類別", "飲食", "子類別", "午餐"))
	e.write("MOZE_20260401_000000.csv", exportOf(t, mozeHeader, rows...))
	e.write("rates.csv", "date,instrument,twd_per_unit\n2026-02-01,JPY,0.2\n")

	// The first plan writes the guesses to the mapping; the second reads them.
	if out, err := e.moze("plan"); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	out, err := e.moze("plan")
	if err != nil {
		t.Fatalf("plan again: %v\n%s", err, out)
	}
	if want := "rates.USD：USD 在 2026-01-06 或更早沒有匯率"; !strings.Contains(out, want) {
		t.Errorf("the report does not say %q:\n%s", want, out)
	}
	if m := asciiSentence.FindString(quotedName.ReplaceAllString(out, "")); m != "" {
		t.Errorf("the report says %q, a sentence in English:\n%s", m, out)
	}
}

// conflictTools are the use cases that make the ledger what a test wants it to be
// before the import runs: reference data that is there already.
type conflictTools struct {
	fx.In

	CreateAccount    finance.CreateAccount
	ArchiveAccount   finance.ArchiveAccount
	CreateInstrument finance.CreateInstrument
	CreateArea       catalog.CreateArea
	ArchiveArea      catalog.ArchiveArea
}

func (e importEnv) tools() conflictTools {
	e.t.Helper()
	var tools conflictTools
	app := fx.New(
		fx.NopLogger,
		fx.Supply(e.pool),
		fx.Provide(func() financeclock.Clock { return fixedClock{} }),
		CatalogModule(),
		FinanceModule(),
		fx.Populate(&tools),
	)
	if err := app.Err(); err != nil {
		e.t.Fatalf("assemble the tools: %v", err)
	}
	return tools
}

// reviewedMapping plans the export, so that the mapping has the guesses, takes the marks
// away as the user does, and returns what the mapping says.
func (e importEnv) reviewedMapping() model.Mapping {
	e.t.Helper()
	e.must("plan")
	e.review()
	loaded, err := file.NewMappingYAML().Load(context.Background(), filepath.Join(e.dir, "mapping.yaml"))
	if err != nil {
		e.t.Fatalf("load the mapping: %v", err)
	}
	return loaded.Mapping
}

// The reference data that the ledger has, and the mapping names, is not made again, and
// is not used when it is not what the mapping says, or when it is archived: the command
// stops, and tells the conflict in Chinese, with the name and the two sides.
func TestImportTellsInChineseWhatTheLedgerHasOtherwise(t *testing.T) {
	ctx := context.Background()

	t.Run("an account of another kind", func(t *testing.T) {
		e := newImportEnv(t)
		m := e.reviewedMapping().Accounts["Fake Cash"]
		other := "bank"
		if m.Kind == other {
			other = "cash"
		}
		if _, err := e.tools().CreateAccount.CreateAccount(ctx, finance.CreateAccountInput{Name: m.Name, Kind: other}); err != nil {
			t.Fatal(err)
		}
		out, err := e.moze("apply")
		wantTold(t, out, err, fmt.Sprintf("帳戶「%s」在帳本裡的種類是「%s」，對照檔寫的是「%s」；兩邊要一致，改對照檔或改帳本都可以", m.Name, other, m.Kind))
	})

	t.Run("an instrument that is not what the mapping says", func(t *testing.T) {
		e := newImportEnv(t)
		m := e.reviewedMapping().Instruments["USD"]
		in := finance.CreateInstrumentInput{Code: "USD", Name: "Dollar", Kind: m.Kind, IsCurrency: m.IsCurrency, Decimals: m.Decimals + 1}
		if _, err := e.tools().CreateInstrument.CreateInstrument(ctx, in); err != nil {
			t.Fatal(err)
		}
		flag := func(isCurrency bool) string {
			if isCurrency {
				return "是幣別"
			}
			return "不是幣別"
		}
		out, err := e.moze("apply")
		wantTold(t, out, err, fmt.Sprintf("標的「USD」在帳本裡是「%s」、%d 位小數、%s，對照檔寫的是「%s」、%d 位小數、%s；兩邊要一致，改對照檔或改帳本都可以",
			in.Kind, in.Decimals, flag(in.IsCurrency), m.Kind, m.Decimals, flag(m.IsCurrency)))
	})

	t.Run("an account that is archived", func(t *testing.T) {
		e := newImportEnv(t)
		m := e.reviewedMapping().Accounts["Fake Cash"]
		tools := e.tools()
		made, err := tools.CreateAccount.CreateAccount(ctx, finance.CreateAccountInput{Name: m.Name, Kind: m.Kind})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tools.ArchiveAccount.ArchiveAccount(ctx, made.ID); err != nil {
			t.Fatal(err)
		}
		out, err := e.moze("apply")
		wantTold(t, out, err, fmt.Sprintf("帳本裡的帳戶「%s」已經封存，對照檔卻用到它；在帳本裡把它恢復使用，或讓對照檔改用別的名稱", m.Name))
	})

	t.Run("an area that is archived", func(t *testing.T) {
		e := newImportEnv(t)
		var area string
		for _, p := range e.reviewedMapping().Projects {
			if p.Kind == model.ProjectArea {
				area = p.Name
				break
			}
		}
		if area == "" {
			t.Fatal("the mapping has no area: the export of the test names one")
		}
		tools := e.tools()
		made, err := tools.CreateArea.CreateArea(ctx, catalog.CreateAreaInput{Name: area})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tools.ArchiveArea.ArchiveArea(ctx, made.ID); err != nil {
			t.Fatal(err)
		}
		out, err := e.moze("apply")
		wantTold(t, out, err, fmt.Sprintf("帳本裡的領域「%s」已經封存，對照檔卻用到它；在帳本裡把它恢復使用，或讓對照檔改用別的名稱", area))
	})
}

// A currency that the rates do not have is something to put right before apply: it
// refuses, and says in Chinese which rate is missing.
func TestImportApplyTellsInChineseThatARateIsMissing(t *testing.T) {
	e := newImportEnv(t)
	e.reviewedMapping()
	if err := os.Remove(filepath.Join(e.dir, "rates.csv")); err != nil {
		t.Fatal(err)
	}
	out, err := e.moze("apply")
	wantTold(t, out, err, "對照檔還有要處理的項目", "rates.USD：USD 在 2026-01-06 或更早沒有匯率")
}

// A mapping file that cannot be read stops apply as it stops plan, with the same words:
// the files are read before the ledger is touched.
func TestImportApplyTellsInChineseWhatIsWrongWithTheMapping(t *testing.T) {
	e := newImportEnv(t)
	path := e.write("mapping.yaml", "accounts:\n  Zed: {name: Zed, kindd: card}\n")
	out, err := e.moze("apply")
	wantTold(t, out, err, path+" 第 2 行：「accounts」裡的「Zed」底下有不認得的鍵「kindd」")
	listed, err := e.ledger().ListAccounts.ListAccounts(context.Background(), finance.ListAccountsInput{IncludeArchived: true})
	if err != nil || len(listed.Accounts) != 0 {
		t.Errorf("accounts = %d, %v, want none: nothing is made before the files are read", len(listed.Accounts), err)
	}
}
