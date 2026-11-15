package model_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
)

// on is a day, as a date at midnight UTC.
func on(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// monthly returns count periods of the principal, falling due on the 5th of each
// month from December 2026, each with a new id.
func monthly(count int, principal string) []model.InstalmentPeriod {
	periods := make([]model.InstalmentPeriod, count)
	for i := range periods {
		periods[i] = model.InstalmentPeriod{
			ID:        uuid.Must(uuid.NewV7()),
			DueOn:     on(2026, time.Month(12+i), 5),
			Principal: dec(principal),
		}
	}
	return periods
}

// instalmentFixtures are what the plans below are made of.
type instalmentFixtures struct {
	card, bank       model.Account
	twd, usd, btc    model.Instrument
	electronics      model.Category
	interest         model.Category
	salary           model.Category
	names            map[uuid.UUID]string
	expenseParams    func(key string, amount string) model.ExpenseParams
	billParams       func(key string, principal string, periods []model.InstalmentPeriod) model.BillInstalmentParams
	interestSchedule func(periods []model.InstalmentPeriod) model.InstalmentSchedule
}

func newInstalmentFixtures(t *testing.T) instalmentFixtures {
	t.Helper()
	f := instalmentFixtures{
		card:        newCash(t, "卡"),
		bank:        newCash(t, "銀行"),
		twd:         newTWD(t),
		usd:         newCurrency(t, "USD", 2),
		btc:         newNonCurrency(t, "BTC", model.InstrumentKindCrypto, 8),
		electronics: topLevel(t, model.DirectionExpense, "3C"),
		interest:    topLevel(t, model.DirectionExpense, "利息"),
		salary:      topLevel(t, model.DirectionIncome, "薪水"),
	}
	f.names = map[uuid.UUID]string{
		f.card.ID: "卡", f.bank.ID: "銀行", f.twd.ID: "TWD", f.usd.ID: "USD", f.btc.ID: "BTC",
		f.electronics.ID: "3C", f.interest.ID: "利息", f.salary.ID: "薪水",
	}
	f.expenseParams = func(key, amount string) model.ExpenseParams {
		return model.ExpenseParams{
			Header: header(key), Account: &f.card, Instrument: f.twd, BaseCurrency: f.twd,
			Lines: []model.CategoryLine{{Category: f.electronics, Amount: dec(amount)}},
		}
	}
	f.billParams = func(key, principal string, periods []model.InstalmentPeriod) model.BillInstalmentParams {
		return model.BillInstalmentParams{
			Header: header(key), Account: f.card, Instrument: f.twd, BaseCurrency: f.twd,
			Principal: dec(principal), Schedule: f.interestSchedule(periods),
		}
	}
	f.interestSchedule = func(periods []model.InstalmentPeriod) model.InstalmentSchedule {
		return model.InstalmentSchedule{Periods: periods, InterestCategory: &f.interest}
	}
	return f
}

// wantPeriod checks what every period transaction of a plan has in common: the
// kind, the plan and the position it is in, the day, the key, and that it is a
// draft.
func wantPeriod(t *testing.T, plan model.InstalmentPlan, i int, period model.InstalmentPeriod) {
	t.Helper()
	got, seq := plan.Periods[i], i+1
	if got.ID != period.ID || got.Kind != model.KindInstalment || got.Status != model.StatusDraft {
		t.Errorf("period %d: id %s, kind %q, status %q, want the id given, an instalment, a draft", seq, got.ID, got.Kind, got.Status)
	}
	if got.PlanID == nil || *got.PlanID != plan.Origin.ID || got.PlanSeq == nil || *got.PlanSeq != seq {
		t.Errorf("period %d: plan %v, position %v, want plan %s, position %d", seq, got.PlanID, got.PlanSeq, plan.Origin.ID, seq)
	}
	if !got.OccurredOn.Equal(model.DateOf(period.DueOn)) {
		t.Errorf("period %d falls on %s, want %s", seq, got.OccurredOn.Format(time.DateOnly), period.DueOn.Format(time.DateOnly))
	}
	if got.IdempotencyKey != model.PeriodIdempotencyKey(plan.Origin.ID, seq) {
		t.Errorf("period %d: key %q, want the one made of the plan and the position", seq, got.IdempotencyKey)
	}
}

// Postings of the example of the design: 12,000 TWD on the card, 12 periods of
// 1,000 on the 5th of each month, and 30 of interest with the first.
func TestNewInstalmentExpenseMakesTheExpenseTheOriginAndChargesTheInstalmentsOnTheirDays(t *testing.T) {
	f := newInstalmentFixtures(t)
	periods := monthly(12, "1000")
	periods[0].Interest = decPtr("30")

	at := time.Date(2026, 11, 14, 12, 30, 0, 0, time.UTC)
	project, area, tag := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	params := f.expenseParams("laptop-00000001", "12000")
	params.OccurredAt = &at
	params.Description, params.Merchant = "筆電", "Apple"
	params.ProjectID, params.AreaID, params.TagIDs = &project, &area, []uuid.UUID{tag}

	plan, err := model.NewInstalmentExpense(params, f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	// The origin is the expense of the purchase: all of it, on the day of the
	// purchase. The account does not pay it but owes it, and is not charged yet.
	wantPostings(t, plan.Origin, f.names,
		"instalment(卡) -12000 TWD",
		"expense(3C) +12000 TWD, base +12000",
	)
	origin := plan.Origin
	if origin.Kind != model.KindExpense || origin.Status != model.StatusDraft || origin.IdempotencyKey != "laptop-00000001" {
		t.Errorf("origin: kind %q, status %q, key %q, want a draft expense with the key of the request", origin.Kind, origin.Status, origin.IdempotencyKey)
	}
	if origin.PlanID == nil || *origin.PlanID != origin.ID || origin.PlanSeq == nil || *origin.PlanSeq != 0 {
		t.Errorf("origin: plan %v, position %v, want the plan to be its own id and the position 0", origin.PlanID, origin.PlanSeq)
	}
	if !origin.OccurredOn.Equal(on(2026, time.November, 14)) {
		t.Errorf("origin falls on %s, want the day of the purchase, 2026-11-14", origin.OccurredOn.Format(time.DateOnly))
	}

	// The first period charges 1,000 of principal and 30 of interest to the card:
	// the principal stops being unbilled and is not an expense, the interest is one.
	wantPostings(t, plan.Periods[0], f.names,
		"instalment(卡) +1000 TWD",
		"account(卡) -1030 TWD",
		"expense(利息) +30 TWD, base +30",
	)
	// The others have only the principal.
	for _, i := range []int{1, 5, 11} {
		wantPostings(t, plan.Periods[i], f.names,
			"instalment(卡) +1000 TWD",
			"account(卡) -1000 TWD",
		)
	}
	if len(plan.Periods) != 12 {
		t.Fatalf("%d periods, want 12", len(plan.Periods))
	}
	for i, period := range periods {
		wantPeriod(t, plan, i, period)
		got := plan.Periods[i]
		// What a period carries over from the origin: it is one purchase, so it is of the
		// project and the area of the purchase, and not of what is going on on its day.
		if got.Description != "筆電" || got.Merchant != "Apple" || got.ProjectID == nil || *got.ProjectID != project ||
			got.AreaID == nil || *got.AreaID != area || !slices.Equal(got.TagIDs, []uuid.UUID{tag}) {
			t.Errorf("period %d: %q, %q, project %v, area %v, tags %v, want the texts, project, area and tags of the origin", i+1,
				got.Description, got.Merchant, got.ProjectID, got.AreaID, got.TagIDs)
		}
		if got.OccurredAt != nil {
			t.Errorf("period %d happened at %v, want no moment: only the day it falls due is known", i+1, got.OccurredAt)
		}
		if err := got.Validate(); err != nil {
			t.Errorf("period %d: Validate: %v", i+1, err)
		}
	}
	// The principal that is owed at the start is charged to the last cent by the
	// periods: nothing is left unbilled when the last has fallen due.
	total := dec("0")
	for _, txn := range plan.Transactions() {
		for _, p := range txn.Postings {
			if p.Book == model.BookInstalment {
				total = total.Add(p.Quantity)
			}
		}
	}
	if !total.IsZero() {
		t.Errorf("instalment postings of the plan add up to %s, want 0: the periods charge the 12000 that the origin owes", total)
	}
	if got := plan.Transactions(); len(got) != 13 || got[0].ID != origin.ID || got[1].ID != periods[0].ID || got[12].ID != periods[11].ID {
		t.Errorf("Transactions has %d, want the origin and then each period in order, 13", len(got))
	}
	// The periods and the origin keep what they were made of to themselves.
	plan.Periods[0].TagIDs[0] = uuid.Nil
	if plan.Periods[1].TagIDs[0] == uuid.Nil || plan.Origin.TagIDs[0] == uuid.Nil {
		t.Error("changing the tags of one period changed another transaction's: they share a slice")
	}
}

func TestNewInstalmentExpenseInAForeignCurrencyHasTheBaseAmountsOfTheExpenseAndOfEachInterest(t *testing.T) {
	f := newInstalmentFixtures(t)
	periods := []model.InstalmentPeriod{
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2026, time.December, 5), Principal: dec("25"), Interest: decPtr("1.50"), InterestBaseAmount: decPtr("48")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.January, 5), Principal: dec("25")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.February, 5), Principal: dec("25"), Interest: decPtr("1.25"), InterestBaseAmount: decPtr("40.25")},
		{ID: uuid.Must(uuid.NewV7()), DueOn: on(2027, time.March, 5), Principal: dec("25")},
	}

	plan, err := model.NewInstalmentExpense(model.ExpenseParams{
		Header: header(testKey), Account: &f.card, Instrument: f.usd, BaseCurrency: f.twd,
		Lines: []model.CategoryLine{{Category: f.electronics, Amount: dec("100"), BaseAmount: decPtr("3200")}},
	}, f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	// The expense has the base amount of the day of the purchase, as an expense of
	// any other kind has. The interest has its own, of the day it is charged.
	wantPostings(t, plan.Origin, f.names,
		"instalment(卡) -100 USD",
		"expense(3C) +100 USD, base +3200",
	)
	wantPostings(t, plan.Periods[0], f.names,
		"instalment(卡) +25 USD",
		"account(卡) -26.5 USD",
		"expense(利息) +1.5 USD, base +48",
	)
	wantPostings(t, plan.Periods[1], f.names, "instalment(卡) +25 USD", "account(卡) -25 USD")
	wantPostings(t, plan.Periods[2], f.names,
		"instalment(卡) +25 USD",
		"account(卡) -26.25 USD",
		"expense(利息) +1.25 USD, base +40.25",
	)
	wantPostings(t, plan.Periods[3], f.names, "instalment(卡) +25 USD", "account(卡) -25 USD")
}

