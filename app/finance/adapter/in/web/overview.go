package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// overviewView is the overview page: what the accounts hold, the instalments that
// are not yet due, what the parties owe me and I them, and the drafts that wait.
// There is no figure for the net worth: it needs the prices of what is held.
type overviewView struct {
	Today string
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
)

// overview reads the balances, the plans that still have something to charge, the
// dealings and the summary of the drafts, and the lists that name them.
func (h *handlers) overview(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	balances, err := h.p.GetBalances.GetBalances(ctx, usecase.GetBalancesInput{})
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
		Data:  buildOverview(names, h.today(), balances.Balances, dealings.Dealings, plans.Plans, drafts),
	}, nil
}

// buildOverview lays the answers out. An account that holds nothing, of any
// instrument and not owing any instalment, is one line in a list that is folded; an
// archived one that holds nothing is not shown at all.
func buildOverview(r *refs, today time.Time, balances []usecase.Balance, dealings []usecase.Dealing, plans []usecase.InstalmentPlanSummary, drafts usecase.GetDraftSummaryOutput) overviewView {
	v := overviewView{Today: webserver.FormatDate(today), Drafts: draftsBox(r, drafts)}

	byAccount := make(map[uuid.UUID][]usecase.Balance)
	for _, b := range balances {
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

// holdsAnything is whether an account holds or owes anything of any instrument: a
// quantity that is not zero, or the principal of an instalment that is not yet due.
func holdsAnything(balances []usecase.Balance) bool {
	for _, b := range balances {
		if !b.Quantity.IsZero() || !b.UnbilledInstalment.IsZero() {
			return true
		}
	}
	return false
}

// balanceLines is a line for each instrument an account holds, in the order the use
// case lists them, and one for what it owes in instalments that are not yet due,
// which is not what it holds and is told apart by its label. An instrument whose
// quantity is nothing and which only has instalments not yet due has just that
// line.
func balanceLines(r *refs, balances []usecase.Balance) []balanceLine {
	var lines []balanceLine
	for _, b := range balances {
		owes := !b.UnbilledInstalment.IsZero()
		if !b.Quantity.IsZero() || !owes {
			lines = append(lines, balanceLine{Amount: r.money(b.InstrumentID, b.Quantity)})
		}
		if owes {
			lines = append(lines, balanceLine{Label: unbilledLabel, Amount: r.money(b.InstrumentID, b.UnbilledInstalment)})
		}
	}
	return lines
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
	box := draftBox{Count: drafts.Count, OwnTransfers: drafts.OwnTransfers, Href: "/app/drafts"}
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

// accountLink is the page of the transactions of an account.
func accountLink(id uuid.UUID) string {
	return link("/app/transactions", url.Values{"account": {id.String()}})
}
