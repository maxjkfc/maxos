package web

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The cards of the overview, and what each says in the states the ledger is in: every number is a use
// case's, so what these tests show is what the page makes of them, and that the cards agree with the
// accounts they are about.

// monthsEndingNow is the twelve months that end with November 2026 (the month of the fake's today), each
// with what was spent in it as the use case would say it.
func monthsEndingNow(totals ...string) usecase.GetSpendingTrendOutput {
	var months []usecase.MonthSpending
	for i, total := range totals {
		months = append(months, usecase.MonthSpending{Month: date(2025, time.December, 1).AddDate(0, i, 0), Total: dec(total)})
	}
	return usecase.GetSpendingTrendOutput{Months: months}
}

// A year of spending, the last month being this one.
var aYear = []string{"31200", "28400", "35100", "29800", "41500", "38200", "33900", "36700", "30100", "44800", "39600", "42350"}

// spentThisMonth is what was spent this month as GetSpending says it: the largest category first, and the
// total the categories add up to.
func spentThisMonth() usecase.GetSpendingOutput {
	return usecase.GetSpendingOutput{
		Total: dec("42350"),
		TopLevel: []usecase.TopLevelSpending{
			{CategoryID: rent, Total: dec("22000"), Own: dec("22000")},
			{CategoryID: food, Total: dec("15200"), Own: dec("12000"), Children: []usecase.CategorySpending{{CategoryID: lunch, Amount: dec("3200")}}},
			{CategoryID: fees, Total: dec("5150"), Own: dec("5150")},
		},
	}
}

func TestTheHeadlineIsTheTotalOfTheBaseCurrencyAndEachOtherInstrumentIsBesideItWithItsOwnCode(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(cash, twd, "1000"),
		held(checking, twd, "250.5"),
		held(checking, usd, "10"),
		held(emptyAcct, usd, "5.25"),
		held(checking, jpy, "1500"),
		held(emptyAcct, btc, "0.12345678"),
	}
	hero := cardOf(t, f.site(t).page("/app/"), "hero-title")

	if got := paragraph(hero, "hero-figure"); got != "1,250.50 TWD" {
		t.Errorf("the headline says %q, want the base currency's total, and only that", got)
	}
	// The others are each their own total, by code, and none is the sum of two instruments.
	if got, want := tilesOf(hero), []string{"0.12345678 BTC", "1,500 JPY", "15.25 USD"}; !slices.Equal(got, want) {
		t.Errorf("the others say %q, want %q", got, want)
	}

	// What is held of another instrument does not move the headline: nothing is converted into it.
	g := newFake()
	g.balances = f.balances[:2]
	if got := paragraph(cardOf(t, g.site(t).page("/app/"), "hero-title"), "hero-figure"); got != "1,250.50 TWD" {
		t.Errorf("the headline says %q without the other instruments, which is not what it says with them", got)
	}
}

