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

func TestUnitKeyIsMadeOfTheRowsTheVersionAndTheAttempt(t *testing.T) {
	u := model.Unit{Kind: model.KindExpenseReward, ID: "abc-1", Version: "0123456789ab"}
	if got, want := u.Key(), "moze:abc-1@0123456789ab"; got != want {
		t.Errorf("Key = %q, want %q", got, want)
	}
	if got := u.Keys(); len(got) != 2 || got[1] != "moze:abc-1@0123456789ab#reward" {
		t.Errorf("Keys = %v, want the key and the key of the reward", got)
	}
	if got, want := u.KeyAt(1), u.Key(); got != want {
		t.Errorf("KeyAt(1) = %q, want the key of the first attempt, %q", got, want)
	}
	if got, want := u.KeyAt(3), "moze:abc-1@0123456789ab~3"; got != want {
		t.Errorf("KeyAt(3) = %q, want %q", got, want)
	}
	if got := u.KeysAt(2); len(got) != 2 || got[0] != "moze:abc-1@0123456789ab~2" || got[1] != "moze:abc-1@0123456789ab~2#reward" {
		t.Errorf("KeysAt(2) = %v, want the key of the attempt and the key of its reward", got)
	}

	for _, tt := range []struct {
		key  string
		want model.ParsedKey
		ok   bool
	}{
		{"moze:abc-1@0123456789ab", model.ParsedKey{ID: "abc-1", Version: "0123456789ab", Attempt: 1}, true},
		{"moze:abc-1@0123456789ab#reward", model.ParsedKey{ID: "abc-1", Version: "0123456789ab", Attempt: 1, Reward: true}, true},
		{"moze:abc-1@0123456789ab~2", model.ParsedKey{ID: "abc-1", Version: "0123456789ab", Attempt: 2}, true},
		{"moze:abc-1@0123456789ab~12#reward", model.ParsedKey{ID: "abc-1", Version: "0123456789ab", Attempt: 12, Reward: true}, true},
		// A mark that is not followed by a number of an attempt after the first is not one.
		{"moze:abc-1@0123456789ab~x", model.ParsedKey{ID: "abc-1", Version: "0123456789ab~x", Attempt: 1}, true},
		{"moze:abc-1@0123456789ab~1", model.ParsedKey{ID: "abc-1", Version: "0123456789ab~1", Attempt: 1}, true},
		{"moze:abc-1+def-2@ffffffffffff", model.ParsedKey{ID: "abc-1+def-2", Version: "ffffffffffff", Attempt: 1}, true},
		{"moze:plan-abc-1@ffffffffffff", model.ParsedKey{ID: "plan-abc-1", Version: "ffffffffffff", Attempt: 1}, true},
		{"moze:abc-1", model.ParsedKey{ID: "abc-1", Attempt: 1}, true},
		{"moze:abc-1#reward", model.ParsedKey{ID: "abc-1", Attempt: 1, Reward: true}, true},
		{"instalment:abc", model.ParsedKey{}, false},
		{"moze:", model.ParsedKey{}, false},
		{"", model.ParsedKey{}, false},
	} {
		got, ok := model.ParseKey(tt.key)
		if ok != tt.ok || got != tt.want {
			t.Errorf("ParseKey(%q) = %+v, %v, want %+v, %v", tt.key, got, ok, tt.want, tt.ok)
		}
	}

	// What a unit writes, ParseKey reads back.
	for attempt := 1; attempt <= 3; attempt++ {
		for i, key := range u.KeysAt(attempt) {
			got, ok := model.ParseKey(key)
			if !ok || got.ID != u.ID || got.Version != u.Version || got.Attempt != attempt || got.Reward != (i == 1) {
				t.Errorf("ParseKey(%q) = %+v, %v, want the unit's ID and version, attempt %d, and the reward for the second", key, got, ok, attempt)
			}
		}
	}

	plan := model.ParsedKey{ID: model.PlanIDPrefix + "abc-1"}
	if got := plan.Rows(); !plan.IsPlan() || len(got) != 1 || got[0] != "abc-1" {
		t.Errorf("a plan is %v, with the rows %v, want it to be one, of its first row", plan.IsPlan(), got)
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
