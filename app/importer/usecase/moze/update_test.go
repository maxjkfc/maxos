package moze_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/moze"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// What the plan says differently after a unit was recorded has to reach the ledger: a
// unit is recorded under a key with its version, so that the ledger, which answers a key
// it has seen with what it stores under it, does not make a changed unit look done.

// export is the export the user makes next: the import reads these rows from now on.
func (w *world) export(rows []model.Row) {
	w.t.Helper()
	inputs := moze.Inputs{Source: fakeSource{rows: rows}, Mapping: w.mapping, Rates: fakeRates{fixtureRates()}}
	w.planner = moze.NewPlanner(inputs)
	w.imp = moze.NewImporter(inputs, w.ledger.ledger(), time.FixedZone("UTC+8", 8*3600))
}

// rowKeyOf is the key of the row with the name.
func (w *world) rowKeyOf(rowName string) string {
	w.t.Helper()
	for _, r := range w.plan().Rows {
		if r.Name == rowName {
			return r.Key
		}
	}
	w.t.Fatalf("no row called %q", rowName)
	return ""
}

// override is the user telling the mapping what to do with a row.
func (w *world) override(rowKey string, ov model.RowOverride) {
	w.t.Helper()
	if w.mapping.loaded.Mapping.Rows == nil {
		w.mapping.loaded.Mapping.Rows = map[string]model.RowOverride{}
	}
	w.mapping.loaded.Mapping.Rows[rowKey] = ov
}

// ofRow are the transactions of the ledger that were recorded from the row.
func (f *fakeLedger) ofRow(rowKey string) []*stored {
	var out []*stored
	for _, t := range f.txns {
		if strings.HasPrefix(t.IdempotencyKey, model.KeyPrefix) && strings.Contains(t.IdempotencyKey, rowKey) {
			out = append(out, t)
		}
	}
	return out
}

func (f *fakeLedger) ofKind(kind string) []*stored {
	var out []*stored
	for _, t := range f.txns {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

func (w *world) mustApply(rows ...model.Row) usecase.ApplyOutput {
	w.t.Helper()
	if len(rows) > 0 {
		w.export(rows)
	}
	return w.apply()
}

func TestAnOverrideAfterApplyReachesTheLedger(t *testing.T) {
	w := applied(t)
	key := w.rowKeyOf("Someone paid") // a 收款: an income, until the user says it was Amy paying back
	if got := w.ledger.ofRow(key); len(got) != 1 || got[0].Kind != "income" || got[0].Status != "draft" {
		t.Fatalf("before the override the row is %d transactions, %v", len(got), got)
	}
	drafts := w.ledger.drafts()

	w.override(key, model.RowOverride{Treatment: model.TreatmentDealing, Party: "Amy"})
	out := w.apply()

	if out.Updated[model.KindDealing] != 1 || sum(out.New) != 0 || len(out.Failed) != 0 || len(out.Stale) != 0 {
		t.Errorf("apply = new %v, updated %v, stale %d, want the one unit updated", out.New, out.Updated, len(out.Stale))
	}
	if got := w.ledger.ofRow(key); len(got) != 1 || got[0].Kind != "dealing" || got[0].Status != "draft" {
		t.Errorf("after the override the row is %d transactions, %v, want one dealing draft and no income", len(got), got)
	}
	if w.ledger.drafts() != drafts {
		t.Errorf("drafts = %d, want %d: the draft is replaced, not added to", w.ledger.drafts(), drafts)
	}

	// The override is in effect when the month is confirmed, and what is confirmed is
	// what the plan says.
	w.mustConfirm("2026-01")
	if got := w.ledger.ofRow(key); got[0].Kind != "dealing" || got[0].Status != "confirmed" {
		t.Errorf("after confirm the row is a %s %s, want a confirmed dealing", got[0].Status, got[0].Kind)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Stale) != 0 {
		t.Errorf("verify = %+v, %v", v, err)
	}
	if st, _ := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files}); st.Orphans != 0 {
		t.Errorf("orphans = %d: the draft that was replaced is not one", st.Orphans)
	}
}

