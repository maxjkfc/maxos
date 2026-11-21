package file_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/mapping"
)

// dayText is a day for canon: none for the zero time.
func dayText(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return t.Format(time.RFC3339)
}

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
		p := m.Projects[k]
		add("projects[%q] kind=%q name=%q area=%q party=%q from=%s to=%s",
			k, p.Kind, p.Name, p.DefaultArea, p.Party, dayText(p.ActiveFrom), dayText(p.ActiveTo))
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

// loadFails loads a mapping file that must be refused, and returns what it says.
func loadFails(t *testing.T, text string) string {
	t.Helper()
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	loaded, err := file.NewMappingYAML().Load(context.Background(), path)
	if err == nil {
		t.Fatalf("Load read %v from text that is wrong:\n%s", canon(loaded.Mapping), text)
	}
	return err.Error()
}

// fullMapping has every section and every key.
const fullMapping = `# A reviewed mapping, with every section and every key (the names are invented).
instruments:
  USD: {name: 美元, kind: fiat, is_currency: true, decimals: 2}
  USDT: {name: Tether USDT, kind: crypto, is_currency: true, decimals: 2, rate_as: USD}
accounts:
  "Fake Card A": {name: Fake Card A, kind: card, institution: FakeBank, closing_day: 5, payment_due_day: 20}
  "Fake Cash": {name: Fake Cash, kind: cash}
  2025: {name: Year Account, kind: bank}
  true: {name: Truth Account, kind: wallet}
projects:
  "Daily": {kind: area, name: Daily}
  "2025-04 Trip": {kind: project, name: Trip, default_area: Daily, active_period: {from: 2026-04-15, to: 2026-04-22}}
  "Crowdfund": {kind: tag, name: Crowdfund}
  "Corp paid": {kind: dealings, party: Corp}
  "Nothing": {kind: none}
  "Block style":
    kind: project
    name: Block
    active_period:
      from: "2026-05-01"
      to: 2026-05-03
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
			"Fake Card A": {Name: "Fake Card A", Kind: "card", Institution: "FakeBank", ClosingDay: 5, PaymentDueDay: 20},
			"Fake Cash":   {Name: "Fake Cash", Kind: "cash"},
			"2025":        {Name: "Year Account", Kind: "bank"},
			"true":        {Name: "Truth Account", Kind: "wallet"},
		},
		Projects: map[string]model.ProjectMap{
			"Daily":        {Kind: "area", Name: "Daily"},
			"2025-04 Trip": {Kind: "project", Name: "Trip", DefaultArea: "Daily", ActiveFrom: day("2026-04-15"), ActiveTo: day("2026-04-22")},
			"Crowdfund":    {Kind: "tag", Name: "Crowdfund"},
			"Corp paid":    {Kind: "dealings", Party: "Corp"},
			"Nothing":      {Kind: "none"},
			"Block style":  {Kind: "project", Name: "Block", ActiveFrom: day("2026-05-01"), ActiveTo: day("2026-05-03")},
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

func TestLoadCountsTheLinesMarkedGuessed(t *testing.T) {
	loaded := loadText(t, `# guessed entries are marked, like this: the line of this comment says so too
accounts:
  A: {name: A, kind: cash}  # guessed
  B: {name: B, kind: cash}  # guessed: not sure about it
  C: {name: C, kind: cash}  # reviewed
  D: {name: D, kind: cash}  #guessed
parties:
  Zed: Zed  # guessed
