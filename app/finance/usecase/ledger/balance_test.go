package ledger_test

import (
	"context"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// cashOn is what the balances of the cash account say it holds in TWD, as GetBalances
// returns them for the input, and whether there is a balance at all.
func (e *env) cashOn(in usecase.GetBalancesInput) (usecase.Balance, bool) {
	e.t.Helper()
	in.AccountID = &e.cash.ID
	out, err := e.svc.GetBalances(context.Background(), in)
	if err != nil {
		e.t.Fatalf("GetBalances: %v", err)
	}
	for _, b := range out.Balances {
		if b.InstrumentID == e.twd.ID {
			return b, true
		}
	}
	return usecase.Balance{}, false
}

// confirmedLunchOn records a lunch of 120 on the day, and confirms it.
func (e *env) confirmedLunchOn(key string, on time.Time) usecase.Transaction {
	e.t.Helper()
	in := e.lunch(key)
	in.OccurredOn = on
	out, err := e.svc.RecordExpense(context.Background(), in)
	if err != nil {
		e.t.Fatalf("RecordExpense(%q): %v", key, err)
	}
	return e.confirm(out.Transaction)
}

// What counts toward a balance is what has happened by today, and today is what
// the clock says in the timezone of the application: the day it falls on as its
// own location reads it, not the day it is in UTC.
func TestGetBalancesCountsTheConfirmedTransactionsUpToTodayInTheTimezoneOfTheClock(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	e.confirmedLunchOn("lunch-tomorrow1", day.AddDate(0, 0, 1)) // 120, dated the 15th
	e.confirmedLunchOn("lunch-nextweek", day.AddDate(0, 0, 7))  // 120, dated the 21st

	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{"the afternoon of the 14th in Taipei", time.Date(2026, 11, 14, 15, 30, 0, 0, taipei), "1000"},
		{"a minute before the 15th in Taipei", time.Date(2026, 11, 14, 23, 59, 0, 0, taipei), "1000"},
		// It is still the 14th in UTC then: the clock's own location decides.
		{"half past midnight on the 15th in Taipei", time.Date(2026, 11, 15, 0, 30, 0, 0, taipei), "880"},
		{"the 15th at the end of the day", time.Date(2026, 11, 15, 23, 59, 0, 0, taipei), "880"},
		{"the 21st", time.Date(2026, 11, 21, 8, 0, 0, 0, taipei), "760"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e.clock.now = tt.now

			got, found := e.cashOn(usecase.GetBalancesInput{})

			if !found || !got.Quantity.Equal(dec(tt.want)) {
				t.Fatalf("balance = %+v, %v, want %s", got, found, tt.want)
			}
			if asked := e.txns.balancesAsked[len(e.txns.balancesAsked)-1]; !asked.Day.Equal(model.DateOf(tt.now)) {
				t.Errorf("the repo was asked about %s, want today as the clock reads it, %s", asked.Day.Format(time.DateOnly), model.DateOf(tt.now).Format(time.DateOnly))
			}
		})
	}
}

func TestGetBalancesAsOfADayCountsUpToThatDayWhateverDayItIsToday(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.cash, e.twd, "1000")
	e.confirmedLunchOn("lunch-tomorrow1", day.AddDate(0, 0, 1))
	e.confirmedLunchOn("lunch-nextweek", day.AddDate(0, 0, 7))

	// Today is the 14th. A day is the day it falls on as its own location reads it:
	// half past midnight on the 15th in Taipei is the 14th in UTC, and the 15th.
	asOf := func(t time.Time) usecase.GetBalancesInput { return usecase.GetBalancesInput{AsOf: &t} }
	tests := []struct {
		name string
		in   usecase.GetBalancesInput
		want string
	}{
		{"no day is today", usecase.GetBalancesInput{}, "1000"},
		{"the day before anything", asOf(day.AddDate(0, 0, -1)), ""},
		{"today named", asOf(day), "1000"},
		{"the 15th", asOf(day.AddDate(0, 0, 1)), "880"},
		{"the 15th named in Taipei just after midnight", asOf(time.Date(2026, 11, 15, 0, 30, 0, 0, taipei)), "880"},
		{"the 21st, after today", asOf(day.AddDate(0, 0, 7)), "760"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, found := e.cashOn(tt.in)

			if tt.want == "" {
				if found {
					t.Fatalf("balance = %+v, want none: nothing had happened yet", got)
				}
				return
			}
			if !found || !got.Quantity.Equal(dec(tt.want)) {
				t.Fatalf("balance = %+v, %v, want %s", got, found, tt.want)
			}
		})
	}
}