func TestWhatTheMappingSaysDifferentlyAfterApplyReachesTheLedger(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(m *model.Mapping)
		// check looks at the calls apply made after the change.
		check func(t *testing.T, w *world, calls []any)
	}{
		{
			name: "a category",
			change: func(m *model.Mapping) {
				m.Expense["Food/Lunch"] = model.CategoryMap{Category: "Food/Meals"}
			},
			check: func(t *testing.T, w *world, calls []any) {
				meals := uuid.Nil
				for _, c := range w.ledger.categories {
					if c.Name == "Meals" {
						meals = c.ID
					}
				}
				n := 0
				for _, c := range calls {
					if in, ok := c.(finance.RecordExpenseInput); ok && in.Lines[0].CategoryID == meals {
						n++
					}
				}
				if meals == uuid.Nil || n == 0 {
					t.Errorf("%d expenses were recorded under the new category (%v), want the lunches", n, meals)
				}
			},
		},
		{
			name: "an area",
			change: func(m *model.Mapping) {
				m.Projects["Daily"] = model.ProjectMap{Kind: model.ProjectArea, Name: "Weekly"}
			},
			check: func(t *testing.T, w *world, calls []any) {
				weekly := uuid.Nil
				for _, a := range w.ledger.areas {
					if a.Name == "Weekly" {
						weekly = a.ID
					}
				}
				for _, c := range calls {
					if in, ok := c.(finance.RecordExpenseInput); ok && in.AreaID != nil && *in.AreaID != weekly {
						t.Errorf("an expense was recorded in the area %v, want the new one", *in.AreaID)
					}
				}
				if weekly == uuid.Nil || len(calls) == 0 {
					t.Errorf("area %v, %d calls", weekly, len(calls))
				}
			},
		},
		{
			name: "a project",
			change: func(m *model.Mapping) {
				m.Projects["Daily"] = model.ProjectMap{Kind: model.ProjectProject, Name: "Daily trip"}
			},
			check: func(t *testing.T, w *world, calls []any) {
				trip := uuid.Nil
				for _, p := range w.ledger.projects {
					if p.Name == "Daily trip" {
						trip = p.ID
					}
				}
				in := 0
				for _, c := range calls {
					if e, ok := c.(finance.RecordExpenseInput); ok && e.ProjectID != nil && *e.ProjectID == trip {
						in++
					}
				}
				if trip == uuid.Nil || in == 0 {
					t.Errorf("project %v, %d expenses recorded in it, want the rows of 專案 Daily", trip, in)
				}
			},
		},
		{
			name: "an account",
			change: func(m *model.Mapping) {
				m.Accounts["Cash"] = model.AccountMap{Name: "Purse", Kind: "cash"}
			},
			check: func(t *testing.T, w *world, calls []any) {
				purse := uuid.Nil
				for _, a := range w.ledger.accounts {
					if a.Name == "Purse" {
						purse = a.ID
					}
				}
				used := false
				for _, c := range calls {
					if in, ok := c.(finance.RecordExpenseInput); ok && in.AccountID != nil && *in.AccountID == purse {
						used = true
					}
				}
				if purse == uuid.Nil || !used {
					t.Errorf("the new account %v is not what the expenses of Cash were recorded in", purse)
				}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := applied(t)
			total, calls := len(w.ledger.txns), len(w.ledger.calls)
			tt.change(&w.mapping.loaded.Mapping)

			out := w.apply()

			if sum(out.Updated) == 0 || sum(out.New) != 0 || len(out.Failed) != 0 || len(out.Stale) != 0 {
				t.Errorf("apply = new %v, updated %v, failed %d: the units that the change is of are updated, and no other", out.New, out.Updated, len(out.Failed))
			}
			if len(w.ledger.txns) != total || w.ledger.drafts() != total {
				t.Errorf("the ledger has %d transactions, %d drafts, want the %d it had: replaced, not added to", len(w.ledger.txns), w.ledger.drafts(), total)
			}
			tt.check(t, w, w.ledger.calls[calls:])
			if again := w.apply(); sum(again.Updated)+sum(again.New) != 0 {
				t.Errorf("a second apply changed %v and %v again", again.Updated, again.New)
			}
		})
	}
}