func TestNewInstalmentExpenseSplitsWhatTheAccountWouldHavePaidTheSharesIncluded(t *testing.T) {
	f := newInstalmentFixtures(t)
	amy := newParty()
	f.names[amy] = "Amy"
	params := f.expenseParams(testKey, "300")
	params.Shares = []model.Share{{PartyID: amy, Amount: dec("300")}}

	plan, err := model.NewInstalmentExpense(params, f.interestSchedule(monthly(3, "200")))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	// The card is on the hook for the 600, my 300 and the 300 Amy owes me, which the
	// three periods of 200 charge. Only my share is an expense.
	wantPostings(t, plan.Origin, f.names,
		"instalment(卡) -600 TWD",
		"expense(3C) +300 TWD, base +300",
		"dealing(Amy) +300 TWD",
	)
	for i := range plan.Periods {
		wantPostings(t, plan.Periods[i], f.names, "instalment(卡) +200 TWD", "account(卡) -200 TWD")
	}
}

func TestNewInstalmentExpenseRefusesWhatCannotBeAPlanAndMakesNothing(t *testing.T) {
	f := newInstalmentFixtures(t)
	party := newParty()
	system := unrecordedCategory(model.DirectionExpense)
	withInterest := func(periods []model.InstalmentPeriod, interest string) []model.InstalmentPeriod {
		periods[0].Interest = decPtr(interest)
		return periods
	}

	tests := []struct {
		name     string
		params   func() model.ExpenseParams
		schedule func() model.InstalmentSchedule
		want     error
	}{
		{"no periods", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(nil) }, model.ErrNoPeriods},
		{"principals that add up to less", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "999")) }, model.ErrPrincipalMismatch},
		{"principals that add up to more", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000.01")) }, model.ErrPrincipalMismatch},
		{"periods that fall due on the same day", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1000")
				periods[2].DueOn = periods[1].DueOn.Add(10 * time.Hour)
				return f.interestSchedule(periods)
			}, model.ErrDueDatesNotIncreasing},
		{"periods that fall due the wrong way round", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1000")
				periods[0], periods[1] = periods[1], periods[0]
				return f.interestSchedule(periods)
			}, model.ErrDueDatesNotIncreasing},
		{"a first period before the purchase", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1000")
				periods[0].DueOn = on(2026, time.November, 13)
				return f.interestSchedule(periods)
			}, model.ErrFirstDueTooEarly},
		{"a period without a day", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1000")
				periods[1].DueOn = time.Time{}
				return f.interestSchedule(periods)
			}, model.ErrDateRequired},
		{"interest without a category", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				return model.InstalmentSchedule{Periods: withInterest(monthly(3, "1000"), "30")}
			}, model.ErrInterestCategoryRequired},
		{"an interest category of incomes", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				return model.InstalmentSchedule{Periods: withInterest(monthly(3, "1000"), "30"), InterestCategory: &f.salary}
			}, model.ErrWrongCategoryDirection},
		{"a system category for the interest", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				return model.InstalmentSchedule{Periods: withInterest(monthly(3, "1000"), "30"), InterestCategory: &system}
			}, model.ErrSystemCategory},
		{"an archived interest category", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				archived := archivedCategory(f.interest)
				return model.InstalmentSchedule{Periods: withInterest(monthly(3, "1000"), "30"), InterestCategory: &archived}
			}, model.ErrArchived},
		{"a period of nothing", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "0")) }, model.ErrAmountNotPositive},
		{"a negative principal", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1500")
				periods[1].Principal = dec("-500")
				return f.interestSchedule(periods)
			}, model.ErrAmountNotPositive},
		{"interest of nothing", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(3, "1000"), "0")) }, model.ErrAmountNotPositive},
		{"negative interest", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(3, "1000"), "-5")) }, model.ErrAmountNotPositive},
		{"a principal finer than the currency", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000.001")) }, model.ErrTooManyDecimals},
		{"interest finer than the currency", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(3, "1000"), "1.234")) }, model.ErrTooManyDecimals},
		{"a base amount of the interest that is not the interest, in the base currency", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := withInterest(monthly(3, "1000"), "30")
				periods[0].InterestBaseAmount = decPtr("31")
				return f.interestSchedule(periods)
			}, model.ErrBaseAmountMismatch},
		{"a base amount of interest that there is none of", func() model.ExpenseParams { return f.expenseParams(testKey, "3000") },
			func() model.InstalmentSchedule {
				periods := monthly(3, "1000")
				periods[1].InterestBaseAmount = decPtr("30")
				return f.interestSchedule(periods)
			}, model.ErrInterestBaseAmountWithoutInterest},
		{"interest in a foreign currency without its base amount",
			func() model.ExpenseParams {
				p := f.expenseParams(testKey, "30")
				p.Instrument = f.usd
				p.Lines[0].BaseAmount = decPtr("960")
				return p
			},
			func() model.InstalmentSchedule { return f.interestSchedule(withInterest(monthly(3, "10"), "1")) }, model.ErrBaseAmountRequired},
		{"an expense that a party paid", func() model.ExpenseParams {
			p := f.expenseParams(testKey, "3000")
			p.Account, p.PaidBy = nil, &party
			return p
		}, func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000")) }, model.ErrInstalmentsWithPaidBy},
		{"an expense that a party paid and an account too", func() model.ExpenseParams {
			p := f.expenseParams(testKey, "3000")
			p.PaidBy = &party
			return p
		}, func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000")) }, model.ErrInstalmentsWithPaidBy},
		{"no account to charge", func() model.ExpenseParams {
			p := f.expenseParams(testKey, "3000")
			p.Account = nil
			return p
		}, func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000")) }, model.ErrNoPayer},
		{"an instrument that is not a currency", func() model.ExpenseParams {
			p := f.expenseParams(testKey, "3000")
			p.Instrument = f.btc
			return p
		}, func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000")) }, model.ErrNotCurrency},
		{"an archived account", func() model.ExpenseParams {
			p := f.expenseParams(testKey, "3000")
			archived := archivedAccount(f.card)
			p.Account = &archived
			return p
		}, func() model.InstalmentSchedule { return f.interestSchedule(monthly(3, "1000")) }, model.ErrArchived},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := model.NewInstalmentExpense(tt.params(), tt.schedule())

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if plan.Origin.ID != uuid.Nil || plan.Periods != nil {
				t.Errorf("plan = %+v, want nothing made of a request that is refused", plan)
			}
		})
	}
}

