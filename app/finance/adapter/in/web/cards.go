package web

import (
	"cmp"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/svgchart"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// How many closed statements the list of a card has, besides the open one.
const closedStatements = 12

// cardsView is the cards and their statements.
type cardsView struct {
	Cards []cardView
	// Without are the cards that have no closing day: no statement is cut for them.
	Without []cardNote
}

type cardNote struct {
	Name     string
	Archived bool
}

// cardView is a card with its open statement and the last closed ones.
type cardView struct {
	Name     string
	Archived bool
	Closing  string
	Due      string
	Rows     []statementRow
}

// statementRow is one statement of a card: the days it covers and its figures.
type statementRow struct {
	Href      string
	ClosingOn string
	Range     string
	Due       string
	// Open is whether the statement has not closed yet.
	Open    bool
	Figures []figureView
}

// figureView is what a card did in one instrument on a statement.
type figureView struct {
	Previous amountView
	Charges  amountView
	Credits  amountView
	Closing  amountView
}

// cards lists the cards: for each the open statement and the last closed ones, which
// are the statements closing in the thirteen months before today and in the month
// after it. A card has its own call, for the days a statement covers are the card's;
// that is the number of cards, and not of transactions.
func (h *handlers) cards(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	names, err := h.loadRefs(ctx, needAccounts)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	today := h.today()
	var v cardsView
	for _, a := range names.accounts {
		if a.Kind != string(model.AccountKindCard) {
			continue
		}
		if a.ClosingDay == nil {
			v.Without = append(v.Without, cardNote{Name: a.Name, Archived: a.ArchivedAt != nil})
			continue
		}
		out, err := h.p.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
			AccountID: a.ID,
			From:      today.AddDate(0, -13, 0),
			To:        today.AddDate(0, 0, 31),
		})
		if err != nil {
			return webserver.Page{}, fail(err)
		}
		v.Cards = append(v.Cards, buildCard(names, a, today, out.Statements))
	}
	return webserver.Page{Name: "cards", Title: "信用卡帳單", Nav: navCards, Data: v}, nil
}

// buildCard lays out the statements of a card that are shown.
func buildCard(r *refs, a usecase.Account, today time.Time, statements []usecase.CardStatementSummary) cardView {
	v := cardView{Name: a.Name, Archived: a.ArchivedAt != nil, Closing: "每月 " + strconv.Itoa(*a.ClosingDay) + " 日"}
	if a.PaymentDueDay != nil {
		v.Due = "每月 " + strconv.Itoa(*a.PaymentDueDay) + " 日"
	}
	for _, s := range shownStatements(statements, today) {
		v.Rows = append(v.Rows, statementRowOf(r, a.ID, s))
	}
	return v
}

// shownStatements picks, from statements that are newest first, the one that is open
// and the last closed ones: the open one is the one whose days include today, and
// those after it have not begun.
func shownStatements(statements []usecase.CardStatementSummary, today time.Time) []usecase.CardStatementSummary {
	var open, closed []usecase.CardStatementSummary
	for _, s := range statements {
		switch {
		case s.Closed:
			if len(closed) < closedStatements {
				closed = append(closed, s)
			}
		case !s.From.After(today) && !s.ClosingOn.Before(today):
			open = append(open, s)
		}
	}
	return append(open, closed...)
}

func statementRowOf(r *refs, card uuid.UUID, s usecase.CardStatementSummary) statementRow {
	row := statementRow{
		Href:      statementLink(card, s.ClosingOn),
		ClosingOn: webserver.FormatDate(s.ClosingOn),
		Range:     webserver.FormatDate(s.From) + " 至 " + webserver.FormatDate(s.ClosingOn),
		Due:       "—",
		Open:      !s.Closed,
	}
	if s.DueOn != nil {
		row.Due = webserver.FormatDate(*s.DueOn)
	}
	for _, f := range s.Figures {
		row.Figures = append(row.Figures, figuresOf(r, f))
	}
	return row
}

func figuresOf(r *refs, f usecase.StatementFigures) figureView {
	return figureView{
		Previous: r.money(f.InstrumentID, f.Previous),
		Charges:  r.money(f.InstrumentID, f.Charges),
		Credits:  r.money(f.InstrumentID, f.Credits),
		Closing:  r.money(f.InstrumentID, f.Closing),
	}
}

