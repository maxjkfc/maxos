package file_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/importer/adapter/out/file"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
)

// guessedAll is a mapping with entries in every section, to be added.
func guessedAll() model.Mapping {
	return model.Mapping{
		Instruments: map[string]model.InstrumentMap{
			"USD":  {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
			"USDT": {Name: "Tether USDT", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
		},
		Accounts: map[string]model.AccountMap{
			"Fake Card A": {Name: "Fake Card A", Kind: "card", Institution: "FakeBank", ClosingDay: 5, PaymentDueDay: 20, OpeningDate: day("2025-01-07")},
			"Fake Cash":   {Name: "Fake Cash", Kind: "cash"},
		},
		Projects: map[string]model.ProjectMap{
			"2025-04 Trip": {Kind: "project", Name: "Trip", DefaultArea: "Daily"},
			"Corp paid":    {Kind: "dealings", Party: "Corp"},
			"Daily":        {Kind: "area", Name: "Daily"},
		},
		Expense: map[string]model.CategoryMap{
			"Food/Lunch": {Category: "Food/Lunch"},
		},
		Income: map[string]model.CategoryMap{
			"收入/收款":    {Category: "收款（待確認）", Tag: "待確認"},
			"收入/信用卡抵扣": {Treatment: "reward"},
		},
		Parties:  map[string]string{"Zed": "Zed"},
		Tags:     map[string]string{"家庭": "家庭"},
		Defaults: model.Defaults{FeeCategory: "手續費", InterestCategory: "利息", RefundCategory: "退款"},
		Rows: map[string]model.RowOverride{
			"abc123-1": {Treatment: "dealing", Party: "Zed"},
			"abc123-2": {Quantity: ptr("0.0123")},
		},
	}
}

// guessedAllCount is how many entries guessedAll has: 2 instruments, 2 accounts,
// 3 projects, 1 expense and 2 income categories, a party, a tag, 3 defaults, 2 rows.
const guessedAllCount = 17

// newFileBody is what the file of guessedAll has after its comment at the top.
const newFileBody = `instruments:
  USD: {name: 美元, kind: fiat, is_currency: true, decimals: 2}  # guessed
  USDT: {name: Tether USDT, kind: crypto, is_currency: true, decimals: 2, rate_as: USD}  # guessed

accounts:
  Fake Card A: {name: Fake Card A, kind: card, institution: FakeBank, closing_day: 5, payment_due_day: 20, opening_date: 2025-01-07}  # guessed
  Fake Cash: {name: Fake Cash, kind: cash}  # guessed

projects:
  2025-04 Trip: {kind: project, name: Trip, default_area: Daily}  # guessed
  Corp paid: {kind: dealings, party: Corp}  # guessed
  Daily: {kind: area, name: Daily}  # guessed

categories:
  expense:
    Food/Lunch: Food/Lunch  # guessed
  income:
    收入/信用卡抵扣: {treatment: reward}  # guessed
    收入/收款: {category: 收款（待確認）, tag: 待確認}  # guessed

parties:
  Zed: Zed  # guessed

tags:
  家庭: 家庭  # guessed

defaults:
  fee_category: 手續費  # guessed
  interest_category: 利息  # guessed
  refund_category: 退款  # guessed

rows:
  abc123-1: {treatment: dealing, party: Zed}  # guessed
  abc123-2: {quantity: "0.0123"}  # guessed
`

func appendTo(t *testing.T, path string, guessed model.Mapping) {
	t.Helper()
	if err := file.NewMappingYAML().Append(context.Background(), path, guessed); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

// appendFails appends what must be refused, and returns the failure it is, which is
// about the file.
func appendFails(t *testing.T, path string, guessed model.Mapping) *filefailure.Failure {
	t.Helper()
	err := file.NewMappingYAML().Append(context.Background(), path, guessed)
	if err == nil {
		t.Fatal("Append added to a file it cannot add to")
	}
	f := failureOf(t, err)
	wantFile(t, f, path)
	return f
}

// setOld gives the file a time of modification long ago, to tell whether it is
// written again.
func setOld(t *testing.T, path string) time.Time {
	t.Helper()
	old := time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
	return old
}

// wantUntouched checks that the file has the text and the time it had.
func wantUntouched(t *testing.T, path, text string, old time.Time) {
	t.Helper()
	if got := readFile(t, path); got != text {
		t.Errorf("the file was changed:\n%s\nwas:\n%s", got, text)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Errorf("the file was written: its time is %v, was %v", info.ModTime(), old)
	}
}

// insertedLines tells which lines of after are not lines of before, when after is
// before with lines put in: all the lines of before are in it, in their order.
func insertedLines(before, after string) (inserted []string, ok bool) {
	old := strings.SplitAfter(before, "\n")
	i := 0
	for _, line := range strings.SplitAfter(after, "\n") {
		if i < len(old) && line == old[i] {
			i++
			continue
		}
		inserted = append(inserted, line)
	}
	return inserted, i == len(old)
}

func TestAppendCreatesTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", "maxos", "moze-mapping.yaml")
	guessed := guessedAll()
	appendTo(t, path, guessed)

	for _, p := range []struct {
		path string
		want os.FileMode
	}{{path, 0o600}, {filepath.Dir(path), 0o700}} {
		info, err := os.Stat(p.path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != p.want {
			t.Errorf("%s has the permissions %v, want %v", p.path, info.Mode().Perm(), p.want)
		}
	}
	if entries, _ := os.ReadDir(filepath.Dir(path)); len(entries) != 1 {
		t.Errorf("the folder has %d files, want the file and nothing left over from writing it", len(entries))
	}

	text := readFile(t, path)
	if !strings.HasPrefix(text, "# maxos import moze") {
		t.Errorf("the file begins with %q, want a comment that says what it is", text[:min(len(text), 40)])
	}
	i := strings.Index(text, "\ninstruments:")
	if i < 0 {
		t.Fatalf("the file has no instruments:\n%s", text)
	}
	if text[i+1:] != newFileBody {
		t.Errorf("the file after its comment is\n%s\nwant\n%s", text[i+1:], newFileBody)
	}
	// The way to override a row is written down, as a comment.
	if !strings.Contains(text[:i], "rows:") {
		t.Error("the comment at the top does not say how a row is overridden")
	}

	loaded := load(t, path)
	if !loaded.Exists || loaded.Guessed != guessedAllCount {
		t.Errorf("Exists = %v, Guessed = %d, want true and %d: one line of each entry, none for the comment", loaded.Exists, loaded.Guessed, guessedAllCount)
	}
	sameMapping(t, loaded.Mapping, guessed)
}

func TestAppendNamesThatNeedQuotesStayWhatTheyAre(t *testing.T) {
	names := []string{
		"2025-04 Trip", "a: b", "#x", "true", "123", "null", "~", "yes", "1:30", "a, b", " lead", "trail ",
		`x'y`, `"q"`, "[x]", "{x}", "- x", "? x", "!x", "&x", "*x", "|x", "%x", "@x", "a #b", "a\tb",
		"0x10", "1e3", "NaN", ".inf", "2026-04-15", "收入/收款", "",
	}
	guessed := model.Mapping{
		Instruments: map[string]model.InstrumentMap{},
		Accounts:    map[string]model.AccountMap{},
		Projects:    map[string]model.ProjectMap{},
		Expense:     map[string]model.CategoryMap{},
		Income:      map[string]model.CategoryMap{},
		Parties:     map[string]string{},
		Tags:        map[string]string{},
		Rows:        map[string]model.RowOverride{},
		Defaults:    model.Defaults{FeeCategory: "a: b", InterestCategory: "true", RefundCategory: "123"},
	}
	for _, n := range names {
		guessed.Instruments[n] = model.InstrumentMap{Name: n, Kind: n, IsCurrency: true, Decimals: 2, RateAs: n}
		guessed.Accounts[n] = model.AccountMap{Name: n, Kind: n, Institution: n}
		guessed.Projects[n] = model.ProjectMap{Kind: n, Name: n, DefaultArea: n, Party: n}
		guessed.Expense[n] = model.CategoryMap{Category: n}
		guessed.Income[n] = model.CategoryMap{Category: n, Treatment: n, Party: n, Tag: n}
		guessed.Parties[n] = n
		guessed.Tags[n] = n
		guessed.Rows[n] = model.RowOverride{Treatment: n, Category: n, Party: n, Tag: n}
	}

	t.Run("in a new file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mapping.yaml")
		appendTo(t, path, guessed)
		sameMapping(t, load(t, path).Mapping, guessed)
	})
	t.Run("in a file that has sections", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "mapping.yaml", "accounts:\n  Other: {name: Other}\nparties: {}\nrows:\n  r: {tag: x}\n")
		appendTo(t, path, guessed)
		want := load(t, writeFile(t, t.TempDir(), "before.yaml", "accounts:\n  Other: {name: Other}\nrows:\n  r: {tag: x}\n")).Mapping.Merge(guessed)
		sameMapping(t, load(t, path).Mapping, want)
	})
}