func TestAPlanMayStartOnTheDayOfItsOrigin(t *testing.T) {
	f := newInstalmentFixtures(t)
	periods := monthly(2, "500")
	periods[0].DueOn = on(2026, time.November, 14) // the day of the purchase

	plan, err := model.NewInstalmentExpense(f.expenseParams(testKey, "1000"), f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	if !plan.Periods[0].OccurredOn.Equal(plan.Origin.OccurredOn) {
		t.Errorf("first period on %s, origin on %s, want the same day", plan.Periods[0].OccurredOn, plan.Origin.OccurredOn)
	}
}

func TestADueDayIsTheDayItsOwnLocationReads(t *testing.T) {
	f := newInstalmentFixtures(t)
	periods := monthly(2, "500")
	// 00:30 on the 5th in Taipei is still the 4th in UTC: the day is the 5th.
	periods[0].DueOn = time.Date(2026, time.December, 5, 0, 30, 0, 0, taipei)

	plan, err := model.NewInstalmentExpense(f.expenseParams(testKey, "1000"), f.interestSchedule(periods))
	if err != nil {
		t.Fatalf("NewInstalmentExpense: %v", err)
	}

	if got := plan.Periods[0].OccurredOn; !got.Equal(on(2026, time.December, 5)) {
		t.Errorf("first period falls on %s, want 2026-12-05", got)
	}
}

// 帳單分期: what an account already owes is turned into a plan, on the day of the
// conversion. It is not an expense.
func TestNewBillInstalmentPaysOffTheDebtAndOwesItAgainAsPrincipalThatIsNotChargedYet(t *testing.T) {
	f := newInstalmentFixtures(t)
	periods := monthly(12, "1000")
	periods[0].Interest = decPtr("30")

	plan, err := model.NewBillInstalment(f.billParams("bill-instalment1", "12000", periods))
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}

	wantPostings(t, plan.Origin, f.names,
		"account(卡) +12000 TWD",
		"instalment(卡) -12000 TWD",
	)
	if plan.Origin.Kind != model.KindInstalment || plan.Origin.Status != model.StatusDraft {
		t.Errorf("origin: kind %q, status %q, want a draft instalment, not an expense", plan.Origin.Kind, plan.Origin.Status)
	}
	if plan.Origin.PlanID == nil || *plan.Origin.PlanID != plan.Origin.ID || plan.Origin.PlanSeq == nil || *plan.Origin.PlanSeq != 0 {
		t.Errorf("origin: plan %v, position %v, want its own id and the position 0", plan.Origin.PlanID, plan.Origin.PlanSeq)
	}
	wantPostings(t, plan.Periods[0], f.names,
		"instalment(卡) +1000 TWD",
		"account(卡) -1030 TWD",
		"expense(利息) +30 TWD, base +30",
	)
	wantPostings(t, plan.Periods[11], f.names, "instalment(卡) +1000 TWD", "account(卡) -1000 TWD")
	for i, period := range periods {
		wantPeriod(t, plan, i, period)
	}

	// No expense but the interest: the principal was an expense when the debt was
	// made, and paying it off in periods is not another one.
	expenses := dec("0")
	for _, txn := range plan.Transactions() {
		for _, p := range txn.Postings {
			if p.Book == model.BookExpense {
				expenses = expenses.Add(p.Quantity)
			}
		}
	}
	if !expenses.Equal(dec("30")) {
		t.Errorf("expenses of the plan add up to %s, want the 30 of interest and nothing for the principal", expenses)
	}
}