// statementLink is the page of the statement of a card that closes on a day.
func statementLink(card uuid.UUID, closingOn time.Time) string {
	return link("/app/cards/"+card.String(), url.Values{"closing": {webserver.FormatDate(closingOn)}})
}

// statementView is one statement: its days, its figures, its transactions, and
// where the charges went.
type statementView struct {
	Card      string
	CardHref  string
	Range     string
	ClosingOn string
	Due       string
	Open      bool
	// Prev and Next are the statements before and after it.
	Prev    string
	Next    string
	Figures []figureView
	// Drafts is how many drafts would change the figures once confirmed.
	Drafts     int
	DraftsHref string
	Rows       []statementTxRow
	Charges    []chargesView
}

// statementTxRow is a transaction on a statement, as a line of the bank's statement.
type statementTxRow struct {
	Href string
	// Date is the statement date, and Happened the day the transaction happened on
	// when that is another.
	Date        string
	Happened    string
	Kind        string
	Description string
	Merchant    string
	Categories  string
	// Amounts are what it moved on the card.
	Amounts []amountView
}

// chargesView is what the card was charged in one instrument on the statement, by
// the top-level category it was spent on: the chart, and the table that has the same
// numbers.
type chargesView struct {
	Instrument string
	Chart      template.HTML
	Rows       []chargeRow
	Total      amountView
}

type chargeRow struct {
	Label  string
	Amount amountView
	Share  string
}

// card shows one statement of a card, the one that has the day of the query, and
// the open one when it has none.
func (h *handlers) card(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	id, err := pathID(r.PathValue("account"), "這個帳戶")
	if err != nil {
		return webserver.Page{}, err
	}
	closing, err := dateParam(r.URL.Query(), "closing")
	if err != nil {
		return webserver.Page{}, err
	}
	statement, err := h.p.GetCardStatement.GetCardStatement(ctx, usecase.GetCardStatementInput{AccountID: id, ClosingOn: closing})
	if err != nil {
		return webserver.Page{}, notFoundAs(err, "找不到這個帳戶。")
	}
	names, err := h.loadRefs(ctx, needAccounts|needCategories)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	return webserver.Page{Name: "card", Title: "信用卡帳單明細", Nav: navCards, Data: buildStatement(names, id, statement)}, nil
}

// buildStatement lays a statement out.
func buildStatement(r *refs, card uuid.UUID, s usecase.CardStatement) statementView {
	v := statementView{
		Card:       r.accountName(card),
		CardHref:   "/app/cards",
		Range:      webserver.FormatDate(s.From) + " 至 " + webserver.FormatDate(s.ClosingOn),
		ClosingOn:  webserver.FormatDate(s.ClosingOn),
		Due:        "—",
		Open:       !s.Closed,
		Prev:       statementLink(card, s.From.AddDate(0, 0, -1)),
		Next:       statementLink(card, s.ClosingOn.AddDate(0, 0, 1)),
		Drafts:     s.Drafts,
		DraftsHref: "/app/drafts",
	}
	if s.DueOn != nil {
		v.Due = webserver.FormatDate(*s.DueOn)
	}
	for _, f := range s.Figures {
		v.Figures = append(v.Figures, figuresOf(r, f))
	}
	for _, st := range s.Transactions {
		t := st.Transaction
		row := statementTxRow{
			Href:        transactionLink(t.ID),
			Date:        webserver.FormatDate(st.StatementDate),
			Kind:        kindLabel(t.Kind),
			Description: t.Description,
			Merchant:    t.Merchant,
			Categories:  joinNames(categoryNames(r, t)),
		}
		if row.Description == "" {
			row.Description = firstMemo(t)
		}
		if !t.OccurredOn.Equal(st.StatementDate) {
			row.Happened = webserver.FormatDate(t.OccurredOn)
		}
		for _, a := range st.Amounts {
			row.Amounts = append(row.Amounts, r.signed(a.InstrumentID, a.Quantity))
		}
		v.Rows = append(v.Rows, row)
	}
	v.Charges = chargesOf(r, s.Transactions)
	return v
}