func TestAQuantityThatChangesAfterApplyReachesTheLedger(t *testing.T) {
	w := applied(t)
	// The purchase of a coin whose quantity the export has as zero is not recorded until
	// the user says how much it was.
	var key string
	for _, r := range w.plan().Rows {
		if r.Account == "Exchange BTC" && r.Type == model.TypeTransferIn && r.Amount.IsZero() {
			key = r.Key
		}
	}
	if key == "" || len(w.ledger.ofKind("trade")) != 1 {
		t.Fatalf("row %q, %d trades before", key, len(w.ledger.ofKind("trade")))
	}

	q := d("0.001")
	w.override(key, model.RowOverride{Quantity: &q})
	if out := w.apply(); out.New[model.KindTrade] != 1 || sum(out.Updated) != 0 {
		t.Errorf("apply = new %v, updated %v, want the purchase recorded", out.New, out.Updated)
	}

	q2 := d("0.002")
	w.override(key, model.RowOverride{Quantity: &q2})
	out := w.apply()
	if out.Updated[model.KindTrade] != 1 || sum(out.New) != 0 {
		t.Errorf("apply = new %v, updated %v, want the quantity to reach the ledger", out.New, out.Updated)
	}
	var quantities []string
	for _, c := range w.ledger.calls {
		if in, ok := c.(finance.RecordTradeInput); ok {
			quantities = append(quantities, in.Quantity.String())
		}
	}
	if want := []string{"0.002", "0.001", "0.002"}; !slices.Equal(quantities, want) {
		t.Errorf("trades recorded with the quantities %v, want %v: the first from the export, then the user's two", quantities, want)
	}
	if got := w.ledger.ofKind("trade"); len(got) != 2 {
		t.Errorf("trades in the ledger = %d, want the two of the export, the one that is the user's counted once", len(got))
	}
}

func TestAConfirmedTransactionThatIsNotWhatThePlanSaysIsReportedAndLeftAlone(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	key := w.rowKeyOf("Someone paid")
	confirmed := w.ledger.ofRow(key)[0].ID

	w.override(key, model.RowOverride{Treatment: model.TreatmentDealing, Party: "Amy"})
	out := w.apply()

	if len(out.Stale) != 1 || sum(out.New) != 0 || sum(out.Updated) != 0 || len(out.Failed) != 0 {
		t.Fatalf("apply = stale %d, new %v, updated %v, want the one unit reported and nothing recorded", len(out.Stale), out.New, out.Updated)
	}
	if out.Stale[0].Unit.Kind != model.KindDealing || len(out.Stale[0].Lines) != 1 {
		t.Errorf("stale = %+v", out.Stale[0])
	}
	got := w.ledger.ofRow(key)
	if len(got) != 1 || got[0].ID != confirmed || got[0].Status != "confirmed" || got[0].Kind != "income" {
		t.Errorf("the row is %v, want the confirmed income, untouched and not recorded again beside it", got)
	}
	if len(w.ledger.discards) != 0 {
		t.Errorf("discards = %v, want none: a confirmed transaction is not discarded", w.ledger.discards)
	}

	// confirm and verify say so until the user voids it.
	_, err := w.confirm("2026-01")
	var stale *usecase.StaleError
	if !errors.Is(err, usecase.ErrStale) || !errors.As(err, &stale) || len(stale.Units) != 1 {
		t.Errorf("confirm err = %v, want the stale unit to be refused for", err)
	}
	if _, err := w.confirm("2026-02"); !errors.Is(err, usecase.ErrStale) {
		t.Errorf("confirming a later month: err = %v, want it refused too", err)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || len(v.Stale) != 1 || v.Open != "2026-01" {
		t.Errorf("verify = %+v, %v, want the stale unit and January open", v, err)
	}
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || st.Months[0].Stale != 1 {
		t.Errorf("status = %+v, %v", st.Months[0], err)
	}

	// The user voids it, and the next apply records what the plan says.
	if n := w.ledger.void("moze:" + key); n != 1 {
		t.Fatalf("voided %d transactions", n)
	}
	out = w.apply()
	if out.Updated[model.KindDealing] != 1 || len(out.Stale) != 0 {
		t.Errorf("apply after the void = updated %v, stale %d, want the unit recorded again", out.Updated, len(out.Stale))
	}
	w.mustConfirm("2026-01")
	v, err = w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Stale) != 0 || len(v.Months) != 1 {
		t.Errorf("verify = %+v, %v, want January to agree with the export", v, err)
	}
	if got := w.ledger.ofRow(key); len(got) != 2 || got[1].Kind != "dealing" || got[1].Status != "confirmed" {
		t.Errorf("the row is %v, want the voided income and the confirmed dealing", got)
	}

	// A void that is the user's say about a version that is the plan's stays void.
	again := w.apply()
	if sum(again.New)+sum(again.Updated) != 0 {
		t.Errorf("a further apply = new %v, updated %v", again.New, again.Updated)
	}
}

