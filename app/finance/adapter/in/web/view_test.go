package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

func TestRoleOf(t *testing.T) {
	tests := []struct {
		kind string
		p    usecase.Posting
		want string
	}{
		{"expense", usecase.Posting{Book: "account"}, "帳戶"},
		{"expense", usecase.Posting{Book: "expense"}, "主要金額"},
		{"expense", usecase.Posting{Book: "dealing", Quantity: dec("300")}, "分攤"},
		{"expense", usecase.Posting{Book: "dealing", Quantity: dec("-300")}, "往來（我欠對方）"},
		{"expense", usecase.Posting{Book: "instalment", Quantity: dec("-300")}, "未到期分期"},
		{"income", usecase.Posting{Book: "income"}, "主要金額"},
		// An expense posting of any other kind is a fee; of an instalment, interest; of a
		// reconciliation, what was not recorded.
		{"transfer", usecase.Posting{Book: "expense"}, "手續費"},
		{"exchange", usecase.Posting{Book: "expense"}, "手續費"},
		{"income", usecase.Posting{Book: "expense"}, "手續費"},
		{"dealing", usecase.Posting{Book: "expense"}, "手續費"},
		{"instalment", usecase.Posting{Book: "expense"}, "利息"},
		{"reconciliation", usecase.Posting{Book: "expense"}, "未記錄"},
		{"reconciliation", usecase.Posting{Book: "income"}, "未記錄"},
		{"reconciliation", usecase.Posting{Book: "adjustment"}, "數量修正"},
		// A dealing of the kind dealing is a dealing, whichever way it goes.
		{"dealing", usecase.Posting{Book: "dealing", Quantity: dec("5")}, "往來"},
		{"dealing", usecase.Posting{Book: "dealing", Quantity: dec("-5")}, "往來"},
		{"reward", usecase.Posting{Book: "reward"}, "回饋"},
		{"exchange", usecase.Posting{Book: "conversion"}, "中介（換匯、買賣）"},
		{"opening", usecase.Posting{Book: "opening"}, "期初餘額"},
		// A book that is not known is shown as it is, not hidden.
		{"expense", usecase.Posting{Book: "something-new"}, "something-new"},
	}
	for _, tt := range tests {
		if got := roleOf(tt.kind, tt.p); got != tt.want {
			t.Errorf("roleOf(%s, %s %s) = %q, want %q", tt.kind, tt.p.Book, tt.p.Quantity, got, tt.want)
		}
	}
}

