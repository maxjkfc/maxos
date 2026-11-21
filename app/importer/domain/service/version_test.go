package service_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/domain/service"
)

// versions are the versions of the units of a plan, by their IDs.
func versions(p model.Plan) map[string]string {
	out := make(map[string]string, len(p.Units))
	for _, u := range p.Units {
		out[u.ID] = u.Version
	}
	return out
}

// changedBetween lists the IDs of the units that both plans have and whose versions
// differ, in the order of the first.
func changedBetween(before, after model.Plan) []string {
	later := versions(after)
	var ids []string
	for _, u := range before.Units {
		if v, ok := later[u.ID]; ok && v != u.Version {
			ids = append(ids, u.ID)
		}
	}
	return ids
}

// withoutText is the unit with no description, merchant, memo or reward description.
func withoutText(u model.Unit) model.Unit {
	u.Description, u.Merchant = "", ""
	u.Lines = slices.Clone(u.Lines)
	for i := range u.Lines {
		u.Lines[i].Memo = ""
	}
	u.Fees = slices.Clone(u.Fees)
	for i := range u.Fees {
		u.Fees[i].Memo = ""
	}
	if u.Reward != nil {
		reward := *u.Reward
		reward.Description = ""
		u.Reward = &reward
	}
	return u
}

func TestTheVersionOfAUnitIsMadeOfItsInputsNotOfTheTextWrittenFromThem(t *testing.T) {
	// Every kind of unit, with a clash of merchants and of 專案 among the rows of a pair
	// and of a plan, and a 專案 paid for a party: the text of each is full of the labels
	// that the plan writes.
	rows := append(everyRecordType(),
		out("Bank", "TWD", "-100", at("21:00"), merchant("Shop A"), project("Trip")),
		in("Card", "TWD", "100", at("21:00"), merchant("Shop B"), project("Corp paid")),
		row(model.TypeBonus, "Card", "TWD", "5", at("22:00"), project("Corp paid"), name("Cash back")),
	)
	p := plan(rows...)
	if len(p.Units) == 0 {
		t.Fatal("no units")
	}
	for _, u := range p.Units {
		if len(u.Version) != 12 {
			t.Errorf("%s %s has the version %q, want 12 hex characters", u.Kind, u.ID, u.Version)
		}
		if got := withoutText(u).Fingerprint(); got != u.Version {
			t.Errorf("%s %s has the version %q, but %q without its text: the version is made of the text that the plan writes, and a new wording would give every unit a new one", u.Kind, u.ID, u.Version, got)
		}
	}
}

func TestVersionsAndNotesAreTheSameOnEveryBuildOfTheSameInputs(t *testing.T) {
	// Every kind of unit, a row with several tags, a pair that says two merchants and
	// two areas, and a 專案 paid for a party: built 30 times, as the order of a map shows
	// itself in some of them.
	rows := rowsOf(append(everyRecordType(),
		row(model.TypeExpense, "Cash", "TWD", "-15", main_("Food", "Lunch"), tags("family", "beta", "alpha"), on("2026-03-11")),
		out("Bank", "TWD", "-100", at("21:00"), merchant("Shop A"), project("Daily")),
		in("Card", "TWD", "100", at("21:00"), merchant("Shop B"), project("Work")),
		row(model.TypeBonus, "Card", "TWD", "5", at("22:00"), project("Corp paid")),
	)...)
	build := func() model.Plan {
		m := testMapping()
		m.Projects["Work"] = model.ProjectMap{Kind: model.ProjectArea, Name: "Work"}
		return serviceBuild(rows, m)
	}
	first := build()
	notes := 0
	for _, u := range first.Units {
		notes += len(u.Notes)
	}
	if notes != 3 {
		t.Fatalf("the units have %d notes, want 3: a merchant and an area of the pair, and the 專案 of the reward", notes)
	}
	for run := 1; run < 30; run++ {
		again := build()
		if len(again.Units) != len(first.Units) {
			t.Fatalf("run %d has %d units, want %d", run, len(again.Units), len(first.Units))
		}
		for i, u := range first.Units {
			if got := again.Units[i]; got.ID != u.ID || got.Version != u.Version || !reflect.DeepEqual(got.Notes, u.Notes) {
				t.Fatalf("run %d: unit %d is %s at %s with the notes %v, want %s at %s with %v", run, i, got.ID, got.Version, got.Notes, u.ID, u.Version, u.Notes)
			}
		}
	}
}