func TestAnApplyThatWasCutShortIsTakenUpWhereItStopped(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	units := w.plan().Units
	victim := units[10]
	w.ledger.failOn = victim.Key()
	first := w.apply()
	if len(first.Failed) != 1 {
		t.Fatalf("failed = %d", len(first.Failed))
	}

	w.ledger.failOn = ""
	second := w.apply()
	if second.New[victim.Kind] != 1 || sum(second.New) != 1 || sum(second.Updated) != 0 || sum(second.Duplicate) != len(units)-1 {
		t.Errorf("second apply = new %v, updated %v, duplicates %d, want the one that failed recorded and the rest found", second.New, second.Updated, sum(second.Duplicate))
	}
	seen := map[string]int{}
	for _, tx := range w.ledger.txns {
		seen[tx.IdempotencyKey]++
	}
	for key, n := range seen {
		if n != 1 {
			t.Errorf("%s is in the ledger %d times", key, n)
		}
	}

	// A unit of two transactions that was cut between them: the expense is there, the
	// reward is recorded now, and nothing is recorded twice.
	w = newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.ledger.failOn = model.RewardKeySuffix
	cut := w.apply()
	if len(cut.Failed) != 1 || cut.Failed[0].Unit.Kind != model.KindExpenseReward {
		t.Fatalf("failed = %+v", cut.Failed)
	}
	w.ledger.failOn = ""
	rest := w.apply()
	if rest.New[model.KindExpenseReward] != 1 || sum(rest.Updated) != 0 || len(w.ledger.discards) != 0 {
		t.Errorf("after the cut: new %v, updated %v, discards %d, want the reward recorded beside the expense that was there", rest.New, rest.Updated, len(w.ledger.discards))
	}
}

// sameMinute is an export with two transfers of one minute that are alike but for what
// the rows say of themselves, which is how the rows come to be paired in the order of
// the file.
func sameMinute(outs, ins []string) []model.Row {
	rows := []model.Row{
		row("", model.TypeInitial, "Bank", "TWD", "1000"),
	}
	for _, n := range outs {
		rows = append(rows, out("2026-01-05 12:00", "Bank", "TWD", "-100", name(n)))
	}
	for _, n := range ins {
		rows = append(rows, in("2026-01-05 12:00", "Cash", "TWD", "100", name(n)))
	}
	return rows
}

func sameMinuteMapping() model.Mapping {
	return model.Mapping{Accounts: map[string]model.AccountMap{
		"Bank": {Name: "Bank", Kind: "bank"}, "Cash": {Name: "Cash", Kind: "cash"},
	}}
}

