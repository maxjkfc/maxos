package web

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// pageSize is how many transactions a page of the list has. A month with more is on
// more than one page: the list asks the use case for one page at a time, so that
// the cost of a page does not grow with the month.
const pageSize = 100

// transactionsView is the list of the transactions of one month.
type transactionsView struct {
	Month   string
	Prev    string
	Next    string
	Current string
	Filters filterView
	Rows    []txRow
	// Page is the number of this page, from 1; PrevPage and NextPage are the links
	// to the ones around it, empty where there is none.
	Page     int
	PrevPage string
	NextPage string
	// Filtered is whether any filter is set, which is what an empty list says.
	Filtered bool
}

// filterView is the form that narrows the list: a choice for each filter, with the
// month it keeps.
type filterView struct {
	Month    string
	Accounts []choice
	Statuses []choice
	Kinds    []choice
	Reset    string
	// WithoutPeriods is whether the list leaves the periods of instalment plans out,
	// which the form keeps, and WithPeriods is the link to the same list with them.
	WithoutPeriods bool
	WithPeriods    string
}

// choice is an option of a select.
type choice struct {
	Value    string
	Label    string
	Selected bool
}

// txRow is a transaction as a row of a list: the day, what it was, what it moved and
// what it belongs to.
type txRow struct {
	Href string
	Date string
	// Posted is the posting date, when it tells something: the transaction has one
	// and it is not the day it happened.
	Posted string
	// Title is what the row is called: the description, or what a transaction that
	// was given none is of (see rowTitle).
	Title string
	// Details are what the row says under its title, each once and only when there
	// is something to say, in this order: the shop, the categories, the kind, where
	// it is in an instalment plan, the accounts. What the title already is, is left
	// out.
	Details   []detail
	Status    string
	StatusKey string
	Class     string
	Movements []movement
	Area      string
	Project   string
}

// detail is one thing a row says under its title: a name, or the names of what the
// transaction is of or to (its categories, its accounts), between which a line may
// break but not inside one.
type detail []string

// movement is what a transaction moved on one account, or in one party's dealing,
// or in the principal of an instalment plan that is not yet due.
type movement struct {
	// Where is the account, or says what else the amount is of, when it is not one.
	Where string
	// Indirect is true when the amount is not a posting to an account (the principal
	// of an instalment that is not yet due, a dealing with a party): a row always
	// says what it is then, because it is not what the account holds.
	Indirect bool
	Amount   amountView
}

// transactions lists the transactions of a month, with the filters of the query. It
// asks for one page, and reads the lists that name the rows once.
func (h *handlers) transactions(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	q, err := queryOf(r, "month", "account", "status", "kind", "periods", "page")
	if err != nil {
		return webserver.Page{}, err
	}
	month, err := monthParam(q, h.today())
	if err != nil {
		return webserver.Page{}, err
	}
	account, withoutAccount, err := accountParam(q)
	if err != nil {
		return webserver.Page{}, err
	}
	withoutPeriods, err := periodsParam(q)
	if err != nil {
		return webserver.Page{}, err
	}
	page, err := pageParam(q)
	if err != nil {
		return webserver.Page{}, err
	}
	f := listFilter{Month: month, Account: account, WithoutAccount: withoutAccount, WithoutPeriods: withoutPeriods, Status: q.Get("status"), Kind: q.Get("kind")}

	to := model.LastDayOf(month)
	out, err := h.p.ListTransactions.ListTransactions(ctx, usecase.ListTransactionsInput{
		Status:             f.Status,
		Kind:               f.Kind,
		From:               &month,
		To:                 &to,
		AccountID:          account,
		WithoutAccount:     withoutAccount,
		ExcludePlanPeriods: withoutPeriods,
		Limit:              pageSize,
		Offset:             (page - 1) * pageSize,
	})
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	names, err := h.loadRefs(ctx, needAccounts|needCategories|needAreas|needProjects|needParties)
	if err != nil {
		return webserver.Page{}, fail(err)
	}

	v := transactionsView{
		Month:    webserver.FormatMonth(month),
		Prev:     f.link(month.AddDate(0, -1, 0), 1),
		Next:     f.link(month.AddDate(0, 1, 0), 1),
		Current:  f.link(model.MonthOf(h.today()), 1),
		Filters:  f.form(names),
		Page:     page,
		Filtered: f.Account != nil || f.WithoutAccount || f.WithoutPeriods || f.Status != "" || f.Kind != "",
	}
	if page > 1 {
		v.PrevPage = f.link(month, page-1)
	}
	if out.HasMore {
		v.NextPage = f.link(month, page+1)
	}
	for _, t := range out.Transactions {
		v.Rows = append(v.Rows, buildRow(names, t))
	}
	return webserver.Page{Name: "transactions", Title: "交易", Nav: navTransactions, Data: v}, nil
}

