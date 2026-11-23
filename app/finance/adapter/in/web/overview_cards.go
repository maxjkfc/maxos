package web

import (
	"html/template"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/svgchart"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// The cards of the overview and its two charts. Every number in them is a use case's: the
// page writes it, and says nothing of what it does not have.

// topCategories is how many of the categories of the month the overview draws, the largest
// first as the use case lists them; the page of the spending has them all.
const topCategories = 5

// spendingCard is what was spent this month: the confirmed spending, which is all that
// GetSpending counts, so a month of which nothing is confirmed has none to say and says so.
type spendingCard struct {
	// Month is the month it is of, which is the one today is in.
	Month string
	// Spent is whether anything is spent in it.
	Spent bool
	Total amountView
	// Last is what was spent in the month before, when that is not nothing.
	Last *amountView
	Href string
	// Drafts is how many drafts wait (Text is how it is written): a month in which nothing
	// is confirmed says where the rest may be.
	Drafts     int
	DraftsText string
	DraftsHref string
}

func spendingCardOf(r *refs, today time.Time, spent usecase.GetSpendingOutput, months []usecase.MonthSpending, drafts int) spendingCard {
	c := spendingCard{
		Month:      webserver.FormatMonth(today),
		Spent:      !spent.Total.IsZero(),
		Total:      r.base(spent.Total),
		Href:       spendingHref,
		Drafts:     drafts,
		DraftsText: countText(drafts),
		DraftsHref: draftsHref,
	}
	// The months of the trend end with this one: the one before it is the one before.
	if n := len(months); n >= 2 && !months[n-2].Total.IsZero() {
		c.Last = new(r.base(months[n-2].Total))
	}
	return c
}

// draftsCard is the drafts that wait and what confirming them today would change of what
// the accounts hold: for all of them together, which is the net of GetDraftSummary and does
// not count money that only moves between my own accounts. What the drafts would change is
// no loss, so it has no colour for one.
type draftsCard struct {
	Count int
	Text  string
	Href  string
	// Nets is what they would change, an instrument at a time; none when they would
	// change nothing today (those dated later, those of no account), and an instrument
	// they change nothing of, being the net of money that moves between my own accounts
	// or of what is spent and taken back, is not written as a change of nothing.
	Nets []amountView
}

func draftsCardOf(r *refs, drafts usecase.GetDraftSummaryOutput) draftsCard {
	c := draftsCard{Count: drafts.Count, Text: countText(drafts.Count), Href: draftsHref}
	for _, a := range drafts.Amounts {
		if !a.Net.IsZero() {
			c.Nets = append(c.Nets, r.changed(a.InstrumentID, a.Net))
		}
	}
	return c
}

// trendCard is what was spent in each of the last twelve months, as columns, with the
// numbers in a table that is folded under them. There is nothing to draw when nothing is
// spent in any of them: the card says so, and does not draw twelve empty columns. The
// columns do not write their numbers over themselves, where one is taken for the number of
// the column next to it: the month that is not over is told apart by its colour, and Current
// says in words beside the chart what it is and what has been spent in it; the largest month
// is the one the dashed line across the top is of. No column is a link: the table has the
// links, and a link on each column is twelve places to stop for a reader with a keyboard.
type trendCard struct {
	Title string
	// Current is what has been spent so far this month, which is not over; nil when nothing has.
	Current *amountView
	// Chart is drawn for a wide screen and Narrow for a phone: the stylesheet shows the one
	// that fits.
	Chart  template.HTML
	Narrow template.HTML
	Rows   []trendRow
	Href   string
}

// narrowChartWidth is the width of the canvas of the charts that are drawn for a phone: with
// the text at 12 units, it is as big as the text of the page on the screen of a phone.
const narrowChartWidth = 240

func trendCardOf(r *refs, today time.Time, months []usecase.MonthSpending) trendCard {
	c := trendCard{Title: lastTwelveMonths, Href: spendingHref}
	if !slices.ContainsFunc(months, func(m usecase.MonthSpending) bool { return !m.Total.IsZero() }) {
		return c
	}
	current := model.MonthOf(today)
	c.Rows, _ = trendBars(r, months, current)
	if i := slices.IndexFunc(months, func(m usecase.MonthSpending) bool { return m.Month.Equal(current) }); i >= 0 && !months[i].Total.IsZero() {
		c.Current = new(r.base(months[i].Total))
	}
	chart := svgchart.Chart{ID: "overview-trend", Title: lastTwelveMonths, MarkMax: true, NumbersApart: true}
	c.Chart = svgchart.Columns(chart, trendColumns(r, months, current, false))
	chart.ID, chart.Width = "overview-trend-narrow", narrowChartWidth
	c.Narrow = svgchart.Columns(chart, trendColumns(r, months, current, true))
	return c
}

// trendColumns is the months as the columns of a chart, with no link on them. The labels of the
// chart drawn for a phone are the numbers of the months alone, which are what the columns of
// that width have room for.
func trendColumns(r *refs, months []usecase.MonthSpending, current time.Time, narrow bool) []svgchart.Bar {
	years := yearMarks(months)
	bars := make([]svgchart.Bar, len(months))
	for i, m := range months {
		total := r.base(m.Total)
		label := strconv.Itoa(int(m.Month.Month()))
		if !narrow {
			label += "月"
		}
		bars[i] = svgchart.Bar{Label: label, Value: m.Total, Text: total.Text + " " + total.Unit, Highlight: m.Month.Equal(current)}
		if years[i] {
			bars[i].Sub = strconv.Itoa(m.Month.Year())
		}
	}
	return bars
}

// yearMarks is whether the year of a month is written under its label: under the first month,
// and under each January, which is where the year changes. The first is left without when the
// second is a January, for the two years would stand side by side, and the January says it.
func yearMarks(months []usecase.MonthSpending) []bool {
	marks := make([]bool, len(months))
	for i, m := range months {
		marks[i] = m.Month.Month() == time.January
	}
	if len(months) > 0 && (len(months) == 1 || !marks[1]) {
		marks[0] = true
	}
	return marks
}

// topCard is the categories this month's spending went to, the largest first, as bars, with no
// link on them: the heading of the card has the one, to the page that has all of them. It is
// drawn for a wide screen and for a phone.
type topCard struct {
	Chart  template.HTML
	Narrow template.HTML
	// More is whether there are categories that are not drawn.
	More bool
	Href string
}

func topCardOf(r *refs, spent usecase.GetSpendingOutput) topCard {
	c := topCard{Href: spendingHref, More: len(spent.TopLevel) > topCategories}
	bars := make([]svgchart.Bar, 0, topCategories)
	for _, t := range spent.TopLevel[:min(len(spent.TopLevel), topCategories)] {
		total := r.base(t.Total)
		bars = append(bars, svgchart.Bar{Label: r.categoryName(t.CategoryID), Value: t.Total, Text: total.Text + " " + total.Unit})
	}
	chart := svgchart.Chart{ID: "overview-top", Title: "本月支出最多的類別"}
	c.Chart = svgchart.HBars(chart, bars)
	chart.ID, chart.Width = "overview-top-narrow", narrowChartWidth
	c.Narrow = svgchart.HBars(chart, bars)
	return c
}

// paymentsCard is the cards that are to be paid, with the page of all the statements. There is a
// card for them when a card that is in use and has a closing day has something to say: a day to
// pay, or what is owed. The cards that have nothing yet are counted, and not listed one by one.
type paymentsCard struct {
	// Cards are the ones that have something to say, the one whose payment comes first being
	// first, and those with no day to pay last.
	Cards []cardDue
	Href  string
	// Idle is how many cards have nothing to say: no payment due day, nothing confirmed on the
	// statement that is open, and none that closed to be paid.
	Idle int
	// SomeEmpty is whether a card that is listed has only its day, because nothing is confirmed
	// on its open statement: the card says so once, and not on each.
	SomeEmpty bool
	// SomeLast is whether a statement that closed is listed, which says what the card owed
	// when it closed, and the card says what that is once.
	SomeLast bool
}

func paymentsCardOf(r *refs, today time.Time, cards []cardStatements) paymentsCard {
	listed, idle := dueCardsOf(r, today, cards)
	c := paymentsCard{Cards: listed, Idle: idle, Href: cardsHref}
	for _, card := range listed {
		c.SomeEmpty = c.SomeEmpty || len(card.Open.Amounts) == 0
		c.SomeLast = c.SomeLast || card.Last != nil
	}
	return c
}

// cardDue is a card that has statements to say: the one that is open, and the one that closed
// last while it is still to be paid. What they say counts the confirmed transactions only, as
// every statement does.
type cardDue struct {
	Name string
	// Last is the statement that closed last, when its payment due day is today or after and the
	// card owed something when it closed: what it says is what was owed then. It is there until
	// that day, whether the card was paid since or not, which the statement does not say.
	Last *dueStatement
	// Open is the statement that is open: what was spent on the card so far.
	Open *dueStatement
	// next is the nearest of the payment due days of the two, nil when neither has one.
	next *time.Time
}

type dueStatement struct {
	Href      string
	ClosingOn string
	// DueOn is the payment due day, a dash when the card has none. In says how far off it is
	// ("今天", "N 天後" or "已過"), and Soon is whether that is within three days, which is said
	// with weight and not with the colour of a loss: a card that is to be paid is no loss.
	DueOn string
	In    string
	Soon  bool
	// Amounts are its figures, an instrument at a time: what the card owed when it closed (only
	// what it owed), or what was spent on it so far, none that is nothing; none at all when
	// nothing is confirmed on it. They have no colour for a loss: what a card owes is no loss.
	Amounts []amountView
}

// dueSoonDays is how many days ahead a payment due day is soon: today is soon, and so is each of the
// days up to the third after it.
const dueSoonDays = 3

// dueCardsOf writes the statements of each card that has an open one. The statement that closed
// last is there while its payment due day has not passed and the card owed something when it
// closed: it is what is to be paid next; the open statement is there with what is spent on it so
// far. A card that has no day to pay and nothing on either is not listed but counted. The cards
// come in the order of the day that is nearest, and in the catalog's order among the same days.
func dueCardsOf(r *refs, today time.Time, cards []cardStatements) (listed []cardDue, idle int) {
	for _, c := range cards {
		shown := shownStatements(c.statements, today)
		var open, last *usecase.CardStatementSummary
		for i := range shown {
			switch {
			case !shown[i].Closed && open == nil:
				open = &shown[i]
			case shown[i].Closed && last == nil:
				last = &shown[i]
			}
		}
		if open == nil {
			continue
		}
		card := cardDue{Name: c.account.Name, Open: new(dueStatementOf(r, c.account.ID, today, *open)), next: open.DueOn}
		if last != nil && last.DueOn != nil && !last.DueOn.Before(today) {
			if owed := dueStatementOf(r, c.account.ID, today, *last); len(owed.Amounts) > 0 {
				card.Last = &owed
				card.next = earlier(card.next, last.DueOn)
			}
		}
		if card.Last == nil && open.DueOn == nil && len(card.Open.Amounts) == 0 {
			idle++
			continue
		}
		listed = append(listed, card)
	}
	slices.SortStableFunc(listed, func(a, b cardDue) int { return compareDue(a.next, b.next) })
	return listed, idle
}

// earlier is the nearer of two days, either of which may be none.
func earlier(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	case b.Before(*a):
		return b
	}
	return a
}

