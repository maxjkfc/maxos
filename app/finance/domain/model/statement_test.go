package model_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// days prints a statement as the days it covers.
func days(p model.StatementPeriod) string {
	return p.From.Format(time.DateOnly) + ".." + p.ClosingOn.Format(time.DateOnly)
}

func TestDateInMonthIsTheDayOrTheLastDayOfAShorterMonth(t *testing.T) {
	tests := []struct {
		name  string
		month time.Time
		day   int
		want  time.Time
	}{
		{"the 1st of any month", on(2026, time.November, 17), 1, on(2026, time.November, 1)},
		{"a day that every month has", on(2026, time.February, 1), 28, on(2026, time.February, 28)},
		{"the 31st of a month of 31 days", on(2026, time.January, 15), 31, on(2026, time.January, 31)},
		{"the 31st of a month of 30 days", on(2026, time.April, 15), 31, on(2026, time.April, 30)},
		{"the 30th of a month of 30 days", on(2026, time.April, 1), 30, on(2026, time.April, 30)},
		{"the 31st of February", on(2026, time.February, 10), 31, on(2026, time.February, 28)},
		{"the 31st of February in a leap year", on(2028, time.February, 10), 31, on(2028, time.February, 29)},
		{"the 30th of February", on(2026, time.February, 10), 30, on(2026, time.February, 28)},
		{"the 30th of February in a leap year", on(2028, time.February, 10), 30, on(2028, time.February, 29)},
		{"the 29th of February", on(2026, time.February, 10), 29, on(2026, time.February, 28)},
		{"the 29th of February in a leap year", on(2028, time.February, 10), 29, on(2028, time.February, 29)},
		{"the 29th of February in 2100, which is not a leap year", on(2100, time.February, 10), 29, on(2100, time.February, 28)},
		{"the 29th of February in 2000, which is a leap year", on(2000, time.February, 10), 29, on(2000, time.February, 29)},
		{"the month is the one the time falls on in its own location", time.Date(2026, time.December, 31, 23, 30, 0, 0, taipei), 31, on(2026, time.December, 31)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.DateInMonth(tt.month, tt.day)

			if !got.Equal(tt.want) || got.Location() != time.UTC {
				t.Fatalf("DateInMonth(%s, %d) = %s, want %s at midnight UTC", tt.month.Format(time.DateOnly), tt.day, got, tt.want.Format(time.DateOnly))
			}
		})
	}
}