func TestTheOrderOfTheTagsOfAUnitIsNotInItsVersion(t *testing.T) {
	// The tags of the row are named by the mapping: the same two names, the other way round.
	rows := rowsOf(row(model.TypeExpense, "Cash", "TWD", "-15", main_("Food", "Lunch"), tags("one", "two")))
	build := func(first, second string) model.Unit {
		m := testMapping()
		m.Tags = map[string]string{"one": first, "two": second}
		return unitOf(t, serviceBuild(rows, m))
	}
	a, b := build("Alpha", "Beta"), build("Beta", "Alpha")
	if reflect.DeepEqual(a.Tags, b.Tags) {
		t.Fatalf("tags = %v and %v, want the same two in another order", a.Tags, b.Tags)
	}
	if a.Version != b.Version {
		t.Errorf("the versions are %q and %q: the order of the tags is no part of what the ledger is asked", a.Version, b.Version)
	}
}

func TestAChangeOfAnInputChangesExactlyTheVersionsOfTheUnitsItReaches(t *testing.T) {
	rows := rowsOf(
		/* 0 */ row(model.TypeExpense, "Cash", "TWD", "-100", main_("Food", "Lunch"), project("Daily"), tags("family", "crowd"), on("2026-03-01")),
		/* 1 */ row(model.TypeExpense, "Card", "TWD", "-50", main_("Food", "Lunch"), project("Trip"), on("2026-03-02")),
		/* 2 */ row(model.TypeExpense, "Card", "TWD", "-70", main_("Shop", "Books"), on("2026-03-03")),
		/* 3 */ row(model.TypeReceivable, "Bank", "TWD", "-300", party("Amy"), main_("應收款項", "借出"), on("2026-03-04")),
		/* 4 */ row(model.TypeExpense, "Bank USD", "USD", "-10", main_("Food", "Lunch"), on("2026-03-05")),
		/* 5 */ out("Bank", "TWD", "-100", on("2026-03-06")),
		/* 6 */ in("Cash", "TWD", "100", on("2026-03-06")),
		/* 7 */ out("Exchange", "USDT", "-100", on("2026-03-07")),
		/* 8 */ in("Exchange BTC", "BTC", "0", on("2026-03-07")),
		/* 9 */ row(model.TypeInitial, "Cash", "TWD", "1000", on("0001-01-01"), at("")),
	)
	// The units, by the rows they are made of.
	unit := func(first int, more ...int) string {
		id := rows[first].Key
		for _, i := range more {
			id += "+" + rows[i].Key
		}
		return id
	}
	mapping := func() model.Mapping {
		m := testMapping()
		m.Rows = map[string]model.RowOverride{rows[8].Key: {Quantity: ptr("0.002")}}
		return m
	}
	build := func(m model.Mapping, rates model.Rates) model.Plan {
		return service.BuildPlan(service.PlanInput{Rows: rows, Mapping: m, Rates: rates})
	}
	before := build(mapping(), testRates())
	if len(before.Units) != 8 || len(before.Skipped) != 0 || len(before.Problems) != 0 {
		t.Fatalf("units = %d, skipped = %+v, problems = %+v, want 8 units recorded whole", len(before.Units), before.Skipped, before.Problems)
	}

	// What a change is, and the units, in the order of the plan, whose version it changes.
	dearerDollar := model.NewRates(map[string][]model.Rate{
		"USD": {{Date: day("2026-03-01"), PerUnit: d("29")}, {Date: day("2026-03-12"), PerUnit: d("31")}},
		"JPY": {{Date: day("2026-03-01"), PerUnit: d("0.2")}},
	})
	for _, tt := range []struct {
		name    string
		change  func(m *model.Mapping)
		rates   *model.Rates
		reaches []string
	}{
		{
			name:    "the category of an expense",
			change:  func(m *model.Mapping) { m.Expense["Food/Lunch"] = model.CategoryMap{Category: "Food/Dinner"} },
			reaches: []string{unit(0), unit(1), unit(4)},
		},
		{
			name:    "the category of another expense",
			change:  func(m *model.Mapping) { m.Expense["Shop/Books"] = model.CategoryMap{Category: "Shop/Gifts"} },
			reaches: []string{unit(2)},
		},
		{
			name: "the name of an area",
			change: func(m *model.Mapping) {
				m.Projects["Daily"] = model.ProjectMap{Kind: model.ProjectArea, Name: "Everyday"}
			},
			reaches: []string{unit(0)},
		},
		{
			name:    "the kind of a project",
			change:  func(m *model.Mapping) { m.Projects["Trip"] = model.ProjectMap{Kind: model.ProjectTag, Name: "Trip"} },
			reaches: []string{unit(1)},
		},
		{
			name:    "the name of a tag",
			change:  func(m *model.Mapping) { m.Tags["crowd"] = "Crowd" },
			reaches: []string{unit(0)},
		},
		{
			name: "the name of an account",
			change: func(m *model.Mapping) {
				m.Accounts["Card"] = model.AccountMap{Name: "Card 2", Kind: "card", Institution: "FakeBank"}
			},
			reaches: []string{unit(1), unit(2)},
		},
		{
			name:    "the name of a party",
			change:  func(m *model.Mapping) { m.Parties["Amy"] = "Amy 2" },
			reaches: []string{unit(3)},
		},
		{
			name: "the day of the opening balances of an account",
			change: func(m *model.Mapping) {
				cash := m.Accounts["Cash"]
				cash.OpeningDate = day("2026-01-01")
				m.Accounts["Cash"] = cash
			},
			reaches: []string{unit(9)},
		},
		{
			name:    "the override of a row",
			change:  func(m *model.Mapping) { m.Rows[rows[2].Key] = model.RowOverride{Tag: "checked"} },
			reaches: []string{unit(2)},
		},
		{
			name:    "the quantity of a trade",
			change:  func(m *model.Mapping) { m.Rows[rows[8].Key] = model.RowOverride{Quantity: ptr("0.003")} },
			reaches: []string{unit(7, 8)},
		},
		{
			name:    "a rate",
			change:  func(*model.Mapping) {},
			rates:   &dearerDollar,
			reaches: []string{unit(4)},
		},
		{
			name:   "an entry that no row has",
			change: func(m *model.Mapping) { m.Expense["Never/Used"] = model.CategoryMap{Category: "Never"} },
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			m, rates := mapping(), testRates()
			tt.change(&m)
			if tt.rates != nil {
				rates = *tt.rates
			}
			after := build(m, rates)
			if len(after.Units) != len(before.Units) {
				t.Fatalf("units = %d, want %d: the change makes no unit and takes none", len(after.Units), len(before.Units))
			}
			if got := changedBetween(before, after); !slices.Equal(got, tt.reaches) {
				t.Errorf("the versions that change are those of %v, want those of %v", got, tt.reaches)
			}
		})
	}
}