// listFilter is what narrows the list of a month.
type listFilter struct {
	Month   time.Time
	Account *uuid.UUID
	// WithoutAccount lists the transactions that are of no account, which an account
	// cannot be chosen for.
	WithoutAccount bool
	// WithoutPeriods lists an instalment plan by its origin and leaves its periods
	// out, as the drafts are counted.
	WithoutPeriods bool
	Status         string
	Kind           string
}

// noAccount is what the account parameter says to list the transactions that are of
// no account.
const noAccount = "none"

// accountParam reads the account to list the transactions of: an id, or noAccount.
func accountParam(q url.Values) (account *uuid.UUID, without bool, err error) {
	if q.Get("account") == noAccount {
		return nil, true, nil
	}
	account, err = idParam(q, "account")
	return account, false, err
}

// link is the page of the list of a month, and of a page of it, with the filters
// kept.
func (f listFilter) link(month time.Time, page int) string {
	q := url.Values{"month": {webserver.FormatMonth(month)}, "status": {f.Status}, "kind": {f.Kind}}
	switch {
	case f.Account != nil:
		q.Set("account", f.Account.String())
	case f.WithoutAccount:
		q.Set("account", noAccount)
	}
	if f.WithoutPeriods {
		q.Set("periods", noPeriods)
	}
	if page > 1 {
		q.Set("page", strconv.Itoa(page))
	}
	return link("/app/transactions", q)
}

// form is the form of the filters, with the choices that are set marked.
func (f listFilter) form(r *refs) filterView {
	v := filterView{
		Month:          webserver.FormatMonth(f.Month),
		Reset:          link("/app/transactions", url.Values{"month": {webserver.FormatMonth(f.Month)}}),
		WithoutPeriods: f.WithoutPeriods,
	}
	if f.WithoutPeriods {
		withPeriods := f
		withPeriods.WithoutPeriods = false
		v.WithPeriods = withPeriods.link(f.Month, 1)
	}
	v.Accounts = append(v.Accounts,
		choice{Value: "", Label: "全部帳戶", Selected: f.Account == nil && !f.WithoutAccount},
		choice{Value: noAccount, Label: "沒有帳戶的交易", Selected: f.WithoutAccount},
	)
	for _, a := range r.accounts {
		label := a.Name
		if a.ArchivedAt != nil {
			label += "（已封存）"
		}
		v.Accounts = append(v.Accounts, choice{Value: a.ID.String(), Label: label, Selected: f.Account != nil && *f.Account == a.ID})
	}
	v.Statuses = choices(statusOptions, "全部狀態", f.Status)
	v.Kinds = choices(kindOptions, "全部種類", f.Kind)
	return v
}

func choices(options []option, all, selected string) []choice {
	out := []choice{{Value: "", Label: all, Selected: selected == ""}}
	for _, o := range options {
		out = append(out, choice{Value: o.Value, Label: o.Label, Selected: o.Value == selected})
	}
	return out
}

