package model_test

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// populatedUnit is a unit with a value in every field, and two of everything that is
// a list.
func populatedUnit() model.Unit {
	num := decimal.RequireFromString
	opt := func(s string) *decimal.Decimal { v := num(s); return &v }
	return model.Unit{
		Kind:    model.KindExpenseReward,
		ID:      "abc-1+def-2",
		Version: "ffffffffffff",
		Rows:    []string{"abc-1", "def-2"},
		Date:    time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		Clock:   "12:30",
		Order:   7,

		Description: "Noodles\n商家：Stall 2",
		Merchant:    "Stall",
		Area:        "Life",
		Project:     "Trip",
		Tags:        []string{"Family", "Crowd"},
		Notes:       []model.Note{{Kind: model.NoteMerchant, Value: "Stall 2"}, {Kind: model.NoteProject, Value: "Daily"}},

		Main:  model.Leg{Account: "Cash", Instrument: "TWD", Amount: num("122")},
		Other: &model.Leg{Account: "Bank", Instrument: "USD", Amount: num("4")},
		Lines: []model.Line{
			{Category: "Food/Lunch", Amount: num("100"), Base: opt("3000"), Discount: opt("5"), Memo: "receipt 1"},
			{Category: "Fees", Amount: num("22"), Base: opt("660"), Discount: opt("1"), Memo: "receipt 2"},
		},
		Direction: "paid_out",
		Party:     "Amy",
		Base:      opt("9"),
		Reward:    &model.RewardPart{Amount: num("100"), Base: opt("3000"), Description: "Noodles （折抵）"},
		Fees: []model.Fee{
			{Account: "Cash", Instrument: "TWD", Category: "Fees", Amount: num("2"), Base: opt("2"), Memo: "fee 1"},
			{Account: "Bank", Instrument: "USD", Category: "Charges", Amount: num("3"), Base: opt("90"), Memo: "fee 2"},
		},
		Periods: []model.Period{
			{DueOn: time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC), Principal: num("200"), Interest: opt("6")},
			{DueOn: time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC), Principal: num("400"), Interest: opt("4")},
		},
		InterestCategory: "Interest",
	}
}

// fieldsOfNoVersion are the fields of a unit that its fingerprint leaves out, each with
// the reason: it is text, which is made of the words of the rows (whose keys are in the
// fingerprint, each a hash of everything the row says) and of the labels that the plan
// writes around them (which FormatVersion stands for, and the Notes for the lines
// that the mapping decides), or it is not what the unit says.
var fieldsOfNoVersion = map[string]string{
	"Version":            "it is the fingerprint itself",
	"Order":              "it is the place of the unit in the order of the plan, which another row can move, and not what the unit says",
	"Description":        "text: the words of the rows, and the labels and the lines of the notes that the plan writes around them",
	"Merchant":           "text: a word of the rows",
	"Lines[].Memo":       "text: a word of the rows, and the category they had, which the plan writes after it",
	"Fees[].Memo":        "text: a word of the rows",
	"Reward.Description": "text: a word of the rows, and the label that the plan writes after it",
}

// unhashedFields changes every field of a unit with a value in every field, one at a
// time, and says which of the changes leave the fingerprint as it was: those fields
// are not in it. A field is changed to another value; a list by the value of each
// element, by one element less and by one more; a field that points to something, by
// being absent, or present when it is absent, and by the value it points to. It also
// says which fields it has changed.
func unhashedFields(t *testing.T, fingerprint func(model.Unit) string) (unhashed, changed map[string]bool) {
	t.Helper()
	u := populatedUnit()
	base := fingerprint(u)
	unhashed, changed = map[string]bool{}, map[string]bool{}

	try := func(path string, field, to reflect.Value) {
		old := reflect.New(field.Type()).Elem()
		old.Set(field)
		field.Set(to)
		if fingerprint(u) == base {
			unhashed[path] = true
		}
		field.Set(old)
		changed[path] = true
	}
	var visit func(path string, v reflect.Value)
	visit = func(path string, v reflect.Value) {
		switch {
		case v.Type() == reflect.TypeFor[decimal.Decimal]() || v.Type() == reflect.TypeFor[time.Time]():
			try(path, v, anotherValue(t, path, v))
		case v.Kind() == reflect.Struct:
			for i := range v.NumField() {
				name := v.Type().Field(i).Name
				if path != "" {
					name = path + "." + name
				}
				visit(name, v.Field(i))
			}
		case v.Kind() == reflect.Slice:
			for i := range v.Len() {
				visit(path+"[]", v.Index(i))
			}
			if v.Len() > 0 {
				try(path, v, v.Slice(0, v.Len()-1))
			}
			try(path, v, reflect.Append(v, reflect.Zero(v.Type().Elem())))
		case v.Kind() == reflect.Pointer && v.IsNil():
			try(path, v, reflect.New(v.Type().Elem()))
		case v.Kind() == reflect.Pointer:
			try(path, v, reflect.Zero(v.Type()))
			visit(path, v.Elem())
		default:
			try(path, v, anotherValue(t, path, v))
		}
	}
	visit("", reflect.ValueOf(&u).Elem())
	return unhashed, changed
}

