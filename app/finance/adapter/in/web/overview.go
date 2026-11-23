package web

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// overviewView is the overview page, which is the home screen: what the accounts hold
// (or would, once the drafts that wait are confirmed: see Preview) as one headline
// figure for each instrument, a card for each of the things worth a look (what was
// spent this month, the drafts that wait, what the parties owe me and I them, the
// cards that are to be paid), the charts of what was spent, then the accounts by kind
// and the instalments that are not yet due. There is no figure for the net worth: it
// needs the prices of what is held, and no instrument is converted to another here.
type overviewView struct {
	Today string
	// Preview is what the page says of the balances being what the accounts would hold
	// with the drafts confirmed, and not what they hold: it has no count when no draft
	// waits, and then the balances are what the accounts hold, as they always were.
	Preview previewView
	Hero    heroView
	// The cards: what was spent this month, the drafts that wait, the dealings (a card
	// only when something is owed), and the cards to be paid (when there are any).
	Spending spendingCard
	Drafts   draftsCard
	Dealings dealingsCard
	Payments paymentsCard
	Trend    trendCard
	Top      topCard
	// Groups are the balances of the accounts by kind of account; ZeroAccounts are the
	// accounts in use that hold nothing at all, which are not worth a row each.
	Groups       []accountGroup
	ZeroAccounts []linkView
	Plans        []planRow
}

// previewView is what the balances are said to be: the use case that reads them says
// how many drafts they count in and whether anything of them is confirmed, from the
// same read, so that the heading, the note and each line say one thing.
type previewView struct {
	// Count is how many drafts wait: the number the balances are said to count in; Text is
	// how it is written.
	Count int
	Text  string
	Href  string
	// AllDrafts is true when nothing of any balance listed is confirmed: every number
	// is a draft's, which the note says.
	AllDrafts bool
}

type linkView struct {
	Name string
	Href string
}

// heroView is the headline: all the balances added up an instrument at a time (the use
// case adds them), the instrument of the base currency large and the others beside
// each other under it, each with its own code. Nothing is converted.
type heroView struct {
	// Main is the total that is large: the base currency's, or, when that is nothing and
	// other instruments are held, the first of those (by code), which is not shown a second
	// time among Others. It is a total of nothing in the base currency when every total
	// adds up to nothing. Nil when there is no balance to write.
	Main *balanceLine
	// Unbilled is what the instrument of Main owes in instalments that are not yet due, a
	// line of its own, when it owes any.
	Unbilled *balanceLine
	// Others are the totals of the other instruments that are not nothing.
	Others []totalTile
	// Size is the class that sizes the figure by how long it is written: a figure of
	// twenty characters is not as big as one of seven, so that it stays inside its card on
	// a phone. Empty for the size of most.
	Size string
}

// totalTile is the lines of the total of one instrument: what is held of it and, apart,
// what is owed of it in instalments that are not yet due.
type totalTile struct {
	Lines []balanceLine
}

// accountGroup is the accounts of one kind.
type accountGroup struct {
	Name     string
	Accounts []accountBalances
	// Totals is the accounts of the kind added up, an instrument at a time, as the use
	// case adds them. A group of one account has none: its total is the account's own.
	Totals []totalTile
}

type accountBalances struct {
	Name string
	// Institution is the one that keeps the account, empty for cash and a wallet that I
	// keep myself.
	Institution string
	Archived    bool
	// Href is the page of the transactions of the account.
	Href  string
	Lines []balanceLine
}

// balanceLine is one line of an account: what it holds of an instrument, or the
// principal of its instalments that has not fallen due.
type balanceLine struct {
	// Label is empty for a balance, and says what it is otherwise.
	Label  string
	Amount amountView
	// Confirmed is the part of a figure that is confirmed, when drafts change it and
	// something is confirmed of it: Amount is then what the account would hold with the
	// drafts confirmed, and this is what it holds now. It is nil for a figure that no
	// draft changes, whose Amount is what the account holds, and for a figure of which
	// none is confirmed (Unconfirmed).
	Confirmed *amountView
	// Unconfirmed is true for a figure that drafts make up the whole of: none of it is
	// confirmed, which is said in words and not as a figure of nothing, so that a list
	// of balances that are all drafts is not a column of zeros. Its Amount is not a loss
	// that is coloured as one: nothing is lost that was held.
	Unconfirmed bool
}

