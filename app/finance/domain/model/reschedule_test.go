package model_test

import (
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// confirmed returns the plan as it is once confirmed: its origin and each period.
func confirmed(t *testing.T, plan model.InstalmentPlan) model.InstalmentPlan {
	t.Helper()
	out := model.InstalmentPlan{Periods: make([]model.Transaction, len(plan.Periods))}
	var err error
	if out.Origin, err = plan.Origin.Confirm(); err != nil {
		t.Fatalf("Confirm the origin: %v", err)
	}
	for i, period := range plan.Periods {
		if out.Periods[i], err = period.Confirm(); err != nil {
			t.Fatalf("Confirm period %d: %v", i+1, err)
		}
	}
	return out
}

// A plan of 12,000 on the card in 12 periods of 1,000, with 30 of interest with the
// first, of a purchase with all that a purchase carries, which the new periods are
// to carry too.
func rescheduleFixture(t *testing.T) (instalmentFixtures, model.InstalmentPlan) {
	t.Helper()
	f := newInstalmentFixtures(t)
	project, area, tag := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	params := f.expenseParams("laptop-00000001", "12000")
	params.Description, params.Merchant = "筆電", "Apple"
	params.ProjectID, params.AreaID, params.TagIDs = &project, &area, []uuid.UUID{tag}
	periods := monthly(12, "1000")
	periods[0].Interest = decPtr("30")

	plan, err := model.NewInstalmentExpense(params, f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	return f, plan
}

func TestRescheduleMakesTheNewPeriodsAsARecordedPlanHasThemAndLeavesTheOriginAlone(t *testing.T) {
	f, plan := rescheduleFixture(t)
	originBefore, periodsBefore := plan.Origin, slices.Clone(plan.Periods)

	// Four periods of 3,000, the first on the day of the purchase and the third with
	// 90 of interest.
	next := monthly(4, "3000")
	next[0].DueOn = on(2026, time.November, 14)
	next[2].Interest = decPtr("90")
	got, err := plan.Reschedule(f.twd, f.twd, f.interestSchedule(next))
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	if !reflect.DeepEqual(got.Origin, originBefore) {
		t.Errorf("origin = %+v, want it as it was: a plan is rescheduled, not made again", got.Origin)
	}
	if len(got.Periods) != 4 {
		t.Fatalf("%d periods, want the 4 of the new schedule", len(got.Periods))
	}
	for i := range got.Periods {
		wantPeriod(t, got, i, next[i])
	}
	wantPostings(t, got.Periods[0], f.names, "instalment(卡) +3000 TWD", "account(卡) -3000 TWD")
	wantPostings(t, got.Periods[2], f.names, "instalment(卡) +3000 TWD", "account(卡) -3090 TWD", "expense(利息) +90 TWD, base +90")
	for i, period := range got.Periods {
		if period.Description != originBefore.Description || period.Merchant != originBefore.Merchant ||
			!reflect.DeepEqual(period.ProjectID, originBefore.ProjectID) || !reflect.DeepEqual(period.AreaID, originBefore.AreaID) ||
			!slices.Equal(period.TagIDs, originBefore.TagIDs) || period.OccurredAt != nil {
			t.Errorf("period %d = %+v, want the texts, project, area and tags of the origin, and no moment", i+1, period)
		}
	}
	// What it was called on is as it was.
	if !reflect.DeepEqual(plan.Origin, originBefore) || !reflect.DeepEqual(plan.Periods, periodsBefore) {
		t.Error("Reschedule changed the plan it was called on")
	}
}

func TestRescheduleOfAConfirmedPlanMakesTheSamePeriodsAsDraftsForTheCallerToConfirm(t *testing.T) {
	f, plan := rescheduleFixture(t)
	plan = confirmed(t, plan)

	got, err := plan.Reschedule(f.twd, f.twd, f.interestSchedule(monthly(3, "4000")))
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	if got.Origin.Status != model.StatusConfirmed {
		t.Errorf("origin is %s, want it as it was, confirmed", got.Origin.Status)
	}
	for i, period := range got.Periods {
		if period.Status != model.StatusDraft || period.ConfirmedAt != nil {
			t.Errorf("period %d is %s, confirmed at %v, want a draft: giving it the status of the plan is the caller's", i+1, period.Status, period.ConfirmedAt)
		}
	}
}

func TestRescheduleOfABillInstalmentSplitsItsPrincipalAgain(t *testing.T) {
	f := newInstalmentFixtures(t)
	plan, err := model.NewBillInstalment(f.billParams("bill-0000000001", "3000", monthly(3, "1000")))
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}

	// The origin holds the principal twice, as the account and as the instalment:
	// the instalment is what is split.
	next := monthly(2, "1500")
	next[1].DueOn = on(2027, time.June, 5)
	got, err := plan.Reschedule(f.twd, f.twd, f.interestSchedule(next))
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	if len(got.Periods) != 2 {
		t.Fatalf("%d periods, want 2", len(got.Periods))
	}
	for i := range got.Periods {
		wantPeriod(t, got, i, next[i])
	}
	wantPostings(t, got.Periods[1], f.names, "instalment(卡) +1500 TWD", "account(卡) -1500 TWD")
}

func TestRescheduleMayStartTheFirstPeriodOnTheDayOfTheOriginAndMoveEveryPeriodEarlier(t *testing.T) {
	f, plan := rescheduleFixture(t)
	next := monthly(12, "1000")
	for i := range next {
		next[i].DueOn = on(2026, time.November, 14).AddDate(0, 0, i)
	}

	got, err := plan.Reschedule(f.twd, f.twd, f.interestSchedule(next))
	if err != nil {
		t.Fatalf("Reschedule: %v", err)
	}

	if !got.Periods[0].OccurredOn.Equal(on(2026, time.November, 14)) || !got.Periods[11].OccurredOn.Equal(on(2026, time.November, 25)) {
		t.Errorf("periods fall on %s to %s, want 2026-11-14 to 2026-11-25",
			got.Periods[0].OccurredOn.Format(time.DateOnly), got.Periods[11].OccurredOn.Format(time.DateOnly))
	}
}

func TestRescheduleRefusesWhatCannotBeAScheduleOfThePlanAndMakesNothing(t *testing.T) {
	f, plan := rescheduleFixture(t)
	system := unrecordedCategory(model.DirectionExpense)
	withInterest := func(periods []model.InstalmentPeriod, interest string) []model.InstalmentPeriod {
		periods[0].Interest = decPtr(interest)
		return periods
	}

	tests := []struct {
		name     string
		schedule func() model.InstalmentSchedule
		want     error
	}{
		{"no periods", func() model.InstalmentSchedule { return f.interestSchedule(nil) }, model.ErrNoPeriods},
		{"principals that add up to less", func() model.InstalmentSchedule { return f.interestSchedule(monthly(4, "2999")) }, model.ErrPrincipalMismatch},
		{"principals that add up to more", func() model.InstalmentSchedule { return f.interestSchedule(monthly(4, "3000.01")) }, model.ErrPrincipalMismatch},
		{"periods that fall due on the same day", func() model.InstalmentSchedule {
			periods := monthly(4, "3000")
			periods[2].DueOn = periods[1].DueOn.Add(10 * time.Hour)
			return f.interestSchedule(periods)
		}, model.ErrDueDatesNotIncreasing},
		{"periods that fall due the wrong way round", func() model.InstalmentSchedule {
			periods := monthly(4, "3000")
			periods[0], periods[1] = periods[1], periods[0]
			return f.interestSchedule(periods)
		}, model.ErrDueDatesNotIncreasing},
		{"a first period before the origin", func() model.InstalmentSchedule {
			periods := monthly(4, "3000")
			periods[0].DueOn = on(2026, time.November, 13)
			return f.interestSchedule(periods)
		}, model.ErrFirstDueTooEarly},
		{"a period without a day", func() model.InstalmentSchedule {
			periods := monthly(4, "3000")
			periods[1].DueOn = time.Time{}
			return f.interestSchedule(periods)
		}, model.ErrDateRequired},
		{"a period of nothing", func() model.InstalmentSchedule { return f.interestSchedule(monthly(4, "0")) }, model.ErrAmountNotPositive},
		{"a negative principal", func() model.InstalmentSchedule {
			periods := monthly(4, "4000")
			periods[1].Principal = dec("-4000")
			return f.interestSchedule(periods)
		}, model.ErrAmountNotPositive},
		{"a principal finer than the currency", func() model.InstalmentSchedule { return f.interestSchedule(monthly(4, "3000.001")) }, model.ErrTooManyDecimals},
		{"interest of nothing", func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(4, "3000"), "0")) }, model.ErrAmountNotPositive},
		{"interest finer than the currency", func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(4, "3000"), "1.234")) }, model.ErrTooManyDecimals},
		{"the interest in the base currency of a period with no interest", func() model.InstalmentSchedule {
			periods := monthly(4, "3000")
			periods[1].InterestBaseAmount = decPtr("30")
			return f.interestSchedule(periods)
		}, model.ErrInterestBaseAmountWithoutInterest},
		{"interest without a category", func() model.InstalmentSchedule {
			return model.InstalmentSchedule{Periods: withInterest(monthly(4, "3000"), "30")}
		}, model.ErrInterestCategoryRequired},
		{"an interest category of incomes", func() model.InstalmentSchedule {
			return model.InstalmentSchedule{Periods: withInterest(monthly(4, "3000"), "30"), InterestCategory: &f.salary}
		}, model.ErrWrongCategoryDirection},
		{"a system category for the interest", func() model.InstalmentSchedule {
			return model.InstalmentSchedule{Periods: withInterest(monthly(4, "3000"), "30"), InterestCategory: &system}
		}, model.ErrSystemCategory},
		{"an archived interest category", func() model.InstalmentSchedule {
			archived := archivedCategory(f.interest)
			return model.InstalmentSchedule{Periods: withInterest(monthly(4, "3000"), "30"), InterestCategory: &archived}
		}, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.Reschedule(f.twd, f.twd, tt.schedule())

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if got.Origin.ID != uuid.Nil || got.Periods != nil {
				t.Errorf("plan = %+v, want nothing of a schedule that is refused", got)
			}
		})
	}
}