// existingBefore is a mapping file that somebody has written by hand: comments, blank
// lines, four spaces in one section, a section that is {}, one that is missing.
const existingBefore = `# Hand-written mapping (invented names).
instruments:
  USD: {name: US Dollar, kind: fiat, is_currency: true, decimals: 2}  # reviewed

# --- accounts ---
accounts:
    Fake Card A: {name: Fake Card A, kind: card, institution: FakeBank, closing_day: 5, payment_due_day: 20}
    Fake Cash: {name: Fake Cash, kind: cash}   # my wallet
    # (more accounts later)

projects: {}  # none yet

categories:
  expense:
    # lunches
    Food/Lunch: Food/Lunch

parties:
  Zed: Zed
rows:
  abc123-1: {treatment: dealing, party: Zed}
# end of the file
`

// existingAfter is existingBefore with existingGuessed added to it.
const existingAfter = `# Hand-written mapping (invented names).
instruments:
  USD: {name: US Dollar, kind: fiat, is_currency: true, decimals: 2}  # reviewed
  EUR: {name: Euro, kind: fiat, is_currency: true, decimals: 2}  # guessed

# --- accounts ---
accounts:
    Fake Card A: {name: Fake Card A, kind: card, institution: FakeBank, closing_day: 5, payment_due_day: 20}
    Fake Cash: {name: Fake Cash, kind: cash}   # my wallet
    Fake Card B: {name: Fake Card B, kind: bank, institution: FakeBank, opening_date: 2025-03-01}  # guessed
    # (more accounts later)

projects:  # none yet
  2025-04 Trip: {kind: project, name: Trip, default_area: Daily}  # guessed
  Daily: {kind: area, name: Daily}  # guessed

categories:
  expense:
    # lunches
    Food/Lunch: Food/Lunch
    Food/Dinner: Food/Dinner  # guessed
  income:
    收入/信用卡抵扣: {treatment: reward}  # guessed
    收入/收款: {category: 收款（待確認）, tag: 待確認}  # guessed

parties:
  Zed: Zed
  Amy: Amy  # guessed
rows:
  abc123-1: {treatment: dealing, party: Zed}
  abc123-2: {quantity: "0.0123"}  # guessed
# end of the file

tags:
  家庭: 家庭  # guessed

defaults:
  fee_category: 手續費  # guessed
  interest_category: 利息  # guessed
`