func TestTransfersOfOneMinuteAreNotRecordedTwiceWhenTheyArePairedOtherwiseLater(t *testing.T) {
	for _, tt := range []struct {
		name string
		// the export of the first apply, and the one the user makes next
		beforeOuts, beforeIns []string
		afterOuts, afterIns   []string
		// how apply finds the transfers of the next export: the drafts of the pairing
		// that is gone are discarded, so that those that are recorded now replace them
		updated, newUnits int
		transfers         int
	}{
		{"the rows of one side swap places", []string{"o1", "o2"}, []string{"i1", "i2"}, []string{"o1", "o2"}, []string{"i2", "i1"}, 2, 0, 2},
		{"a row is added and the pairing moves", []string{"o1"}, []string{"i1"}, []string{"o0", "o1"}, []string{"i1", "i0"}, 2, 0, 2},
	} {
		t.Run(tt.name+", as drafts", func(t *testing.T) {
			w := newWorld(t, sameMinute(tt.beforeOuts, tt.beforeIns), sameMinuteMapping()).reviewed()
			w.apply()
			if got := len(w.ledger.ofKind("transfer")); got != len(tt.beforeOuts) {
				t.Fatalf("transfers = %d before, want %d", got, len(tt.beforeOuts))
			}

			out := w.mustApply(sameMinute(tt.afterOuts, tt.afterIns)...)

			if got := len(w.ledger.ofKind("transfer")); got != tt.transfers {
				t.Errorf("transfers = %d, want %d: the rows are paired otherwise, and no pair is there twice", got, tt.transfers)
			}
			if out.Updated[model.KindTransfer] != tt.updated || out.New[model.KindTransfer] != tt.newUnits || len(out.Stale) != 0 || len(out.Failed) != 0 {
				t.Errorf("apply = new %v, updated %v, stale %d, want %d updated and %d new", out.New, out.Updated, len(out.Stale), tt.updated, tt.newUnits)
			}
			if w.ledger.drafts() != len(w.ledger.txns) {
				t.Errorf("a transaction that is not a draft: %d of %d are", w.ledger.drafts(), len(w.ledger.txns))
			}
		})
	}

	t.Run("confirmed", func(t *testing.T) {
		w := newWorld(t, sameMinute([]string{"o1", "o2"}, []string{"i1", "i2"}), sameMinuteMapping()).reviewed()
		w.apply()
		w.mustConfirm("2026-01")
		ids := map[uuid.UUID]bool{}
		for _, tx := range w.ledger.ofKind("transfer") {
			ids[tx.ID] = true
		}

		out := w.mustApply(sameMinute([]string{"o1", "o2"}, []string{"i2", "i1"})...)

		if len(out.Stale) != 2 || sum(out.New)+sum(out.Updated) != 0 {
			t.Errorf("apply = stale %d, new %v, updated %v, want the two new pairings reported and no transfer recorded beside the confirmed ones", len(out.Stale), out.New, out.Updated)
		}
		got := w.ledger.ofKind("transfer")
		if len(got) != 2 {
			t.Fatalf("transfers = %d, want the 2 that are confirmed", len(got))
		}
		for _, tx := range got {
			if !ids[tx.ID] || tx.Status != "confirmed" {
				t.Errorf("transfer %v is not one of the confirmed ones", tx.IdempotencyKey)
			}
		}
		if _, err := w.confirm("2026-01"); !errors.Is(err, usecase.ErrStale) {
			t.Errorf("confirm err = %v, want it to say that the transfers are not what the plan says", err)
		}
		v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
		if err != nil || len(v.Stale) != 2 {
			t.Errorf("verify = %+v, %v, want the two reported", v, err)
		}
	})
}