func TestStatementContainingCutsTheDaysAtTheClosingDateWhichIsOnTheStatementItEnds(t *testing.T) {
	tests := []struct {
		name       string
		closingDay int
		day        time.Time
		want       string
	}{
		{"the day before the closing date", 5, on(2026, time.November, 4), "2026-10-06..2026-11-05"},
		{"the closing date, which is on the statement it closes", 5, on(2026, time.November, 5), "2026-10-06..2026-11-05"},
		{"the day after the closing date, which is on the next", 5, on(2026, time.November, 6), "2026-11-06..2026-12-05"},
		{"the first day of the statement", 5, on(2026, time.October, 6), "2026-10-06..2026-11-05"},
		{"the day before the first day belongs to the one before", 5, on(2026, time.October, 5), "2026-09-06..2026-10-05"},
		{"the middle of the statement", 5, on(2026, time.November, 21), "2026-11-06..2026-12-05"},
		{"across the end of the year", 5, on(2026, time.December, 20), "2026-12-06..2027-01-05"},
		{"the first days of the year, on the statement of the year before", 5, on(2027, time.January, 2), "2026-12-06..2027-01-05"},
		{"the closing date of January", 5, on(2027, time.January, 5), "2026-12-06..2027-01-05"},
		{"a closing day of 1", 1, on(2026, time.November, 1), "2026-10-02..2026-11-01"},
		{"the day after a closing day of 1", 1, on(2026, time.November, 2), "2026-11-02..2026-12-01"},
		{"a closing day of 31 in a month of 31 days", 31, on(2026, time.January, 31), "2026-01-01..2026-01-31"},
		{"a closing day of 31 closes a month of 30 days on its last day", 31, on(2026, time.April, 30), "2026-04-01..2026-04-30"},
		{"a closing day of 31 closes February on its last day", 31, on(2026, time.February, 28), "2026-02-01..2026-02-28"},
		{"a closing day of 31 closes February of a leap year on the 29th", 31, on(2028, time.February, 29), "2028-02-01..2028-02-29"},
		{"the day after February of a leap year closed", 31, on(2028, time.March, 1), "2028-03-01..2028-03-31"},
		{"a closing day of 30 closes February on the 28th", 30, on(2026, time.February, 28), "2026-01-31..2026-02-28"},
		{"a closing day of 30 starts March on the 1st", 30, on(2026, time.March, 1), "2026-03-01..2026-03-30"},
		{"a closing day of 30 closes February of a leap year on the 29th", 30, on(2028, time.February, 29), "2028-01-31..2028-02-29"},
		{"a closing day of 29 closes February on the 28th", 29, on(2026, time.February, 28), "2026-01-30..2026-02-28"},
		{"a closing day of 29 closes February of a leap year on the 29th", 29, on(2028, time.February, 29), "2028-01-30..2028-02-29"},
		{"the day after a closing day of 29 in a leap year", 29, on(2028, time.March, 1), "2028-03-01..2028-03-29"},
		{"a time of day is not part of the day", 5, time.Date(2026, time.November, 5, 23, 59, 0, 0, time.UTC), "2026-10-06..2026-11-05"},
		// It is the day in the location of the time: this is the 6th in Taipei, though
		// it is still the 5th in UTC.
		{"the day it is in its own location", 5, time.Date(2026, time.November, 6, 0, 30, 0, 0, taipei), "2026-11-06..2026-12-05"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.StatementContaining(tt.closingDay, tt.day)

			if days(got) != tt.want {
				t.Fatalf("StatementContaining(%d, %s) = %s, want %s", tt.closingDay, tt.day.Format(time.DateOnly), days(got), tt.want)
			}
			if got.From.Location() != time.UTC || got.ClosingOn.Location() != time.UTC {
				t.Errorf("the days are in %s and %s, want dates at midnight UTC", got.From.Location(), got.ClosingOn.Location())
			}
		})
	}
}

// Every day is on exactly one statement, for every closing day, over four years that
// have a leap year in them and the ends of them all: the statement of a day has the
// day in it, the day after its closing date starts the next, which closes a month
// later, and the statement before it ends the day before it starts. This is what
// makes the Closing of a statement the Previous of the next.
func TestEveryDayIsOnOneStatementWhateverTheClosingDay(t *testing.T) {
	first, last := on(2026, time.January, 1), on(2029, time.December, 31)
	for closingDay := model.MinDayOfMonth; closingDay <= model.MaxDayOfMonth; closingDay++ {
		for d := first; !d.After(last); d = d.AddDate(0, 0, 1) {
			p := model.StatementContaining(closingDay, d)
			if d.Before(p.From) || d.After(p.ClosingOn) {
				t.Fatalf("closing day %d: the statement of %s is %s, which does not have the day in it", closingDay, d.Format(time.DateOnly), days(p))
			}
			if got := model.StatementContaining(closingDay, p.ClosingOn); got != p {
				t.Fatalf("closing day %d: the statement of its closing date, %s, is %s, want %s", closingDay, p.ClosingOn.Format(time.DateOnly), days(got), days(p))
			}
			if got := model.StatementContaining(closingDay, p.From); got != p {
				t.Fatalf("closing day %d: the statement of its first day, %s, is %s, want %s", closingDay, p.From.Format(time.DateOnly), days(got), days(p))
			}
			next := model.StatementContaining(closingDay, p.ClosingOn.AddDate(0, 0, 1))
			if !next.From.Equal(p.ClosingOn.AddDate(0, 0, 1)) {
				t.Fatalf("closing day %d: the statement after %s starts on %s, want the day after it", closingDay, days(p), next.From.Format(time.DateOnly))
			}
			if got := next.ClosingOn; !got.After(p.ClosingOn) || got.After(p.ClosingOn.AddDate(0, 1, 3)) {
				t.Fatalf("closing day %d: the statement after %s closes on %s, want it a month later", closingDay, days(p), got.Format(time.DateOnly))
			}
			// The closing date is the closing day of its month, or the last day of a
			// month that is shorter.
			want := min(closingDay, model.LastDayOf(p.ClosingOn).Day())
			if p.ClosingOn.Day() != want {
				t.Fatalf("closing day %d: %s closes on the %d, want the %d", closingDay, days(p), p.ClosingOn.Day(), want)
			}
		}
	}
}