// anotherValue is a value of the type of v that is not v: a number, a text, a day.
func anotherValue(t *testing.T, path string, v reflect.Value) reflect.Value {
	t.Helper()
	switch x := v.Interface().(type) {
	case decimal.Decimal:
		return reflect.ValueOf(x.Add(decimal.NewFromInt(1)))
	case time.Time:
		return reflect.ValueOf(x.AddDate(0, 0, 1))
	}
	another := reflect.New(v.Type()).Elem()
	switch v.Kind() {
	case reflect.String:
		another.SetString(v.String() + "!")
	case reflect.Int:
		another.SetInt(v.Int() + 1)
	case reflect.Bool:
		another.SetBool(!v.Bool())
	default:
		t.Fatalf("%s is a %s: teach the test how to change it, so that no field of a unit goes unchecked", path, v.Type())
	}
	return another
}

// fieldProblems says what is wrong with what the fingerprint is made of: a field that
// it leaves out and the list of what is left out does not have, a field that the list has
// and the fingerprint does not leave out, and a field that the list has and no unit has.
func fieldProblems(t *testing.T, fingerprint func(model.Unit) string) []string {
	t.Helper()
	unhashed, changed := unhashedFields(t, fingerprint)
	var problems []string
	for _, path := range slices.Sorted(maps.Keys(unhashed)) {
		if _, ok := fieldsOfNoVersion[path]; !ok {
			problems = append(problems, path+" is not in the fingerprint, and is not on the list of what it leaves out: hash it, or say why not")
		}
	}
	for _, path := range slices.Sorted(maps.Keys(fieldsOfNoVersion)) {
		switch {
		case !changed[path]:
			problems = append(problems, path+" is on the list of what the fingerprint leaves out, and no unit has it")
		case !unhashed[path]:
			problems = append(problems, path+" is on the list of what the fingerprint leaves out, but it has it")
		}
	}
	return problems
}

func TestFingerprintHasEveryFieldOfAUnitExceptTheTextAndTheOrder(t *testing.T) {
	for _, problem := range fieldProblems(t, model.Unit.Fingerprint) {
		t.Error(problem)
	}
}

func TestTheCheckOfTheFieldsFindsWhatIsNeitherInTheFingerprintNorOnTheList(t *testing.T) {
	for _, tt := range []struct {
		name        string
		fingerprint func(model.Unit) string
		want        []string
	}{
		{
			"a fingerprint that forgets the periods",
			func(u model.Unit) string { u.Periods = nil; return u.Fingerprint() },
			[]string{"Periods is not in the fingerprint", "Periods[].DueOn is not in", "Periods[].Principal is not in", "Periods[].Interest is not in"},
		},
		{
			"a fingerprint that forgets the notes",
			func(u model.Unit) string { u.Notes = nil; return u.Fingerprint() },
			[]string{"Notes is not in the fingerprint", "Notes[].Kind is not in", "Notes[].Value is not in"},
		},
		{
			"a fingerprint that forgets the base amount of the reward",
			func(u model.Unit) string {
				if u.Reward != nil {
					reward := *u.Reward // a copy: the unit shares the reward with the check
					reward.Base = nil
					u.Reward = &reward
				}
				return u.Fingerprint()
			},
			[]string{"Reward.Base is not in"},
		},
		{
			"a fingerprint that has the text",
			func(u model.Unit) string {
				text := u.Description
				for _, l := range u.Lines {
					text += l.Memo
				}
				return u.Fingerprint() + text
			},
			[]string{"Description is on the list of what the fingerprint leaves out, but it has it", "Lines[].Memo is on the list"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			problems := strings.Join(fieldProblems(t, tt.fingerprint), "\n")
			for _, want := range tt.want {
				if !strings.Contains(problems, want) {
					t.Errorf("problems = %q, want one that says %q", problems, want)
				}
			}
		})
	}
}