func TestNewBillInstalmentHasTheFeesOfTheConversionAfterItsOwnPostings(t *testing.T) {
	f := newInstalmentFixtures(t)
	fees := topLevel(t, model.DirectionExpense, "手續費")
	f.names[fees.ID] = "手續費"
	params := f.billParams(testKey, "3000", monthly(3, "1000"))
	params.Fees = []model.Fee{{Account: f.bank, Instrument: f.twd, Category: fees, Amount: dec("100")}}

	plan, err := model.NewBillInstalment(params)
	if err != nil {
		t.Fatalf("NewBillInstalment: %v", err)
	}

	wantPostings(t, plan.Origin, f.names,
		"account(卡) +3000 TWD",
		"instalment(卡) -3000 TWD",
		"account(銀行) -100 TWD",
		"expense(手續費) +100 TWD, base +100",
	)
}

func TestNewBillInstalmentRefusesWhatCannotBeAPlanAndMakesNothing(t *testing.T) {
	f := newInstalmentFixtures(t)
	tests := []struct {
		name string
		make func() model.BillInstalmentParams
		want error
	}{
		{"principals that do not add up to the debt", func() model.BillInstalmentParams {
			return f.billParams(testKey, "3000", monthly(3, "900"))
		}, model.ErrPrincipalMismatch},
		{"a debt of nothing", func() model.BillInstalmentParams {
			return f.billParams(testKey, "0", monthly(3, "0"))
		}, model.ErrAmountNotPositive},
		{"no periods", func() model.BillInstalmentParams {
			return f.billParams(testKey, "3000", nil)
		}, model.ErrNoPeriods},
		{"a first period before the conversion", func() model.BillInstalmentParams {
			periods := monthly(3, "1000")
			periods[0].DueOn = on(2026, time.November, 13)
			return f.billParams(testKey, "3000", periods)
		}, model.ErrFirstDueTooEarly},
		{"periods that do not fall due on later days", func() model.BillInstalmentParams {
			periods := monthly(3, "1000")
			periods[2].DueOn = periods[1].DueOn
			return f.billParams(testKey, "3000", periods)
		}, model.ErrDueDatesNotIncreasing},
		{"interest without a category", func() model.BillInstalmentParams {
			periods := monthly(3, "1000")
			periods[0].Interest = decPtr("30")
			p := f.billParams(testKey, "3000", periods)
			p.Schedule.InterestCategory = nil
			return p
		}, model.ErrInterestCategoryRequired},
		{"a debt that is not in a currency", func() model.BillInstalmentParams {
			p := f.billParams(testKey, "3000", monthly(3, "1000"))
			p.Instrument = f.btc
			return p
		}, model.ErrNotCurrency},
		{"an archived account", func() model.BillInstalmentParams {
			p := f.billParams(testKey, "3000", monthly(3, "1000"))
			p.Account = archivedAccount(f.card)
			return p
		}, model.ErrArchived},
		{"a fee booked to an income category", func() model.BillInstalmentParams {
			p := f.billParams(testKey, "3000", monthly(3, "1000"))
			p.Fees = []model.Fee{{Account: f.bank, Instrument: f.twd, Category: f.salary, Amount: dec("100")}}
			return p
		}, model.ErrWrongCategoryDirection},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := model.NewBillInstalment(tt.make())

			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if plan.Origin.ID != uuid.Nil || plan.Periods != nil {
				t.Errorf("plan = %+v, want nothing made of a request that is refused", plan)
			}
		})
	}
}

