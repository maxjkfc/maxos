package model_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func day(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}

func sample(amount string) model.Row {
	return model.Row{
		Account: "Cash", Currency: "TWD", Type: model.TypeExpense, Main: "Food", Sub: "Lunch",
		Amount: d(amount), Date: day("2026-03-10"), Clock: "12:00", Name: "Noodles", Description: "a\nb",
	}
}

func keys(rows ...model.Row) []string {
	model.AssignKeys(rows)
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Key
	}
	return out
}

func TestKeysStayApartForIdenticalRowsAndAreStableAcrossExports(t *testing.T) {
	first := keys(sample("-10"), sample("-10"), sample("-20"))
	if first[0] == first[1] || first[0] == first[2] {
		t.Fatalf("keys = %v, want every row to have its own", first)
	}

	// The same rows exported again with more after them have the same keys.
	longer := keys(sample("-10"), sample("-10"), sample("-20"), sample("-10"))
	for i := range first {
		if longer[i] != first[i] {
			t.Errorf("key %d = %q, was %q: a longer export must not change the keys of the rows it has", i, longer[i], first[i])
		}
	}
	if longer[3] == longer[0] || longer[3] == longer[1] {
		t.Errorf("a third identical row has the key of an earlier one: %v", longer)
	}
}

func TestKeyIgnoresTheFormOfNumbersAndTheWhitespaceAroundText(t *testing.T) {
	a, b := sample("-10"), sample("-10.00")
	b.Name = "  Noodles \t"
	b.Description = "a\r\nb "
	if keys(a)[0] != keys(b)[0] {
		t.Error("the same row written with another number format and spaces has another key")
	}

	c := sample("-10")
	c.Name = "Rice"
	if keys(a)[0] == keys(c)[0] {
		t.Error("rows that differ have one key")
	}
}

func TestRatesLookupTakesTheNearestEarlierDay(t *testing.T) {
	r := model.NewRates(map[string][]model.Rate{
		"USD": {{Date: day("2026-03-12"), PerUnit: d("31")}, {Date: day("2026-03-01"), PerUnit: d("30")}},
	})
	for _, tt := range []struct {
		day  string
		want string
		ok   bool
	}{
		{"2026-03-01", "30", true},
		{"2026-03-05", "30", true},
		{"2026-03-12", "31", true},
		{"2026-04-01", "31", true},
		{"2026-02-28", "", false},
	} {
		got, ok := r.Lookup("USD", day(tt.day))
		if ok != tt.ok || (ok && !got.PerUnit.Equal(d(tt.want))) {
			t.Errorf("Lookup(%s) = %v, %v, want %v, %v", tt.day, got.PerUnit, ok, tt.want, tt.ok)
		}
	}
	if _, ok := r.Lookup("JPY", day("2026-03-12")); ok {
		t.Error("a code with no rates has a rate")
	}
	if got := model.BaseAmountOf(model.Rate{PerUnit: d("0.2087")}, d("-1234")); !got.Equal(d("257.54")) {
		t.Errorf("BaseAmountOf = %s, want 257.54 (a size, to two decimals)", got)
	}
}

func TestMappingValidateFindsWhatCannotBeMadeOfIt(t *testing.T) {
	m := model.Mapping{
		Instruments: map[string]model.InstrumentMap{"XYZ": {Name: "", Kind: "gold", Decimals: 30}},
		Accounts: map[string]model.AccountMap{
			"a": {Name: "A", Kind: "card", ClosingDay: 40},
			"b": {Name: "A", Kind: "bank"},
			"c": {Name: "C", Kind: "cash", PaymentDueDay: 5},
		},
		Projects: map[string]model.ProjectMap{
			"p": {Kind: model.ProjectProject, Name: "P", DefaultArea: "nope"},
			"q": {Kind: model.ProjectDealings},
			"r": {Kind: "other"},
		},
		Expense: map[string]model.CategoryMap{"x/y": {Category: ""}},
		Income:  map[string]model.CategoryMap{"x/z": {Treatment: model.TreatmentDealing}},
		Rows:    map[string]model.RowOverride{"k": {Treatment: "mystery"}},
	}
	problems := m.Validate()
	if len(problems) < 12 {
		t.Errorf("problems = %d, want every one of them found: %v", len(problems), problems)
	}
	if ok := (model.Mapping{Accounts: map[string]model.AccountMap{"a": {Name: "A", Kind: "bank"}}}).Validate(); len(ok) != 0 {
		t.Errorf("a valid mapping has problems: %v", ok)
	}
}

