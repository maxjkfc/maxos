// Package model holds what the MOZE import knows: the rows of a MOZE export, the
// mapping that says what they are in the ledger, the rates that give the foreign
// ones an amount in the base currency, and the plan that follows from them.
//
// Nothing here reads a file or calls the ledger: the plan is a value computed from
// rows, a mapping and rates, and what is recorded from it is the use cases' work.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// RecordType is the 記錄類型 column of an export: what MOZE calls the kind of a
// row. It is not a ledger Kind: some of them are only half of a transaction (a
// 轉出 and a 轉入 are the two sides of one), some are not a transaction at all (a
// 餘額調整 is what the ledger works out when it reconciles).
type RecordType string

// The record types an export has.
const (
	TypeExpense           RecordType = "支出"
	TypeTransferOut       RecordType = "轉出"
	TypeTransferIn        RecordType = "轉入"
	TypeBonus             RecordType = "紅利回饋"
	TypeIncome            RecordType = "收入"
	TypeAdjustment        RecordType = "餘額調整"
	TypeInitial           RecordType = "初始金額"
	TypeReceivable        RecordType = "應收款項"
	TypeInterest          RecordType = "利息"
	TypeInstalmentPayment RecordType = "分期還款"
	TypeRefund            RecordType = "退款"
	TypeBillInstalment    RecordType = "帳單分期"
	TypeFee               RecordType = "手續費"
)

// Row is one row of a MOZE export, as it says it.
type Row struct {
	// Pos is the place of the row among the data rows of the file, from 0: the
	// order of the file, which is by day and time.
	Pos int
	// Line is the line of the file the row begins on, from 1, where line 1 is the
	// header. It is for messages; a description of several lines takes several.
	Line int

	Account  string
	Currency string
	Type     RecordType
	Main     string
	Sub      string
	// Amount is signed as the account sees it: negative when money leaves.
	Amount decimal.Decimal
	// Fee is 手續費: charged on top of Amount, zero or negative as the file has it.
	Fee decimal.Decimal
	// Discount is 折扣: what the shop took off, zero or positive. What was paid is
	// Amount + Discount.
	Discount decimal.Decimal

	Name     string
	Merchant string
	// Date is the day of the row at midnight UTC, and the zero time for a row with
	// no date, which only 初始金額 may be.
	Date time.Time
	// Clock is the time of day, "15:04", or empty.
	Clock       string
	Project     string
	Description string
	// Tags are the words of 標籤 without their "#".
	Tags  []string
	Party string

	// Key identifies the row from one export to the next: see AssignKeys.
	Key string
}

// Effect is what the row does to the balance of its account, in its currency: what
// leaves or comes in, the fee that is charged beside it, and the discount that was
// not paid.
func (r Row) Effect() decimal.Decimal {
	return r.Amount.Add(r.Discount).Add(r.Fee)
}

// HasDate tells whether the row has a day.
func (r Row) HasDate() bool { return !r.Date.IsZero() }

// Month is the month of the row, "2006-01", or empty for a row with no date.
func (r Row) Month() string { return MonthOf(r.Date) }

// Before orders rows as the ledger is told of them: by day, by time of day, and
// then as the file has them.
func (r Row) Before(o Row) bool {
	if !r.Date.Equal(o.Date) {
		return r.Date.Before(o.Date)
	}
	if r.Clock != o.Clock {
		return r.Clock < o.Clock
	}
	return r.Pos < o.Pos
}

// MonthOf names the month a day is in: "2006-01". The zero time has none.
func MonthOf(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01")
}

// KeyPrefix is what every idempotency key of a transaction made by the import
// starts with. It is how the import tells its own transactions from the others.
const KeyPrefix = "moze:"

// AssignKeys gives each row its key: a hash of its content, with the number of
// rows of the same content that came before it in the file, so that identical rows
// stay apart and every row keeps its key when the file is exported again with more
// rows after it. The content is what the row says, with the whitespace around each
// field and the form of the numbers left out, so that a new export of the same
// rows gives the same keys.
func AssignKeys(rows []Row) {
	seen := make(map[string]int, len(rows))
	for i := range rows {
		sum := sha256.Sum256([]byte(rows[i].content()))
		base := hex.EncodeToString(sum[:12])
		seen[base]++
		rows[i].Key = fmt.Sprintf("%s-%d", base, seen[base])
	}
}

// content is the normalised text of the row that its key is made of.
func (r Row) content() string {
	date := ""
	if r.HasDate() {
		date = r.Date.Format(time.DateOnly)
	}
	fields := []string{
		r.Account, r.Currency, string(r.Type), r.Main, r.Sub,
		r.Amount.String(), r.Fee.String(), r.Discount.String(),
		r.Name, r.Merchant, date, r.Clock, r.Project, r.Description,
		strings.Join(r.Tags, ";"), r.Party,
	}
	for i, f := range fields {
		f = strings.ReplaceAll(f, "\r\n", "\n")
		fields[i] = strings.TrimSpace(f)
	}
	return strings.Join(fields, "\x1f")
}