func TestAPeriodsKeyIsMadeOfThePlanAndThePositionAndOfNothingElse(t *testing.T) {
	plan := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())

	keys := map[string]string{}
	for _, id := range []uuid.UUID{plan, other} {
		for seq := 1; seq <= 120; seq++ {
			key := model.PeriodIdempotencyKey(id, seq)
			if again := model.PeriodIdempotencyKey(id, seq); again != key {
				t.Fatalf("the key of period %d of %s is %q, and then %q: it must come out the same every time", seq, id, key, again)
			}
			// It is the ledger's own: a request that gave it would be answered with the
			// period, so it is refused wherever a caller gives a key. It is a key the
			// table holds all the same, which is not shorter or longer than any other (the
			// characters are all ASCII).
			if _, err := model.NormalizeIdempotencyKey(key); !errors.Is(err, model.ErrReservedIdempotencyKey) {
				t.Fatalf("the key %q of period %d is taken from a caller: err = %v, want ErrReservedIdempotencyKey", key, seq, err)
			}
			if n := len(key); n < model.MinKeyRunes || n > model.MaxKeyRunes {
				t.Fatalf("the key %q of period %d is %d characters, want %d to %d", key, seq, n, model.MinKeyRunes, model.MaxKeyRunes)
			}
			if !strings.Contains(key, id.String()) {
				t.Errorf("the key %q does not name its plan %s", key, id)
			}
			if previous, taken := keys[key]; taken {
				t.Fatalf("the key %q is that of %s and of period %d of %s", key, previous, seq, id)
			}
			keys[key] = fmt.Sprintf("period %d of %s", seq, id)
		}
	}
}

