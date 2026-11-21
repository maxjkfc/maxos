package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// The query parameters of the pages. A parameter that does not read is a 400 page
// that says which one, never a page of nothing: a month that is not a month must
// not look like a month without transactions. The page says what the parameter
// should be and never repeats what it was given: that is the request's own, and as
// long as the request wants it to be.

// The first and the last year a page takes a month of: far enough on both sides
// of any ledger, near enough that no date arithmetic on it can overflow.
const (
	firstYear = 1900
	lastYear  = 2999
)

// queryOf reads the query string of a request for the parameters a page takes,
// which are all of one value. A query that does not parse (a bad escape, a
// semicolon where an ampersand should be) and a parameter of the page that is given
// more than once are a 400 page: net/http's own Query leaves out what it cannot read
// without a word, and a page must not answer as if the parameter was not there.
func queryOf(r *http.Request, singles ...string) (url.Values, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, webserver.BadRequest("網址的參數讀不懂：請用 &，並把特殊字元寫成 %XX。")
	}
	for _, name := range singles {
		if len(q[name]) > 1 {
			return nil, webserver.BadRequest(name + " 只能給一個值。")
		}
	}
	return q, nil
}

// monthParam reads the month the page is of, as the first day of it, at midnight
// UTC, the way the use cases take a month. A page that is not given one is of the
// month today is in.
func monthParam(q url.Values, today time.Time) (time.Time, error) {
	value := q.Get("month")
	if value == "" {
		return model.MonthOf(today), nil
	}
	month, err := time.Parse("2006-01", value)
	if err != nil || month.Year() < firstYear || month.Year() > lastYear {
		return time.Time{}, webserver.BadRequest(fmt.Sprintf("month 必須是 %d 到 %d 年之間的 YYYY-MM，例如 2026-11。", firstYear, lastYear))
	}
	return month, nil
}

// dateParam reads a day, YYYY-MM-DD, or nothing when the parameter is not there.
func dateParam(q url.Values, name string) (*time.Time, error) {
	value := q.Get(name)
	if value == "" {
		return nil, nil
	}
	day, err := time.Parse(time.DateOnly, value)
	if err != nil || day.Year() < firstYear || day.Year() > lastYear {
		return nil, webserver.BadRequest(fmt.Sprintf("%s 必須是 %d 到 %d 年之間的 YYYY-MM-DD，例如 2026-11-14。", name, firstYear, lastYear))
	}
	return &day, nil
}

// idParam reads an id, or nothing when the parameter is not there.
func idParam(q url.Values, name string) (*uuid.UUID, error) {
	value := q.Get(name)
	if value == "" {
		return nil, nil
	}
	id, err := uuid.Parse(value)
	if err != nil {
		return nil, webserver.BadRequest(name + " 不是有效的 id。")
	}
	return &id, nil
}

// pageParam reads the number of the page of a long list, from 1.
func pageParam(q url.Values) (int, error) {
	value := q.Get("page")
	if value == "" {
		return 1, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 || n > maxPage {
		return 0, webserver.BadRequest(fmt.Sprintf("page 必須是 1 到 %d 的整數。", maxPage))
	}
	return n, nil
}

// maxPage is the last page of a list that a request may ask for.
const maxPage = 10000

// noPeriods is what the periods parameter says to list an instalment plan by its
// origin and leave its periods out, which is how the drafts are counted.
const noPeriods = "none"

// periodsParam reads whether the list leaves the periods of instalment plans out.
func periodsParam(q url.Values) (bool, error) {
	switch q.Get("periods") {
	case "":
		return false, nil
	case noPeriods:
		return true, nil
	default:
		return false, webserver.BadRequest("periods 只能是 none，或不給。")
	}
}

// pathID reads the id in the path of a page that is of one thing, which is not
// there when it is not an id: a page of nothing is a page that is not found.
func pathID(value, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, webserver.NotFound("找不到" + what + "。")
	}
	return id, nil
}

// link is a path with a query: the values that are empty are left out, so that a
// link to the page of a filter that is not set is the plain link.
func link(path string, query url.Values) string {
	clean := url.Values{}
	for name, values := range query {
		for _, v := range values {
			if v != "" {
				clean.Add(name, v)
			}
		}
	}
	if len(clean) == 0 {
		return path
	}
	return path + "?" + clean.Encode()
}

// fail turns what a use case returned into what the page answers: the errors that
// are about what was asked for are the user's to see, with the status that fits;
// anything else is a failure of the server, which the page does not describe.
func fail(err error) error {
	var webErr *webserver.Error
	switch {
	case errors.As(err, &webErr):
		return err
	case errors.Is(err, model.ErrNotFound):
		return &webserver.Error{Status: http.StatusNotFound, Message: "找不到這筆資料。", Err: err}
	case errors.Is(err, model.ErrNoStatements):
		return &webserver.Error{Status: http.StatusNotFound, Message: "這個帳戶沒有帳單：只有設了結帳日的信用卡才有。", Err: err}
	case errors.Is(err, model.ErrInvalidStatus):
		return &webserver.Error{Status: http.StatusBadRequest, Message: "status 必須是 draft、confirmed 或 void。", Err: err}
	case errors.Is(err, model.ErrInvalidKind):
		return &webserver.Error{Status: http.StatusBadRequest, Message: "kind 不是已知的交易種類。", Err: err}
	case errors.Is(err, model.ErrInvalidPage), errors.Is(err, model.ErrDateRangeReversed),
		errors.Is(err, model.ErrMonthRequired), errors.Is(err, model.ErrDateRangeRequired):
		return &webserver.Error{Status: http.StatusBadRequest, Message: "請求的範圍不正確。", Err: err}
	case errors.Is(err, model.ErrConflictingFilters):
		return &webserver.Error{Status: http.StatusBadRequest, Message: "帳戶和「沒有帳戶」不能同時選。", Err: err}
	default:
		return err
	}
}
