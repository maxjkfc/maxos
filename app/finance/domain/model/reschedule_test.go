package model_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
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

// rescheduleAtPurchase is a reschedule on the day of the purchase, when nothing of
// the plan has fallen due: its past is empty.
func rescheduleAtPurchase(f instalmentFixtures, s model.InstalmentSchedule) model.RescheduleParams {
	return model.RescheduleParams{Instrument: f.twd, BaseCurrency: f.twd, Schedule: s, Today: on(2026, time.November, 14)}
}

func TestRescheduleMakesTheNewPeriodsAsARecordedPlanHasThemAndLeavesTheOriginAlone(t *testing.T) {
	f, plan := rescheduleFixture(t)
	originBefore, periodsBefore := plan.Origin, slices.Clone(plan.Periods)

	// Four periods of 3,000, the first on the day of the purchase and the third with
	// 90 of interest.
	next := monthly(4, "3000")
	next[0].DueOn = on(2026, time.November, 14)
	next[2].Interest = decPtr("90")
	got, err := plan.Reschedule(rescheduleAtPurchase(f, f.interestSchedule(next)))
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

	got, err := plan.Reschedule(rescheduleAtPurchase(f, f.interestSchedule(monthly(3, "4000"))))
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
	got, err := plan.Reschedule(rescheduleAtPurchase(f, f.interestSchedule(next)))
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

	got, err := plan.Reschedule(rescheduleAtPurchase(f, f.interestSchedule(next)))
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
		{"an archived interest category that the plan does not use", func() model.InstalmentSchedule {
			archived := archivedCategory(topLevel(t, model.DirectionExpense, "其他"))
			return model.InstalmentSchedule{Periods: withInterest(monthly(4, "3000"), "30"), InterestCategory: &archived}
		}, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.Reschedule(rescheduleAtPurchase(f, tt.schedule()))

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

	_, err := plan.Reschedule(rescheduleAtPurchase(f, f.interestSchedule(monthly(4, "2999.5"))))

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

		if _, err := void.Reschedule(rescheduleAtPurchase(f, schedule())); !errors.Is(err, model.ErrPlanVoid) {
			t.Fatalf("err = %v, want ErrPlanVoid", err)
		}
	})
	t.Run("a period that is of another status than the origin", func(t *testing.T) {
		mixed := confirmed(t, plan)
		mixed.Periods[3] = plan.Periods[3]

		if _, err := mixed.Reschedule(rescheduleAtPurchase(f, schedule())); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
	t.Run("another instrument than the plan is counted in", func(t *testing.T) {
		if _, err := plan.Reschedule(model.RescheduleParams{Instrument: f.usd, BaseCurrency: f.twd, Schedule: schedule(), Today: on(2026, time.November, 14)}); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
	t.Run("an origin that has no instalment posting", func(t *testing.T) {
		lunch, err := model.NewExpense(f.expenseParams("lunch-0000000001", "120"))
		if err != nil {
			t.Fatalf("NewExpense: %v", err)
		}

		if _, err := (model.InstalmentPlan{Origin: lunch}).Reschedule(rescheduleAtPurchase(f, schedule())); !errors.Is(err, model.ErrMalformedPlan) {
			t.Fatalf("err = %v, want ErrMalformedPlan", err)
		}
	})
}

// On the 20th of February 2027 three of the 12 periods of the plan have fallen due,
// the 5th of December, January and February, and the plan is paid off: the three are
// sent again as they were, and the 9,000 that remains goes into one period, the 5th
// of March, with 270 of interest.
var february20 = on(2027, time.February, 20)

func payoff() []model.InstalmentPeriod {
	periods := monthly(4, "1000")
	periods[0].Interest = decPtr("30")
	periods[3].Principal = dec("9000")
	periods[3].Interest = decPtr("270")
	return periods
}

func TestRescheduleToPayOffEarlySendingWhatFellDueAsItWasNeedsNoLeave(t *testing.T) {
	f, plan := rescheduleFixture(t)
	for _, status := range []string{"a draft plan", "a confirmed plan"} {
		t.Run(status, func(t *testing.T) {
			plan := plan
			if status == "a confirmed plan" {
				plan = confirmed(t, plan)
			}
			periods := payoff()

			got, err := plan.Reschedule(model.RescheduleParams{
				Instrument: f.twd, BaseCurrency: f.twd, Schedule: f.interestSchedule(periods), Today: february20,
			})

			if err != nil {
				t.Fatalf("Reschedule: %v", err)
			}
			if len(got.Periods) != 4 {
				t.Fatalf("%d periods, want the 3 that fell due and the one that remains", len(got.Periods))
			}
			wantPostings(t, got.Periods[0], f.names, "instalment(卡) +1000 TWD", "account(卡) -1030 TWD", "expense(利息) +30 TWD, base +30")
			wantPostings(t, got.Periods[3], f.names, "instalment(卡) +9000 TWD", "account(卡) -9270 TWD", "expense(利息) +270 TWD, base +270")
		})
	}
}

func TestRescheduleThatChangesWhatFellDueIsRefusedWithoutLeaveAndSaysWhichPeriods(t *testing.T) {
	f, plan := rescheduleFixture(t)
	other := topLevel(t, model.DirectionExpense, "其他")
	type difference struct {
		seq    int
		change string
	}
	tests := []struct {
		name        string
		change      func(periods []model.InstalmentPeriod) []model.InstalmentPeriod
		category    *model.Category
		want        []difference
		wantAnother bool
	}{
		{"a period that fell due is dropped", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p = slices.Delete(p, 2, 3)
			p[2].Principal = dec("10000")
			return p
		}, nil, []difference{{3, "dropped"}}, false},
		{"a period is added before today", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[3].Principal = dec("8500")
			added := model.InstalmentPeriod{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.February, 10), Principal: dec("500")}
			return slices.Insert(p, 3, added)
		}, nil, []difference{{4, "added"}}, false},
		{"the day of a period that fell due", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[1].DueOn = on(2027, time.January, 6)
			return p
		}, nil, []difference{{2, "day"}}, false},
		{"the principal of a period that fell due", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[1].Principal, p[3].Principal = dec("1100"), dec("8900")
			return p
		}, nil, []difference{{2, "principal"}}, false},
		{"the interest of a period that fell due", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[0].Interest = decPtr("33")
			return p
		}, nil, []difference{{1, "interest"}}, false},
		{"interest on a period that fell due with none", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[1].Interest = decPtr("5")
			return p
		}, nil, []difference{{2, "interest"}}, false},
		{"no interest on a period that fell due with some", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[0].Interest = nil
			return p
		}, nil, []difference{{1, "interest"}}, false},
		{"several periods at once", func(p []model.InstalmentPeriod) []model.InstalmentPeriod {
			p[0].Interest = decPtr("33")
			p[2].Principal, p[3].Principal = dec("1200"), dec("8800")
			return p
		}, nil, []difference{{1, "interest"}, {3, "principal"}}, false},
		{"another category for the interest that fell due", func(p []model.InstalmentPeriod) []model.InstalmentPeriod { return p }, &other, nil, true},
	}
	for _, tt := range tests {
		for _, status := range []string{"a draft plan", "a confirmed plan"} {
			t.Run(tt.name+", "+status, func(t *testing.T) {
				plan := plan
				if status == "a confirmed plan" {
					plan = confirmed(t, plan)
				}
				category := &f.interest
				if tt.category != nil {
					category = tt.category
				}
				r := model.RescheduleParams{
					Instrument: f.twd, BaseCurrency: f.twd, Today: february20,
					Schedule: model.InstalmentSchedule{Periods: tt.change(payoff()), InterestCategory: category},
				}

				got, err := plan.Reschedule(r)

				var changed *model.PastPeriodsChangedError
				if !errors.As(err, &changed) || !errors.Is(err, model.ErrPastPeriodsChanged) {
					t.Fatalf("err = %v, want a *PastPeriodsChangedError that is an ErrPastPeriodsChanged", err)
				}
				if len(changed.Differences) != len(tt.want) || changed.InterestCategoryChanged != tt.wantAnother {
					t.Fatalf("differences = %+v, category changed %v, want %d differences and category changed %v", changed.Differences, changed.InterestCategoryChanged, len(tt.want), tt.wantAnother)
				}
				for i, want := range tt.want {
					if d := changed.Differences[i]; d.Seq != want.seq || !strings.Contains(d.Change, want.change) {
						t.Errorf("difference %d = %+v, want period %d %s", i, d, want.seq, want.change)
					}
				}
				if !strings.Contains(err.Error(), "ChangePast") {
					t.Errorf("err = %q, want it to say that ChangePast is what lets what fell due change", err)
				}
				if got.Origin.ID != uuid.Nil || got.Periods != nil {
					t.Errorf("plan = %+v, want nothing of a schedule that is refused", got)
				}

				// With leave, it is the new schedule, whatever it changes.
				r.ChangePast = true
				if _, err := plan.Reschedule(r); err != nil {
					t.Errorf("Reschedule with ChangePast: %v, want it to change what fell due", err)
				}
			})
		}
	}
}