func TestAProjectNeverGetsAnActivePeriodAndNoTransactionIsPutInOneItsRowDoesNotName(t *testing.T) {
	const trip = "2026-03 Fake Trip"
	rows := []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("2026-03-01 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch"), project(trip)),
		row("2026-03-02 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch"), project(trip)),
		row("2026-03-02 12:00", model.TypeExpense, "Cash", "TWD", "-20", cat("Food", "Lunch")), // on a day of the trip, not the trip's
		row("2026-03-03 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch"), project(trip)),
	}
	w := newWorld(t, rows, model.Mapping{Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash", Kind: "cash"}}}).reviewed()
	if p := w.plan().Mapping.Projects[trip]; p.Kind != model.ProjectProject {
		t.Fatalf("the trip is a %q, want a project", p.Kind)
	}
	if got := w.plan().Projects; len(got) != 1 || got[0].From.IsZero() || got[0].Value != trip {
		t.Fatalf("project evidence = %+v, want the period of the trip as information", got)
	}

	w.apply()

	if len(w.ledger.projects) != 1 || w.ledger.projects[0].ActivePeriod != nil {
		t.Errorf("projects = %+v, want one with no active period", w.ledger.projects)
	}
	named, unnamed := 0, 0
	for _, c := range w.ledger.calls {
		in, ok := c.(finance.RecordExpenseInput)
		if !ok {
			continue
		}
		if in.ProjectID != nil {
			named++
		} else {
			unnamed++
		}
	}
	if named != 3 || unnamed != 1 {
		t.Errorf("expenses with a project = %d, without = %d, want the three of the trip and the one that is not", named, unnamed)
	}
}

func TestAProjectTheLedgerHasIsUsedAsItIsAndADifferentDefaultAreaIsReported(t *testing.T) {
	rows := []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("2026-03-01 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch"), project("Trip")),
	}
	w := newWorld(t, rows, model.Mapping{
		Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash", Kind: "cash"}},
		Projects: map[string]model.ProjectMap{
			"Trip": {Kind: model.ProjectProject, Name: "Trip", DefaultArea: "Fun"},
			"Fun":  {Kind: model.ProjectArea, Name: "Fun"},
		},
	}).reviewed()
	other := catalog.Area{ID: w.ledger.id(), Name: "Other"}
	w.ledger.areas = append(w.ledger.areas, other)
	w.ledger.projects = append(w.ledger.projects, catalog.Project{ID: w.ledger.id(), Name: "Trip", DefaultAreaID: &other.ID})

	out := w.apply()

	if out.Created[usecase.CreatedProjects] != 0 || len(w.ledger.projects) != 1 {
		t.Errorf("created %v, projects %d: the project the ledger has is used", out.Created, len(w.ledger.projects))
	}
	want := []usecase.ProjectDifference{{Project: "Trip", Mapping: "Fun", Ledger: "Other"}}
	if len(out.ProjectDifferences) != 1 || out.ProjectDifferences[0] != want[0] {
		t.Errorf("differences = %+v, want %+v", out.ProjectDifferences, want)
	}
	if again := w.apply(); len(again.ProjectDifferences) != 1 {
		t.Errorf("the difference is not said again: %+v", again.ProjectDifferences)
	}
}

func TestApplyRefusesWhileTheMappingHasGuessesTheUserHasNotLookedAt(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping()).reviewed()
	w.mapping.loaded.Guessed = 3

	_, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files})

	var blocked *usecase.BlockedError
	if !errors.As(err, &blocked) || blocked.Guessed != 3 || len(blocked.Missing()) != 0 || len(blocked.Plan.Problems) != 0 {
		t.Fatalf("err = %v, want the refusal to be for the 3 guesses alone", err)
	}
	if len(w.ledger.txns) != 0 || len(w.ledger.created) != 0 {
		t.Errorf("apply wrote %d transactions and made %v before refusing", len(w.ledger.txns), w.ledger.created)
	}

	// A plan can be made with them, and so can the other commands.
	if _, err := w.planner.Plan(context.Background(), usecase.PlanInput{Files: w.files}); err != nil {
		t.Errorf("Plan with guesses: %v", err)
	}
	if _, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files}); err != nil {
		t.Errorf("Status with guesses: %v", err)
	}

	w.mapping.loaded.Guessed = 0
	if _, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files}); err != nil {
		t.Errorf("apply once the marks are taken away: %v", err)
	}
}

func TestTheRefusalListsWhatTheMappingLacksTheDefaultsToo(t *testing.T) {
	w := newWorld(t, fixture(), fixtureMapping())
	m := fixtureMapping()
	m.Defaults = model.Defaults{FeeCategory: "Fees", InterestCategory: "Interest"} // the refund category is missing
	w.mapping.loaded.Mapping = m
	w.reviewed()
	w.mapping.loaded.Mapping.Defaults.RefundCategory = ""

	_, err := w.imp.Apply(context.Background(), usecase.ApplyInput{Files: w.files})

	var blocked *usecase.BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want the plan to be refused", err)
	}
	var got []string
	for _, e := range blocked.Missing() {
		got = append(got, e.String())
	}
	if want := []string{"defaults.refund_category"}; !slices.Equal(got, want) {
		t.Errorf("missing = %v, want %v: a default that is missing is told too", got, want)
	}
}