// existingGuessed has some entries that existingBefore has, with other values, and
// some that it does not.
func existingGuessed() model.Mapping {
	return model.Mapping{
		Instruments: map[string]model.InstrumentMap{
			"USD": {Name: "Dollar", Kind: "fiat", IsCurrency: true, Decimals: 2},
			"EUR": {Name: "Euro", Kind: "fiat", IsCurrency: true, Decimals: 2},
		},
		Accounts: map[string]model.AccountMap{
			"Fake Card A": {Name: "Another", Kind: "bank"},
			"Fake Card B": {Name: "Fake Card B", Kind: "bank", Institution: "FakeBank", OpeningDate: day("2025-03-01")},
		},
		Projects: map[string]model.ProjectMap{
			"Daily":        {Kind: "area", Name: "Daily"},
			"2025-04 Trip": {Kind: "project", Name: "Trip", DefaultArea: "Daily"},
		},
		Expense: map[string]model.CategoryMap{
			"Food/Lunch":  {Category: "Elsewhere"},
			"Food/Dinner": {Category: "Food/Dinner"},
		},
		Income: map[string]model.CategoryMap{
			"收入/收款":    {Category: "收款（待確認）", Tag: "待確認"},
			"收入/信用卡抵扣": {Treatment: "reward"},
		},
		Parties:  map[string]string{"Zed": "Someone", "Amy": "Amy"},
		Tags:     map[string]string{"家庭": "家庭"},
		Defaults: model.Defaults{FeeCategory: "手續費", InterestCategory: "利息"},
		Rows: map[string]model.RowOverride{
			"abc123-1": {Treatment: "skip"},
			"abc123-2": {Quantity: ptr("0.0123")},
		},
	}
}

func TestAppendAddsToAFileSomebodyWrote(t *testing.T) {
	crlf := func(s string) string { return strings.ReplaceAll(s, "\n", "\r\n") }
	for name, tt := range map[string]struct{ before, after, eol string }{
		"LF":   {existingBefore, existingAfter, "\n"},
		"CRLF": {crlf(existingBefore), crlf(existingAfter), "\r\n"},
	} {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "mapping.yaml", tt.before)
			before := load(t, path)
			appendTo(t, path, existingGuessed())

			got := readFile(t, path)
			if got != tt.after {
				t.Errorf("the file is\n%s\nwant\n%s", got, tt.after)
			}

			// Nothing of the file is lost but the {} of the empty section: every other
			// line is there as it was, in its place, and the rest are lines put in, that
			// end the way the lines of the file do.
			rewritten := strings.Replace(tt.before, "projects: {}  # none yet", "projects:  # none yet", 1)
			inserted, ok := insertedLines(rewritten, got)
			if !ok {
				t.Error("a line of the file was changed or is gone")
			}
			for _, line := range inserted {
				text := strings.TrimSuffix(line, tt.eol)
				switch {
				case text == line:
					t.Errorf("a line put in does not end the way the others do: %q", line)
				case text == "", strings.HasSuffix(text, "# guessed"), slices.Contains([]string{"  income:", "tags:", "defaults:"}, text):
				default:
					t.Errorf("a line that is not an entry was put in: %q", line)
				}
			}

			// The entries of the file win, and the new ones are there.
			loaded := load(t, path)
			sameMapping(t, loaded.Mapping, before.Mapping.Merge(existingGuessed()))
			if loaded.Guessed != 12 {
				t.Errorf("Guessed = %d, want 12", loaded.Guessed)
			}
		})
	}
}

