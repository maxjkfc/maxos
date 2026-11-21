package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// transactionView is the page of one transaction: when it happened, what it is
// and every one of its parts.
type transactionView struct {
	Kind      string
	Status    string
	StatusKey string
	Date      string
	// Posted is the posting date and Occurred the moment it happened, when it is
	// known; the rest are the moments it was written, confirmed and voided, in the
	// timezone of the user.
	Posted      string
	Occurred    string
	Created     string
	Confirmed   string
	Voided      string
	Description string
	Merchant    string
	Area        string
	Project     string
	Tags        []string
	Parts       []partView
	Plan        *planView
	// Month is the list of the month the transaction happened in.
	Month string
	Back  string
}

// partView is one part of a transaction: a posting with what it is for, or the
// discount of one.
type partView struct {
	// Role says what the part is of the transaction: the main amount, a fee, a
	// share of what I paid for someone, interest, and so on.
	Role   string
	Target string
	Amount amountView
	Memo   string
	// Note explains a part that does not count as it looks.
	Note string
}

// planView is the instalment plan a transaction is part of: its origin and its
// periods, each with a link, so that the others of the plan are one click away.
type planView struct {
	Position string
	Rows     []planPart
}

type planPart struct {
	Href      string
	Label     string
	Date      string
	Status    string
	StatusKey string
	Moves     []movement
	Current   bool
}

// transaction shows a transaction by its id, and the plan it is part of.
func (h *handlers) transaction(r *http.Request) (webserver.Page, error) {
	ctx := r.Context()
	id, err := pathID(r.PathValue("id"), "這筆交易")
	if err != nil {
		return webserver.Page{}, err
	}
	t, err := h.p.GetTransaction.GetTransaction(ctx, id)
	if err != nil {
		return webserver.Page{}, notFoundAs(err, "找不到這筆交易。")
	}
	var plan *usecase.InstalmentPlan
	if t.PlanID != nil {
		p, err := h.p.GetInstalmentPlan.GetInstalmentPlan(ctx, *t.PlanID)
		if err != nil {
			return webserver.Page{}, fail(err)
		}
		plan = &p
	}
	names, err := h.loadRefs(ctx, needAccounts|needCategories|needAreas|needProjects|needParties|needTags)
	if err != nil {
		return webserver.Page{}, fail(err)
	}
	return webserver.Page{
		Name:  "transaction",
		Title: "交易明細",
		Nav:   navTransactions,
		Data:  buildTransaction(names, h.location(), t, plan),
	}, nil
}

// buildTransaction lays a transaction out: its facts, its parts in the order of its
// postings, and its plan.
func buildTransaction(r *refs, loc *time.Location, t usecase.Transaction, plan *usecase.InstalmentPlan) transactionView {
	v := transactionView{
		Kind:        kindLabel(t.Kind),
		Status:      statusLabel(t.Status),
		StatusKey:   statusKey(t.Status),
		Date:        webserver.FormatDate(t.OccurredOn),
		Created:     webserver.FormatMoment(t.CreatedAt, loc),
		Description: t.Description,
		Merchant:    t.Merchant,
		Area:        r.optionalName(t.AreaID),
		Project:     r.optionalName(t.ProjectID),
		Parts:       partsOf(r, t),
		Month:       webserver.FormatMonth(t.OccurredOn),
		Back:        link("/app/transactions", url.Values{"month": {webserver.FormatMonth(t.OccurredOn)}}),
	}
	if t.PostedOn != nil {
		v.Posted = webserver.FormatDate(*t.PostedOn)
	}
	if t.OccurredAt != nil {
		v.Occurred = webserver.FormatMoment(*t.OccurredAt, loc)
	}
	if t.ConfirmedAt != nil {
		v.Confirmed = webserver.FormatMoment(*t.ConfirmedAt, loc)
	}
	if t.VoidedAt != nil {
		v.Voided = webserver.FormatMoment(*t.VoidedAt, loc)
	}
	for _, tag := range t.TagIDs {
		v.Tags = append(v.Tags, r.name(tag))
	}
	if plan != nil && t.PlanSeq != nil {
		v.Plan = buildPlan(r, t, *t.PlanSeq, *plan)
	}
	return v
}