func TestNormalizeIdempotencyKeyRefusesTheFormOfTheKeysOfPeriodsAndNoOther(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		reserved bool
	}{
		{"the key of a period", model.PeriodIdempotencyKey(uuid.Must(uuid.NewV7()), 3), true},
		{"the form with anything after it", "instalment:laptop-0001", true},
		{"the form with whitespace around it, which is trimmed first", "  \tinstalment:laptop-0001\n", true},
		{"the form alone", "instalment:", true},
		{"the word without the colon", "instalment-laptop-0001", false},
		{"the form after something else", "laptop-instalment:0001", false},
		{"the form in another case, which is another key to the table", "Instalment:laptop-0001", false},
		{"an ordinary key", "lunch-2026-11-14-a", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := model.NormalizeIdempotencyKey(tt.key)

			if !tt.reserved {
				if err != nil || got != strings.TrimSpace(tt.key) {
					t.Fatalf("NormalizeIdempotencyKey(%q) = %q, %v, want the key trimmed", tt.key, got, err)
				}
				return
			}
			if !errors.Is(err, model.ErrReservedIdempotencyKey) || got != "" {
				t.Fatalf("NormalizeIdempotencyKey(%q) = %q, %v, want ErrReservedIdempotencyKey", tt.key, got, err)
			}
			if !strings.Contains(err.Error(), "instalment:") {
				t.Errorf("err = %q, want it to say what form is reserved", err)
			}
		})
	}
}