func TestStatementsClosingBetweenListsTheClosingDatesInTheRangeBothEndsIncluded(t *testing.T) {
	tests := []struct {
		name       string
		closingDay int
		from, to   time.Time
		want       []string
	}{
		{"a closing date on each end", 5, on(2026, time.October, 5), on(2026, time.December, 5),
			[]string{"2026-09-06..2026-10-05", "2026-10-06..2026-11-05", "2026-11-06..2026-12-05"}},
		{"the day after the first closing date leaves it out", 5, on(2026, time.October, 6), on(2026, time.December, 5),
			[]string{"2026-10-06..2026-11-05", "2026-11-06..2026-12-05"}},
		{"the day before the last closing date leaves it out", 5, on(2026, time.October, 5), on(2026, time.December, 4),
			[]string{"2026-09-06..2026-10-05", "2026-10-06..2026-11-05"}},
		{"one day that is a closing date", 5, on(2026, time.November, 5), on(2026, time.November, 5),
			[]string{"2026-10-06..2026-11-05"}},
		{"one day that is not", 5, on(2026, time.November, 6), on(2026, time.November, 6), nil},
		{"a range between two closing dates has none", 5, on(2026, time.November, 6), on(2026, time.December, 4), nil},
		{"a range that ends before it starts has none", 5, on(2026, time.December, 5), on(2026, time.November, 5), nil},
		{"across the end of the year", 5, on(2026, time.December, 1), on(2027, time.February, 28),
			[]string{"2026-11-06..2026-12-05", "2026-12-06..2027-01-05", "2027-01-06..2027-02-05"}},
		{"a closing day of 31 closes each month on its last day", 31, on(2027, time.January, 1), on(2027, time.April, 30),
			[]string{"2027-01-01..2027-01-31", "2027-02-01..2027-02-28", "2027-03-01..2027-03-31", "2027-04-01..2027-04-30"}},
		{"a closing day of 30 in a leap year", 30, on(2028, time.February, 1), on(2028, time.March, 31),
			[]string{"2028-01-31..2028-02-29", "2028-03-01..2028-03-30"}},
		{"the times of day and the zones are not part of the days", 5, time.Date(2026, time.November, 5, 23, 0, 0, 0, time.UTC), time.Date(2026, time.November, 5, 0, 0, 0, 0, taipei),
			[]string{"2026-10-06..2026-11-05"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.StatementsClosingBetween(tt.closingDay, tt.from, tt.to)

			var gotDays []string
			for _, p := range got {
				gotDays = append(gotDays, days(p))
			}
			if fmt.Sprint(gotDays) != fmt.Sprint(tt.want) {
				t.Fatalf("StatementsClosingBetween = %v, want %v", gotDays, tt.want)
			}
			for _, p := range got {
				if again := model.StatementContaining(tt.closingDay, p.ClosingOn); again != p {
					t.Errorf("the statement closing on %s is %s when it is found by its day, want the same", p.ClosingOn.Format(time.DateOnly), days(again))
				}
			}
		})
	}
}