// buildRow lays a transaction out as a row of a list.
func buildRow(r *refs, t usecase.Transaction) txRow {
	kind := kindLabel(t.Kind)
	categories := categoryNames(r, t)
	title := rowTitle(t, joinNames(categories), kind)
	row := txRow{
		Href:      transactionLink(t.ID),
		Date:      webserver.FormatDate(t.OccurredOn),
		Title:     title,
		Status:    statusLabel(t.Status),
		StatusKey: statusKey(t.Status),
		Class:     statusKey(t.Status),
		Movements: movementsOf(r, t),
		Area:      r.optionalName(t.AreaID),
		Project:   r.optionalName(t.ProjectID),
	}
	if t.PostedOn != nil {
		if posted := webserver.FormatDate(*t.PostedOn); posted != row.Date {
			row.Posted = posted
		}
	}
	details := []detail{{strings.TrimSpace(t.Merchant)}, categories, {kind}}
	if t.PlanSeq != nil {
		details = append(details, detail{planLabel(*t.PlanSeq)})
	}
	details = append(details, accountNames(r, t))
	for _, d := range details {
		if text := joinNames(d); text != "" && text != title {
			row.Details = append(row.Details, d)
		}
	}
	return row
}

// rowTitle is what a row is called: the description; when there is none, the first
// memo of its postings; then what it is of (the categories), and last the kind,
// which every transaction has.
func rowTitle(t usecase.Transaction, categories, kind string) string {
	for _, title := range []string{t.Description, firstMemo(t), categories} {
		if title = strings.TrimSpace(title); title != "" {
			return title
		}
	}
	return kind
}

// transactionLink is the page of a transaction.
func transactionLink(id uuid.UUID) string {
	return "/app/transactions/" + id.String()
}

// statusKey is the status when it is one of the three, which a class of the style
// may be named after, and nothing otherwise.
func statusKey(status string) string {
	switch status {
	case string(model.StatusDraft), string(model.StatusConfirmed), string(model.StatusVoid):
		return status
	default:
		return ""
	}
}

// planLabel says where a transaction is in an instalment plan.
func planLabel(seq int) string {
	if seq == 0 {
		return "分期起點"
	}
	return "分期第 " + strconv.Itoa(seq) + " 期"
}

// categoryNames are the categories of the expense and income postings of a
// transaction, each once, in the order of the postings.
func categoryNames(r *refs, t usecase.Transaction) []string {
	var names []string
	seen := make(map[uuid.UUID]bool)
	for _, p := range t.Postings {
		if p.CategoryID == nil || seen[*p.CategoryID] {
			continue
		}
		seen[*p.CategoryID] = true
		names = append(names, r.categoryName(*p.CategoryID))
	}
	return names
}

// accountNames are the accounts the postings of a transaction are to, each once.
func accountNames(r *refs, t usecase.Transaction) []string {
	var names []string
	seen := make(map[uuid.UUID]bool)
	for _, p := range t.Postings {
		if p.AccountID == nil || seen[*p.AccountID] {
			continue
		}
		seen[*p.AccountID] = true
		names = append(names, r.accountName(*p.AccountID))
	}
	return names
}

// firstMemo is the first memo a posting of the transaction has, which is what to
// call a transaction that was given no description.
func firstMemo(t usecase.Transaction) string {
	for _, p := range t.Postings {
		if p.Memo != "" {
			return p.Memo
		}
	}
	return ""
}

// movementsOf is what a transaction moved, as a reader asks it: what each account
// gained or lost. A transaction that moved no account, such as the origin of an
// instalment plan, which only makes principal owed that is not yet due, or an
// expense that a party paid for me, shows what it did instead, and each of those
// amounts is marked as not being an account's (Indirect), so that it is told what it
// is.
func movementsOf(r *refs, t usecase.Transaction) []movement {
	for _, book := range []model.Book{model.BookAccount, model.BookInstalment, model.BookDealing} {
		var moves []movement
		for _, p := range t.Postings {
			if p.Book != string(book) {
				continue
			}
			where := targetOf(r, p)
			switch book {
			case model.BookInstalment:
				where += "・" + unbilledLabel
			case model.BookDealing:
				where = dealingLabel + "・" + where
			}
			moves = append(moves, movement{Where: where, Indirect: book != model.BookAccount, Amount: r.signed(p.InstrumentID, p.Quantity)})
		}
		if len(moves) > 0 {
			return moves
		}
	}
	return nil
}

// joinNames puts names in one cell, as a list.
func joinNames(names []string) string {
	return strings.Join(names, "、")
}