func TestTheHeadlineSaysWhatItIsMadeOfAsTheBalancesDo(t *testing.T) {
	tests := []struct {
		name  string
		world func(f *fake)
		// figure is what the headline says; under is what is under it; red is whether it is the colour of a
		// loss; preview is whether it says that it is a preview; drafty is whether the note says that nothing
		// is confirmed.
		figure, under        string
		red, preview, drafty bool
	}{
		{
			name: "no draft: the balance, with nothing said of a preview",
			world: func(f *fake) {
				f.balances = []usecase.PreviewBalance{held(cash, twd, "880")}
			},
			figure: "880.00 TWD",
		},
		{
			name: "confirmed and changed by drafts: what it would be, with what is confirmed of it under it",
			world: func(f *fake) {
				f.balances = []usecase.PreviewBalance{{AccountID: cash, InstrumentID: twd, Held: figure("1000", "-250"), Unbilled: figure("0", "0")}}
				f.drafts = usecase.GetDraftSummaryOutput{Count: 4}
			},
			figure: "750.00 TWD", under: "已確認 1,000.00 TWD", preview: true,
		},
		{
			name: "all of it drafts': nothing of it is confirmed, which is said, and it is not red though it is owed",
			world: func(f *fake) {
				f.balances = []usecase.PreviewBalance{{AccountID: cash, InstrumentID: twd, Held: figure("0", "-900"), Unbilled: figure("0", "0")}}
				f.drafts = usecase.GetDraftSummaryOutput{Count: 4}
			},
			figure: "−900.00 TWD", under: "尚未確認", preview: true, drafty: true,
		},
		{
			name: "owed of what is confirmed: red, as the loss it is",
			world: func(f *fake) {
				f.balances = []usecase.PreviewBalance{held(card, twd, "-500")}
			},
			figure: "−500.00 TWD", red: true,
		},
		{
			name: "owed in part of what is confirmed and in part by drafts: red, and says what is confirmed",
			world: func(f *fake) {
				f.balances = []usecase.PreviewBalance{{AccountID: card, InstrumentID: twd, Held: figure("-500", "-2000"), Unbilled: figure("0", "0")}}
				f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
			},
			figure: "−2,500.00 TWD", under: "已確認 −500.00 TWD", red: true, preview: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			tt.world(f)
			hero := cardOf(t, f.site(t).page("/app/"), "hero-title")

			if got := paragraph(hero, "hero-figure"); got != tt.figure {
				t.Errorf("the headline says %q, want %q", got, tt.figure)
			}
			if got := paragraph(hero, "hero-sub"); got != tt.under {
				t.Errorf("under the headline it says %q, want %q", got, tt.under)
			}
			if got := strings.Contains(between(hero, `<p class="hero-figure">`, "</p>"), `class="amt neg"`); got != tt.red {
				t.Errorf("the headline is red = %v, want %v", got, tt.red)
			}
			if got := strings.Contains(hero, `<span class="pill pill-draft">`); got != tt.preview {
				t.Errorf("the headline says it is a preview = %v, want %v", got, tt.preview)
			}
			if got := strings.Contains(hero, "全是草稿"); got != tt.drafty {
				t.Errorf("the headline says that every number is a draft's = %v, want %v", got, tt.drafty)
			}
		})
	}
}

// What is owed in instalments that are not yet due is not in the balance, and is a line of its own under the
// headline, written as the rows of the accounts write it.
func TestTheHeadlineHasWhatIsNotYetDueApartFromTheBalance(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		{AccountID: card, InstrumentID: twd, Held: figure("-500", "-2000"), Unbilled: figure("-11000", "-6000")},
		held(cash, twd, "3000"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	hero := cardOf(t, f.site(t).page("/app/"), "hero-title")

	if got := paragraph(hero, "hero-figure"); got != "500.00 TWD" {
		t.Errorf("the headline says %q, want what the accounts hold, without what is not yet due", got)
	}
	subs := regexp.MustCompile(`(?s)<p class="hero-sub[^"]*">(.*?)</p>`).FindAllStringSubmatch(hero, -1)
	var lines []string
	for _, m := range subs {
		lines = append(lines, textOf(m[1]))
	}
	// What is confirmed of what the accounts hold, which the debt of the card is a part of, and then what is not yet due.
	want := []string{"已確認 2,500.00 TWD", "未到期分期 −17,000.00 TWD 已確認 −11,000.00 TWD"}
	if !slices.Equal(lines, want) {
		t.Errorf("under the headline: %q, want %q", lines, want)
	}
}

// With only instruments other than the base currency held, the headline is a total of nothing of it, and does not
// put another instrument in its place.
func TestTheHeadlineIsNothingOfTheBaseCurrencyWhenOnlyOtherInstrumentsAreHeld(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{held(checking, usd, "5")}
	hero := cardOf(t, f.site(t).page("/app/"), "hero-title")

	if got := paragraph(hero, "hero-figure"); got != "0.00 TWD" {
		t.Errorf("the headline says %q, want nothing of the base currency", got)
	}
	if got, want := tilesOf(hero), []string{"5.00 USD"}; !slices.Equal(got, want) {
		t.Errorf("the others say %q, want %q", got, want)
	}
}

// Drafts that move no balance today are waiting, and the ledger has no balance: the headline says that, with
// how many drafts wait, and has no figure.
func TestTheHeadlineWhenDraftsWaitAndNoAccountHoldsAnythingToday(t *testing.T) {
	f := newFake()
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2}
	hero := cardOf(t, f.site(t).page("/app/"), "hero-title")

	wantIn(t, hero, `<span class="pill pill-draft">預覽（含 2 筆草稿）</span>`, "還沒有已確認的餘額")
	wantNotIn(t, hero, `class="hero-figure"`, `class="tiles"`, "全是草稿")
}