func TestDueDateIsTheFirstPaymentDueDayStrictlyAfterTheClosingDate(t *testing.T) {
	tests := []struct {
		name       string
		closingDay int
		dueDay     int
		on         time.Time // a day of the statement
		want       string
	}{
		{"a due day after the closing day is in the month it closes in", 5, 20, on(2026, time.November, 3), "2026-11-20"},
		{"a due day before the closing day is in the next month", 20, 5, on(2026, time.November, 3), "2026-12-05"},
		// Paid by the closing date itself would be paid before the statement is known.
		{"a due day that is the closing day is in the next month", 5, 5, on(2026, time.November, 3), "2026-12-05"},
		{"the day after the closing day is in the same month", 5, 6, on(2026, time.November, 3), "2026-11-06"},
		{"a due day before the closing day, across the end of the year", 20, 5, on(2026, time.December, 3), "2027-01-05"},
		{"a due day after the closing day in the last month of the year", 5, 25, on(2026, time.December, 3), "2026-12-25"},
		{"a due day of 31 in a month of 31 days", 15, 31, on(2026, time.January, 3), "2026-01-31"},
		{"a due day of 31 is the last day of a month of 30 days", 15, 31, on(2026, time.April, 3), "2026-04-30"},
		{"a due day of 31 is the last day of February", 15, 31, on(2026, time.February, 3), "2026-02-28"},
		{"a due day of 30 is the last day of February in a leap year", 15, 30, on(2028, time.February, 3), "2028-02-29"},
		// January closes on the 31st, so the next day that is the 31st is February's last.
		{"a due day of 31 after a closing day of 31 is in the next month, on its last day", 31, 31, on(2026, time.January, 20), "2026-02-28"},
		{"a due day of 31 after a closing day of 31 in a month of 30 days", 31, 31, on(2026, time.April, 20), "2026-05-31"},
		// February closes on the 28th, and a due day of 29 is its last day, which is not
		// after it.
		{"a due day that is a later day than the closing day but the same last day of a short month", 28, 29, on(2026, time.February, 10), "2026-03-29"},
		{"the same in a leap year, where February has a 29th", 28, 29, on(2028, time.February, 10), "2028-02-29"},
		{"a closing day of 30 and a due day of 31 in a month of 31 days", 30, 31, on(2026, time.January, 10), "2026-01-31"},
		{"a closing day of 30 and a due day of 31 in February", 30, 31, on(2026, time.February, 10), "2026-03-31"},
		{"a due day of 1", 5, 1, on(2026, time.November, 3), "2026-12-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			period := model.StatementContaining(tt.closingDay, tt.on)

			got := period.DueDate(tt.dueDay)

			if got.Format(time.DateOnly) != tt.want || got.Location() != time.UTC {
				t.Fatalf("the statement %s with a due day of %d is due on %s, want %s", days(period), tt.dueDay, got.Format(time.DateOnly), tt.want)
			}
			if !got.After(period.ClosingOn) {
				t.Errorf("due on %s, which is not after the closing date %s", got.Format(time.DateOnly), period.ClosingOn.Format(time.DateOnly))
			}
		})
	}
}

func TestAStatementIsClosedWhenItsClosingDateIsBeforeToday(t *testing.T) {
	period := model.StatementContaining(5, on(2026, time.November, 3)) // closes on the 5th
	tests := []struct {
		name  string
		today time.Time
		want  bool
	}{
		{"long before", on(2026, time.October, 20), false},
		{"the day before the closing date", on(2026, time.November, 4), false},
		{"the closing date itself, when it is still open", on(2026, time.November, 5), false},
		{"the closing date, however late in the day", time.Date(2026, time.November, 5, 23, 59, 0, 0, time.UTC), false},
		{"the day after", on(2026, time.November, 6), true},
		{"the day after in Taipei, when it is still the closing date in UTC", time.Date(2026, time.November, 6, 0, 30, 0, 0, taipei), true},
		{"long after", on(2027, time.June, 1), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := period.Closed(tt.today); got != tt.want {
				t.Fatalf("Closed(%s) = %v, want %v", tt.today.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

func TestStatementFiguresCloseAtThePreviousLessTheChargesPlusTheCredits(t *testing.T) {
	usd := uuid.Must(uuid.NewV7())
	tests := []struct {
		name                       string
		previous, charges, credits string
		wantClosing                string
	}{
		{name: "owing, spending and paying", previous: "-1000", charges: "500", credits: "300", wantClosing: "-1200"},
		{name: "paying it all off", previous: "-1000", charges: "0", credits: "1000", wantClosing: "0"},
		{name: "paying more than is owed leaves the card holding", previous: "-1000", charges: "0", credits: "1500", wantClosing: "500"},
		{name: "starting from nothing", previous: "0", charges: "250.50", credits: "0", wantClosing: "-250.50"},
		{name: "a card that held something", previous: "200", charges: "50", credits: "10", wantClosing: "160"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := model.NewStatementFigures(usd, dec(tt.previous), dec(tt.charges), dec(tt.credits))

			if got.InstrumentID != usd || !got.Previous.Equal(dec(tt.previous)) || !got.Charges.Equal(dec(tt.charges)) || !got.Credits.Equal(dec(tt.credits)) {
				t.Errorf("figures = %+v, want what was given as it was", got)
			}
			if !got.Closing.Equal(dec(tt.wantClosing)) {
				t.Errorf("closing = %s, want %s: the previous less the charges plus the credits", got.Closing, tt.wantClosing)
			}
		})
	}
}
