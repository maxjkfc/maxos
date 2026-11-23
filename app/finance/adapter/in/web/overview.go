package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// overviewView is the overview page: what the accounts hold (or would, once the drafts
// that wait are confirmed: see Preview), the instalments that are not yet due, what
// the parties owe me and I them, and the drafts that wait. There is no figure for the
// net worth: it needs the prices of what is held.
type overviewView struct {
	Today string
	// Preview is what the page says of the balances being what the accounts would hold
	// with the drafts confirmed, and not what they hold: it has no count when no draft
	// waits, and then the balances are what the accounts hold, as they always were.
	Preview previewView
	// Groups are the balances of the accounts by institution, the accounts that have
	// none last; ZeroAccounts are the accounts in use that hold nothing at all,
	// which are not worth a row each.
	Groups       []balanceGroup
	ZeroAccounts []linkView
	Plans        []planRow
	Dealings     []dealingRow
	ZeroParties  []string
	Drafts       draftBox
}

// previewView is what the balances are said to be: the use case that reads them says
// how many drafts they count in and whether anything of them is confirmed, from the
// same read, so that the heading, the note and each line say one thing.
type previewView struct {
	// Count is how many drafts wait: the number the balances are said to count in.
	Count int
	Href  string
	// AllDrafts is true when nothing of any balance listed is confirmed: every number
	// is a draft's, which the note says.
	AllDrafts bool
}

type linkView struct {
	Name string
	Href string
}

// balanceGroup is the accounts of one institution.
type balanceGroup struct {
	Name     string
	Accounts []accountBalances
}

type accountBalances struct {
	Name     string
	Kind     string
	Archived bool
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

// draftBox is the drafts that wait, as the overview shows them.
type draftBox struct {
	Count int
	// OwnTransfers is how many of them only move money between my own accounts,
	// which Amounts do not count.
	OwnTransfers int
	Href         string
	// Amounts is what confirming them today would change in the balances.
	Amounts []draftAmount
}

// draftAmount is what the drafts would move of one instrument.
type draftAmount struct {
	Outflow amountView
	Inflow  amountView
	Net     amountView
}

const (
	noInstitution = "沒有機構"
	unbilledLabel = "未到期分期"
	dealingLabel  = "往來"
	draftsHref    = "/app/drafts"
)

// overview reads the balances with the drafts that wait counted in, the plans that
// still have something to charge, the dealings and the summary of the drafts, and the
// lists that name them.
func (h *handlers) overview(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
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
	names, err := h.loadRefs(ctx, needAccounts|needInstitutions|needParties)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	return webserver.Page{
		Name:  "overview",
		Title: "總覽",
		Nav:   navOverview,
		Data:  buildOverview(names, h.today(), preview, dealings.Dealings, plans.Plans, drafts),
	}, nil
}

// buildOverview lays the answers out. An account that holds nothing, of any
// instrument and not owing any instalment, and of which nothing is confirmed, is one
// line in a list that is folded; an archived one that holds nothing is not shown at
// all.
func buildOverview(r *refs, today time.Time, preview usecase.GetPreviewBalancesOutput, dealings []usecase.Dealing, plans []usecase.InstalmentPlanSummary, drafts usecase.GetDraftSummaryOutput) overviewView {
	v := overviewView{Today: webserver.FormatDate(today), Drafts: draftsBox(r, drafts)}

	byAccount := make(map[uuid.UUID][]usecase.PreviewBalance)
	for _, b := range preview.Balances {
		byAccount[b.AccountID] = append(byAccount[b.AccountID], b)
	}
	groups := make(map[uuid.UUID]*balanceGroup)
	for _, a := range r.accounts {
		held := byAccount[a.ID]
		if !holdsAnything(held) {
			if a.ArchivedAt == nil {
				v.ZeroAccounts = append(v.ZeroAccounts, linkView{Name: a.Name, Href: accountLink(a.ID)})
			}
			continue
		}
		key := uuid.Nil
		if a.InstitutionID != nil {
			key = *a.InstitutionID
		}
		group, ok := groups[key]
		if !ok {
			group = &balanceGroup{Name: noInstitution}
			if key != uuid.Nil {
				group.Name = r.name(key)
			}
			groups[key] = group
		}
		group.Accounts = append(group.Accounts, accountBalances{
			Name:     a.Name,
			Kind:     accountKindLabel(a.Kind),
			Archived: a.ArchivedAt != nil,
			Href:     accountLink(a.ID),
			Lines:    balanceLines(r, held),
		})
	}
	// By the name of the institution, as the catalog lists them; those with no
	// institution last.
	for _, institution := range r.institutions {
		if group, ok := groups[institution.ID]; ok {
			v.Groups = append(v.Groups, *group)
		}
	}
	if group, ok := groups[uuid.Nil]; ok {
		v.Groups = append(v.Groups, *group)
	}
	v.Preview = previewView{Count: preview.DraftCount, Href: draftsHref, AllDrafts: preview.DraftCount > 0 && !preview.HasConfirmed && len(v.Groups) > 0}

	for _, p := range plans {
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

	v.Dealings, v.ZeroParties = dealingRows(r, dealings)
	return v
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
// whole of is the amount, with none of it confirmed, and without the colour of a loss,
// for nothing is lost that was held.
func figureLine(r *refs, label string, instrument uuid.UUID, f usecase.PreviewFigure) balanceLine {
	switch f.Basis {
	case usecase.PreviewBasisDrafts:
		return balanceLine{Label: label, Amount: r.plain(instrument, f.Total), Unconfirmed: true}
	case usecase.PreviewBasisBoth:
		return balanceLine{Label: label, Amount: r.money(instrument, f.Total), Confirmed: new(r.plain(instrument, f.Confirmed))}
	default:
		return balanceLine{Label: label, Amount: r.money(instrument, f.Total)}
	}
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

// draftsBox is the count of the drafts and what they would change in the balances.
func draftsBox(r *refs, drafts usecase.GetDraftSummaryOutput) draftBox {
	box := draftBox{Count: drafts.Count, OwnTransfers: drafts.OwnTransfers, Href: draftsHref}
	for _, a := range drafts.Amounts {
		box.Amounts = append(box.Amounts, draftAmountOf(r, a))
	}
	return box
}

func draftAmountOf(r *refs, a usecase.DraftAmount) draftAmount {
	return draftAmount{
		Outflow: r.money(a.InstrumentID, a.Outflow),
		Inflow:  r.money(a.InstrumentID, a.Inflow),
		Net:     r.signed(a.InstrumentID, a.Net),
	}
}

// takenOutOf is what the drafts would move of one instrument, as it is taken out of a
// total: each amount with a minus sign and without the colour of a loss, for it is
// no loss but money that the rows of the accounts have twice.
func takenOutOf(r *refs, a usecase.DraftAmount) draftAmount {
	return draftAmount{
		Outflow: r.taken(a.InstrumentID, a.Outflow),
		Inflow:  r.taken(a.InstrumentID, a.Inflow),
		Net:     r.signed(a.InstrumentID, a.Net),
	}
}

// accountLink is the page of the transactions of an account.
func accountLink(id uuid.UUID) string {
	return link("/app/transactions", url.Values{"account": {id.String()}})
}