func TestAMonthWhoseRowsAllComeToNothingIsAMonthToConfirm(t *testing.T) {
	rows := []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("2026-01-05 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch")),
		// February has one expense that a discount paid in full: it does nothing to a balance.
		row("2026-02-03 09:00", model.TypeExpense, "Cash", "TWD", "-100", cat("Food", "Lunch"), discount("100")),
	}
	w := newWorld(t, rows, model.Mapping{Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash", Kind: "cash"}}}).reviewed()
	w.apply()

	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || len(st.Months) != 2 || st.Months[1].Month != "2026-02" || st.Months[1].Drafts != 1 {
		t.Fatalf("status = %+v, %v, want February to be there with its draft", st.Months, err)
	}
	w.mustConfirm("2026-01")
	out := w.mustConfirm("2026-02")
	if out.Confirmed != 2 {
		t.Errorf("confirmed %d, want the expense and its reward", out.Confirmed)
	}
	if v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files}); err != nil || len(v.Months) != 2 || v.Unexplained != 0 {
		t.Errorf("verify = %+v, %v", v, err)
	}
}

func TestABackDatedRowMovesTheDayOfTheOpeningBalanceAndTheLedgerFollows(t *testing.T) {
	first := []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("2026-02-10 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch")),
	}
	w := newWorld(t, first, model.Mapping{Accounts: map[string]model.AccountMap{"Cash": {Name: "Cash", Kind: "cash"}}}).reviewed()
	w.apply()
	if got := w.ledger.ofKind("opening"); len(got) != 1 || !got[0].OccurredOn.Equal(day("2026-02-09")) {
		t.Fatalf("opening = %v, want it the day before the first row", got)
	}

	// A longer export has an older row: the opening balance is the day before it, and the
	// key of the row of the opening is the same.
	out := w.mustApply(append(first, row("2026-01-20 09:00", model.TypeExpense, "Cash", "TWD", "-5", cat("Food", "Lunch")))...)
	if out.Updated[model.KindOpening] != 1 || sum(out.New) != 1 {
		t.Errorf("apply = new %v, updated %v, want the opening moved and the new row recorded", out.New, out.Updated)
	}
	if got := w.ledger.ofKind("opening"); len(got) != 1 || !got[0].OccurredOn.Equal(day("2026-01-19")) {
		t.Errorf("opening = %v, want it to follow to 19 January: the ledger does not keep the day it had", got)
	}
}

func TestTheMappingCanKeepTheDayOfAnOpeningBalanceWhereItWas(t *testing.T) {
	first := []model.Row{
		row("", model.TypeInitial, "Cash", "TWD", "1000"),
		row("2026-02-10 09:00", model.TypeExpense, "Cash", "TWD", "-10", cat("Food", "Lunch")),
	}
	w := newWorld(t, first, model.Mapping{Accounts: map[string]model.AccountMap{
		"Cash": {Name: "Cash", Kind: "cash", OpeningDate: day("2026-02-09")},
	}}).reviewed()
	w.apply()

	out := w.mustApply(append(first, row("2026-01-20 09:00", model.TypeExpense, "Cash", "TWD", "-5", cat("Food", "Lunch")))...)
	if sum(out.Updated) != 0 || out.New[model.KindExpense] != 1 {
		t.Errorf("apply = new %v, updated %v, want the new row only: the opening is where the mapping says", out.New, out.Updated)
	}
	if got := w.ledger.ofKind("opening"); len(got) != 1 || !got[0].OccurredOn.Equal(day("2026-02-09")) {
		t.Errorf("opening = %v, want it on 9 February, where the mapping keeps it", got)
	}
}