func TestGetBalancesListsTheUnbilledInstalmentNextToWhatTheAccountHolds(t *testing.T) {
	e := newEnv(t)
	origin := e.recordPlan("laptop-00000001")
	e.confirm(origin)

	out, err := e.svc.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &e.bank.ID})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}

	// The card has been charged nothing, and still has a row: all that there is of it
	// is the 12,000 that is owed and not yet due.
	if len(out.Balances) != 1 || out.Balances[0].InstrumentID != e.twd.ID {
		t.Fatalf("balances = %+v, want the one of the card in TWD", out.Balances)
	}
	if got := out.Balances[0]; !got.Quantity.IsZero() || !got.UnbilledInstalment.Equal(dec("-12000")) {
		t.Errorf("balance = %+v, want nothing held and -12000 of unbilled instalment", got)
	}

	// On the day the first period falls due it reaches the balance, with its interest.
	e.clock.now = time.Date(2026, 12, 5, 9, 0, 0, 0, taipei)
	out, err = e.svc.GetBalances(context.Background(), usecase.GetBalancesInput{AccountID: &e.bank.ID})
	if err != nil {
		t.Fatalf("GetBalances: %v", err)
	}
	if got := out.Balances[0]; !got.Quantity.Equal(dec("-1030")) || !got.UnbilledInstalment.Equal(dec("-11000")) {
		t.Errorf("balance on the first due day = %+v, want -1030 held and -11000 of unbilled instalment", got)
	}
}

func TestGetDealingsCountsUpToTodayOrTheDayAsOfNames(t *testing.T) {
	e := newEnv(t)
	friend := e.refs.addParty("Amy", false)
	asOf := time.Date(2026, 12, 1, 0, 30, 0, 0, taipei)

	if _, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &friend}); err != nil {
		t.Fatalf("GetDealings: %v", err)
	}
	if !e.txns.dealingsDay.Equal(day) {
		t.Errorf("the repo was asked about %s, want today, 2026-11-14", e.txns.dealingsDay.Format(time.DateOnly))
	}

	if _, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{PartyID: &friend, AsOf: &asOf}); err != nil {
		t.Fatalf("GetDealings as of a day: %v", err)
	}
	if want := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC); !e.txns.dealingsDay.Equal(want) {
		t.Errorf("the repo was asked about %s, want the day named, as its own location reads it, 2026-12-01", e.txns.dealingsDay.Format(time.DateOnly))
	}

	e.clock.now = time.Date(2027, 1, 2, 0, 5, 0, 0, taipei)
	e.refs.addParty("Bob", false)
	if _, err := e.svc.GetDealings(context.Background(), usecase.GetDealingsInput{}); err != nil {
		t.Fatalf("GetDealings of everyone: %v", err)
	}
	if want := time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC); !e.txns.dealingsDay.Equal(want) {
		t.Errorf("the repo was asked about %s after the clock moved, want 2027-01-02", e.txns.dealingsDay.Format(time.DateOnly))
	}
}

// Reconcile compares what an account holds on the day it is observed, whatever
// day it is today, and counts the account postings only.
func TestReconcileCountsTheDayOfTheObservationAndNotTodayAndNotWhatIsNotYetCharged(t *testing.T) {
	e := newEnv(t)
	e.confirmedOpening("opening-0000001", e.bank, e.twd, "-5000")
	origin := e.recordPlan("laptop-00000001")
	e.confirm(origin) // 12,000 unbilled on the bank: not what it holds

	// Observed on the day of the tests, which is also today.
	out, err := e.svc.Reconcile(context.Background(), e.reconcileOf("reconcile-00001", e.bank, e.twd, "-5000"))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !out.RecordedBalance.Equal(dec("-5000")) || !out.Difference.IsZero() || out.Transaction != nil {
		t.Fatalf("output = %+v, want recorded -5000 with nothing to reconcile: the unbilled instalment is not held", out)
	}

	// Observed on a day in the future of today, after the first period fell due.
	later := e.reconcileOf("reconcile-00002", e.bank, e.twd, "-6030")
	later.OccurredOn = time.Date(2026, 12, 5, 0, 0, 0, 0, time.UTC)
	out, err = e.svc.Reconcile(context.Background(), later)
	if err != nil {
		t.Fatalf("Reconcile on a later day: %v", err)
	}
	if !out.RecordedBalance.Equal(dec("-6030")) || !out.Difference.IsZero() {
		t.Errorf("output = %+v, want what the card holds once the first period fell due, -5000 and -1030: -6030", out)
	}
	if got := e.held(e.bank, e.twd, later.OccurredOn.AddDate(0, 0, -1)); !got.Equal(dec("-5000")) {
		t.Errorf("the day before the period, the card holds %s, want -5000", got)
	}
}