func TestAppendTwiceChangesNothingTheSecondTime(t *testing.T) {
	t.Run("a new file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "mapping.yaml")
		appendTo(t, path, guessedAll())
		first := readFile(t, path)
		old := setOld(t, path)
		appendTo(t, path, guessedAll())
		wantUntouched(t, path, first, old)
	})
	t.Run("a file somebody wrote", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "mapping.yaml", existingBefore)
		appendTo(t, path, existingGuessed())
		first := readFile(t, path)
		old := setOld(t, path)
		appendTo(t, path, existingGuessed())
		wantUntouched(t, path, first, old)
	})
}

func TestAppendAddsWhatIsNewToAFileItWrote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	appendTo(t, path, model.Mapping{
		Instruments: map[string]model.InstrumentMap{"USD": {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2}},
	})
	before := readFile(t, path)

	appendTo(t, path, model.Mapping{
		Instruments: map[string]model.InstrumentMap{
			"USD": {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
			"EUR": {Name: "歐元", Kind: "fiat", IsCurrency: true, Decimals: 2},
		},
		Accounts: map[string]model.AccountMap{"Fake Cash": {Name: "Fake Cash", Kind: "cash"}},
	})
	after := readFile(t, path)

	inserted, ok := insertedLines(before, after)
	if !ok {
		t.Fatalf("a line of the file was changed:\n%s\nwas:\n%s", after, before)
	}
	want := []string{
		"  EUR: {name: 歐元, kind: fiat, is_currency: true, decimals: 2}  # guessed\n",
		"\n",
		"accounts:\n",
		"  Fake Cash: {name: Fake Cash, kind: cash}  # guessed\n",
	}
	if !slices.Equal(inserted, want) {
		t.Errorf("lines put in: %q, want %q", inserted, want)
	}
	if loaded := load(t, path); loaded.Guessed != 3 {
		t.Errorf("Guessed = %d, want 3", loaded.Guessed)
	}
}

func TestAppendKeepsWhatTheFileSaysOfAnEntry(t *testing.T) {
	text := "accounts:\n  A: {name: Mine, kind: bank}\n"
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	old := setOld(t, path)
	appendTo(t, path, model.Mapping{Accounts: map[string]model.AccountMap{"A": {Name: "Theirs", Kind: "cash"}}})
	wantUntouched(t, path, text, old)
	if got := load(t, path).Mapping.Accounts["A"].Name; got != "Mine" {
		t.Errorf("the account is called %q, want what the file has", got)
	}
}

func TestAppendWithNothingToAddLeavesTheFileAlone(t *testing.T) {
	text := "# mine\naccounts:\n  A: {name: A}   # reviewed\n"
	tests := map[string]model.Mapping{
		"no entries":              {},
		"sections with none":      {Accounts: map[string]model.AccountMap{}, Rows: map[string]model.RowOverride{}},
		"entries it has":          {Accounts: map[string]model.AccountMap{"A": {Name: "A"}}},
		"defaults that are empty": {Defaults: model.Defaults{}},
	}
	for name, guessed := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "mapping.yaml", text)
			old := setOld(t, path)
			appendTo(t, path, guessed)
			wantUntouched(t, path, text, old)
		})
	}
	t.Run("a file that is not there is not made", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "config", "mapping.yaml")
		appendTo(t, path, model.Mapping{})
		if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
			t.Errorf("Stat = %v, want nothing to be made when there is nothing to put in it", err)
		}
	})
}

func TestAppendToEmptySections(t *testing.T) {
	before := "# top\naccounts: {}  # none yet\nprojects:\ntags: ~\nparties: { }\nrows:   null   # nothing\n"
	after := `# top
accounts:  # none yet
  A: {name: A, kind: cash}  # guessed
projects:
  P: {kind: area, name: P}  # guessed
tags:
  Tag One: Tag One  # guessed
parties:
  Zed: Zed  # guessed
rows:   # nothing
  r1: {treatment: skip}  # guessed
`
	guessed := model.Mapping{
		Accounts: map[string]model.AccountMap{"A": {Name: "A", Kind: "cash"}},
		Projects: map[string]model.ProjectMap{"P": {Kind: "area", Name: "P"}},
		Tags:     map[string]string{"Tag One": "Tag One"},
		Parties:  map[string]string{"Zed": "Zed"},
		Rows:     map[string]model.RowOverride{"r1": {Treatment: "skip"}},
	}
	path := writeFile(t, t.TempDir(), "mapping.yaml", before)
	appendTo(t, path, guessed)
	if got := readFile(t, path); got != after {
		t.Errorf("the file is\n%s\nwant\n%s", got, after)
	}
	sameMapping(t, load(t, path).Mapping, guessed)
}

