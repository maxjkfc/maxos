package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/catalog/domain/model"
)

// day returns midnight UTC on the given day of December 2026.
func day(d int) time.Time {
	return time.Date(2026, time.December, d, 0, 0, 0, 0, time.UTC)
}

func period(t *testing.T, from, to int) *model.Period {
	t.Helper()
	p, err := model.NewPeriod(day(from), day(to))
	if err != nil {
		t.Fatalf("NewPeriod(%d, %d): %v", from, to, err)
	}
	return &p
}

func newProject(t *testing.T) model.Project {
	t.Helper()
	p, err := model.NewProject(uuid.Must(uuid.NewV7()), "日本旅行")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

func TestDateOfIsTheCalendarDayInTheTimesOwnLocation(t *testing.T) {
	taipei := time.FixedZone("UTC+8", 8*60*60)
	// 00:30 on the 2nd in Taipei is still the 1st in UTC.
	got := model.DateOf(time.Date(2026, time.December, 2, 0, 30, 0, 0, taipei))
	if want := day(2); !got.Equal(want) || got.Location() != time.UTC {
		t.Fatalf("DateOf = %v, want %v", got, want)
	}
}

func TestNewPeriodHoldsDatesAndAllowsOneDay(t *testing.T) {
	p, err := model.NewPeriod(time.Date(2026, time.December, 1, 15, 4, 5, 0, time.UTC), time.Date(2026, time.December, 1, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("one-day period rejected: %v", err)
	}
	if !p.From.Equal(day(1)) || !p.To.Equal(day(1)) {
		t.Fatalf("period = %v, want both ends on the 1st at midnight", p)
	}
}

func TestNewPeriodRejectsAnEndBeforeTheStart(t *testing.T) {
	_, err := model.NewPeriod(day(8), day(1))
	if !errors.Is(err, model.ErrPeriodReversed) {
		t.Fatalf("err = %v, want ErrPeriodReversed", err)
	}
}

// A date that is not given is the zero time, which DateOf would turn into the
// first day of the year 1: a period from the beginning of time (no start), or one
// that ends before it starts (no end), and not what was meant.
func TestNewPeriodRequiresBothEnds(t *testing.T) {
	tests := []struct {
		name     string
		from, to time.Time
	}{
		{name: "no start", from: time.Time{}, to: day(8)},
		{name: "no end", from: day(1), to: time.Time{}},
		{name: "neither", from: time.Time{}, to: time.Time{}},
		{name: "no start, the zero time in another location", from: time.Time{}.In(time.FixedZone("UTC+8", 8*60*60)), to: day(8)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NewPeriod(tt.from, tt.to)

			if !errors.Is(err, model.ErrPeriodIncomplete) {
				t.Fatalf("err = %v, want ErrPeriodIncomplete", err)
			}
			if errors.Is(err, model.ErrPeriodReversed) {
				t.Errorf("err = %v, want a missing end to be told from an end before the start", err)
			}
			if !got.From.IsZero() || !got.To.IsZero() {
				t.Errorf("period = %v, want none with the error", got)
			}
		})
	}
}

func TestPeriodCoversIncludesBothEnds(t *testing.T) {
	outer := period(t, 1, 8)
	tests := []struct {
		name  string
		inner *model.Period
		want  bool
	}{
		{name: "the same days", inner: period(t, 1, 8), want: true},
		{name: "inside", inner: period(t, 2, 7), want: true},
		{name: "one day on the first day", inner: period(t, 1, 1), want: true},
		{name: "one day on the last day", inner: period(t, 8, 8), want: true},
		{name: "starts a day early", inner: period(t, 0, 5), want: false},
		{name: "ends a day late", inner: period(t, 5, 9), want: false},
		{name: "entirely outside", inner: period(t, 10, 12), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outer.Covers(*tt.inner); got != tt.want {
				t.Errorf("Covers(%v) = %v, want %v", tt.inner, got, tt.want)
			}
		})
	}
}