func TestTheTextOfAUnitIsNotInItsVersion(t *testing.T) {
	want := populatedUnit().Fingerprint()
	for name, change := range map[string]func(u *model.Unit){
		"description":             func(u *model.Unit) { u.Description = "Another wording\n專案：Elsewhere" },
		"merchant":                func(u *model.Unit) { u.Merchant = "Another stall" },
		"memo of a line":          func(u *model.Unit) { u.Lines[0].Memo = "another receipt" },
		"memo of a fee":           func(u *model.Unit) { u.Fees[1].Memo = "another fee" },
		"description of a reward": func(u *model.Unit) { u.Reward.Description = "another reward" },
		"place in the order":      func(u *model.Unit) { u.Order = 99 },
		"version it had before":   func(u *model.Unit) { u.Version = "000000000000" },
	} {
		u := populatedUnit()
		change(&u)
		if got := u.Fingerprint(); got != want {
			t.Errorf("a change of the %s changes the fingerprint: it is made of the inputs of the unit, not of the text that is written from them", name)
		}
	}
}

func TestFingerprintIgnoresTheOrderOfTagsAndTheFormOfNumbers(t *testing.T) {
	want := populatedUnit().Fingerprint()
	if len(want) != 12 {
		t.Fatalf("Fingerprint = %q, want 12 hex characters", want)
	}
	if got := populatedUnit().Fingerprint(); got != want {
		t.Errorf("the same unit has the fingerprints %q and %q", want, got)
	}
	reordered := populatedUnit()
	reordered.Tags = []string{"Crowd", "Family"}
	if got := reordered.Fingerprint(); got != want {
		t.Error("the order of the tags changes the fingerprint")
	}
	sameNumbers := populatedUnit()
	sameNumbers.Main.Amount = decimal.RequireFromString("122.00")
	*sameNumbers.Base = decimal.RequireFromString("9.0")
	if got := sameNumbers.Fingerprint(); got != want {
		t.Error("the form of a number changes the fingerprint")
	}
	absent := populatedUnit()
	absent.Lines[0].Base = nil
	zero := populatedUnit()
	zero.Lines[0].Base = new(decimal.Decimal)
	if absent.Fingerprint() == zero.Fingerprint() {
		t.Error("a base amount that is not there and one that is nothing have the one fingerprint")
	}
}

func TestFingerprintOfAKnownUnitIsPinned(t *testing.T) {
	// What the fingerprint is made of is what the versions of every unit that was
	// recorded are made of: a change to it, or to model.FormatVersion, is a deliberate
	// change of all of them, and is made by changing what is expected here.
	minimal := model.Unit{
		Kind: model.KindIncome, ID: "abc-1", Rows: []string{"abc-1"}, Date: time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC),
		Main:  model.Leg{Account: "Bank", Instrument: "TWD", Amount: decimal.RequireFromString("50000")},
		Lines: []model.Line{{Category: "Salary", Amount: decimal.RequireFromString("50000")}},
	}
	for _, tt := range []struct {
		name string
		unit model.Unit
		want string
	}{
		{"a unit with a value in every field", populatedUnit(), "f20e537fe4a8"},
		{"a unit of an income", minimal, "6e87757a2117"},
	} {
		if got := tt.unit.Fingerprint(); got != tt.want {
			t.Errorf("%s: Fingerprint = %q, want %q", tt.name, got, tt.want)
		}
	}
}