func TestAChangeOfAnyRowOfAPlanChangesItsVersionThoughItsIDIsThatOfTheFirst(t *testing.T) {
	// A fee or a discount on the payment or the interest of a period is a difference:
	// the plan records neither, and says nothing of it but what it makes of the rows.
	// The rows are in the version whole, so that it is a new one.
	base := plan(instalmentRows("Bank", "Bank USD")...)
	was := unitOf(t, base)
	for i, r := range instalmentRows("Bank", "Bank USD") {
		if r.Type != model.TypeInstalmentPayment && r.Type != model.TypeInterest {
			continue
		}
		rows := instalmentRows("Bank", "Bank USD")
		rows[i].Fee = d("-1")
		changed := unitOf(t, plan(rows...))
		if changed.ID != was.ID {
			t.Fatalf("row %d: the plan has the ID %q, want %q: the first row is as it was", i, changed.ID, was.ID)
		}
		if changed.Version == was.Version {
			t.Errorf("row %d (%s): the version is as it was, though a row of the plan is not", i, r.Type)
		}
	}
}

func TestAClashBetweenTheRowsOfAUnitIsANoteAndTheVersionHasTheNoteNotTheLine(t *testing.T) {
	areas := func(x, y string) model.Mapping {
		m := testMapping()
		m.Projects["X"] = model.ProjectMap{Kind: model.ProjectArea, Name: x}
		m.Projects["Y"] = model.ProjectMap{Kind: model.ProjectArea, Name: y}
		return m
	}
	rows := rowsOf(out("Bank", "TWD", "-100", project("X")), in("Card", "TWD", "100", project("Y")))

	// The same rows, and the mapping that makes the 專案 of both one area, or two: the
	// unit is the same in everything it has a place for, and has a note of the one that has no place.
	one := unitOf(t, serviceBuild(rows, areas("Life", "Life")))
	two := unitOf(t, serviceBuild(rows, areas("Life", "Work")))
	if one.Area != "Life" || two.Area != "Life" {
		t.Fatalf("areas = %q and %q, want the area of the 轉出 in both", one.Area, two.Area)
	}
	if len(one.Notes) != 0 {
		t.Errorf("notes = %v, want none for rows that say one area", one.Notes)
	}
	if want := []model.Note{{Kind: model.NoteProject, Value: "Y"}}; !reflect.DeepEqual(two.Notes, want) {
		t.Errorf("notes = %v, want %v: the 專案 of the 轉入 has no place", two.Notes, want)
	}
	if !strings.Contains(two.Description, "專案：Y") {
		t.Errorf("description = %q, want the line that says the note", two.Description)
	}
	if one.Version == two.Version {
		t.Error("a note changes nothing in the version")
	}

	// The version is made of the note, not of the line that says it.
	reworded := two
	reworded.Description = strings.ReplaceAll(two.Description, "專案：Y", "Another label: Y")
	if reworded.Fingerprint() != two.Version {
		t.Error("the version is made of the wording of the line of a note")
	}
	forgotten := two
	forgotten.Notes = nil
	if forgotten.Fingerprint() == two.Version {
		t.Error("the version is not made of the notes")
	}

	// Two merchants are a note of the second, whatever the mapping.
	merchants := rowsOf(out("Bank", "TWD", "-100", merchant("Shop A")), in("Card", "TWD", "100", merchant("Shop B")))
	u := unitOf(t, plan(merchants...))
	if want := []model.Note{{Kind: model.NoteMerchant, Value: "Shop B"}}; u.Merchant != "Shop A" || !reflect.DeepEqual(u.Notes, want) {
		t.Errorf("merchant = %q, notes = %v, want Shop A and %v", u.Merchant, u.Notes, want)
	}
	if !strings.Contains(u.Description, "商家：Shop B") {
		t.Errorf("description = %q, want the line that says the note", u.Description)
	}
}