// The mismatch carries both amounts, as the schedule of a new plan's does.
func TestRescheduleToAnotherAmountReturnsAMismatchThatCarriesBothAmounts(t *testing.T) {
	f, plan := rescheduleFixture(t)

	_, err := plan.Reschedule(f.twd, f.twd, f.interestSchedule(monthly(4, "2999.5")))

	var mismatch *model.PrincipalMismatchError
	if !errors.As(err, &mismatch) || !errors.Is(err, model.ErrPrincipalMismatch) {
		t.Fatalf("err = %v, want a *PrincipalMismatchError that is an ErrPrincipalMismatch", err)
	}
	if !mismatch.Principals.Equal(dec("11998")) || !mismatch.Amount.Equal(dec("12000")) || mismatch.InstrumentCode != "TWD" {
		t.Errorf("mismatch = %+v, want the 11998 the schedule adds up to, the 12000 the plan splits, in TWD", mismatch)
	}
}

func TestRescheduleRefusesAPlanThatIsVoidAndOneThatIsNotWhole(t *testing.T) {
	f, plan := rescheduleFixture(t)
	schedule := func() model.InstalmentSchedule { return f.interestSchedule(monthly(4, "3000")) }

	t.Run("a plan that is void", func(t *testing.T) {
		void := confirmed(t, plan)
		var err error
		if void.Origin, err = void.Origin.Void(); err != nil {
			t.Fatalf("Void the origin: %v", err)
		}
		for i := range void.Periods {
			if void.Periods[i], err = void.Periods[i].Void(); err != nil {
				t.Fatalf("Void period %d: %v", i+1, err)
			}
		}

		if _, err := void.Reschedule(f.twd, f.twd, schedule()); !errors.Is(err, model.ErrPlanVoid) {
			t.Fatalf("err = %v, want ErrPlanVoid", err)
		}
	})
	t.Run("a period that is of another status than the origin", func(t *testing.T) {
		mixed := confirmed(t, plan)
		mixed.Periods[3] = plan.Periods[3]

		if _, err := mixed.Reschedule(f.twd, f.twd, schedule()); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
	t.Run("another instrument than the plan is counted in", func(t *testing.T) {
		if _, err := plan.Reschedule(f.usd, f.twd, schedule()); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
	t.Run("an origin that has no instalment posting", func(t *testing.T) {
		lunch, err := model.NewExpense(f.expenseParams("lunch-0000000001", "120"))
		if err != nil {
			t.Fatalf("NewExpense: %v", err)
		}

		if _, err := (model.InstalmentPlan{Origin: lunch}).Reschedule(f.twd, f.twd, schedule()); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
}
