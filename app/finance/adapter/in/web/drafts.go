package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// draftsView is the drafts that wait, by month and then by account: the page that
// an imported month is checked on before it is confirmed. The rows of a group are
// the transactions page of that month and account, a click away.
type draftsView struct {
	Count   int
	Amounts []draftAmount
	Months  []draftMonthView
}

type draftMonthView struct {
	Label string
	Href  string
	Count int
	// Amounts are what all the drafts of the month would move.
	Amounts  []draftAmount
	Accounts []draftAccountView
}

type draftAccountView struct {
	Name string
	Href string
	// Count is how many drafts move the account.
	Count   int
	Amounts []draftAmount
	// NoAccount marks the drafts that move none, which have no amount.
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
	v := draftsView{Count: s.Count}
	for _, a := range s.Amounts {
		v.Amounts = append(v.Amounts, draftAmountOf(r, a))
	}
	for _, m := range s.Months {
		month := draftMonthView{
			Label: webserver.FormatMonth(m.Month),
			Href:  draftsOf(m.Month, nil),
			Count: m.Count,
		}
		for _, a := range m.Amounts {
			month.Amounts = append(month.Amounts, draftAmountOf(r, a))
		}
		for _, a := range m.Accounts {
			row := draftAccountView{Count: a.Count, NoAccount: a.AccountID == nil}
			if a.AccountID != nil {
				row.Name = r.accountName(*a.AccountID)
			} else {
				row.Name = "沒有帳戶的草稿"
			}
			row.Href = draftsOf(m.Month, a.AccountID)
			for _, amount := range a.Amounts {
				row.Amounts = append(row.Amounts, draftAmountOf(r, amount))
			}
			month.Accounts = append(month.Accounts, row)
		}
		v.Months = append(v.Months, month)
	}
	return v
}

// draftsOf is the transactions page of the drafts of a month, of an account when
// there is one.
func draftsOf(month time.Time, account *uuid.UUID) string {
	q := url.Values{"month": {webserver.FormatMonth(month)}, "status": {"draft"}}
	if account != nil {
		q.Set("account", account.String())
	}
	return link("/app/transactions", q)
}