// The number of drafts is written with the commas of its thousands, as every figure of the page is.
func TestHowManyDraftsWaitIsWrittenWithTheCommasOfItsThousands(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{held(cash, twd, "1")}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2945}
	page := f.site(t).page("/app/")

	wantIn(t, cardOf(t, page, "hero-title"), "預覽（含 2,945 筆草稿）")
	if got := paragraph(cardOf(t, page, "drafts-title"), "stat-figure"); got != "2,945 筆" {
		t.Errorf("the card of the drafts says %q, want 2,945 筆", got)
	}
}

func TestTheSpendingCardIsWhatWasConfirmedAndSpentThisMonth(t *testing.T) {
	f := newFake()
	f.spent = spentThisMonth()
	f.trend = monthsEndingNow(aYear...)
	page := f.site(t).page("/app/")
	spending := cardOf(t, page, "spending-title")

	// This month's total, and what the month before it was, from the months of the trend.
	wantAmounts(t, spending, "42,350.00 TWD", "39,600.00 TWD")
	if got := paragraph(spending, "stat-figure"); got != "42,350.00 TWD" {
		t.Errorf("the card says %q, want what was spent", got)
	}
	wantIn(t, spending, "2026-11", `href="/app/spending"`)
	wantNotIn(t, spending, `class="amt neg"`)
	// It asked for this month, which the clock says, and for the twelve months that end with it.
	if !f.spentIn.Month.Equal(date(2026, time.November, 1)) {
		t.Errorf("the spending was asked for %v, want November 2026", f.spentIn.Month)
	}
	if !f.trendIn.From.Equal(date(2025, time.December, 1)) || !f.trendIn.To.Equal(date(2026, time.November, 1)) {
		t.Errorf("the trend was asked from %v to %v, want the twelve months that end with this one", f.trendIn.From, f.trendIn.To)
	}
}

// A month in which nothing is confirmed has nothing spent, which the card says plainly and does not write as a figure
// of nothing; the drafts that wait are where the rest may be, and the card says where.
func TestTheSpendingCardOfAMonthInWhichNothingIsConfirmed(t *testing.T) {
	f := newFake()
	f.drafts = usecase.GetDraftSummaryOutput{Count: 12}
	page := f.site(t).page("/app/")
	spending := cardOf(t, page, "spending-title")

	wantIn(t, spending, "還沒有已確認的支出", `<a class="nowrap" href="/app/drafts">查看草稿</a>`)
	wantIn(t, spending, "12 筆草稿")
	wantNotIn(t, spending, `class="stat-figure"`, "0.00")

	// With no draft there is nowhere to point to.
	f.drafts = usecase.GetDraftSummaryOutput{}
	spending = cardOf(t, f.site(t).page("/app/"), "spending-title")
	wantIn(t, spending, "還沒有已確認的支出")
	wantNotIn(t, spending, `href="/app/drafts"`, "筆草稿")

	// A month with nothing in it that follows a month with something says what that was.
	f.trend = monthsEndingNow("0", "0", "0", "0", "0", "0", "0", "0", "0", "0", "39600", "0")
	spending = cardOf(t, f.site(t).page("/app/"), "spending-title")
	wantAmounts(t, spending, "39,600.00 TWD")
}

func TestTheDraftsCardSaysHowManyWaitAndWhatConfirmingThemWouldChange(t *testing.T) {
	f := newFake()
	f.drafts = usecase.GetDraftSummaryOutput{Count: 3, Amounts: []usecase.DraftAmount{
		{InstrumentID: twd, Outflow: dec("2480345.5"), Inflow: dec("210000"), Net: dec("-2270345.5")},
		{InstrumentID: usd, Outflow: dec("300"), Net: dec("-300")},
		{InstrumentID: btc, Inflow: dec("0.5"), Net: dec("0.5")},
	}}
	drafts := cardOf(t, f.site(t).page("/app/"), "drafts-title")

	if got := paragraph(drafts, "stat-figure"); got != "3 筆" {
		t.Errorf("the card says %q, want how many wait", got)
	}
	// The change of each instrument, by the sign of what it does, in the order the use case lists them: and none of them is
	// the colour of a loss, for nothing of it has happened.
	wantAmounts(t, drafts, "−2,270,345.50 TWD", "−300.00 USD", "+0.50000000 BTC")
	wantNotIn(t, drafts, `class="amt neg"`)
	wantIn(t, drafts, `<a class="card-link" href="/app/drafts">查看草稿</a>`, `<span class="nowrap">「未到期分期」</span>`)
	wantNotIn(t, drafts, "<table")
}