func TestMappingMergeKeepsWhatIsAlreadyThere(t *testing.T) {
	m := model.Mapping{Accounts: map[string]model.AccountMap{"a": {Name: "mine"}}, Defaults: model.Defaults{FeeCategory: "mine"}}
	g := model.Mapping{Accounts: map[string]model.AccountMap{"a": {Name: "guess"}, "b": {Name: "guess"}}, Defaults: model.Defaults{FeeCategory: "guess", RefundCategory: "guess"}}
	got := m.Merge(g)
	if got.Accounts["a"].Name != "mine" || got.Accounts["b"].Name != "guess" {
		t.Errorf("accounts = %+v", got.Accounts)
	}
	if got.Defaults != (model.Defaults{FeeCategory: "mine", RefundCategory: "guess"}) {
		t.Errorf("defaults = %+v", got.Defaults)
	}
}

func TestSplitCategory(t *testing.T) {
	for in, want := range map[string][2]string{
		"Food":          {"Food", ""},
		"Food/Lunch":    {"Food", "Lunch"},
		" Food / Lunch": {"Food", "Lunch"},
	} {
		if p, c := model.SplitCategory(in); p != want[0] || c != want[1] {
			t.Errorf("SplitCategory(%q) = %q, %q, want %q, %q", in, p, c, want[0], want[1])
		}
	}
}

func TestUnitKeyIsMadeOfTheRowsAndTheVersion(t *testing.T) {
	u := model.Unit{Kind: model.KindExpenseReward, ID: "abc-1", Version: "0123456789ab"}
	if got, want := u.Key(), "moze:abc-1@0123456789ab"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
	if got := u.Keys(); len(got) != 2 || got[1] != "moze:abc-1@0123456789ab#reward" {
		t.Errorf("Keys = %v, want the key and the key of the reward", got)
	}

	for _, tt := range []struct {
		key  string
		want model.ParsedKey
		ok   bool
	}{
		{"moze:abc-1@0123456789ab", model.ParsedKey{ID: "abc-1", Version: "0123456789ab"}, true},
		{"moze:abc-1@0123456789ab#reward", model.ParsedKey{ID: "abc-1", Version: "0123456789ab", Reward: true}, true},
		{"moze:abc-1+def-2@ffffffffffff", model.ParsedKey{ID: "abc-1+def-2", Version: "ffffffffffff"}, true},
		{"moze:plan-abc-1@ffffffffffff", model.ParsedKey{ID: "plan-abc-1", Version: "ffffffffffff"}, true},
		{"moze:abc-1", model.ParsedKey{ID: "abc-1"}, true},
		{"moze:abc-1#reward", model.ParsedKey{ID: "abc-1", Reward: true}, true},
		{"instalment:abc", model.ParsedKey{}, false},
		{"moze:", model.ParsedKey{}, false},
		{"", model.ParsedKey{}, false},
	} {
		got, ok := model.ParseKey(tt.key)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseKey(%q) = %+v, %v, want %+v, %v", tt.key, got, ok, tt.want, tt.ok)
		}
	}
}