// The difference is told by the day it fell due: the period that is dropped is the
// one the old schedule had at that place, with the day it had.
func TestRescheduleSaysTheDayOfThePastPeriodThatIsNotAsItWas(t *testing.T) {
	f, plan := rescheduleFixture(t)
	periods := payoff()
	periods[1].DueOn = on(2027, time.January, 6)

	_, err := plan.Reschedule(model.RescheduleParams{Instrument: f.twd, BaseCurrency: f.twd, Schedule: f.interestSchedule(periods), Today: february20})

	var changed *model.PastPeriodsChangedError
	if !errors.As(err, &changed) || len(changed.Differences) != 1 || changed.Differences[0].Seq != 2 || !changed.Differences[0].DueOn.Equal(on(2027, time.January, 5)) {
		t.Fatalf("err = %v, want period 2, which fell due on 2027-01-05, to be the one that differs", err)
	}
	if !changed.Today.Equal(february20) || !strings.Contains(err.Error(), "2027-01-05") {
		t.Errorf("err = %q, today %s, want the day it fell due named", err, changed.Today)
	}
}

// Today is not past: a period that falls due on the day can be moved or changed,
// and the day after it cannot.
func TestRescheduleLetsAPeriodThatFallsDueTodayChangeAndNotOneThatFellDueYesterday(t *testing.T) {
	f, plan := rescheduleFixture(t)
	change := func() []model.InstalmentPeriod {
		periods := payoff()
		periods[2].Principal, periods[3].Principal = dec("2000"), dec("8000")
		periods[2].DueOn = on(2027, time.February, 20)
		return periods
	}

	t.Run("the day it falls due", func(t *testing.T) {
		_, err := plan.Reschedule(model.RescheduleParams{
			Instrument: f.twd, BaseCurrency: f.twd, Schedule: f.interestSchedule(change()), Today: on(2027, time.February, 5),
		})
		if err != nil {
			t.Fatalf("Reschedule: %v, want the period that falls due today to be free", err)
		}
	})
	t.Run("the day after", func(t *testing.T) {
		_, err := plan.Reschedule(model.RescheduleParams{
			Instrument: f.twd, BaseCurrency: f.twd, Schedule: f.interestSchedule(change()), Today: on(2027, time.February, 6),
		})
		if !errors.Is(err, model.ErrPastPeriodsChanged) {
			t.Fatalf("err = %v, want ErrPastPeriodsChanged: the period fell due yesterday", err)
		}
	})
}