func TestTheDraftsCardWhenTheyWouldChangeNothingToday(t *testing.T) {
	f := newFake()
	// Dated later, or of no account: they wait, and move no balance today.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2, OwnTransfers: 2}
	drafts := cardOf(t, f.site(t).page("/app/"), "drafts-title")

	if got := paragraph(drafts, "stat-figure"); got != "2 筆" {
		t.Errorf("the card says %q, want how many wait", got)
	}
	wantNotIn(t, drafts, `class="stat-nets"`, "<table")
	wantIn(t, drafts, `href="/app/drafts"`)
}

func TestTheDraftsCardWhenNoneWait(t *testing.T) {
	drafts := cardOf(t, newFake().site(t).page("/app/"), "drafts-title")

	wantIn(t, drafts, "沒有等著確認的草稿")
	wantNotIn(t, drafts, `class="stat-figure"`, `href="/app/drafts"`)
}

// A reader works out what the drafts change from the accounts: what each would hold less what is confirmed of it. For each
// instrument that is what the card of the drafts says all of them change, and the card does not have to be taken on trust.
func TestTheDraftsCardSaysWhatTheAccountsOfTheDraftsChange(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		{AccountID: cash, InstrumentID: twd, Held: figure("800", "-120"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: twd, Held: figure("0", "49000"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: usd, Held: figure("10", "-10"), Unbilled: figure("0", "0")},
		held(emptyAcct, btc, "0.5"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 5, Amounts: []usecase.DraftAmount{
		{InstrumentID: twd, Outflow: dec("120"), Inflow: dec("49000"), Net: dec("48880")},
		{InstrumentID: usd, Outflow: dec("10"), Net: dec("-10")},
	}}
	page := f.site(t).page("/app/")

	got := draftsOfTheBalances(t, rowsOf(accountsOf(page)))
	nets := map[string]decimal.Decimal{}
	for _, p := range amountPairsOf(cardOf(t, page, "drafts-title")) {
		nets[p[1]] = numberOf(t, strings.TrimPrefix(p[0], "+"))
	}
	if len(got) != 2 || len(nets) != 2 {
		t.Fatalf("the accounts say the drafts change %v, and the card says %v, want TWD and USD in both", got, nets)
	}
	for unit, net := range nets {
		if !got[unit].Equal(net) {
			t.Errorf("%s: the accounts say the drafts change %s, and the card of the drafts says %s", unit, got[unit], net)
		}
	}
}

// rowsOf is the rows of the accounts alone, without what a group adds up to in its head: the heads write their figures as the
// rows do, and a reader adding up the rows would count them twice.
func rowsOf(accounts string) string {
	return regexp.MustCompile(`(?s)<div class="group-head">.*?</div>`).ReplaceAllString(accounts, "")
}

func TestTheDealingsCardIsThereOnlyWhenSomethingIsOwed(t *testing.T) {
	f := newFake()
	f.dealings = []usecase.Dealing{
		{PartyID: alice, InstrumentID: twd, Quantity: dec("300")},
		{PartyID: alice, InstrumentID: usd, Quantity: dec("-12.5")},
		{PartyID: bob, InstrumentID: twd, Quantity: dec("0")},
	}
	page := f.site(t).page("/app/")
	dealings := cardOf(t, page, "dealings-title")

	// A party is a row, with a line for each instrument, which says whose the amount is; the party whose
	// dealings add up to nothing is only named.
	wantAmounts(t, dealings, "300.00 TWD", "−12.50 USD")
	if got := textOf(between(dealings, `<ul class="stat-rows">`, "</ul>")); got != "Alice 300.00 TWD 對方欠我 −12.50 USD 我欠對方" {
		t.Errorf("the card says %q", got)
	}
	wantIn(t, dealings, "<li>Bob</li>")

	// What is all nothing is nothing to show: there is no card.
	f.dealings = []usecase.Dealing{{PartyID: bob, InstrumentID: twd, Quantity: dec("0")}}
	if page = f.site(t).page("/app/"); hasCard(page, "dealings-title") {
		t.Errorf("the page has a card of dealings when all of them are nothing:\n%s", page)
	}
	f.dealings = nil
	if page = f.site(t).page("/app/"); hasCard(page, "dealings-title") {
		t.Errorf("the page has a card of dealings when there are none:\n%s", page)
	}
}

// statement is a statement of a card, with what the card did of the base currency on it, as ListCardStatements says it.
func statement(from, closing time.Time, due *time.Time, closed bool, charges, closingBalance string) usecase.CardStatementSummary {
	s := usecase.CardStatementSummary{From: from, ClosingOn: closing, DueOn: due, Closed: closed}
	if charges != "" {
		s.Figures = []usecase.StatementFigures{{InstrumentID: twd, Charges: dec(charges), Closing: dec(closingBalance)}}
	}
	return s
}

func TestThePaymentsCardSaysWhatEachCardOwesAndWhenItIsDue(t *testing.T) {
	f := newFake()
	second := seqID(310)
	f.accounts = append(f.accounts,
		usecase.Account{ID: second, Name: "第二張卡", Kind: "card", ClosingDay: new(12), PaymentDueDay: new(28)},
		// A card that is archived is not asked about, and one with no closing day has no statement.
		usecase.Account{ID: seqID(311), Name: "封存的卡", Kind: "card", ClosingDay: new(12), ArchivedAt: new(date(2026, 1, 1))},
	)
	f.statements = []usecase.CardStatementSummary{
		// Open, the newest first: the days of one that has not begun are not listed.
		statement(date(2026, 12, 6), date(2027, 1, 5), new(date(2027, 1, 20)), false, "", ""),
		statement(date(2026, 11, 6), date(2026, 12, 5), new(date(2026, 12, 20)), false, "3650", "-11650"),
		// Closed, and to be paid by tomorrow.
		statement(date(2026, 10, 6), date(2026, 11, 5), new(date(2026, 11, 22)), true, "8000", "-8000"),
		statement(date(2026, 9, 6), date(2026, 10, 5), new(date(2026, 10, 20)), true, "5000", "-5000"),
	}
	page := f.site(t).page("/app/")
	payments := cardOf(t, page, "payments-title")

	// Each card in use that has a closing day is a row: the statement that closed last while it is to be paid, and the one that
	// is open, each with its due day; the amounts are what the statements say, and a card that owes is no loss.
	rows := regexp.MustCompile(`(?s)<li>(.*?)</li>`).FindAllStringSubmatch(payments, -1)
	if len(rows) != 2 {
		t.Fatalf("%d cards, want the two that are in use and have a closing day:\n%s", len(rows), payments)
	}
	want := "信用卡 上一期 繳款日 2026-11-22 期末餘額 −8,000.00 TWD 本期 繳款日 2026-12-20 本期消費 3,650.00 TWD"
	if got := textOf(rows[0][1]); got != want {
		t.Errorf("the first card says %q, want %q", got, want)
	}
	if got := textOf(rows[1][1]); !strings.HasPrefix(got, "第二張卡 ") {
		t.Errorf("the second card says %q", got)
	}
	wantNotIn(t, payments, `class="amt neg"`, "封存的卡", "備用卡")
	// Every open statement has something on it, so there is nothing to say of what is missing.
	wantNotIn(t, payments, `<p class="stat-sub`)
	// The links go to the statements: the open one, and the one that closed.
	wantIn(t, payments,
		`href="/app/cards/`+card.String()+`?closing=2026-12-05"`,
		`href="/app/cards/`+card.String()+`?closing=2026-11-05"`,
		`href="/app/cards"`)

	// One call for each card that is asked about, for the days around today: the last statement that closed and the one that is open.
	var asked []string
	for _, in := range f.statementIn {
		asked = append(asked, in.AccountID.String())
		if !in.From.Equal(date(2026, 10, 17)) || !in.To.Equal(date(2026, 12, 22)) {
			t.Errorf("the statements were asked for from %v to %v, want 35 days back and 31 days ahead of the 21st of November", in.From, in.To)
		}
	}
	if want := []string{card.String(), second.String()}; !slices.Equal(asked, want) {
		t.Errorf("the statements were asked of %v, want %v", asked, want)
	}
}

// The statement that closed last is listed while it is still to be paid: on the day it is due, and not the day after. A
// statement with nothing on it is nothing to pay, and an open one with nothing on it says so.
func TestThePaymentsCardListsTheLastStatementUntilTheDayAfterItIsDue(t *testing.T) {
	tests := []struct {
		name       string
		closed     usecase.CardStatementSummary
		wantLast   bool
		wantOpenIn string
	}{
		{"due today", statement(date(2026, 10, 6), date(2026, 11, 5), new(date(2026, 11, 21)), true, "100", "-100"), true, "本期消費 3,650.00 TWD"},
		{"due yesterday", statement(date(2026, 10, 6), date(2026, 11, 5), new(date(2026, 11, 20)), true, "100", "-100"), false, "本期消費 3,650.00 TWD"},
		{"with no due day", statement(date(2026, 10, 6), date(2026, 11, 5), nil, true, "100", "-100"), false, "本期消費 3,650.00 TWD"},
		{"due, with nothing on it", statement(date(2026, 10, 6), date(2026, 11, 5), new(date(2026, 11, 28)), true, "", ""), false, "本期消費 3,650.00 TWD"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFake()
			f.statements = []usecase.CardStatementSummary{
				statement(date(2026, 11, 6), date(2026, 12, 5), new(date(2026, 12, 20)), false, "3650", "-3650"),
				tt.closed,
			}
			payments := cardOf(t, f.site(t).page("/app/"), "payments-title")

			if got := strings.Contains(payments, "上一期"); got != tt.wantLast {
				t.Errorf("the card lists the statement that closed = %v, want %v:\n%s", got, tt.wantLast, payments)
			}
			wantIn(t, textOf(payments), tt.wantOpenIn)
		})
	}
}

// An open statement with nothing confirmed on it, as a ledger of drafts has, is a card to be paid with nothing on it: it
// says when it is due and writes no figure of nothing, and the card says once, under all of them, why a row has no amount.
// A card whose statement has no due day says so with a dash.
func TestThePaymentsCardOfAStatementWithNothingConfirmedOnIt(t *testing.T) {
	f := newFake()
	second := usecase.Account{ID: seqID(310), Name: "第二張卡", Kind: "card", ClosingDay: new(12), PaymentDueDay: new(28)}
	f.accounts = append(f.accounts, second)
	f.statements = []usecase.CardStatementSummary{statement(date(2026, 11, 6), date(2026, 12, 5), nil, false, "", "")}
	payments := cardOf(t, f.site(t).page("/app/"), "payments-title")

	rows := regexp.MustCompile(`(?s)<li>(.*?)</li>`).FindAllStringSubmatch(payments, -1)
	if len(rows) != 2 || textOf(rows[0][1]) != "信用卡 本期 繳款日 —" || textOf(rows[1][1]) != "第二張卡 本期 繳款日 —" {
		t.Errorf("the rows say %q, want each card with when it is due and nothing else", rows)
	}
	wantNotIn(t, payments, `class="amt`)
	if n := strings.Count(payments, `<p class="stat-sub`); n != 1 {
		t.Errorf("%d notes under the cards, want 1 for all of them", n)
	}
}

func TestThePaymentsCardIsThereOnlyForCardsThatHaveStatements(t *testing.T) {
	f := newFake()
	f.accounts = slices.DeleteFunc(f.accounts, func(a usecase.Account) bool { return a.ClosingDay != nil })
	f.statements = []usecase.CardStatementSummary{statement(date(2026, 11, 6), date(2026, 12, 5), new(date(2026, 12, 20)), false, "1", "-1")}
	page := f.site(t).page("/app/")

	if hasCard(page, "payments-title") || len(f.statementIn) != 0 {
		t.Errorf("a card of payments, or a call for statements (%d), for a ledger of no card that has a closing day", len(f.statementIn))
	}
}

func TestTheSpendingTrendIsTwelveColumnsEachOfAMonthWithItsNumbersInATable(t *testing.T) {
	f := newFake()
	f.trend = monthsEndingNow(aYear...)
	f.spent = spentThisMonth()
	page := f.site(t).page("/app/")
	trend := cardOf(t, page, "trend-title")

	if n := strings.Count(trend, `class="chart-bar`); n != 12 {
		t.Errorf("%d columns, want twelve", n)
	}
	// This month is the one that stands out, and every month leads to its spending.
	if n := strings.Count(trend, "chart-bar-hl"); n != 1 {
		t.Errorf("%d columns are marked, want this month only", n)
	}
	wantIn(t, trend, `<a href="/app/spending?month=2025-12">`, `<a href="/app/spending?month=2026-11">`, `id="overview-trend-title"`)
	// The chart is described in the numbers it draws, to a reader that does not see it, and they are in a table beside it.
	wantIn(t, between(trend, `<desc id="overview-trend-desc">`, "</desc>"), "12月: 31,200.00 TWD", "11月: 42,350.00 TWD")
	table := between(trend, "<table>", "</table>")
	if n := strings.Count(table, "<tr"); n != 13 {
		t.Errorf("%d rows in the table of the months, want twelve and the heading", n)
	}
	wantIn(t, table, amount("31,200.00", "TWD"), amount("42,350.00", "TWD"), `<tr class="current" aria-current="true">`)
	wantIn(t, trend, `<a class="card-link" href="/app/spending">查看支出</a>`)
}

// With nothing spent in any of the months there is nothing to draw: the card says that, and the chart is not twelve
// columns of nothing.
func TestTheSpendingTrendOfALedgerThatSpentNothingIsNoChart(t *testing.T) {
	trend := cardOf(t, newFake().site(t).page("/app/"), "trend-title")

	wantIn(t, trend, "還沒有已確認的支出")
	wantNotIn(t, trend, "<svg", "<table")
}

func TestTheTopCategoriesAreTheFirstFiveTheUseCaseListsAsBars(t *testing.T) {
	f := newFake()
	f.spent = spentThisMonth()
	top := cardOf(t, f.site(t).page("/app/"), "top-title")

	// Three categories are three bars, the largest first, as the use case lists them, and each says what was spent.
	if n := strings.Count(top, `class="chart-track"`); n != 3 {
		t.Errorf("%d bars, want 3", n)
	}
	text := textOf(between(top, "<svg", "</svg>"))
	at := func(name string) int { return strings.Index(text, name) }
	if at("房租") < 0 || at("房租") >= at("飲食") || at("飲食") >= at("手續費") {
		t.Errorf("the bars do not name 房租, 飲食 and 手續費 in that order, the largest first: %s", text)
	}
	wantIn(t, top, "22,000.00 TWD", "15,200.00 TWD")
	// All of them are drawn, so there is nothing more to see on the page of the spending.
	wantIn(t, top, `<a class="card-link" href="/app/spending">查看支出</a>`)

	// Seven categories are five bars, and the page of the spending is where the rest are.
	for i := range 4 {
		f.spent.TopLevel = append(f.spent.TopLevel, usecase.TopLevelSpending{CategoryID: seqID(8100 + i), Total: dec(fmt.Sprint(100 - i)), Own: dec("1")})
	}
	top = cardOf(t, f.site(t).page("/app/"), "top-title")
	if n := strings.Count(top, `class="chart-track"`); n != 5 {
		t.Errorf("%d bars of seven categories, want 5", n)
	}
	wantIn(t, top, `<a class="card-link" href="/app/spending">查看全部類別</a>`)
}

func TestTheTopCategoriesOfAMonthWithNothingSpentAreNoBars(t *testing.T) {
	top := cardOf(t, newFake().site(t).page("/app/"), "top-title")

	wantIn(t, top, "還沒有已確認的支出")
	wantNotIn(t, top, "<svg")
}

// The headline and the heads of the groups are what the rows of the accounts add up to, an instrument at a time: the
// page's figures agree with the accounts they are about, for what is held and not what is confirmed of it.
func TestTheHeadlineAndTheHeadsOfTheGroupsAreWhatTheRowsAddUpTo(t *testing.T) {
	f := newFake()
	extra := usecase.Account{ID: seqID(320), Name: "第二個戶頭", Kind: "bank", InstitutionID: &bank}
	f.accounts = append(f.accounts, extra)
	f.balances = []usecase.PreviewBalance{
		{AccountID: cash, InstrumentID: twd, Held: figure("800", "-120"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: twd, Held: figure("0", "49000"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: usd, Held: figure("10", "-10"), Unbilled: figure("0", "0")},
		{AccountID: extra.ID, InstrumentID: twd, Held: figure("1500.5", "0"), Unbilled: figure("0", "0")},
		{AccountID: extra.ID, InstrumentID: jpy, Held: figure("0", "3000"), Unbilled: figure("0", "0")},
		{AccountID: card, InstrumentID: twd, Held: figure("-500", "-2000"), Unbilled: figure("-11000", "-6000")},
		{AccountID: spareCard, InstrumentID: twd, Held: figure("-40", "0"), Unbilled: figure("0", "0")},
		held(emptyAcct, btc, "0.25"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 9}
	page := f.site(t).page("/app/")

	// What each instrument is in all the rows, and in the rows of a group.
	sumOf := func(markup string) map[string]decimal.Decimal {
		sums := map[string]decimal.Decimal{}
		for _, line := range balanceLineMarkup.FindAllStringSubmatch(markup, -1) {
			if label, total, unit := line[1], line[2], line[3]; label == "" {
				sums[unit] = sums[unit].Add(numberOf(t, total))
			}
		}
		return sums
	}
	headOf := func(markup string) map[string]decimal.Decimal {
		sums := map[string]decimal.Decimal{}
		for _, line := range balanceLineMarkup.FindAllStringSubmatch(markup, -1) {
			if label, total, unit := line[1], line[2], line[3]; label == "" {
				sums[unit] = numberOf(t, total)
			}
		}
		return sums
	}
	equal := func(what string, got, want map[string]decimal.Decimal) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: %v, want %v", what, got, want)
			return
		}
		for unit, n := range want {
			if !got[unit].Equal(n) {
				t.Errorf("%s: %s is %s, want %s", what, unit, got[unit], n)
			}
		}
	}

	rows := rowsOf(accountsOf(page))
	hero := cardOf(t, page, "hero-title")
	all := headOf(hero)
	all["TWD"] = numberOf(t, strings.Fields(paragraph(hero, "hero-figure"))[0])
	equal("the headline and the rows of the accounts", all, sumOf(rows))

	// The heads: a group that lists more than one account says what they add up to, and a group of one says nothing, for its row does.
	for _, kind := range []string{"銀行戶頭", "信用卡"} {
		group := groupOf(page, kind)
		head := between(group, "<ul", "</ul>")
		equal("the head of the group of "+kind, headOf(head), sumOf(rowsOf(strings.Replace(group, head, "", 1))))
	}
	wantNotIn(t, groupOf(page, "現金"), `class="tiles"`)
}

// The overview reads what it shows in a fixed number of calls, and one more for each card that has a closing day, which is how
// many cards there are and not how many transactions: the calls are these, and no others.
func TestTheOverviewReadsInAFixedNumberOfCallsAndOneMoreForEachCardThatHasAClosingDay(t *testing.T) {
	fixed := map[string]int{
		"GetPreviewBalances": 1, "GetDealings": 1, "ListInstalmentPlans": 1, "GetDraftSummary": 1, "GetSpending": 1, "GetSpendingTrend": 1,
		"ListInstruments": 1, "ListAccounts": 1, "ListInstitutions": 1, "ListParties": 1, "ListCategories": 1,
	}
	f := newFake() // one card has a closing day
	f.site(t).page("/app/")
	want := map[string]int{"ListCardStatements": 1}
	for use, n := range fixed {
		want[use] = n
	}
	if fmt.Sprint(f.calls) != fmt.Sprint(want) {
		t.Errorf("calls:\n%v\nwant\n%v", f.calls, want)
	}

	// Three more cards that have a closing day are three more calls, and nothing else changes.
	g := newFake()
	for i := range 3 {
		g.accounts = append(g.accounts, usecase.Account{ID: seqID(330 + i), Name: fmt.Sprintf("卡 %d", i), Kind: "card", ClosingDay: new(10 + i)})
	}
	g.site(t).page("/app/")
	want["ListCardStatements"] = 4
	if fmt.Sprint(g.calls) != fmt.Sprint(want) {
		t.Errorf("calls with four cards:\n%v\nwant\n%v", g.calls, want)
	}
}