func TestFingerprintChangesWithWhatIsRecordedAndNotWithTheOrderOfTags(t *testing.T) {
	base := func() model.Unit {
		cents := d("5")
		return model.Unit{
			Kind: model.KindExpense, ID: "abc-1", Date: day("2026-03-10"), Clock: "12:00",
			Description: "Noodles", Merchant: "Stall", Area: "Life", Project: "Trip", Tags: []string{"a", "b"},
			Main:  model.Leg{Account: "Cash", Instrument: "TWD", Amount: d("100")},
			Lines: []model.Line{{Category: "Food/Lunch", Amount: d("100"), Discount: &cents, Memo: "m"}},
			Fees:  []model.Fee{{Account: "Cash", Instrument: "TWD", Category: "Fees", Amount: d("1")}},
		}
	}
	want := base().Fingerprint()
	if len(want) != 12 {
		t.Fatalf("Fingerprint = %q, want 12 hex characters", want)
	}
	if got := base().Fingerprint(); got != want {
		t.Errorf("the same unit has the fingerprints %q and %q", want, got)
	}
	reordered := base()
	reordered.Tags = []string{"b", "a"}
	if got := reordered.Fingerprint(); got != want {
		t.Errorf("the order of the tags changes the fingerprint")
	}
	sameNumber := base()
	sameNumber.Main.Amount = d("100.00")
	if got := sameNumber.Fingerprint(); got != want {
		t.Errorf("the form of an amount changes the fingerprint")
	}

	for name, change := range map[string]func(u *model.Unit){
		"kind":        func(u *model.Unit) { u.Kind = model.KindIncome },
		"day":         func(u *model.Unit) { u.Date = day("2026-03-11") },
		"time":        func(u *model.Unit) { u.Clock = "12:01" },
		"description": func(u *model.Unit) { u.Description = "Rice" },
		"merchant":    func(u *model.Unit) { u.Merchant = "Shop" },
		"area":        func(u *model.Unit) { u.Area = "Work" },
		"project":     func(u *model.Unit) { u.Project = "" },
		"tags":        func(u *model.Unit) { u.Tags = []string{"a"} },
		"account":     func(u *model.Unit) { u.Main.Account = "Bank" },
		"amount":      func(u *model.Unit) { u.Main.Amount = d("101") },
		"category":    func(u *model.Unit) { u.Lines[0].Category = "Food/Dinner" },
		"memo":        func(u *model.Unit) { u.Lines[0].Memo = "n" },
		"discount":    func(u *model.Unit) { u.Lines[0].Discount = nil },
		"fee":         func(u *model.Unit) { u.Fees[0].Amount = d("2") },
		"party":       func(u *model.Unit) { u.Party = "Amy" },
		"other leg":   func(u *model.Unit) { u.Other = &model.Leg{Account: "Bank", Instrument: "TWD", Amount: d("1")} },
		"period":      func(u *model.Unit) { u.Periods = []model.Period{{DueOn: day("2026-04-01"), Principal: d("1")}} },
	} {
		u := base()
		change(&u)
		if got := u.Fingerprint(); got == want {
			t.Errorf("a change of the %s does not change the fingerprint", name)
		}
	}
}

func TestMappingEntriesAreCountedOnceIncludingTheDefaults(t *testing.T) {
	m := model.Mapping{
		Accounts: map[string]model.AccountMap{"b": {Name: "B"}, "a": {Name: "A"}},
		Tags:     map[string]string{"t": "t"},
		Defaults: model.Defaults{FeeCategory: "Fees", RefundCategory: "Refunds"},
		Rows:     map[string]model.RowOverride{"k-1": {}},
	}
	want := []model.EntryRef{
		{Section: model.SectionAccounts, Key: "a"}, {Section: model.SectionAccounts, Key: "b"},
		{Section: model.SectionTags, Key: "t"},
		{Section: model.SectionDefaults, Key: model.DefaultFeeCategory}, {Section: model.SectionDefaults, Key: model.DefaultRefundCategory},
		{Section: model.SectionRows, Key: "k-1"},
	}
	got := m.Items()
	if len(got) != len(want) || m.Entries() != len(want) {
		t.Fatalf("Items = %v (%d entries), want %v", got, m.Entries(), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Items[%d] = %v, want %v", i, got[i], want[i])
		}
	}
	if (model.Mapping{Defaults: model.Defaults{InterestCategory: "x"}}).Empty() {
		t.Error("a mapping with a default is empty")
	}
	if !(model.Mapping{}).Empty() {
		t.Error("a mapping with nothing is not empty")
	}
}

func TestMonthsCountAMonthWhoseRowsAllComeToNothing(t *testing.T) {
	p := model.Plan{
		Effects: []model.Effect{{Date: day("2026-01-05"), Quantity: d("10")}},
		Units: []model.Unit{
			{Date: day("2026-03-02")}, // an expense that a discount paid in full: no effect
		},
		Adjustments: []model.Adjustment{{Date: day("2026-04-01")}},
	}
	got := p.Months()
	want := []string{"2026-01", "2026-02", "2026-03", "2026-04"}
	if len(got) != len(want) {
		t.Fatalf("Months = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Months = %v, want %v", got, want)
		}
	}
	if m := (model.Plan{}).Months(); m != nil {
		t.Errorf("Months of nothing = %v", m)
	}
}
