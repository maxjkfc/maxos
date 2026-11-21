package moze_test

import (
	"context"
	"strings"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// A void keeps its idempotency key, and the ledger answers a key it has seen with what
// it stores under it. A version that was voided and is wanted again (the plan went back
// to what it said before) is therefore recorded under a key of its own, the next
// attempt: but only when the void was what made room for another version, which is gone
// too. A void that nothing came after is the user giving the unit up, and stays.

// without takes the override of the row away: the plan says what it said before.
func (w *world) without(rowKey string) {
	w.t.Helper()
	delete(w.mapping.loaded.Mapping.Rows, rowKey)
}

// keysOf are the keys the ledger has for the row, in the order they were made, with
// their status.
func (w *world) keysOf(rowKey string) []string {
	var out []string
	for _, x := range w.ledger.ofRow(rowKey) {
		out = append(out, strings.TrimPrefix(x.IdempotencyKey, model.KeyPrefix+rowKey)+" "+x.Status)
	}
	return out
}

// theWayBack is the plan going to another version of the 收款 and back, the user voiding
// the confirmed version each time apply says to. It stops after the void of the second
// version, with the plan as it was at first.
func theWayBack(t *testing.T) (w *world, key string) {
	t.Helper()
	w = applied(t)
	w.mustConfirm("2026-01")
	key = w.rowKeyOf("Someone paid")
	w.override(key, model.RowOverride{Treatment: model.TreatmentDealing, Party: "Amy"})
	if out := w.apply(); len(out.Stale) != 1 {
		t.Fatalf("apply with the override = %d stale", len(out.Stale))
	}
	if n := w.ledger.void("moze:" + key); n != 1 {
		t.Fatalf("voided %d transactions", n)
	}
	if out := w.apply(); out.Updated[model.KindDealing] != 1 {
		t.Fatalf("apply after the first void = updated %v, want the new version recorded", out.Updated)
	}
	w.mustConfirm("2026-01")
	w.without(key)
	if out := w.apply(); len(out.Stale) != 1 || len(out.Revived) != 0 {
		t.Fatalf("apply with the override taken away = stale %d, want the confirmed second version told", len(out.Stale))
	}
	if n := w.ledger.void("moze:" + key); n != 1 {
		t.Fatalf("voided %d transactions", n)
	}
	return w, key
}

func TestAVersionThatWasVoidedToMakeRoomIsRecordedAgainWhenThePlanGoesBackToIt(t *testing.T) {
	w, key := theWayBack(t)
	if got := w.keysOf(key); len(got) != 2 || !strings.HasSuffix(got[0], " void") || !strings.HasSuffix(got[1], " void") {
		t.Fatalf("keys = %v, want both versions void", got)
	}
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || st.Months[0].NotApplied != 1 || st.Months[0].Voided != 0 {
		t.Errorf("status = %+v, %v, want the unit to be one that apply records", st.Months[0], err)
	}

	out := w.apply()
	if out.Revived[model.KindIncome] != 1 || sum(out.Updated) != 0 || sum(out.New) != 0 || len(out.Voided) != 0 || len(out.Stale) != 0 || len(out.Failed) != 0 {
		t.Fatalf("apply = revived %v, updated %v, new %v, voided %d, stale %d, want the one unit revived", out.Revived, out.Updated, out.New, len(out.Voided), len(out.Stale))
	}
	got := w.keysOf(key)
	if len(got) != 3 || !strings.Contains(got[2], "~2 draft") {
		t.Errorf("keys = %v, want the version recorded again under the key of a second attempt", got)
	}
	if rows := w.ledger.ofRow(key); rows[2].Kind != "income" {
		t.Errorf("the transaction recorded again is a %s, want an income: what the plan says", rows[2].Kind)
	}

	// It is the unit of the plan now: confirmed, it is what the export says, and a
	// further apply has nothing to do.
	w.mustConfirm("2026-01")
	again := w.apply()
	if sum(again.New)+sum(again.Updated)+sum(again.Revived) != 0 || len(again.Voided) != 0 || len(again.Stale) != 0 {
		t.Errorf("a further apply = %+v, want nothing to do", again)
	}
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Stale) != 0 || len(v.Months) != 1 {
		t.Errorf("verify = %+v, %v, want January to agree with the export", v, err)
	}
	if n := len(w.ledger.ofRow(key)); n != 3 {
		t.Errorf("the ledger has %d transactions of the row, want 3: nothing recorded beside the new one", n)
	}
}