type planRow struct {
	Account   string
	Href      string
	Principal amountView
	Charged   amountView
	Remaining amountView
	NextDue   string
}

type dealingRow struct {
	Party string
	Lines []dealingLine
}

type dealingLine struct {
	Amount  amountView
	Meaning string
}

const (
	unbilledLabel = "未到期分期"
	dealingLabel  = "往來"
	draftsHref    = "/app/drafts"
	spendingHref  = "/app/spending"
	cardsHref     = "/app/cards"
)

// kindOrder is the order the accounts are listed in, by kind: what is kept in a bank, what is
// owed on a card, what is in the pocket, then what is invested, and what is held elsewhere.
// A kind that is not here is last, and the kinds that are among themselves by name.
var kindOrder = []model.AccountKind{
	model.AccountKindBank,
	model.AccountKindCard,
	model.AccountKindCash,
	model.AccountKindBrokerage,
	model.AccountKindExchange,
	model.AccountKindWallet,
	model.AccountKindLoan,
}

// kindRank is the place of a kind of account in the list of the overview.
func kindRank(kind string) int {
	if i := slices.Index(kindOrder, model.AccountKind(kind)); i >= 0 {
		return i
	}
	return len(kindOrder)
}

// overviewAnswers is what the use cases answered, which the overview lays out.
type overviewAnswers struct {
	today    time.Time
	preview  usecase.GetPreviewBalancesOutput
	dealings []usecase.Dealing
	plans    []usecase.InstalmentPlanSummary
	drafts   usecase.GetDraftSummaryOutput
	// spent is what was spent this month, and trend the months of the last year.
	spent usecase.GetSpendingOutput
	trend []usecase.MonthSpending
	// cards are the statements of each card that has a closing day.
	cards []cardStatements
}