`)
	// The comment at the top, A, B and Zed; not C, which is reviewed, and not D, whose
	// mark is not the one the import writes.
	if loaded.Guessed != 4 {
		t.Errorf("Guessed = %d, want 4", loaded.Guessed)
	}
	if len(loaded.Mapping.Accounts) != 4 {
		t.Errorf("Accounts = %d, want the marks to be comments and nothing else", len(loaded.Mapping.Accounts))
	}
}

func TestLoadRefusesWhatIsWrong(t *testing.T) {
	tests := []struct {
		name    string
		text    string
		wantErr []string
	}{
		{"an unknown section", "accounts:\n  A: {name: A}\naccountz:\n  B: {name: B}\n", []string{"line 3", `"accountz"`}},
		{"an unknown key of an account", "accounts:\n  A: {name: A, kindd: card}\n", []string{"line 2", `"kindd"`, "an account"}},
		{"an unknown key of a period", "projects:\n  P:\n    kind: project\n    active_period: {from: 2026-01-01, till: 2026-01-02}\n", []string{"line 4", `"till"`}},
		{"an unknown key of a category", "categories:\n  expense:\n    X: {category: Y, colour: red}\n", []string{"line 3", `"colour"`}},
		{"an unknown section of categories", "categories:\n  savings: {}\n", []string{"line 2", `"savings"`}},
		{"an unknown default", "defaults:\n  tax_category: Tax\n", []string{"line 2", `"tax_category"`}},
		{"an unknown key of a row", "rows:\n  a: {note: x}\n", []string{"line 2", `"note"`}},
		{"decimals that is not a number", "instruments:\n  USD: {name: x, kind: fiat, decimals: abc}\n", []string{"line 2", "decimals", `"abc"`}},
		{"a day of the month that is not a number", "accounts:\n  A: {name: A, closing_day: five}\n", []string{"line 2", "closing_day", `"five"`}},
		{"is_currency that is not true or false", "instruments:\n  USD: {name: x, kind: fiat, is_currency: maybe}\n", []string{"line 2", "is_currency", `"maybe"`}},
		{"half a period", "projects:\n  P: {kind: project, name: P, active_period: {from: 2026-01-01}}\n", []string{"line 2", "active_period", "both"}},
		{"a period with an end and no start", "projects:\n  P: {kind: project, name: P, active_period: {to: 2026-01-01}}\n", []string{"line 2", "active_period", "both"}},
		{"a day that is not a day", "projects:\n  P: {kind: project, name: P, active_period: {from: 2026-13-01, to: 2026-01-02}}\n", []string{"line 2", "from", `"2026-13-01"`}},
		{"a quantity that is not a number", "rows:\n  a: {quantity: lots}\n", []string{"line 2", "quantity", `"lots"`}},
		{"a key twice", "accounts:\n  A: {name: A}\n  B: {name: B}\n  A: {name: C}\n", []string{"line 4", "twice", "line 2"}},
		{"a section twice", "accounts: {}\nparties: {}\naccounts: {}\n", []string{"line 3", "twice", "line 1"}},
		{"a key of an entry twice", "accounts:\n  A: {name: A, name: B}\n", []string{"line 2", "twice"}},
		{"a section that is a list", "accounts: [a, b]\n", []string{"line 1", "accounts", "mapping"}},
		{"an entry that is a text", "accounts:\n  A: card\n", []string{"line 2", "an account", "mapping"}},
		{"a name that is a mapping", "parties:\n  Zed: {a: b}\n", []string{"line 2", "Zed"}},
		{"a file that is a list", "- a\n- b\n", []string{"line 1", "mapping"}},
		{"a file that is a text", "hello\n", []string{"line 1", "mapping"}},
		{"a syntax error", "accounts: [a\nparties: {}\n", []string{"line"}},
		{"two documents", "accounts: {}\n---\nparties: {}\n", []string{"line 3", "second"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustSay(t, loadFails(t, tt.text), tt.wantErr...)
		})
	}
}

func TestLoadErrorNamesTheFile(t *testing.T) {
	path := writeFile(t, t.TempDir(), "mapping.yaml", "accounts:\n  A: {kindd: x}\n")
	_, err := file.NewMappingYAML().Load(context.Background(), path)
	if err == nil {
		t.Fatal("Load read a file with a key that is not known")
	}
	mustSay(t, err.Error(), path, "line 2")
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
