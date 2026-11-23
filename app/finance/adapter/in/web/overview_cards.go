package web

import (
	"html/template"
	"slices"
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
	// change nothing today (those dated later, those of no account).
	Nets []amountView
}

func draftsCardOf(r *refs, drafts usecase.GetDraftSummaryOutput) draftsCard {
	c := draftsCard{Count: drafts.Count, Text: countText(drafts.Count), Href: draftsHref}
	for _, a := range drafts.Amounts {
		c.Nets = append(c.Nets, r.changed(a.InstrumentID, a.Net))
	}
	return c
}

// trendCard is what was spent in each of the last twelve months, as columns, with the
// numbers in a table beside them. There is nothing to draw when nothing is spent in any
// of them: the card says so, and does not draw twelve empty columns.
type trendCard struct {
	Title string
	Chart template.HTML
	Rows  []trendRow
	Href  string
}

func trendCardOf(r *refs, today time.Time, months []usecase.MonthSpending) trendCard {
	c := trendCard{Title: lastTwelveMonths, Href: spendingHref}
	if !slices.ContainsFunc(months, func(m usecase.MonthSpending) bool { return !m.Total.IsZero() }) {
		return c
	}
	rows, columns := trendBars(r, months, model.MonthOf(today))
	c.Rows = rows
	c.Chart = svgchart.Columns(svgchart.Chart{ID: "overview-trend", Title: lastTwelveMonths}, columns)
	return c
}

// topCard is the categories this month's spending went to, the largest first, as bars.
type topCard struct {
	Chart template.HTML
	// More is whether there are categories that are not drawn.
	More bool
	Href string
}

func topCardOf(r *refs, spent usecase.GetSpendingOutput) topCard {
	c := topCard{Href: spendingHref, More: len(spent.TopLevel) > topCategories}
	bars := make([]svgchart.Bar, 0, topCategories)
	for _, t := range spent.TopLevel[:min(len(spent.TopLevel), topCategories)] {
		total := r.base(t.Total)
		bars = append(bars, svgchart.Bar{Label: r.categoryName(t.CategoryID), Value: t.Total, Text: total.Text + " " + total.Unit, Href: spendingHref})
	}
	c.Chart = svgchart.HBars(svgchart.Chart{ID: "overview-top", Title: "本月支出最多的類別"}, bars)
	return c
}

// paymentsCard is the cards that have statements, with the page of all of them: a card there
// is for each card that is in use and has a closing day, and none when there is not one.
type paymentsCard struct {
	Cards []cardDue
	Href  string
	// SomeEmpty is whether the open statement of any of the cards has nothing confirmed on it:
	// a row without an amount is that, and the card says so once and not on each row.
	SomeEmpty bool
}

func paymentsCardOf(r *refs, today time.Time, cards []cardStatements) paymentsCard {
	c := paymentsCard{Cards: dueCardsOf(r, today, cards), Href: cardsHref}
	for _, card := range c.Cards {
		c.SomeEmpty = c.SomeEmpty || len(card.Open.Amounts) == 0
	}
	return c
}

// cardDue is a card that has statements: the one that is open, and the one that closed last
// while it is still to be paid. What they say counts the confirmed transactions only, as every
// statement does.
type cardDue struct {
	Name string
	// Href is the open statement.
	Href string
	// Last is the statement that closed last, when its payment due day is today or after
	// and it has something on it: what the card owed when it closed.
	Last *dueStatement
	// Open is the statement that is open: what was spent on the card so far.
	Open *dueStatement
}

type dueStatement struct {
	Href      string
	ClosingOn string
	// DueOn is the payment due day, a dash when the card has none.
	DueOn string
	// Amounts are its figures, an instrument at a time; none when nothing is confirmed on it.
	// They have no colour for a loss: what a card owes is no loss.
	Amounts []amountView
}

// dueCardsOf writes the statements of each card that has an open one. The statement that
// closed last is there while its payment due day has not passed, and has figures: it is what
// is to be paid next; the open statement is there always, with what is spent on it so far.
func dueCardsOf(r *refs, today time.Time, cards []cardStatements) []cardDue {
	var out []cardDue
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
		card := cardDue{Name: c.account.Name, Href: statementLink(c.account.ID, open.ClosingOn), Open: new(dueStatementOf(r, c.account.ID, *open))}
		if last != nil && last.DueOn != nil && !last.DueOn.Before(today) && len(last.Figures) > 0 {
			card.Last = new(dueStatementOf(r, c.account.ID, *last))
		}
		out = append(out, card)
	}
	return out
}

// dueStatementOf writes a statement: what the card owed when it closed, or what was spent on
// it so far when it is open.
func dueStatementOf(r *refs, card uuid.UUID, s usecase.CardStatementSummary) dueStatement {
	d := dueStatement{Href: statementLink(card, s.ClosingOn), ClosingOn: webserver.FormatDate(s.ClosingOn), DueOn: "—"}
	if s.DueOn != nil {
		d.DueOn = webserver.FormatDate(*s.DueOn)
	}
	for _, f := range s.Figures {
		amount := f.Charges
		if s.Closed {
			amount = f.Closing
		}
		d.Amounts = append(d.Amounts, r.plain(f.InstrumentID, amount))
	}
	return d
}