func TestAppendToCategories(t *testing.T) {
	expense := map[string]model.CategoryMap{"A/B": {Category: "A/B"}}
	income := map[string]model.CategoryMap{"X/Y": {Category: "X/Y"}}
	tests := []struct {
		name    string
		before  string
		guessed model.Mapping
		after   string
	}{
		{
			"income under the categories that have expense only",
			"categories:\n  expense:\n    A/B: A/B\n",
			model.Mapping{Income: income},
			"categories:\n  expense:\n    A/B: A/B\n  income:\n    X/Y: X/Y  # guessed\n",
		},
		{
			"expense after income, and an income of its own",
			"categories:\n  income:\n    X/Y: X/Y\n",
			model.Mapping{Expense: expense, Income: map[string]model.CategoryMap{"X/Z": {Category: "X/Z"}}},
			"categories:\n  income:\n    X/Y: X/Y\n    X/Z: X/Z  # guessed\n  expense:\n    A/B: A/B  # guessed\n",
		},
		{
			"categories that the file lacks",
			"parties:\n  Zed: Zed\n",
			model.Mapping{Expense: expense, Income: income},
			"parties:\n  Zed: Zed\n\ncategories:\n  expense:\n    A/B: A/B  # guessed\n  income:\n    X/Y: X/Y  # guessed\n",
		},
		{
			"categories that are {}",
			"categories: {}\nparties:\n  Zed: Zed\n",
			model.Mapping{Expense: expense},
			"categories:\n  expense:\n    A/B: A/B  # guessed\nparties:\n  Zed: Zed\n",
		},
		{
			"sections that are {} and nothing",
			"categories:\n  expense: {}\n  income:\n",
			model.Mapping{Expense: expense, Income: income},
			"categories:\n  expense:\n    A/B: A/B  # guessed\n  income:\n    X/Y: X/Y  # guessed\n",
		},
		{
			"four spaces",
			"categories:\n    expense:\n        C/D: C/D\n",
			model.Mapping{Expense: expense, Income: income},
			"categories:\n    expense:\n        C/D: C/D\n        A/B: A/B  # guessed\n    income:\n        X/Y: X/Y  # guessed\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "mapping.yaml", tt.before)
			appendTo(t, path, tt.guessed)
			if got := readFile(t, path); got != tt.after {
				t.Errorf("the file is\n%s\nwant\n%s", got, tt.after)
			}
			// And it reads as what it should: what it had and what was added.
			before := load(t, writeFile(t, t.TempDir(), "before.yaml", tt.before)).Mapping
			sameMapping(t, load(t, path).Mapping, before.Merge(tt.guessed))
		})
	}
}

func TestAppendAfterAnEntryOfSeveralLines(t *testing.T) {
	before := `projects:
  Trip:
    kind: project
    name: Trip
    default_area: Daily

parties:
  Zed: Zed
`
	after := `projects:
  Trip:
    kind: project
    name: Trip
    default_area: Daily
  Daily: {kind: area, name: Daily}  # guessed

parties:
  Zed: Zed
`
	path := writeFile(t, t.TempDir(), "mapping.yaml", before)
	appendTo(t, path, model.Mapping{Projects: map[string]model.ProjectMap{"Daily": {Kind: "area", Name: "Daily"}}})
	if got := readFile(t, path); got != after {
		t.Errorf("the file is\n%s\nwant\n%s", got, after)
	}
}