func TestAVersionVoidedThenTheDraftOfAnotherDiscardedWhenThePlanGoesBack(t *testing.T) {
	// void the first version, apply records the second, the plan goes back: the first is
	// void and the second is a draft. Apply must not leave the unit nowhere: it records
	// the first again, which is what replaces the draft of the second.
	w := applied(t)
	w.mustConfirm("2026-01")
	key := w.rowKeyOf("Someone paid")
	w.override(key, model.RowOverride{Treatment: model.TreatmentDealing, Party: "Amy"})
	w.apply()
	w.ledger.void("moze:" + key)
	if out := w.apply(); out.Updated[model.KindDealing] != 1 {
		t.Fatalf("apply after the void = %+v", out)
	}
	second := w.ledger.ofRow(key)[1]
	if second.Status != "draft" {
		t.Fatalf("the second version is %s, want a draft", second.Status)
	}
	w.without(key)

	out := w.apply()
	if out.Revived[model.KindIncome] != 1 || sum(out.Updated) != 0 || len(out.Failed) != 0 {
		t.Fatalf("apply = revived %v, updated %v, want the first version recorded again", out.Revived, out.Updated)
	}
	if w.ledger.byID(second.ID) != nil {
		t.Error("the draft of the second version is still in the ledger: the version recorded again replaces it")
	}
	got := w.keysOf(key)
	if len(got) != 2 || !strings.HasSuffix(got[0], " void") || !strings.Contains(got[1], "~2 draft") {
		t.Errorf("keys = %v, want the void first version and the draft of its second attempt", got)
	}
}

func TestAVoidNothingCameAfterIsLeftAndSaid(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	key := w.rowKeyOf("Someone paid")
	before := len(w.ledger.txns)
	if n := w.ledger.void("moze:" + key); n != 1 {
		t.Fatalf("voided %d transactions", n)
	}

	out := w.apply()
	if len(out.Voided) != 1 || out.Voided[0].Unit.Kind != model.KindIncome || len(out.Voided[0].Lines) != 1 {
		t.Fatalf("voided = %+v, want the one unit told", out.Voided)
	}
	if sum(out.New)+sum(out.Updated)+sum(out.Revived) != 0 || len(out.Stale) != 0 {
		t.Errorf("apply = new %v, updated %v, revived %v, want nothing recorded", out.New, out.Updated, out.Revived)
	}
	// It is not one of the units the ledger already had: it is not there, and it is said.
	if want := len(w.plan().Units) - 1; sum(out.Duplicate) != want {
		t.Errorf("duplicates = %d, want %d: the voided unit is not counted as one", sum(out.Duplicate), want)
	}
	if len(w.ledger.txns) != before {
		t.Errorf("the ledger has %d transactions, want %d", len(w.ledger.txns), before)
	}
	st, err := w.imp.Status(context.Background(), usecase.StatusInput{Files: w.files})
	if err != nil || st.Months[0].Voided != 1 || st.Months[0].NotApplied != 0 {
		t.Errorf("status = %+v, %v, want the unit counted as voided", st.Months[0], err)
	}
	// The month it is in does not wait for it.
	if _, err := w.confirm("2026-01"); err != nil {
		t.Errorf("confirm = %v, want a voided unit not to stand in the way", err)
	}
}

func TestARewardedExpenseVoidedToMakeRoomIsRecordedAgainWithItsRewardAndResumesWhereItStopped(t *testing.T) {
	w := applied(t)
	w.mustConfirm("2026-01")
	key := w.rowKeyOf("Coffee") // paid in full by a discount: an expense and a reward
	if n := len(w.ledger.ofRow(key)); n != 2 {
		t.Fatalf("the row is %d transactions, want the expense and the reward", n)
	}
	otherPlace := model.RowOverride{Treatment: model.TreatmentExpense, Category: "Drinks/Coffee"}

	w.override(key, otherPlace)
	if out := w.apply(); len(out.Stale) != 1 {
		t.Fatalf("apply with the override = %d stale", len(out.Stale))
	}
	if n := w.ledger.void("moze:" + key); n != 2 {
		t.Fatalf("voided %d transactions, want both", n)
	}
	w.apply()
	w.mustConfirm("2026-01")
	w.without(key)
	if out := w.apply(); len(out.Stale) != 1 {
		t.Fatalf("apply with the override taken away = %d stale", len(out.Stale))
	}
	if n := w.ledger.void("moze:" + key); n != 2 {
		t.Fatalf("voided %d transactions, want both", n)
	}

	// The ledger refuses the reward on the first try: the unit is recorded in part, under
	// the second attempt, and the next apply takes it up there, not under a third.
	w.ledger.failOn = model.RewardKeySuffix
	out := w.apply()
	if len(out.Failed) != 1 {
		t.Fatalf("apply = failed %d, want the reward refused", len(out.Failed))
	}
	w.ledger.failOn = ""
	out = w.apply()
	if len(out.Failed) != 0 || sum(out.Duplicate) == 0 {
		t.Fatalf("apply = %+v", out)
	}
	var attempts []string
	for _, x := range w.ledger.ofRow(key) {
		if x.Status == "draft" {
			attempts = append(attempts, strings.TrimPrefix(x.IdempotencyKey, model.KeyPrefix+key))
		}
	}
	if len(attempts) != 2 || !strings.Contains(attempts[0], "~2") || !strings.Contains(attempts[1], "~2") || strings.Contains(strings.Join(attempts, " "), "~3") {
		t.Errorf("drafts of the row = %v, want the expense and its reward of the second attempt, and no third", attempts)
	}
	w.mustConfirm("2026-01")
	v, err := w.imp.Verify(context.Background(), usecase.VerifyInput{Files: w.files})
	if err != nil || v.Unexplained != 0 || len(v.Stale) != 0 {
		t.Errorf("verify = %+v, %v", v, err)
	}
}