func TestANoteOfAPlanIsOnceAndInTheOrderOfTheRows(t *testing.T) {
	rows := instalmentRows("Bank", "Bank USD")
	for i, shop := range map[int]string{0: "Shop A", 1: "Shop B", 2: "Shop B", 3: "Shop C", 4: "Shop B", 5: "Shop A"} {
		rows[i].Merchant = shop
	}
	u := unitOf(t, plan(rows...))
	want := []model.Note{
		{Kind: model.NoteMerchant, Value: "Shop B"},
		{Kind: model.NoteMerchant, Value: "Shop C"},
	}
	// The merchant of the plan is the first, the 利息 and 分期還款 rows have the others.
	if u.Merchant != "Shop A" || !reflect.DeepEqual(u.Notes, want) {
		t.Errorf("merchant = %q, notes = %v, want Shop A and %v: each merchant that has no place once, in the order of the rows", u.Merchant, u.Notes, want)
	}
	// The description says them in that order.
	if b, c := strings.Index(u.Description, "商家：Shop B"), strings.Index(u.Description, "商家：Shop C"); b < 0 || c < b {
		t.Errorf("description = %q, want the lines of the notes in their order", u.Description)
	}
}

func TestANoteOfAProjectPaidForAPartyIsOnTheUnitThatIsNoDealingOfIt(t *testing.T) {
	p := plan(
		row(model.TypeBonus, "Card", "TWD", "5", project("Corp paid"), name("Cash back")),
		row(model.TypeExpense, "Card", "TWD", "-20", main_("Food", "Lunch"), project("Corp paid"), on("2026-03-11")),
	)
	if len(p.Units) != 2 {
		t.Fatalf("units = %d, want 2", len(p.Units))
	}
	reward, dealing := p.Units[0], p.Units[1]
	if want := []model.Note{{Kind: model.NoteProject, Value: "Corp paid"}}; !reflect.DeepEqual(reward.Notes, want) {
		t.Errorf("reward notes = %v, want %v", reward.Notes, want)
	}
	if !strings.Contains(reward.Description, "專案：Corp paid") {
		t.Errorf("description = %q, want the line that says the note", reward.Description)
	}
	if dealing.Kind != model.KindDealing || len(dealing.Notes) != 0 {
		t.Errorf("%s has the notes %v, want a dealing of Corp, which has the party and no note", dealing.Kind, dealing.Notes)
	}

	// A plan whose every row is of that 專案 has the note once.
	rows := instalmentRows("Bank", "Bank USD")
	for i := range rows {
		rows[i].Project = "Corp paid"
	}
	u := unitOf(t, plan(rows...))
	if want := []model.Note{{Kind: model.NoteProject, Value: "Corp paid"}}; !reflect.DeepEqual(u.Notes, want) || strings.Count(u.Description, "專案：Corp paid") != 1 {
		t.Errorf("notes = %v, description = %q, want the note once, and once the line that says it", u.Notes, u.Description)
	}
}