// withFees is rows that have a fee on every type that the ledger takes one on, and a
// discount where it takes one: the plan has nothing to say of them.
func withFees() []model.Row {
	return []model.Row{
		row("2026-04-20 10:00", model.TypeRefund, "Card", "TWD", "100", cat("Shop", "Books"), fee("-3"), name("Refund with a fee")),
		row("2026-04-20 11:00", model.TypeFee, "Bank", "TWD", "-30", cat("手續費", "手續費"), fee("-2")),
		row("2026-04-20 12:00", model.TypeBonus, "Bank USD", "USD", "1.5", cat("紅利回饋", "紅利回饋"), fee("-0.1")),
		row("2026-04-20 13:00", model.TypeIncome, "Bank", "TWD", "500", cat("收入", "薪水"), fee("-4")),
		row("2026-04-20 14:00", model.TypeReceivable, "Cash", "TWD", "-50", party("Amy"), cat("應收款項", "借出"), fee("-1")),
		row("2026-04-20 15:00", model.TypeExpense, "Card", "TWD", "-80", cat("Food", "Lunch"), discount("20"), fee("-2")),
		out("2026-04-21 12:00", "Bank", "TWD", "-200", fee("-5")),
		in("2026-04-21 12:00", "Card", "TWD", "200", fee("-1")),
		out("2026-04-22 12:00", "Bank", "TWD", "-3000", cat("轉帳", "兌換"), fee("-7")),
		in("2026-04-22 12:00", "Bank USD", "USD", "100", cat("轉帳", "兌換"), fee("-0.2")),
	}
}

func TestAPlanThatHasNoDifferenceIsOneWhoseMonthsConfirmWithNothingUnexplained(t *testing.T) {
	w := newWorld(t, fixtureWith(withFees()...), fixtureMapping()).reviewed()
	plan := w.plan()
	if len(plan.Differences) != 0 {
		t.Fatalf("differences = %+v, want none: the fees and the discount all have a place", plan.Differences)
	}
	if out := w.apply(); len(out.Failed) != 0 {
		t.Fatalf("apply failed: %+v", out.Failed)
	}
	for _, m := range plan.Months() {
		if out := w.mustConfirm(m); out.Check.Unexplained() != 0 {
			t.Errorf("%s: %d differences nothing explains: %+v", m, out.Check.Unexplained(), out.Check.Differences())
		}
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || v.Open != "" {
		t.Errorf("verify = %d unexplained, open %q, %v", v.Unexplained, v.Open, err)
	}
}

func TestWhatTheLedgerHasNoPlaceForIsAnExplainedDifference(t *testing.T) {
	// A discount on a 應收款項: only an expense has a discount, so the account is 3 below
	// what the export says, which the plan says in advance.
	extra := row("2026-04-20 14:00", model.TypeReceivable, "Cash", "TWD", "-50", party("Amy"), cat("應收款項", "借出"), discount("3"), name("Lent with a discount"))
	w := newWorld(t, fixtureWith(extra), fixtureMapping()).reviewed()
	plan := w.plan()
	if len(plan.Differences) != 1 || plan.Differences[0].Reasons[0] != model.DifferenceDiscount {
		t.Fatalf("differences = %+v, want the one discount that the ledger has no place for", plan.Differences)
	}
	w.apply()
	var last usecase.ConfirmOutput
	for _, m := range plan.Months() {
		last = w.mustConfirm(m)
		if last.Check.Unexplained() != 0 {
			t.Errorf("%s: %d differences nothing explains", m, last.Check.Unexplained())
		}
	}
	// Cash lacks the 77 of the 轉出 that has no 轉入, which the fixture has, and the 3
	// of the discount, which the export says it was paid.
	explained := false
	for _, l := range last.Check.Differences() {
		if l.Account == "Cash" && l.Unrecorded.Equal(d("-74")) && l.Unexplained().IsZero() {
			explained = true
		}
	}
	if !explained {
		t.Errorf("differences = %+v, want Cash to be told as 74 below what the export says, and explained", last.Check.Differences())
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 {
		t.Errorf("verify = %d unexplained, %v", v.Unexplained, err)
	}
}