// overview reads the balances with the drafts that wait counted in, the dealings, the
// plans that still have something to charge, the summary of the drafts, what was spent
// this month and in the months of the last year, the statements of each card that has
// a closing day, and the lists that name them. The number of calls is fixed but for the
// cards, which have one call each: that is the number of cards, and not of
// transactions.
func (h *handlers) overview(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	today := h.today()
	month := model.MonthOf(today)
	from, to := trendWindow(month, month)

	preview, err := h.p.GetPreviewBalances.GetPreviewBalances(ctx, usecase.GetPreviewBalancesInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	dealings, err := h.p.GetDealings.GetDealings(ctx, usecase.GetDealingsInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	plans, err := h.p.ListInstalmentPlans.ListInstalmentPlans(ctx, usecase.ListInstalmentPlansInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	drafts, err := h.p.GetDraftSummary.GetDraftSummary(ctx, usecase.GetDraftSummaryInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	spent, err := h.p.GetSpending.GetSpending(ctx, usecase.GetSpendingInput{Month: month})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	trend, err := h.p.GetSpendingTrend.GetSpendingTrend(ctx, usecase.GetSpendingTrendInput{From: from, To: to})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	names, err := h.loadRefs(ctx, needAccounts|needInstitutions|needParties|needCategories)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	cards, err := h.cardStatements(ctx, names, today)
	if err != nil {
		return webserver.Page{}, err
	}
	return webserver.Page{
		Name:  "overview",
		Title: "總覽",
		Nav:   navOverview,
		Data: buildOverview(names, overviewAnswers{
			today: today, preview: preview, dealings: dealings.Dealings, plans: plans.Plans, drafts: drafts,
			spent: spent, trend: trend.Months, cards: cards,
		}),
	}, nil
}

// buildOverview lays the answers out. An account that holds nothing, of any
// instrument and not owing any instalment, and of which nothing is confirmed, is one
// line in a list that is folded; an archived one that holds nothing is not shown at
// all.
func buildOverview(r *refs, a overviewAnswers) overviewView {
	v := overviewView{
		Today:    webserver.FormatDate(a.today),
		Hero:     heroOf(r, a.preview.Totals),
		Spending: spendingCardOf(r, a.today, a.spent, a.trend, a.drafts.Count),
		Drafts:   draftsCardOf(r, a.drafts),
		Dealings: dealingsCardOf(r, a.dealings),
		Payments: paymentsCardOf(r, a.today, a.cards),
		Trend:    trendCardOf(r, a.today, a.trend),
		Top:      topCardOf(r, a.spent),
	}
	v.Preview = previewView{
		Count:     a.preview.DraftCount,
		Text:      countText(a.preview.DraftCount),
		Href:      draftsHref,
		AllDrafts: a.preview.DraftCount > 0 && !a.preview.HasConfirmed && v.Hero.Main != nil,
	}
	v.Groups, v.ZeroAccounts = accountGroups(r, a.preview)

	for _, p := range a.plans {
		next := "—"
		if p.NextDueOn != nil {
			next = webserver.FormatDate(*p.NextDueOn)
		}
		v.Plans = append(v.Plans, planRow{
			Account:   r.accountName(p.AccountID),
			Href:      "/app/transactions/" + p.PlanID.String(),
			Principal: r.money(p.InstrumentID, p.Principal),
			Charged:   r.money(p.InstrumentID, p.Charged),
			Remaining: r.money(p.InstrumentID, p.Remaining),
			NextDue:   next,
		})
	}
	return v
}

// heroOf writes the totals of the instruments. The headline is the total of the base
// currency when that is not nothing, else the first instrument that has any, in the order
// the use case gives (by code): a headline of nothing over the figures of what is held is
// not what a reader looks for, and nothing is converted to make it. The others that are
// not nothing come after it, each with its own code. When every total adds up to nothing
// the headline is the base currency's, which says so: there is no headline only when
// there is no balance at all.
func heroOf(r *refs, totals []usecase.PreviewTotal) heroView {
	if len(totals) == 0 {
		return heroView{}
	}
	var held []usecase.PreviewTotal
	for _, t := range totals {
		if hasAnything(t.Held) || hasAnything(t.Unbilled) {
			held = append(held, t)
		}
	}
	main := usecase.PreviewTotal{InstrumentID: model.BaseCurrencyID}
	if i := slices.IndexFunc(held, func(t usecase.PreviewTotal) bool { return t.InstrumentID == model.BaseCurrencyID }); i >= 0 {
		main = held[i]
	} else if len(held) > 0 {
		main = held[0]
	}

	var hero heroView
	for _, t := range held {
		if t.InstrumentID != main.InstrumentID {
			hero.Others = append(hero.Others, totalTileOf(r, t))
		}
	}
	line := figureLine(r, "", main.InstrumentID, main.Held)
	hero.Main = &line
	hero.Size = heroSize(line.Amount.Text)
	if shows(main.Unbilled) {
		hero.Unbilled = new(figureLine(r, unbilledLabel, main.InstrumentID, main.Unbilled))
	}
	return hero
}

// How many characters a headline has before it is written smaller: the figure of most
// balances is shorter than the first, and what is longer than the second is that of a
// very large one. The sizes are in the stylesheet, which fits them to the width of a phone.
const (
	heroMediumFrom = 16
	heroLargeFrom  = 20
)

// heroSize is the class of the headline of a figure written with the given text.
func heroSize(amount string) string {
	switch n := utf8.RuneCountInString(amount); {
	case n >= heroLargeFrom:
		return "hero-l"
	case n >= heroMediumFrom:
		return "hero-m"
	}
	return ""
}

// totalTileOf is a total as the lines of the balance of an account are: what is held,
// and what is owed in instalments that are not yet due, a line of its own.
func totalTileOf(r *refs, t usecase.PreviewTotal) totalTile {
	return totalTile{Lines: balanceLines(r, []usecase.PreviewBalance{{InstrumentID: t.InstrumentID, Held: t.Held, Unbilled: t.Unbilled}})}
}

// totalTilesOf is the totals that are not nothing, as they come.
func totalTilesOf(r *refs, totals []usecase.PreviewTotal) []totalTile {
	var tiles []totalTile
	for _, t := range totals {
		if hasAnything(t.Held) || hasAnything(t.Unbilled) {
			tiles = append(tiles, totalTileOf(r, t))
		}
	}
	return tiles
}

// accountGroups lists the accounts that hold something under the kind of account they are,
// in the order of kindOrder and by name in each, as the catalog lists them. The accounts that
// hold nothing are only named, those of them that are in use.
func accountGroups(r *refs, preview usecase.GetPreviewBalancesOutput) ([]accountGroup, []linkView) {
	byAccount := make(map[uuid.UUID][]usecase.PreviewBalance)
	for _, b := range preview.Balances {
		byAccount[b.AccountID] = append(byAccount[b.AccountID], b)
	}
	totals := make(map[string][]usecase.PreviewTotal, len(preview.Kinds))
	for _, k := range preview.Kinds {
		totals[k.Kind] = k.Totals
	}

	var zero []linkView
	groups := make(map[string]*accountGroup)
	for _, a := range r.accounts {
		held := byAccount[a.ID]
		if !holdsAnything(held) {
			if a.ArchivedAt == nil {
				zero = append(zero, linkView{Name: a.Name, Href: accountLink(a.ID)})
			}
			continue
		}
		group, ok := groups[a.Kind]
		if !ok {
			group = &accountGroup{Name: accountKindLabel(a.Kind)}
			groups[a.Kind] = group
		}
		group.Accounts = append(group.Accounts, accountBalances{
			Name:        a.Name,
			Institution: r.optionalName(a.InstitutionID),
			Archived:    a.ArchivedAt != nil,
			Href:        accountLink(a.ID),
			Lines:       balanceLines(r, held),
		})
	}

	kinds := slices.SortedFunc(maps.Keys(groups), func(a, b string) int {
		return cmp.Or(cmp.Compare(kindRank(a), kindRank(b)), cmp.Compare(a, b))
	})
	out := make([]accountGroup, 0, len(kinds))
	for _, kind := range kinds {
		group := *groups[kind]
		if len(group.Accounts) > 1 {
			group.Totals = totalTilesOf(r, totals[kind])
		}
		out = append(out, group)
	}
	return out, zero
}

// holdsAnything is whether an account holds or owes anything of any instrument, or
// would: a figure that is not nothing in total, or that something is confirmed of, as
// when the drafts take all of what an account holds. An account that does neither is
// listed in the fold, and one of which something is confirmed is listed in the open.
func holdsAnything(balances []usecase.PreviewBalance) bool {
	for _, b := range balances {
		if hasAnything(b.Held) || hasAnything(b.Unbilled) {
			return true
		}
	}
	return false
}

// hasAnything is whether a figure is not nothing, in total or in what is confirmed.
func hasAnything(f usecase.PreviewFigure) bool {
	return !f.Total.IsZero() || !f.Confirmed.IsZero()
}

// shows is whether a figure is a line of its own: its total is not nothing, or drafts
// change it, which they do when they take all of what is confirmed of it, and then the
// line says that it is nothing, and what is confirmed.
func shows(f usecase.PreviewFigure) bool {
	return !f.Total.IsZero() || !f.Drafts.IsZero()
}

// balanceLines is a line for each instrument an account holds, in the order the use
// case lists them, and one for what it owes in instalments that are not yet due,
// which is not what it holds and is told apart by its label. An instrument whose
// quantity is nothing and which only has instalments not yet due has just that
// line.
func balanceLines(r *refs, balances []usecase.PreviewBalance) []balanceLine {
	var lines []balanceLine
	for _, b := range balances {
		owes := shows(b.Unbilled)
		if shows(b.Held) || !owes {
			lines = append(lines, figureLine(r, "", b.InstrumentID, b.Held))
		}
		if owes {
			lines = append(lines, figureLine(r, unbilledLabel, b.InstrumentID, b.Unbilled))
		}
	}
	return lines
}

// figureLine writes a figure as what it is made of says: what no draft changes is the
// amount, as it always was; what is confirmed in part and changed by drafts is the
// amount it would be, with what is confirmed of it beside; and what drafts make the
// whole of is the amount, with none of it confirmed. Only a loss that is confirmed has the
// colour of one: a figure that is negative only because the drafts would make it so is a
// plain figure with its sign, and so is one that is all drafts, for nothing that was held
// is lost, and a reader has no alarm to take from it.
func figureLine(r *refs, label string, instrument uuid.UUID, f usecase.PreviewFigure) balanceLine {
	amount := r.plain(instrument, f.Total)
	if confirmedBasis(f) {
		amount = r.money(instrument, f.Total)
	}
	switch f.Basis {
	case usecase.PreviewBasisDrafts:
		return balanceLine{Label: label, Amount: amount, Unconfirmed: true}
	case usecase.PreviewBasisBoth:
		return balanceLine{Label: label, Amount: amount, Confirmed: new(r.plain(instrument, f.Confirmed))}
	default:
		return balanceLine{Label: label, Amount: amount}
	}
}

// confirmedBasis is whether what a figure is, when it is a loss, is a loss that is confirmed:
// no draft changes a figure of the basis confirmed, so what it is is all confirmed; one that
// drafts change is confirmed in the part that is not theirs, and is a loss that is confirmed
// when that part is negative too; one that is all drafts has none.
func confirmedBasis(f usecase.PreviewFigure) bool {
	switch f.Basis {
	case usecase.PreviewBasisDrafts:
		return false
	case usecase.PreviewBasisBoth:
		return f.Confirmed.IsNegative()
	default:
		return true
	}
}

// dealingsCard is what the parties owe me and I them: there is a card for it only when
// something is owed, and the parties whose dealings all add up to nothing are only named.
type dealingsCard struct {
	Rows []dealingRow
	Zero []string
}

func dealingsCardOf(r *refs, dealings []usecase.Dealing) dealingsCard {
	rows, zero := dealingRows(r, dealings)
	return dealingsCard{Rows: rows, Zero: zero}
}

// dealingRows is a row for each party that owes me or that I owe anything, with a
// line for each instrument; the parties whose dealings all add up to nothing are
// only named. The use case lists them by party, and the instruments of a party
// together.
func dealingRows(r *refs, dealings []usecase.Dealing) ([]dealingRow, []string) {
	var (
		rows []dealingRow
		zero []string
	)
	for i := 0; i < len(dealings); {
		j := i
		for j < len(dealings) && dealings[j].PartyID == dealings[i].PartyID {
			j++
		}
		row := dealingRow{Party: r.name(dealings[i].PartyID)}
		owed := false
		for _, d := range dealings[i:j] {
			owed = owed || !d.Quantity.IsZero()
			row.Lines = append(row.Lines, dealingLine{Amount: r.money(d.InstrumentID, d.Quantity), Meaning: dealingMeaning(d)})
		}
		if owed {
			rows = append(rows, row)
		} else {
			zero = append(zero, row.Party)
		}
		i = j
	}
	return rows, zero
}

// dealingMeaning says in words whose the amount is: a positive amount is what the
// party owes me and a negative one what I owe them, which the sign alone does not
// say to a reader who has not read the glossary.
func dealingMeaning(d usecase.Dealing) string {
	switch {
	case d.Quantity.IsPositive():
		return "對方欠我"
	case d.Quantity.IsNegative():
		return "我欠對方"
	default:
		return "沒有欠款"
	}
}

// accountLink is the page of the transactions of an account.
func accountLink(id uuid.UUID) string {
	return link("/app/transactions", url.Values{"account": {id.String()}})
}

// cardStatements is the statements one card has, as ListCardStatements lists them.
type cardStatements struct {
	account    usecase.Account
	statements []usecase.CardStatementSummary
}

// How far the statements asked for reach from today: the last that closed closed within
// the month before, and the one that is open closes within the month after.
const (
	dueDaysBack  = 35
	dueDaysAhead = 31
)

// cardStatements reads the statements around today of each card that is in use and has a
// closing day, one call for each: the days a statement covers are the card's own. The
// number of calls is the number of cards, and does not change with the number of
// transactions.
func (h *handlers) cardStatements(ctx context.Context, r *refs, today time.Time) ([]cardStatements, error) {
	var cards []cardStatements
	for _, a := range r.accounts {
		if a.Kind != string(model.AccountKindCard) || a.ClosingDay == nil || a.ArchivedAt != nil {
			continue
		}
		out, err := h.p.ListCardStatements.ListCardStatements(ctx, usecase.ListCardStatementsInput{
			AccountID: a.ID,
			From:      today.AddDate(0, 0, -dueDaysBack),
			To:        today.AddDate(0, 0, dueDaysAhead),
		})
		if err != nil {
			return nil, fail(err)
		}
		cards = append(cards, cardStatements{account: a, statements: out.Statements})
	}
	return cards, nil
}