func TestAppendWithLinesThatEndNoLine(t *testing.T) {
	tests := []struct {
		name, before string
		guessed      model.Mapping
		after        string
	}{
		{
			"the last line is the end of a section",
			"accounts:\n  A: {name: A}",
			model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}},
			"accounts:\n  A: {name: A}\n  B: {name: B}  # guessed\n",
		},
		{
			"the last line is the end of the file",
			"accounts:\n  A: {name: A}",
			model.Mapping{Parties: map[string]string{"Zed": "Zed"}},
			"accounts:\n  A: {name: A}\n\nparties:\n  Zed: Zed  # guessed\n",
		},
		{
			"with CRLF",
			"accounts:\r\n  A: {name: A}",
			model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}, Parties: map[string]string{"Zed": "Zed"}},
			"accounts:\r\n  A: {name: A}\r\n  B: {name: B}  # guessed\r\n\r\nparties:\r\n  Zed: Zed  # guessed\r\n",
		},
		{
			"something follows it",
			"accounts:\n  A: {name: A}\nparties:\n  Zed: Zed",
			model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}},
			"accounts:\n  A: {name: A}\n  B: {name: B}  # guessed\nparties:\n  Zed: Zed",
		},
		{
			"a file with no section",
			"# only a comment",
			model.Mapping{Parties: map[string]string{"Zed": "Zed"}},
			"# only a comment\n\nparties:\n  Zed: Zed  # guessed\n",
		},
		{
			"a file with nothing in it",
			"",
			model.Mapping{Parties: map[string]string{"Zed": "Zed"}},
			"parties:\n  Zed: Zed  # guessed\n",
		},
		{
			"a file that ends with a blank line",
			"accounts:\n  A: {name: A}\n\n",
			model.Mapping{Parties: map[string]string{"Zed": "Zed"}},
			"accounts:\n  A: {name: A}\n\nparties:\n  Zed: Zed  # guessed\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "mapping.yaml", tt.before)
			appendTo(t, path, tt.guessed)
			if got := readFile(t, path); got != tt.after {
				t.Errorf("the file is %q, want %q", got, tt.after)
			}
		})
	}
}

func TestAppendKeepsThePermissionsOfTheFile(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "mapping.yaml", "accounts:\n  A: {name: A}\n")
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}})
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("the file has the permissions %v, want the 0640 it had", info.Mode().Perm())
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("the folder has %d files, want the file and nothing left over from writing it", len(entries))
	}
}

func TestAppendWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	target := writeFile(t, dir, "real.yaml", "accounts:\n  A: {name: A}\n")
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no links here: %v", err)
	}
	appendTo(t, link, model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}})
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the link was replaced by a file")
	}
	if got := readFile(t, target); !strings.Contains(got, "  B: {name: B}  # guessed\n") {
		t.Errorf("the file the link points to is\n%s\nwant B in it", got)
	}
}

func TestAppendRefusesAFileBetweenBraces(t *testing.T) {
	text := "parties: {Zed: Zed}  # mine\n"
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	old := setOld(t, path)

	t.Run("that has to be added to", func(t *testing.T) {
		f := appendFails(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed", "Amy": "Amy"}})
		wantFailure(t, f, filefailure.Braces, 1, "parties")
		wantUntouched(t, path, text, old)
	})
	t.Run("that has what is to be added already", func(t *testing.T) {
		appendTo(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}})
		wantUntouched(t, path, text, old)
	})
	t.Run("a section of the categories", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "categories.yaml", "categories:\n  expense: {A: B}\n")
		f := appendFails(t, path, model.Mapping{Expense: map[string]model.CategoryMap{"C": {Category: "C"}}})
		wantFailure(t, f, filefailure.Braces, 2, "categories.expense")
	})
	t.Run("a file that is one mapping between braces", func(t *testing.T) {
		whole := "{accounts: {A: {name: A}}}\n"
		path := writeFile(t, t.TempDir(), "whole.yaml", whole)
		old := setOld(t, path)
		wantFailure(t, appendFails(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}}), filefailure.Braces, 1, "")
		wantUntouched(t, path, whole, old)
		// With nothing to add it is no error.
		appendTo(t, path, model.Mapping{Accounts: map[string]model.AccountMap{"A": {Name: "A"}}})
		wantUntouched(t, path, whole, old)
	})
	t.Run("a file that is {}", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "empty.yaml", "{}\n")
		wantFailure(t, appendFails(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}}), filefailure.Braces, 1, "")
	})
}

// A section whose empty value is not written beside its key cannot be made the key of
// lines without moving things the file says: the user lays it out.
func TestAppendRefusesALayoutItCannotAddTo(t *testing.T) {
	t.Run("a section", func(t *testing.T) {
		text := "accounts:\n  {}\n"
		path := writeFile(t, t.TempDir(), "mapping.yaml", text)
		old := setOld(t, path)
		f := appendFails(t, path, model.Mapping{Accounts: map[string]model.AccountMap{"A": {Name: "A"}}})
		wantFailure(t, f, filefailure.Layout, 1, "accounts")
		wantUntouched(t, path, text, old)
	})
	t.Run("a section of the categories", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "mapping.yaml", "categories:\n  expense:\n    {}\n")
		f := appendFails(t, path, model.Mapping{Expense: map[string]model.CategoryMap{"C": {Category: "C"}}})
		wantFailure(t, f, filefailure.Layout, 2, "categories.expense")
	})
}