func TestTheBuildersRefuseAKeyInTheFormOfThoseOfPeriods(t *testing.T) {
	f := newInstalmentFixtures(t)
	key := model.PeriodIdempotencyKey(uuid.Must(uuid.NewV7()), 1)

	if _, err := model.NewExpense(f.expenseParams(key, "120")); !errors.Is(err, model.ErrReservedIdempotencyKey) {
		t.Errorf("NewExpense: err = %v, want ErrReservedIdempotencyKey", err)
	}
	if _, err := model.NewInstalmentExpense(f.expenseParams(key, "12000"), f.interestSchedule(monthly(12, "1000"))); !errors.Is(err, model.ErrReservedIdempotencyKey) {
		t.Errorf("NewInstalmentExpense: err = %v, want ErrReservedIdempotencyKey", err)
	}
	if _, err := model.NewBillInstalment(f.billParams(key, "12000", monthly(12, "1000"))); !errors.Is(err, model.ErrReservedIdempotencyKey) {
		t.Errorf("NewBillInstalment: err = %v, want ErrReservedIdempotencyKey", err)
	}
}

func TestAnInstalmentPlanSummaryIsFinishedWhenNothingIsLeftToCharge(t *testing.T) {
	tests := []struct {
		name               string
		principal, charged string
		wantRemaining      string
		wantFinished       bool
	}{
		{"nothing charged yet", "12000", "0", "12000", false},
		{"some charged", "12000", "1000", "11000", false},
		{"all but a cent charged", "12000", "11999.99", "0.01", false},
		{"all charged", "12000", "12000", "0", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := model.InstalmentPlanSummary{Principal: dec(tt.principal), Charged: dec(tt.charged)}

			if got := s.Remaining(); !got.Equal(dec(tt.wantRemaining)) {
				t.Errorf("remaining = %s, want %s", got, tt.wantRemaining)
			}
			if got := s.Finished(); got != tt.wantFinished {
				t.Errorf("Finished = %v, want %v", got, tt.wantFinished)
			}
		})
	}
}