func TestRescheduleRefusesAPeriodAddedBeforeTodayWhenNothingHasFallenDue(t *testing.T) {
	f, plan := rescheduleFixture(t)
	periods := monthly(12, "1000")
	periods[0].DueOn = on(2026, time.November, 20)

	_, err := plan.Reschedule(model.RescheduleParams{Instrument: f.twd, BaseCurrency: f.twd, Schedule: f.interestSchedule(periods), Today: on(2026, time.December, 1)})

	var changed *model.PastPeriodsChangedError
	if !errors.As(err, &changed) || len(changed.Differences) < 1 || changed.Differences[0].Seq != 1 || changed.Differences[0].Change != "added" {
		t.Fatalf("err = %v, want period 1, a period before today that the plan did not have, added", err)
	}
}

// The category of the interest is part of the past only when some of what fell due
// has interest: otherwise it can be chosen again.
func TestRescheduleLetsTheCategoryOfTheInterestChangeWhenNoPastPeriodHasInterest(t *testing.T) {
	f := newInstalmentFixtures(t)
	other := topLevel(t, model.DirectionExpense, "其他")
	// Three periods of 1,000, the last with 10 of interest, in a plan whose past, on
	// the 20th of January, is the first two.
	periods := monthly(3, "1000")
	periods[2].Interest = decPtr("10")
	plan, err := model.NewInstalmentExpense(f.expenseParams("laptop-00000009", "3000"), f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	next := monthly(3, "1000")
	next[2].Interest = decPtr("10")

	_, err = plan.Reschedule(model.RescheduleParams{
		Instrument: f.twd, BaseCurrency: f.twd, Today: on(2027, time.January, 20),
		Schedule: model.InstalmentSchedule{Periods: next, InterestCategory: &other},
	})

	if err != nil {
		t.Fatalf("Reschedule: %v, want the category to be free: nothing that fell due has interest", err)
	}
}

func TestRescheduleRefusesAChangeOfTheInterestInTheBaseCurrencyOfAPeriodThatFellDue(t *testing.T) {
	f := newInstalmentFixtures(t)
	// 300 USD, 9,000 TWD, in three periods of 100 USD, the first with 2 USD of interest
	// that was 62 TWD on the day it was charged.
	periods := monthly(3, "100")
	periods[0].Interest, periods[0].InterestBaseAmount = decPtr("2"), decPtr("62")
	params := model.ExpenseParams{
		Header: header("usd-plan-0000001"), Account: &f.card, Instrument: f.usd, BaseCurrency: f.twd,
		Lines: []model.CategoryLine{{Category: f.electronics, Amount: dec("300"), BaseAmount: decPtr("9000")}},
	}
	plan, err := model.NewInstalmentExpense(params, f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	reschedule := func(baseAmount string) error {
		next := monthly(3, "100")
		next[0].Interest, next[0].InterestBaseAmount = decPtr("2"), decPtr(baseAmount)
		_, err := plan.Reschedule(model.RescheduleParams{
			Instrument: f.usd, BaseCurrency: f.twd, Today: on(2027, time.January, 1), Schedule: f.interestSchedule(next),
		})
		return err
	}

	if err := reschedule("62"); err != nil {
		t.Fatalf("Reschedule with the interest as it was: %v", err)
	}
	err = reschedule("64")
	var changed *model.PastPeriodsChangedError
	if !errors.As(err, &changed) || len(changed.Differences) != 1 || !strings.Contains(changed.Differences[0].Change, "interest in the base currency") {
		t.Fatalf("err = %v, want the interest in the base currency of period 1 to be what differs", err)
	}
}

// The category of the interest that the periods of the plan have is allowed when it
// is archived since, so that a plan can still be rescheduled; any other has to be in
// use.
func TestRescheduleAllowsTheArchivedCategoryThePlanUsesAndNoOtherArchivedOne(t *testing.T) {
	f, plan := rescheduleFixture(t)
	other := topLevel(t, model.DirectionExpense, "其他")
	next := func() []model.InstalmentPeriod {
		periods := monthly(4, "3000")
		periods[1].Interest = decPtr("90")
		return periods
	}
	reschedule := func(category model.Category) error {
		_, err := plan.Reschedule(rescheduleAtPurchase(f, model.InstalmentSchedule{Periods: next(), InterestCategory: &category}))
		return err
	}

	if err := reschedule(archivedCategory(f.interest)); err != nil {
		t.Errorf("the archived category the periods use: err = %v, want it allowed", err)
	}
	if err := reschedule(archivedCategory(other)); !errors.Is(err, model.ErrArchived) {
		t.Errorf("another archived category: err = %v, want ErrArchived", err)
	}
	if err := reschedule(other); err != nil {
		t.Errorf("another category in use: err = %v, want it allowed", err)
	}
	system := unrecordedCategory(model.DirectionExpense)
	if err := reschedule(system); !errors.Is(err, model.ErrSystemCategory) {
		t.Errorf("a system category: err = %v, want ErrSystemCategory", err)
	}

	// A plan whose periods have no interest has no category in use: an archived one is
	// archived.
	plain, err := model.NewInstalmentExpense(f.expenseParams("laptop-00000008", "3000"), f.interestSchedule(monthly(3, "1000")))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}
	archived := archivedCategory(f.interest)
	periods := monthly(3, "1000")
	periods[1].Interest = decPtr("10")
	_, err = plain.Reschedule(rescheduleAtPurchase(f, model.InstalmentSchedule{Periods: periods, InterestCategory: &archived}))
	if !errors.Is(err, model.ErrArchived) {
		t.Errorf("an archived category for a plan that has no interest: err = %v, want ErrArchived", err)
	}
}