func TestAppendRefusesAFileItCannotRead(t *testing.T) {
	text := "accounts:\n  A: {nme: A}\n"
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	old := setOld(t, path)
	f := appendFails(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}})
	wantFailure(t, f, filefailure.KeyUnknown, 2, unknownKey("nme", "accounts.A", accountKeys)...)
	wantUntouched(t, path, text, old)
}

// The reason of the system is what the user is told, as it is, when a file cannot be
// read or written.
func TestAppendFailsWithTheReasonOfTheSystem(t *testing.T) {
	t.Run("a file that cannot be read", func(t *testing.T) {
		dir := t.TempDir() // a folder is there, and it is not a mapping
		f := appendFails(t, dir, model.Mapping{Parties: map[string]string{"Zed": "Zed"}})
		wantFailure(t, f, filefailure.Unreadable, 0)
		if f.Err == nil {
			t.Error("the failure has no Err: the user is told the reason of the system as it is")
		}
	})
	t.Run("a file that cannot be written", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes where it is told not to")
		}
		dir := t.TempDir()
		if err := os.Chmod(dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
		path := filepath.Join(dir, "mapping.yaml")
		f := appendFails(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}})
		wantFailure(t, f, filefailure.NotWritten, 0)
		if f.Err == nil {
			t.Error("the failure has no Err: the user is told the reason of the system as it is")
		}
	})
}

// The text with the entries put in is read back before anything is written. When it
// cannot even be read, because of the way the file ends, the failure is the same one
// as when it reads differently, with no entry to name.
func TestAppendRefusesAFileWhoseNewTextCannotBeRead(t *testing.T) {
	text := "accounts:\n  A: {name: A}\n...\n"
	path := writeFile(t, t.TempDir(), "mapping.yaml", text)
	old := setOld(t, path)
	f := appendFails(t, path, model.Mapping{Accounts: map[string]model.AccountMap{"B": {Name: "B"}}})
	wantFailure(t, f, filefailure.WouldReadDifferently, 0)
	wantUntouched(t, path, text, old)
}

// A file that has an active period on a project is added to like any other: what it
// has is left as it is, the period too, and only the new entries are put in.
func TestAppendLeavesAnActivePeriodAlone(t *testing.T) {
	text := `# my mapping
projects:
  Trip: {kind: project, name: Trip, default_area: Daily, active_period: {from: 2026-03-08, to: 2026-03-17}}  # guessed
  Hike:
    kind: project
    active_period:
      from: 2026-04-01
      to: 2026-04-02
parties:
  Zed: Zed
`
	t.Run("to add to", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "mapping.yaml", text)
		appendTo(t, path, model.Mapping{
			Projects: map[string]model.ProjectMap{"Camp": {Kind: "project", Name: "Camp"}},
			Parties:  map[string]string{"Amy": "Amy"},
		})
		inserted, ok := insertedLines(text, readFile(t, path))
		if !ok {
			t.Errorf("a line of the file was changed or is gone:\n%s", readFile(t, path))
		}
		want := []string{"  Camp: {kind: project, name: Camp}  # guessed\n", "  Amy: Amy  # guessed\n"}
		if !slices.Equal(inserted, want) {
			t.Errorf("lines put in: %q, want %q", inserted, want)
		}
		loaded := load(t, path)
		if loaded.Guessed != 3 || len(loaded.Warnings) != 2 {
			t.Errorf("Guessed = %d, Warnings = %d, want 3 and 2: the period is still there, and still ignored", loaded.Guessed, len(loaded.Warnings))
		}
	})
	t.Run("nothing to add", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "mapping.yaml", text)
		old := setOld(t, path)
		appendTo(t, path, model.Mapping{Parties: map[string]string{"Zed": "Zed"}})
		wantUntouched(t, path, text, old)
	})
}