func TestActivePeriodMustLieWithinThePeriod(t *testing.T) {
	tests := []struct {
		name   string
		active *model.Period
		ok     bool
	}{
		{name: "inside", active: period(t, 2, 7), ok: true},
		{name: "the whole period", active: period(t, 1, 8), ok: true},
		{name: "starts before the period", active: period(t, 0, 3), ok: false},
		{name: "ends after the period", active: period(t, 7, 9), ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newProject(t)
			if err := p.SetPeriod(period(t, 1, 8)); err != nil {
				t.Fatalf("SetPeriod: %v", err)
			}

			err := p.SetActivePeriod(tt.active)
			if tt.ok {
				if err != nil {
					t.Fatalf("SetActivePeriod: %v", err)
				}
				if p.ActivePeriod == nil || p.ActivePeriod.String() != tt.active.String() {
					t.Fatalf("ActivePeriod = %v, want %v", p.ActivePeriod, tt.active)
				}
				return
			}
			if !errors.Is(err, model.ErrActivePeriodOutsidePeriod) {
				t.Fatalf("err = %v, want ErrActivePeriodOutsidePeriod", err)
			}
			if p.ActivePeriod != nil {
				t.Fatalf("ActivePeriod = %v after the error, want it unchanged (unset)", p.ActivePeriod)
			}
		})
	}
}

func TestPeriodMayNotLeaveTheActivePeriodOutside(t *testing.T) {
	p := newProject(t)
	if err := p.SetPeriod(period(t, 1, 8)); err != nil {
		t.Fatalf("SetPeriod: %v", err)
	}
	if err := p.SetActivePeriod(period(t, 2, 7)); err != nil {
		t.Fatalf("SetActivePeriod: %v", err)
	}

	err := p.SetPeriod(period(t, 3, 8))
	if !errors.Is(err, model.ErrActivePeriodOutsidePeriod) {
		t.Fatalf("err = %v, want ErrActivePeriodOutsidePeriod", err)
	}
	if p.Period.String() != period(t, 1, 8).String() {
		t.Fatalf("Period = %v after the error, want it unchanged", p.Period)
	}

	if err := p.SetPeriod(period(t, 2, 7)); err != nil {
		t.Fatalf("a period of exactly the active days rejected: %v", err)
	}
}

func TestThePeriodRulesNeedBothPeriods(t *testing.T) {
	t.Run("an active period without a period", func(t *testing.T) {
		p := newProject(t)
		if err := p.SetActivePeriod(period(t, 2, 7)); err != nil {
			t.Fatalf("SetActivePeriod: %v", err)
		}
	})
	t.Run("a period without an active period", func(t *testing.T) {
		p := newProject(t)
		if err := p.SetPeriod(period(t, 1, 8)); err != nil {
			t.Fatalf("SetPeriod: %v", err)
		}
	})
	t.Run("clearing the period frees the active period", func(t *testing.T) {
		p := newProject(t)
		_ = p.SetPeriod(period(t, 1, 8))
		_ = p.SetActivePeriod(period(t, 2, 7))

		if err := p.SetPeriod(nil); err != nil {
			t.Fatalf("clear the period: %v", err)
		}
		if p.Period != nil {
			t.Fatalf("Period = %v, want it cleared", p.Period)
		}
		if err := p.SetActivePeriod(period(t, 20, 25)); err != nil {
			t.Fatalf("SetActivePeriod after clearing the period: %v", err)
		}
	})
	t.Run("clearing the active period", func(t *testing.T) {
		p := newProject(t)
		_ = p.SetActivePeriod(period(t, 2, 7))

		if err := p.SetActivePeriod(nil); err != nil {
			t.Fatalf("clear the active period: %v", err)
		}
		if p.ActivePeriod != nil {
			t.Fatalf("ActivePeriod = %v, want it cleared", p.ActivePeriod)
		}
	})
}

func TestAProjectDoesNotShareItsPeriodsWithTheCaller(t *testing.T) {
	p := newProject(t)
	given := period(t, 1, 8)
	if err := p.SetPeriod(given); err != nil {
		t.Fatalf("SetPeriod: %v", err)
	}

	given.To = day(20)
	if p.Period.To.Equal(day(20)) {
		t.Fatal("changing the period the caller passed changed the project")
	}
}
