package moze_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// What the mapping file says that is odd (a key that is no longer used) is said by every
// command that reads the mapping, first: the plan, and each command that goes on to the
// ledger, whether it does what it was asked or refuses.
func TestWhatTheMappingFileWarnsOfIsSaidByEveryCommand(t *testing.T) {
	warning := model.Warning{Code: model.WarningActivePeriodIgnored, Subject: "projects.Trip", Args: []string{"7"}}
	said := func(t *testing.T, command string, got []model.Warning) {
		t.Helper()
		if len(got) == 0 || !reflect.DeepEqual(got[0], warning) {
			t.Errorf("%s says %+v, want the warning of the mapping file first", command, got)
		}
	}
	ctx := context.Background()

	w := applied(t)
	w.mapping.loaded.Warnings = []model.Warning{warning}
	said(t, "plan", w.plan().Warnings)

	apply, err := w.imp.Apply(ctx, usecase.ApplyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	said(t, "apply", apply.Warnings)
	status, err := w.imp.Status(ctx, usecase.StatusInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	said(t, "status", status.Warnings)
	verify, err := w.imp.Verify(ctx, usecase.VerifyInput{Files: w.files})
	if err != nil {
		t.Fatal(err)
	}
	said(t, "verify", verify.Warnings)
	confirm, err := w.confirm("2026-01")
	if err != nil {
		t.Fatal(err)
	}
	said(t, "confirm", confirm.Warnings)

	// A refusal says it as well: a month that comes after one that is open.
	refused, err := w.confirm("2026-03")
	if !errors.Is(err, usecase.ErrMonthOrder) {
		t.Fatalf("confirm err = %v", err)
	}
	said(t, "a refused confirm", refused.Warnings)

	// And so does a mapping with guesses nobody has looked at, which apply refuses.
	w.mapping.loaded.Guessed = 2
	blocked, err := w.imp.Apply(ctx, usecase.ApplyInput{Files: w.files})
	var refusal *usecase.BlockedError
	if !errors.As(err, &refusal) {
		t.Fatalf("apply err = %v, want it refused for the guesses", err)
	}
	said(t, "a refused apply", blocked.Warnings)
}
