package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// draftsView is the drafts that wait, by month and then by account: the page that
// an imported month is checked on before it is confirmed. The rows of a group are
// the transactions page of that month and account, a click away, which lists the
// drafts the group counts.
type draftsView struct {
	Count        int
	OwnTransfers int
	Months       []draftMonthView
}

type draftMonthView struct {
	Label string
	Href  string
	Count int
	// OwnTransfers is how many of the drafts of the month only move money between my
	// own accounts, which the amounts of the month do not count.
	OwnTransfers int
	// OwnTransferAmounts is the money they move, as it is taken out of the rows of the
	// accounts, which have it twice (it leaves one and arrives in another), to make the
	// amounts of the month: each of its amounts is written with a minus sign.
	OwnTransferAmounts []draftAmount
	// Amounts are what all the drafts of the month would change in the balances.
	Amounts  []draftAmount
	Accounts []draftAccountView
}

type draftAccountView struct {
	Name string
	Href string
	// Count is how many drafts are of the account.
	Count   int
	Amounts []draftAmount
	// NoAccount marks the drafts that are of none, which have no amount.
	NoAccount bool
}

// drafts reads the summary of the drafts and the lists that name its accounts and
// instruments: a fixed number of calls, whatever number of drafts there are.
func (h *handlers) drafts(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	summary, err := h.p.GetDraftSummary.GetDraftSummary(ctx, usecase.GetDraftSummaryInput{})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	names, err := h.loadRefs(ctx, needAccounts)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	return webserver.Page{Name: "drafts", Title: "草稿", Nav: navDrafts, Data: buildDrafts(names, summary)}, nil
}

// buildDrafts lays the summary out, the months and the accounts in the order the use
// case gives them: the newest month first, the accounts by name.
func buildDrafts(r *refs, s usecase.GetDraftSummaryOutput) draftsView {
	v := draftsView{Count: s.Count, OwnTransfers: s.OwnTransfers}
	for _, m := range s.Months {
		month := draftMonthView{
			Label:        webserver.FormatMonth(m.Month),
			Href:         draftsOf(m.Month, ""),
			Count:        m.Count,
			OwnTransfers: m.OwnTransfers,
		}
		for _, a := range m.OwnTransferAmounts {
			month.OwnTransferAmounts = append(month.OwnTransferAmounts, takenOutOf(r, a))
		}
		for _, a := range m.Amounts {
			month.Amounts = append(month.Amounts, draftAmountOf(r, a))
		}
		for _, a := range m.Accounts {
			row := draftAccountView{Count: a.Count, NoAccount: a.AccountID == nil}
			if a.AccountID != nil {
				row.Name = r.accountName(*a.AccountID)
				row.Href = draftsOf(m.Month, a.AccountID.String())
			} else {
				row.Name = "沒有帳戶的草稿"
				row.Href = draftsOf(m.Month, noAccount)
			}
			for _, amount := range a.Amounts {
				row.Amounts = append(row.Amounts, draftAmountOf(r, amount))
			}
			month.Accounts = append(month.Accounts, row)
		}
		v.Months = append(v.Months, month)
	}
	return v
}

// draftsOf is the transactions page of the drafts of a month: of an account, or of
// none (noAccount), when there is one to say; all of the month's otherwise. It lists a
// plan by its origin and leaves its periods out, which is how the page counts it.
func draftsOf(month time.Time, account string) string {
	q := url.Values{
		"month":   {webserver.FormatMonth(month)},
		"status":  {string(model.StatusDraft)},
		"account": {account},
		"periods": {noPeriods},
	}
	return link("/app/transactions", q)
}

// draftAmount is what the drafts would move of one instrument.
type draftAmount struct {
	Outflow amountView
	Inflow  amountView
	Net     amountView
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