// compareDue orders days with the nearest first and none last.
func compareDue(a, b *time.Time) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return a.Compare(*b)
}

// dueStatementOf writes a statement: what the card owed when it closed, or what was spent on
// it so far when it is open.
func dueStatementOf(r *refs, card uuid.UUID, today time.Time, s usecase.CardStatementSummary) dueStatement {
	d := dueStatement{Href: statementLink(card, s.ClosingOn), ClosingOn: webserver.FormatDate(s.ClosingOn), DueOn: "—"}
	if s.DueOn != nil {
		d.DueOn = webserver.FormatDate(*s.DueOn)
		d.In, d.Soon = dueIn(today, *s.DueOn)
	}
	for _, f := range s.Figures {
		amount := f.Charges
		if s.Closed {
			amount = f.Closing
		}
		// A statement that closed says what the card owed, which is below nothing: a figure that
		// is not is a credit, which is not what is to be paid. What is charged on the one that is
		// open is above nothing, and a charge of nothing is not written.
		if amount.IsZero() || (s.Closed && amount.IsPositive()) {
			continue
		}
		d.Amounts = append(d.Amounts, r.plain(f.InstrumentID, amount))
	}
	return d
}

// dueIn says how far off a payment due day is: "今天", "N 天後", and "已過" once it has gone by. It
// is soon from today to the third day after.
func dueIn(today, due time.Time) (text string, soon bool) {
	days := calendarDays(today, due)
	switch {
	case days < 0:
		return "已過", false
	case days == 0:
		return "今天", true
	}
	return strconv.Itoa(days) + " 天後", days <= dueSoonDays
}

// calendarDays is how many calendar days it is from one day to the other, whatever the hour of
// either and the zone each is in: the day is the one that is written.
func calendarDays(from, to time.Time) int {
	a := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	b := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(b.Sub(a).Hours() / 24)
}