func TestMovementsAreAccountsFirstThenWhatElseWasDone(t *testing.T) {
	r := &refs{
		instruments: map[uuid.UUID]usecase.Instrument{twd: {ID: twd, Code: "TWD", Decimals: 2}},
		accountByID: map[uuid.UUID]usecase.Account{card: {ID: card, Name: "信用卡"}, cash: {ID: cash, Name: "現金"}},
		names:       map[uuid.UUID]string{bob: "Bob"},
	}
	tests := []struct {
		name string
		t    usecase.Transaction
		want []string
	}{
		{"an expense", transaction(seqID(1), "expense", "confirmed", date(2026, 11, 1),
			accountPosting(1, cash, twd, "-120"), expensePosting(2, lunch, twd, "120", "120")), []string{"現金 −120.00"}},
		{"the origin of an instalment plan moves no account", transaction(seqID(2), "expense", "confirmed", date(2026, 11, 1),
			usecase.Posting{Seq: 1, Book: "instalment", AccountID: &card, InstrumentID: twd, Quantity: dec("-3000")}, expensePosting(2, rent, twd, "3000", "3000")),
			[]string{"信用卡・未到期分期 −3,000.00"}},
		{"what a party paid for me moves no account", transaction(seqID(3), "expense", "confirmed", date(2026, 11, 1),
			dealingPosting(1, bob, twd, "-300"), expensePosting(2, lunch, twd, "300", "300")), []string{"往來・Bob −300.00"}},
		{"a transfer moves two", transaction(seqID(4), "transfer", "confirmed", date(2026, 11, 1),
			accountPosting(1, cash, twd, "-10"), accountPosting(2, card, twd, "10")), []string{"現金 −10.00", "信用卡 +10.00"}},
		{"a posting that lost its account is not a failure", transaction(seqID(5), "transfer", "confirmed", date(2026, 11, 1),
			usecase.Posting{Seq: 1, Book: "account", InstrumentID: twd, Quantity: dec("10")}), []string{" +10.00"}},
		{"nothing", transaction(seqID(6), "opening", "confirmed", date(2026, 11, 1)), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, m := range movementsOf(r, tt.t) {
				got = append(got, fmt.Sprintf("%s %s", m.Where, m.Amount.Text))
			}
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("movements = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestADiscountIsAPartOfItsOwn(t *testing.T) {
	r := &refs{
		instruments: map[uuid.UUID]usecase.Instrument{twd: {ID: twd, Code: "TWD", Decimals: 2}},
		categories:  map[uuid.UUID]usecase.Category{food: {ID: food, Name: "飲食"}},
		accountByID: map[uuid.UUID]usecase.Account{cash: {ID: cash, Name: "現金"}},
	}
	discount := dec("50")
	b := dec("450")
	tx := transaction(seqID(1), "expense", "confirmed", date(2026, 11, 1),
		accountPosting(1, cash, twd, "-450"),
		usecase.Posting{Seq: 2, Book: "expense", CategoryID: &food, InstrumentID: twd, Quantity: dec("450"), BaseAmount: &b, Discount: &discount})
	var roles []string
	for _, p := range partsOf(r, tx) {
		roles = append(roles, p.Role+" "+p.Amount.Text)
	}
	want := []string{"帳戶 −450.00", "主要金額 +450.00", "折扣 50.00"}
	if fmt.Sprint(roles) != fmt.Sprint(want) {
		t.Errorf("parts = %q, want %q: what was paid is the expense, and the discount only a record beside it", roles, want)
	}
}

func TestTheChargesAreLaidOutAsTheUseCaseDividedThem(t *testing.T) {
	r := &refs{
		instruments: map[uuid.UUID]usecase.Instrument{
			twd: {ID: twd, Code: "TWD", Decimals: 2}, usd: {ID: usd, Code: "USD", Decimals: 2},
		},
		categories: map[uuid.UUID]usecase.Category{food: {ID: food, Name: "飲食"}, rent: {ID: rent, Name: "房租"}},
	}
	// What the use case says: the lines of each instrument in the order it gives, and the
	// figures that the lines add up to. The page does not add them up or put them in order.
	charges := []usecase.StatementCharges{
		{InstrumentID: twd, Categories: []usecase.CategoryCharge{{CategoryID: rent, Amount: dec("2000")}, {CategoryID: food, Amount: dec("600")}}, Shares: dec("600"), Other: dec("200")},
		{InstrumentID: usd, Categories: []usecase.CategoryCharge{{CategoryID: food, Amount: dec("20")}}},
	}
	figures := []usecase.StatementFigures{{InstrumentID: twd, Charges: dec("3400")}, {InstrumentID: usd, Charges: dec("20")}}

	got := chargesViews(r, charges, figures)

	if len(got) != 2 || got[0].Instrument != "TWD" || got[1].Instrument != "USD" {
		t.Fatalf("charges are of %v, want TWD and then USD", got)
	}
	var rows []string
	for _, row := range got[0].Rows {
		rows = append(rows, row.Label+" "+row.Amount.Text+" "+row.Share)
	}
	want := []string{
		"房租 2,000.00 58.8%",
		"飲食 600.00 17.6%",
		"分攤（我替別人付） 600.00 17.6%",
		"其他（轉帳、往來、分期本金等） 200.00 5.9%",
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Errorf("rows = %q, want %q", rows, want)
	}
	if got[0].Total.Text != "3,400.00" {
		t.Errorf("total = %s, want 3,400.00, which the figures say", got[0].Total.Text)
	}
	if got[1].Total.Text != "20.00" || len(got[1].Rows) != 1 || got[1].Rows[0].Label != "飲食" || got[1].Rows[0].Share != "100.0%" {
		t.Errorf("usd = %+v", got[1])
	}
	if got[0].Chart == "" || got[1].Chart == "" {
		t.Error("a chart is missing")
	}
}

func TestNoChargesIsNoChart(t *testing.T) {
	if got := chargesViews(&refs{}, nil, nil); len(got) != 0 {
		t.Errorf("charges = %+v, want none", got)
	}
}

func TestWhichStatementsAreShown(t *testing.T) {
	today := date(2026, time.November, 21)
	var statements []usecase.CardStatementSummary
	for i := -2; i < 16; i++ { // the newest first: two that have not begun, then the open one
		closing := date(2026, time.December, 5).AddDate(0, -i, 0)
		statements = append(statements, usecase.CardStatementSummary{
			From: closing.AddDate(0, -1, 1), ClosingOn: closing, Closed: closing.Before(today),
		})
	}
	got := shownStatements(statements, today)
	if len(got) != 1+closedStatements {
		t.Fatalf("%d statements shown, want the open one and %d closed ones", len(got), closedStatements)
	}
	if got[0].Closed || !got[0].ClosingOn.Equal(date(2026, time.December, 5)) {
		t.Errorf("the first is %+v, want the open one that closes on the 5th of December", got[0])
	}
	for _, s := range got[1:] {
		if !s.Closed {
			t.Errorf("%+v is not closed", s)
		}
	}
	if last := got[len(got)-1]; !last.ClosingOn.Equal(date(2025, time.December, 5)) {
		t.Errorf("the last is %v, want the twelfth closed one", last.ClosingOn)
	}

	// The day it closes the statement is still open; the day after, closed.
	onClosing := []usecase.CardStatementSummary{{From: date(2026, 10, 6), ClosingOn: date(2026, 11, 5)}}
	if got := shownStatements(onClosing, date(2026, 11, 5)); len(got) != 1 {
		t.Errorf("on its closing day the statement is not shown as open: %v", got)
	}
	if got := shownStatements(nil, today); got != nil {
		t.Errorf("shown = %v for no statements", got)
	}
}

func TestTheWindowOfTheTrend(t *testing.T) {
	current := date(2026, time.November, 1)
	tests := []struct {
		selected, wantFrom, wantTo time.Time
	}{
		{date(2026, 11, 1), date(2025, 12, 1), date(2026, 11, 1)},
		{date(2026, 3, 1), date(2025, 12, 1), date(2026, 11, 1)},
		{date(2025, 12, 1), date(2025, 12, 1), date(2026, 11, 1)}, // the first of the twelve
		{date(2025, 11, 1), date(2024, 12, 1), date(2025, 11, 1)}, // one month too early: it ends the window
		{date(2020, 1, 1), date(2019, 2, 1), date(2020, 1, 1)},
		{date(2026, 12, 1), date(2026, 1, 1), date(2026, 12, 1)}, // a month to come
	}
	for _, tt := range tests {
		from, to := trendWindow(tt.selected, current)
		if !from.Equal(tt.wantFrom) || !to.Equal(tt.wantTo) {
			t.Errorf("trendWindow(%s) = %s to %s, want %s to %s", tt.selected.Format("2006-01"), from.Format("2006-01"), to.Format("2006-01"), tt.wantFrom.Format("2006-01"), tt.wantTo.Format("2006-01"))
		}
		if n := (to.Year()-from.Year())*12 + int(to.Month()) - int(from.Month()) + 1; n != trendMonths {
			t.Errorf("trendWindow(%s) has %d months", tt.selected.Format("2006-01"), n)
		}
		if tt.selected.Before(from) || tt.selected.After(to) {
			t.Errorf("trendWindow(%s) leaves the selected month out", tt.selected.Format("2006-01"))
		}
	}
}

func TestParameters(t *testing.T) {
	today := date(2026, time.November, 21)
	t.Run("month", func(t *testing.T) {
		good := map[string]time.Time{
			"":        date(2026, 11, 1),
			"2026-03": date(2026, 3, 1),
			"1900-01": date(1900, 1, 1),
			"2999-12": date(2999, 12, 1),
		}
		for value, want := range good {
			got, err := monthParam(url.Values{"month": {value}}, today)
			if err != nil || !got.Equal(want) {
				t.Errorf("month %q = %v, %v; want %v", value, got, err, want)
			}
		}
		for _, bad := range []string{"2026-13", "2026-00", "2026-1", "26-11", "2026/11", "2026-11-01", " 2026-11", "nov", "1899-12", "3000-01", "-2026-01", "0000-01"} {
			_, err := monthParam(url.Values{"month": {bad}}, today)
			var webErr *webserver.Error
			if !errors.As(err, &webErr) || webErr.Status != http.StatusBadRequest {
				t.Errorf("month %q: err = %v, want a 400", bad, err)
			}
		}
	})
	t.Run("date", func(t *testing.T) {
		if got, err := dateParam(url.Values{"closing": {"2026-11-05"}}, "closing"); err != nil || got == nil || !got.Equal(date(2026, 11, 5)) {
			t.Errorf("= %v, %v", got, err)
		}
		if got, err := dateParam(url.Values{}, "closing"); err != nil || got != nil {
			t.Errorf("no date = %v, %v", got, err)
		}
		for _, bad := range []string{"2026-02-30", "2026-11", "tomorrow", "2026-11-5", "1000-01-01"} {
			if _, err := dateParam(url.Values{"closing": {bad}}, "closing"); err == nil {
				t.Errorf("date %q was taken", bad)
			}
		}
	})
	t.Run("id", func(t *testing.T) {
		if got, err := idParam(url.Values{"account": {cash.String()}}, "account"); err != nil || got == nil || *got != cash {
			t.Errorf("= %v, %v", got, err)
		}
		if got, err := idParam(url.Values{}, "account"); err != nil || got != nil {
			t.Errorf("no id = %v, %v", got, err)
		}
		if _, err := idParam(url.Values{"account": {"x"}}, "account"); err == nil {
			t.Error("an id that is none was taken")
		}
	})
	t.Run("page", func(t *testing.T) {
		for value, want := range map[string]int{"": 1, "1": 1, "7": 7, "10000": 10000} {
			if got, err := pageParam(url.Values{"page": {value}}); err != nil || got != want {
				t.Errorf("page %q = %d, %v; want %d", value, got, err, want)
			}
		}
		for _, bad := range []string{"0", "-1", "x", "1.5", "10001", "99999999999999999999"} {
			if _, err := pageParam(url.Values{"page": {bad}}); err == nil {
				t.Errorf("page %q was taken", bad)
			}
		}
	})
}

func TestALinkLeavesOutWhatIsNotSet(t *testing.T) {
	if got := link("/app/x", url.Values{"a": {""}, "b": {"2"}, "c": {"x y&z"}}); got != "/app/x?b=2&c=x+y%26z" {
		t.Errorf("link = %q", got)
	}
	if got := link("/app/x", url.Values{"a": {""}}); got != "/app/x" {
		t.Errorf("link = %q, want the plain path", got)
	}
}

func TestWhatAUseCaseSaysBecomesWhatThePageAnswers(t *testing.T) {
	tests := []struct {
		err        error
		wantStatus int
	}{
		{model.ErrNotFound, 404},
		{fmt.Errorf("account: %w", model.ErrNoStatements), 404},
		{fmt.Errorf("list: %w", model.ErrInvalidStatus), 400},
		{fmt.Errorf("list: %w", model.ErrInvalidKind), 400},
		{fmt.Errorf("list: %w", model.ErrInvalidPage), 400},
		{fmt.Errorf("list: %w", model.ErrConflictingFilters), 400},
		{model.ErrDateRangeReversed, 400},
		{model.ErrMonthRequired, 400},
		{errors.New("connection refused"), 0}, // not the user's to see: it is left as it is
	}
	for _, tt := range tests {
		got := fail(tt.err)
		var webErr *webserver.Error
		switch {
		case tt.wantStatus == 0 && errors.As(got, &webErr):
			t.Errorf("fail(%v) = %v: a failure of the server is made a page the user is to read", tt.err, got)
		case tt.wantStatus != 0 && (!errors.As(got, &webErr) || webErr.Status != tt.wantStatus):
			t.Errorf("fail(%v) = %v, want a %d", tt.err, got, tt.wantStatus)
		}
		if !errors.Is(got, tt.err) {
			t.Errorf("fail(%v) lost the cause", tt.err)
		}
	}
	already := webserver.BadRequest("x")
	if got := fail(already); got != already {
		t.Errorf("an error that is a page's own was changed to %v", got)
	}
}