// buildPlan lists the origin and the periods of the plan, the one being looked at
// marked.
func buildPlan(r *refs, current usecase.Transaction, seq int, plan usecase.InstalmentPlan) *planView {
	v := &planView{Position: planLabel(seq) + "，共 " + strconv.Itoa(len(plan.Periods)) + " 期"}
	all := append([]usecase.Transaction{plan.Origin}, plan.Periods...)
	for _, t := range all {
		part := planPart{
			Href:      transactionLink(t.ID),
			Date:      webserver.FormatDate(t.OccurredOn),
			Status:    statusLabel(t.Status),
			StatusKey: statusKey(t.Status),
			Moves:     movementsOf(r, t),
			Current:   t.ID == current.ID,
		}
		if t.PlanSeq != nil {
			part.Label = planLabel(*t.PlanSeq)
		}
		v.Rows = append(v.Rows, part)
	}
	return v
}

// partsOf is a row for each posting of a transaction, in order, and one more after
// an expense that has a discount. What a posting is for follows from its book and
// the kind of the transaction, which is how the ledger makes them (see
// docs/design/m1-ledger.md, section 3): the expense postings of an expense are what
// was bought, and those of any other kind are a fee, the interest of a period of an
// instalment plan, or what a reconciliation found missing; a dealing posting of an
// expense is a share of what I paid for someone, or what a party paid for me.
func partsOf(r *refs, t usecase.Transaction) []partView {
	var parts []partView
	for _, p := range t.Postings {
		part := partView{
			Role:   roleOf(t.Kind, p),
			Target: targetOf(r, p),
			Amount: r.signed(p.InstrumentID, p.Quantity),
			Memo:   p.Memo,
		}
		parts = append(parts, part)
		if p.Discount != nil {
			parts = append(parts, partView{
				Role:   "折扣",
				Target: targetOf(r, p),
				Amount: r.money(p.InstrumentID, *p.Discount),
				Note:   "店家結帳當下少收的金額，只留作記錄；支出以實付金額計。",
			})
		}
	}
	return parts
}

// roleOf says what a posting of a transaction of the kind is for.
func roleOf(kind string, p usecase.Posting) string {
	switch p.Book {
	case "account":
		return "帳戶"
	case "expense":
		switch kind {
		case "expense":
			return "主要金額"
		case "instalment":
			return "利息"
		case "reconciliation":
			return "未記錄"
		default:
			return "手續費"
		}
	case "income":
		if kind == "reconciliation" {
			return "未記錄"
		}
		return "主要金額"
	case "dealing":
		switch {
		case kind != "expense":
			return "往來"
		case p.Quantity.IsNegative():
			return "別人代付"
		default:
			return "分攤"
		}
	case "reward":
		return "回饋"
	case "instalment":
		return unbilledLabel
	case "conversion":
		return "中介（換匯、買賣）"
	case "opening":
		return "期初餘額"
	case "adjustment":
		return "數量修正"
	default:
		return p.Book
	}
}

// targetOf is what a posting is to: its account, its category, or its party.
func targetOf(r *refs, p usecase.Posting) string {
	switch {
	case p.AccountID != nil:
		return r.accountName(*p.AccountID)
	case p.CategoryID != nil:
		return r.categoryName(*p.CategoryID)
	case p.PartyID != nil:
		return r.name(*p.PartyID)
	default:
		return ""
	}
}

// notFoundAs makes a 404 with the message of the page of an error that says what
// was asked for is not there; any other error is left to fail.
func notFoundAs(err error, message string) error {
	if errors.Is(err, model.ErrNotFound) {
		return &webserver.Error{Status: http.StatusNotFound, Message: message, Err: err}
	}
	return fail(err)
}