// What is written has to read back as the file with the entries put in, value for
// value, and not only with the same keys. A line that looks like a comment inside a
// block scalar is a line of its value: an entry put in after the lines above it cuts
// the scalar short, and what the file says of that entry is changed without a word.
// Append refuses, names the entry that would change, and leaves the file as it is.
func TestAppendRefusesToChangeWhatAnEntrySays(t *testing.T) {
	tests := []struct {
		name, text string
		guessed    model.Mapping
		wantArgs   []string // the section and the key of the entry the failure names: the first one that would read differently
	}{
		{
			"a block scalar that ends with a line like a comment",
			"parties:\n  Zed: |\n    first line\n    # looks like a comment\n",
			model.Mapping{Parties: map[string]string{"Amy": "Amy"}},
			[]string{"parties", "Zed"},
		},
		{
			"the same with a section after it",
			"parties:\n  Zed: |\n    first line\n    # looks like a comment\ntags:\n  t: T\n",
			model.Mapping{Parties: map[string]string{"Amy": "Amy"}},
			[]string{"parties", "Zed"},
		},
		{
			"a block scalar of the expense categories, and an expense category to add",
			"categories:\n  expense:\n    Food/Lunch: |\n      lunch\n      # looks like a comment\n",
			model.Mapping{Expense: map[string]model.CategoryMap{"A/B": {Category: "A/B"}}},
			[]string{"categories.expense", "Food/Lunch"},
		},
		{
			"a block scalar of the expense categories, and an income section to add",
			"categories:\n  expense:\n    Food/Lunch: |\n      lunch\n      # looks like a comment\nparties:\n  Zed: Zed\n",
			model.Mapping{Income: map[string]model.CategoryMap{"X/Y": {Category: "X/Y"}}},
			[]string{"categories.expense", "Food/Lunch"},
		},
		{
			"a block scalar that keeps the blank lines after it",
			"parties:\n  Zed: |+\n    first line\n\ntags:\n  t: T\n",
			model.Mapping{Parties: map[string]string{"Amy": "Amy"}},
			[]string{"parties", "Zed"},
		},
		{
			"a double-quoted scalar of several lines",
			"parties:\n  Zed: \"first line\n    # not a comment\"\n",
			model.Mapping{Parties: map[string]string{"Amy": "Amy"}},
			[]string{"parties", "Amy"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "mapping.yaml", tt.text)
			old := setOld(t, path)
			f := appendFails(t, path, tt.guessed)
			wantFailure(t, f, filefailure.WouldReadDifferently, 0, tt.wantArgs...)
			wantUntouched(t, path, tt.text, old)
		})
	}
}

// A value the file cannot hold is refused, not written as another one: an opening
// date is a day, and the time of day or the zone of a time would be lost.
func TestAppendRefusesAnOpeningDateItCannotKeep(t *testing.T) {
	tests := map[string]time.Time{
		"a time of day": time.Date(2025, 1, 7, 15, 30, 0, 0, time.UTC),
		"another zone":  time.Date(2025, 1, 7, 0, 0, 0, 0, time.FixedZone("UTC+8", 8*3600)),
	}
	for name, opening := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mapping.yaml")
			guessed := model.Mapping{Accounts: map[string]model.AccountMap{"A": {Name: "A", Kind: "bank", OpeningDate: opening}}}
			wantFailure(t, appendFails(t, path, guessed), filefailure.WouldReadDifferently, 0, "accounts", "A")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Errorf("Stat = %v, want no file to be made of what it cannot keep", err)
			}
		})
	}
}

// A number is what it is whatever way it is written: 1.50 is read back as 1.5, and
// that is the same quantity, not a value that changed.
func TestAppendKeepsANumberThatIsWrittenAnotherWay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	guessed := model.Mapping{Rows: map[string]model.RowOverride{
		"r1": {Quantity: ptr("1.50")},
		"r2": {Quantity: ptr("100")},
		"r3": {Quantity: ptr("1e2")},
		"r4": {Quantity: ptr("-0.0010")},
	}}
	appendTo(t, path, guessed)
	sameMapping(t, load(t, path).Mapping, guessed)
}

func TestAppendRefusesNamesThatCannotBeOneLine(t *testing.T) {
	long := strings.Repeat("k", 200)
	keys := map[string]string{"a line break": "two\nlines", "more than 128 characters": long, "not UTF-8": "bad\xffname"}
	for name, key := range keys {
		t.Run(name, func(t *testing.T) {
			guessed := model.Mapping{Accounts: map[string]model.AccountMap{key: {Name: "x"}}}

			text := "accounts:\n  A: {name: A}\n"
			path := writeFile(t, t.TempDir(), "mapping.yaml", text)
			old := setOld(t, path)
			wantFailure(t, appendFails(t, path, guessed), filefailure.EntryNotWritable, 0, "accounts", key)
			wantUntouched(t, path, text, old)

			made := filepath.Join(t.TempDir(), "mapping.yaml")
			wantFailure(t, appendFails(t, made, guessed), filefailure.EntryNotWritable, 0, "accounts", key)
			if _, err := os.Stat(made); !os.IsNotExist(err) {
				t.Errorf("Stat = %v, want no file to be made of what cannot be written", err)
			}
		})
	}
	t.Run("not when the file has it", func(t *testing.T) {
		text := "accounts:\n  " + long + ": {name: x}\n"
		path := writeFile(t, t.TempDir(), "mapping.yaml", text)
		old := setOld(t, path)
		appendTo(t, path, model.Mapping{Accounts: map[string]model.AccountMap{long: {Name: "x"}}})
		wantUntouched(t, path, text, old)
	})
}

func TestAppendWithAContextThatIsDone(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "mapping.yaml")
	if err := file.NewMappingYAML().Append(done, path, guessedAll()); err == nil {
		t.Error("Append went on with a context that is done")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("Stat = %v, want no file to be made", err)
	}
}