// chargeBucket is what one instrument was charged for.
type chargeBucket struct {
	byCategory map[uuid.UUID]decimal.Decimal
	// shares is what I paid for others, which they owe me, and other what no category
	// explains: a transfer, a dealing, a fee, the interest of an instalment.
	shares, other, total decimal.Decimal
}

// chargesOf adds up what the card was charged on the transactions of a statement,
// for each instrument, by the top-level category of what it was spent on. The
// charge of an expense goes to the categories of its lines, less what I paid for
// others, which is a row of its own; the charge of any other kind of transaction (a
// transfer out of the card, a dealing, a fee, the interest of a period of an
// instalment) is a row called other. So the rows of an instrument add up to what the
// statement says the card was charged in it.
func chargesOf(r *refs, txns []usecase.StatementTransaction) []chargesView {
	buckets := make(map[uuid.UUID]*chargeBucket)
	for _, st := range txns {
		for _, amount := range st.Amounts {
			if !amount.Quantity.IsNegative() {
				continue
			}
			charge := amount.Quantity.Neg()
			b, ok := buckets[amount.InstrumentID]
			if !ok {
				b = &chargeBucket{byCategory: make(map[uuid.UUID]decimal.Decimal)}
				buckets[amount.InstrumentID] = b
			}
			b.total = b.total.Add(charge)
			if st.Transaction.Kind != string(model.KindExpense) {
				b.other = b.other.Add(charge)
				continue
			}
			explained := decimal.Zero
			for _, p := range st.Transaction.Postings {
				if p.InstrumentID != amount.InstrumentID {
					continue
				}
				switch {
				case p.Book == string(model.BookExpense) && p.CategoryID != nil:
					top := r.topCategory(*p.CategoryID, nil)
					b.byCategory[top] = b.byCategory[top].Add(p.Quantity)
					explained = explained.Add(p.Quantity)
				case p.Book == string(model.BookDealing) && p.Quantity.IsPositive():
					b.shares = b.shares.Add(p.Quantity)
					explained = explained.Add(p.Quantity)
				}
			}
			b.other = b.other.Add(charge.Sub(explained))
		}
	}

	instruments := make([]uuid.UUID, 0, len(buckets))
	for id := range buckets {
		instruments = append(instruments, id)
	}
	slices.SortFunc(instruments, func(a, b uuid.UUID) int { return cmp.Compare(r.code(a), r.code(b)) })

	var views []chargesView
	for _, id := range instruments {
		views = append(views, chargesViewOf(r, id, buckets[id]))
	}
	return views
}

func chargesViewOf(r *refs, instrument uuid.UUID, b *chargeBucket) chargesView {
	type line struct {
		label  string
		amount decimal.Decimal
	}
	var lines []line
	for id, amount := range b.byCategory {
		lines = append(lines, line{r.categoryName(id), amount})
	}
	slices.SortFunc(lines, func(x, y line) int {
		if c := y.amount.Cmp(x.amount); c != 0 {
			return c
		}
		return cmp.Compare(x.label, y.label)
	})
	if b.shares.IsPositive() {
		lines = append(lines, line{"分攤（我替別人付的）", b.shares})
	}
	if !b.other.IsZero() {
		lines = append(lines, line{"其他（轉帳、往來、手續費、分期利息等）", b.other})
	}

	code := r.code(instrument)
	v := chargesView{Instrument: code, Total: r.money(instrument, b.total)}
	bars := make([]svgchart.Bar, len(lines))
	for i, l := range lines {
		amount := r.money(instrument, l.amount)
		v.Rows = append(v.Rows, chargeRow{Label: l.label, Amount: amount, Share: webserver.FormatPercent(l.amount, b.total)})
		bars[i] = svgchart.Bar{Label: l.label, Value: l.amount, Text: amount.Text + " " + amount.Unit}
	}
	v.Chart = svgchart.HBars(svgchart.Chart{
		ID:    "charges-" + code,
		Title: fmt.Sprintf("本期消費依類別（%s）", code),
	}, bars)
	return v
}
