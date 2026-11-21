package file_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// canon is a mapping as lines of text, each entry on one in the order of the keys,
// for comparing mappings that have times and decimals in them.
func canon(m model.Mapping) []string {
	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }
	for _, k := range slices.Sorted(maps.Keys(m.Instruments)) {
		add("instruments[%q] %+v", k, m.Instruments[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Accounts)) {
		add("accounts[%q] %+v", k, m.Accounts[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Projects)) {
		add("projects[%q] %+v", k, m.Projects[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Expense)) {
		add("expense[%q] %+v", k, m.Expense[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Income)) {
		add("income[%q] %+v", k, m.Income[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Parties)) {
		add("parties[%q] %q", k, m.Parties[k])
	}
	for _, k := range slices.Sorted(maps.Keys(m.Tags)) {
		add("tags[%q] %q", k, m.Tags[k])
	}
	add("defaults %+v", m.Defaults)
	for _, k := range slices.Sorted(maps.Keys(m.Rows)) {
		o := m.Rows[k]
		quantity := "none"
		if o.Quantity != nil {
			quantity = o.Quantity.String()
		}
		add("rows[%q] treatment=%q category=%q party=%q tag=%q quantity=%s", k, o.Treatment, o.Category, o.Party, o.Tag, quantity)
	}
	return out
}

// sameMapping checks that two mappings say the same.
func sameMapping(t *testing.T, got, want model.Mapping) {
	t.Helper()
	g, w := canon(got), canon(want)
	if !slices.Equal(g, w) {
		t.Errorf("mapping\n got %s\nwant %s", strings.Join(g, "\n     "), strings.Join(w, "\n     "))
	}
}

func load(t *testing.T, path string) mapping.Loaded {
	t.Helper()
	loaded, err := file.NewMappingYAML().Load(context.Background(), path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return loaded
}

// loadText loads a mapping file of the text.
func loadText(t *testing.T, text string) mapping.Loaded {
	t.Helper()
	return load(t, writeFile(t, t.TempDir(), "mapping.yaml", text))
}

// loadFails loads a mapping file that must be refused, and returns the failure it is,
// which is about the file.
func loadFails(t *testing.T, text string) *model.Failure {
	t.Helper()
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	loaded, err := file.NewMappingYAML().Load(context.Background(), path)
	if err == nil {
		t.Fatalf("Load read %v from text that is wrong:\n%s", canon(loaded.Mapping), text)
	}
	f := failureOf(t, err)
	wantFile(t, f, path)
	return f
}

// The keys that a failure lists as the ones that are known, where a key is not.
var (
	sectionKeys    = []string{"instruments", "accounts", "projects", "categories", "parties", "tags", "defaults", "rows"}
	instrumentKeys = []string{"name", "kind", "is_currency", "decimals", "rate_as"}
	accountKeys    = []string{"name", "kind", "institution", "closing_day", "payment_due_day", "opening_date"}
	projectKeys    = []string{"kind", "name", "default_area", "party", "active_period"}
	categoryKeys   = []string{"category", "treatment", "party", "tag"}
	defaultsKeys   = []string{"fee_category", "interest_category", "refund_category"}
	rowKeys        = []string{"treatment", "category", "party", "tag", "quantity"}
)

// unknownKey is what the failure for a key that is not known says: the key, where it
// is, and the keys that are known there.
func unknownKey(key, where string, known []string) []string {
	return append([]string{key, where}, known...)
}

// fullMapping has every section and every key.
const fullMapping = `# A reviewed mapping, with every section and every key (the names are invented).
instruments:
  USD: {name: 美元, kind: fiat, is_currency: true, decimals: 2}
  USDT: {name: Tether USDT, kind: crypto, is_currency: true, decimals: 2, rate_as: USD}
accounts:
  "Fake Card A": {name: Fake Card A, kind: card, institution: FakeBank, closing_day: 5, payment_due_day: 20, opening_date: 2025-01-07}
  "Fake Cash": {name: Fake Cash, kind: cash, opening_date: "2025-02-03"}
  2025: {name: Year Account, kind: bank}
  true: {name: Truth Account, kind: wallet}
projects:
  "Daily": {kind: area, name: Daily}
  "2025-04 Trip": {kind: project, name: Trip, default_area: Daily}
  "Crowdfund": {kind: tag, name: Crowdfund}
  "Corp paid": {kind: dealings, party: Corp}
  "Nothing": {kind: none}
  "Block style":
    kind: project
    name: Block
    default_area: Daily
categories:
  expense:
    "Food/Lunch": Food/Lunch
    "Food/Dinner": {category: Food/Dinner, tag: 晚餐}
    "Fun": Fun/Games
  income:
    "收入/收款": {category: 收款（待確認）, tag: 待確認}
    "收入/信用卡抵扣": {treatment: reward}
    "收入/公司代收款": {treatment: dealing, party: 公司}
parties: {Zed: Zed, Amy: Amy Example}
tags: {家庭: 家庭}
defaults: {fee_category: 手續費, interest_category: 利息, refund_category: 退款}
rows:
  "abc123-1": {treatment: dealing, party: Zed}
  "abc123-2": {quantity: "0.0123"}
  "abc123-3": {quantity: 1.50, category: Food/Lunch, tag: 家庭, treatment: expense}
`

func fullMappingWant() model.Mapping {
	return model.Mapping{
		Instruments: map[string]model.InstrumentMap{
			"USD":  {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
			"USDT": {Name: "Tether USDT", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
		},
		Accounts: map[string]model.AccountMap{
			"Fake Card A": {Name: "Fake Card A", Kind: "card", Institution: "FakeBank", ClosingDay: 5, PaymentDueDay: 20, OpeningDate: day("2025-01-07")},
			"Fake Cash":   {Name: "Fake Cash", Kind: "cash", OpeningDate: day("2025-02-03")},
			"2025":        {Name: "Year Account", Kind: "bank"},
			"true":        {Name: "Truth Account", Kind: "wallet"},
		},
		Projects: map[string]model.ProjectMap{
			"Daily":        {Kind: "area", Name: "Daily"},
			"2025-04 Trip": {Kind: "project", Name: "Trip", DefaultArea: "Daily"},
			"Crowdfund":    {Kind: "tag", Name: "Crowdfund"},
			"Corp paid":    {Kind: "dealings", Party: "Corp"},
			"Nothing":      {Kind: "none"},
			"Block style":  {Kind: "project", Name: "Block", DefaultArea: "Daily"},
		},
		Expense: map[string]model.CategoryMap{
			"Food/Lunch":  {Category: "Food/Lunch"},
			"Food/Dinner": {Category: "Food/Dinner", Tag: "晚餐"},
			"Fun":         {Category: "Fun/Games"},
		},
		Income: map[string]model.CategoryMap{
			"收入/收款":    {Category: "收款（待確認）", Tag: "待確認"},
			"收入/信用卡抵扣": {Treatment: "reward"},
			"收入/公司代收款": {Treatment: "dealing", Party: "公司"},
		},
		Parties:  map[string]string{"Zed": "Zed", "Amy": "Amy Example"},
		Tags:     map[string]string{"家庭": "家庭"},
		Defaults: model.Defaults{FeeCategory: "手續費", InterestCategory: "利息", RefundCategory: "退款"},
		Rows: map[string]model.RowOverride{
			"abc123-1": {Treatment: "dealing", Party: "Zed"},
			"abc123-2": {Quantity: ptr("0.0123")},
			"abc123-3": {Treatment: "expense", Category: "Food/Lunch", Tag: "家庭", Quantity: ptr("1.50")},
		},
	}
}

func TestLoadReadsEverySectionAndKey(t *testing.T) {
	loaded := loadText(t, fullMapping)
	if !loaded.Exists || loaded.Guessed != 0 {
		t.Errorf("Exists = %v, Guessed = %d, want true and 0", loaded.Exists, loaded.Guessed)
	}
	sameMapping(t, loaded.Mapping, fullMappingWant())
}

func TestLoadKeepsNamesAsTheTextTheyAreWrittenWith(t *testing.T) {
	loaded := loadText(t, `accounts:
  2025: {name: 2025, kind: bank}
  true: {name: yes, kind: cash}
  1.50: {name: 1.50}
  0x10: {name: "null"}
  null: {name: x}
parties:
  2025: 2026
  off: on
`)
	want := model.Mapping{
		Accounts: map[string]model.AccountMap{
			"2025": {Name: "2025", Kind: "bank"},
			"true": {Name: "yes", Kind: "cash"},
			"1.50": {Name: "1.50"},
			"0x10": {Name: "null"},
			"null": {Name: "x"},
		},
		Parties: map[string]string{"2025": "2026", "off": "on"},
	}
	sameMapping(t, loaded.Mapping, want)
}

func TestLoadWithNothingInIt(t *testing.T) {
	tests := map[string]string{
		"an empty file":            "",
		"blank lines":              "\n\n   \n",
		"comments":                 "# nothing here yet\n# nothing at all\n",
		"a document start":         "---\n",
		"sections with no entries": "instruments:\naccounts: {}\nprojects: ~\ncategories:\n  expense:\n  income: {}\nparties: null\ntags:\ndefaults:\nrows: {}\n",
	}
	for name, text := range tests {
		t.Run(name, func(t *testing.T) {
			loaded := loadText(t, text)
			if !loaded.Exists || loaded.Guessed != 0 || !loaded.Mapping.Empty() {
				t.Errorf("Load = %v %d %v, want a mapping that exists and is empty", loaded.Exists, loaded.Guessed, canon(loaded.Mapping))
			}
		})
	}
}

func TestLoadOfAFileThatIsNotThere(t *testing.T) {
	loaded, err := file.NewMappingYAML().Load(context.Background(), filepath.Join(t.TempDir(), "none.yaml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Exists || loaded.Guessed != 0 || !loaded.Mapping.Empty() {
		t.Errorf("Load = %+v, want an empty mapping that does not exist", loaded)
	}
}

// Only the mark that ends an entry counts as one of the entries the import guessed and
// the user has not looked at yet: the comment after its value, or after its key when
// the entry goes on below. A comment of a section or of a line of its own does not
// count, nor does the text of a value, nor a comment that merely has the words. The
// comment is the mark when it begins with "# guessed" as a word: "# guessed" and
// "# guessed: not sure", not "# guessedly" or "#guessed".
func TestLoadCountsTheEntriesMarkedGuessed(t *testing.T) {
	tests := []struct {
		name string
		text string
		want int
	}{
		{"the way Append writes an entry", "accounts:\n  A: {name: A, kind: cash}  # guessed\n  B: {name: B, kind: cash}  # guessed\n", 2},
		{"a value that is a text", "parties:\n  A: Name  # guessed\n  \"B\": \"Name\"  # guessed\n", 2},
		{"the key of an entry that goes on below", "projects:\n  A:  # guessed\n    kind: project\n    name: A\n", 1},
		{"an entry with no value", "tags:\n  t:  # guessed\n  u:\n", 1},
		{"words after the mark", "accounts:\n  A: {name: A}  # guessed: not sure\n  B: {name: B}  # guessed, check it\n  C: {name: C}  # guessed\n", 3},
		{"an entry of several lines in braces", "accounts:\n  A: {name: A,\n    kind: cash}  # guessed\n", 1},
		{"an entry of a section in braces", "parties: {\n  Zed: Zed,  # guessed\n  Amy: Amy\n}\n", 1},
		{"an entry of every section", `instruments:
  USD: {name: x, kind: fiat, is_currency: true, decimals: 2}  # guessed
accounts:
  A: {name: A, kind: cash}  # guessed
projects:
  P: {kind: area, name: P}  # guessed
categories:
  expense:
    Food/Lunch: Food/Lunch  # guessed
  income:
    X: Y  # guessed
parties:
  Zed: Zed  # guessed
tags:
  t: T  # guessed
defaults:
  fee_category: Fee  # guessed
rows:
  r1: {treatment: skip}  # guessed
`, 9},

		{"a comment of a line of its own", "# guessed\naccounts:\n  # guessed\n  A: {name: A}\n  # guessed\nparties:\n  Zed: Zed\n# guessed\n", 0},
		{"the comment of a section", "accounts:  # guessed\n  A: {name: A}\nparties:  # guessed\ntags: {t: T}  # guessed\ncategories:  # guessed\n  expense:  # guessed\n    X: Y\n", 0},
		{"the start of the document", "--- # guessed\naccounts:\n  A: {name: A}\n", 0},
		{"text in a value", "parties:\n  A: \"contains # guessed\"\n  B: 'and # guessed'\n  C: |\n    text # guessed\n    # guessed\n  D: x\n", 0},
		{"a comment that has the words and is not the mark", "parties:\n  A: x  # guessedly\n  B: x  #guessed\n  C: x  # I guessed this\n  D: x  # not guessed\n  E: x  # Guessed\n  F: x  # guess\n  G: x  # guessed2\n", 0},
		{"a comment inside an entry, not at its end", "accounts:\n  A:\n    name: A  # guessed\n    kind: cash\n  B: {name: B, # guessed\n    kind: cash}\n", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := loadText(t, tt.text).Guessed; got != tt.want {
				t.Errorf("Guessed = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestLoadRefusesWhatIsWrong(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		wantCode model.FailureCode
		wantLine int
		wantArgs []string
	}{
		{"an unknown section", "accounts:\n  A: {name: A}\naccountz:\n  B: {name: B}\n", model.FailureKeyUnknown, 3, unknownKey("accountz", "", sectionKeys)},
		{"an unknown key of an account", "accounts:\n  A: {name: A, kindd: card}\n", model.FailureKeyUnknown, 2, unknownKey("kindd", "accounts.A", accountKeys)},
		{"an unknown key of an instrument", "instruments:\n  USD: {name: x, rate: 31}\n", model.FailureKeyUnknown, 2, unknownKey("rate", "instruments.USD", instrumentKeys)},
		{"an unknown key of a project", "projects:\n  P: {kind: area, colour: red}\n", model.FailureKeyUnknown, 2, unknownKey("colour", "projects.P", projectKeys)},
		{"an unknown key of a category", "categories:\n  expense:\n    X: {category: Y, colour: red}\n", model.FailureKeyUnknown, 3, unknownKey("colour", "categories.expense.X", categoryKeys)},
		{"an unknown section of categories", "categories:\n  savings: {}\n", model.FailureKeyUnknown, 2, unknownKey("savings", "categories", []string{"expense", "income"})},
		{"an unknown default", "defaults:\n  tax_category: Tax\n", model.FailureKeyUnknown, 2, unknownKey("tax_category", "defaults", defaultsKeys)},
		{"an unknown key of a row", "rows:\n  a: {note: x}\n", model.FailureKeyUnknown, 2, unknownKey("note", "rows.a", rowKeys)},
		{"decimals that is not a number", "instruments:\n  USD: {name: x, kind: fiat, decimals: abc}\n", model.FailureNotWholeNumber, 2, []string{"decimals", "abc"}},
		{"a day of the month that is not a number", "accounts:\n  A: {name: A, closing_day: five}\n", model.FailureNotWholeNumber, 2, []string{"closing_day", "five"}},
		{"an opening date that is not a day", "accounts:\n  A: {name: A, opening_date: yesterday}\n", model.FailureNotDay, 2, []string{"opening_date", "yesterday", "2026-04-15"}},
		{"an opening date that is not a date of the calendar", "accounts:\n  A: {name: A, opening_date: 2025-13-01}\n", model.FailureNotDay, 2, []string{"opening_date", "2025-13-01", "2026-04-15"}},
		{"an opening date with a time of day", "accounts:\n  A: {name: A, opening_date: 2025-01-07T10:00:00Z}\n", model.FailureNotDay, 2, []string{"opening_date", "2025-01-07T10:00:00Z", "2026-04-15"}},
		{"an opening date that is a list", "accounts:\n  A: {name: A, opening_date: [2025, 1, 7]}\n", model.FailureValueNotSingle, 2, []string{"opening_date"}},
		{"is_currency that is not true or false", "instruments:\n  USD: {name: x, kind: fiat, is_currency: maybe}\n", model.FailureNotBoolean, 2, []string{"is_currency", "maybe"}},
		{"a quantity that is not a number", "rows:\n  a: {quantity: lots}\n", model.FailureNotNumber, 2, []string{"quantity", "lots"}},
		{"a key twice", "accounts:\n  A: {name: A}\n  B: {name: B}\n  A: {name: C}\n", model.FailureKeyTwice, 4, []string{"accounts", "A", "2"}},
		{"a section twice", "accounts: {}\nparties: {}\naccounts: {}\n", model.FailureKeyTwice, 3, []string{"", "accounts", "1"}},
		{"a key of an entry twice", "accounts:\n  A: {name: A, name: B}\n", model.FailureKeyTwice, 2, []string{"accounts.A", "name", "2"}},
		{"an expense section twice", "categories:\n  expense: {}\n  expense: {}\n", model.FailureKeyTwice, 3, []string{"categories", "expense", "2"}},
		{"a section that is a list", "accounts: [a, b]\n", model.FailureMustBeMapping, 1, []string{"accounts"}},
		{"categories that is a text", "categories: x\n", model.FailureMustBeMapping, 1, []string{"categories"}},
		{"an expense section that is a list", "categories:\n  expense: [x]\n", model.FailureMustBeMapping, 2, []string{"categories.expense"}},
		{"defaults that is a text", "defaults: x\n", model.FailureMustBeMapping, 1, []string{"defaults"}},
		{"an entry that is a text", "accounts:\n  A: card\n", model.FailureMustBeMapping, 2, []string{"accounts.A"}},
		{"a name that is a mapping", "parties:\n  Zed: {a: b}\n", model.FailureValueNotSingle, 2, []string{"Zed"}},
		{"a name that is not one value", "accounts:\n  ? [a, b]\n  : {name: x}\n", model.FailureKeyNotSingle, 2, []string{"accounts"}},
		{"a file that is a list", "- a\n- b\n", model.FailureNotSections, 1, nil},
		{"a file that is a text", "hello\n", model.FailureNotSections, 1, nil},
		{"a syntax error", "accounts: [a\nparties: {}\n", model.FailureYAMLSyntax, 1, nil},
		{"a syntax error that the library gives no line for", "parties:\n  A: \x01\n", model.FailureYAMLSyntax, 0, nil},
		{"two documents", "accounts: {}\n---\nparties: {}\n", model.FailureSecondDocument, 3, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantFailure(t, loadFails(t, tt.text), tt.wantCode, tt.wantLine, tt.wantArgs...)
		})
	}
}

func TestLoadFailureNamesTheFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "mapping.yaml", "accounts:\n  A: {kindd: x}\n")
	_, err := file.NewMappingYAML().Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load read a file with a key that is not known")
	}
	f := failureOf(t, err)
	wantFailure(t, f, model.FailureKeyUnknown, 2, unknownKey("kindd", "accounts.A", accountKeys)...)
	wantFile(t, f, path)
}

func TestLoadOfAFileThatCannotBeRead(t *testing.T) {
	dir := t.TempDir() // a folder is there, and it is not a mapping
	_, err := file.NewMappingYAML().Load(context.Background(), dir)
	if err == nil {
		t.Fatal("Load of a folder gave no error")
	}
	f := failureOf(t, err)
	wantFailure(t, f, model.FailureFileUnreadable, 0)
	wantFile(t, f, dir)
	if f.Err == nil {
		t.Error("the failure has no Err: the user is told the reason of the system as it is")
	}
}

// warnActivePeriod is the warning for the active_period of the project of the key, on
// the line.
func warnActivePeriod(project string, line int) model.Warning {
	return model.Warning{Code: model.WarningActivePeriodIgnored, Subject: "projects." + project, Args: []string{strconv.Itoa(line)}}
}

// A mapping that an earlier version of the import wrote has an active period on some
// of its projects. The import no longer sets one, since the ledger puts every
// transaction that has no project of its own into the project that is active on its
// day, but the key is still read and ignored: the project loads without a period, and
// a warning says which project and what line, once for each, in the order of the file.
func TestLoadIgnoresAnActivePeriod(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		projects int
		warnings []model.Warning
		guessed  int
	}{
		{
			"in flow style, as the first version wrote it, with the mark of a guess",
			"projects:\n  Trip: {kind: project, name: Trip, default_area: Daily, active_period: {from: 2026-03-08, to: 2026-03-17}}  # guessed\n",
			1, []model.Warning{warnActivePeriod("Trip", 2)}, 1,
		},
		{
			"in block style",
			"projects:\n  Trip:\n    kind: project\n    name: Trip\n    active_period:\n      from: 2026-03-08\n      to: 2026-03-17\n",
			1, []model.Warning{warnActivePeriod("Trip", 5)}, 0,
		},
		{
			"whatever it says",
			"projects:\n  A: {kind: project, name: A, active_period: later}\n  B: {kind: project, name: B, active_period: [2026-03-08, 2026-03-17]}\n" +
				"  C: {kind: project, name: C, active_period: {from: nonsense}}\n  D: {kind: project, name: D, active_period: ~}\n" +
				"  E:\n    kind: project\n    active_period:\n",
			5, []model.Warning{
				warnActivePeriod("A", 2), warnActivePeriod("B", 3), warnActivePeriod("C", 4), warnActivePeriod("D", 5), warnActivePeriod("E", 8),
			}, 0,
		},
		{
			"several, with a mark on two of them, and the lines are the lines of the keys",
			"projects:\n  A: {kind: area, name: A}\n" +
				"  B: {kind: project, name: B, active_period: {from: 2026-03-08, to: 2026-03-17}}  # guessed\n" +
				"  # a comment\n  C:\n    kind: project\n    active_period:\n      from: 2026-04-01\n      to: 2026-04-02\n" +
				"  \"2025 Trip\": {kind: project, name: T, active_period: {from: 2026-05-01, to: 2026-05-02}}  # guessed\n",
			4, []model.Warning{warnActivePeriod("B", 3), warnActivePeriod("C", 7), warnActivePeriod("2025 Trip", 10)}, 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeFile(t, dir, "mapping.yaml", tt.text)
			old := setOld(t, path)
			loaded := load(t, path)
			if len(loaded.Mapping.Projects) != tt.projects {
				t.Errorf("%d projects, want %d", len(loaded.Mapping.Projects), tt.projects)
			}
			if !slices.EqualFunc(loaded.Warnings, tt.warnings, func(a, b model.Warning) bool {
				return a.Code == b.Code && a.Subject == b.Subject && slices.Equal(a.Args, b.Args)
			}) {
				t.Errorf("Warnings = %+v, want %+v", loaded.Warnings, tt.warnings)
			}
			if loaded.Guessed != tt.guessed {
				t.Errorf("Guessed = %d, want %d", loaded.Guessed, tt.guessed)
			}
			wantUntouched(t, path, tt.text, old) // Load writes nothing
		})
	}
}

// The period is only ignored where a version of the import wrote it: on a project. On
// anything else it is a key that is not known, like any other.
func TestLoadStillRefusesAnActivePeriodElsewhere(t *testing.T) {
	f := loadFails(t, "accounts:\n  A: {name: A, active_period: {from: 2026-03-08, to: 2026-03-17}}\n")
	wantFailure(t, f, model.FailureKeyUnknown, 2, unknownKey("active_period", "accounts.A", accountKeys)...)
}

// What a mapping may be is for the mapping to say (model.Mapping.Validate), not for
// the file that has it.
func TestLoadLeavesValidationToTheMapping(t *testing.T) {
	loaded := loadText(t, `instruments:
  USD: {name: x, kind: spaceship, decimals: 99}
accounts:
  A: {name: A, kind: boat, closing_day: 40}
`)
	if loaded.Mapping.Instruments["USD"].Kind != "spaceship" || loaded.Mapping.Accounts["A"].ClosingDay != 40 {
		t.Errorf("Load = %v, want what the file says, whatever it is", canon(loaded.Mapping))
	}
	if problems := loaded.Mapping.Validate(); len(problems) < 3 {
		t.Errorf("Validate() = %v, want it to find what is wrong with them", problems)
	}
}

func TestLoadWithAContextThatIsDone(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := file.NewMappingYAML().Load(done, filepath.Join(t.TempDir(), "mapping.yaml"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}
