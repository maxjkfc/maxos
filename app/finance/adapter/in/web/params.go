package web

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/maxjkfc/maxos/app/finance/domain/model"
	"github.com/maxjkfc/maxos/pkg/webserver"
)

// The query parameters of the pages. A parameter that does not read is a 400 page
// that says which one, never a page of nothing: a month that is not a month must
// not look like a month without transactions.

// The first and the last year a page takes a month of: far enough on both sides
// of any ledger, near enough that no date arithmetic on it can overflow.
const (
	firstYear = 1900
	lastYear  = 2999
)

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
		return time.Time{}, webserver.BadRequest(fmt.Sprintf("month 必須是 %d 到 %d 年之間的 YYYY-MM，例如 2026-11；收到的是「%s」。", firstYear, lastYear, value))
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
		return nil, webserver.BadRequest(fmt.Sprintf("%s 必須是 YYYY-MM-DD，例如 2026-11-14；收到的是「%s」。", name, value))
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
		return nil, webserver.BadRequest(fmt.Sprintf("%s 不是有效的 id：「%s」。", name, value))
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
		return 0, webserver.BadRequest(fmt.Sprintf("page 必須是 1 到 %d 的整數；收到的是「%s」。", maxPage, value))
	}
	return n, nil
}

// maxPage is the last page of a list that a request may ask for.
const maxPage = 10000

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
		return &webserver.Error{Status: 404, Message: "找不到這筆資料。", Err: err}
	case errors.Is(err, model.ErrNoStatements):
		return &webserver.Error{Status: 404, Message: "這個帳戶沒有帳單：只有設了結帳日的信用卡才有。", Err: err}
	case errors.Is(err, model.ErrInvalidStatus):
		return &webserver.Error{Status: 400, Message: "status 必須是 draft、confirmed 或 void。", Err: err}
	case errors.Is(err, model.ErrInvalidKind):
		return &webserver.Error{Status: 400, Message: "kind 不是已知的交易種類。", Err: err}
	case errors.Is(err, model.ErrInvalidPage), errors.Is(err, model.ErrDateRangeReversed),
		errors.Is(err, model.ErrMonthRequired), errors.Is(err, model.ErrDateRangeRequired):
		return &webserver.Error{Status: 400, Message: "請求的範圍不正確。", Err: err}
	default:
		return err
	}
}
