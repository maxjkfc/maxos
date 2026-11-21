// Package file implements the outbound ports of the importer that keep what they
// hold in files: the MOZE export it reads, the rates the user builds, and the
// mapping the user reviews.
package file

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/filefailure"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/out/source"
)

var _ source.Source = (*MozeCSV)(nil)

// The columns of a MOZE export, by the names of its header.
const (
	colAccount     = "帳戶"
	colCurrency    = "幣種"
	colType        = "記錄類型"
	colMain        = "主類別"
	colSub         = "子類別"
	colAmount      = "金額"
	colFee         = "手續費"
	colDiscount    = "折扣"
	colName        = "名稱"
	colMerchant    = "商家"
	colDate        = "日期"
	colTime        = "時間"
	colProject     = "專案"
	colDescription = "描述"
	colTags        = "標籤"
	colParty       = "對象"
)

// mozeColumns are the columns the header of an export has to have.
var mozeColumns = []string{
	colAccount, colCurrency, colType, colMain, colSub, colAmount, colFee, colDiscount,
	colName, colMerchant, colDate, colTime, colProject, colDescription, colTags, colParty,
}

// The forms of a day and of a time of day that are read, the first of each being
// the one MOZE writes.
var (
	mozeDateLayouts  = []string{"2006/01/02", time.DateOnly}
	mozeClockLayouts = []string{"15:04", "15:04:05"}
)

// mozeDateExample is a day written the way MOZE writes it: a failure for a text that is
// not a day shows it, so that the user sees what is meant.
const mozeDateExample = "2026/03/10"

// MozeCSV reads the CSV file that MOZE exports.
type MozeCSV struct{}

// NewMozeCSV returns a reader of MOZE exports.
func NewMozeCSV() *MozeCSV { return &MozeCSV{} }

// Rows reads the export at path, as ParseMozeCSV does. What is wrong with it is a
// *filefailure.Failure that names the path: a file that is not there, one that cannot be
// read, or a failure of its contents.
func (*MozeCSV) Rows(ctx context.Context, path string) ([]model.Row, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &filefailure.Failure{Code: filefailure.Missing, File: path}
	}
	if err != nil {
		return nil, unreadable(path, err)
	}
	defer func() { _ = f.Close() }()

	rows, err := ParseMozeCSV(f)
	if err != nil {
		return nil, inFile(path, err)
	}
	return rows, nil
}

// ParseMozeCSV reads the rows of a MOZE export: a UTF-8 file, with or without a byte
// order mark, with LF or CRLF line endings. Its columns are found by the names of
// the header, in any order, and a column that is not one of the 16 of MOZE is left
// out; a column that is missing is a failure. The rows come in the order of the
// file, each with its keys (model.AssignKeys). A file that has a header and no
// row has no rows; a file with no header is a failure.
//
// What a row says is kept as the file has it: its numbers with their signs, its
// record type whatever it is (the plan tells of those the import does not know).
// What is wrong with the file is a *filefailure.Failure (see failAt) that has the line it
// is about, and the column or the value when it is one.
func ParseMozeCSV(r io.Reader) ([]model.Row, error) {
	table, err := openCSV(r, 0, mozeColumns...)
	if err != nil {
		return nil, err
	}
	rows := []model.Row{}
	for {
		rec, err := table.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		row, err := mozeRow(rec)
		if err != nil {
			return nil, err
		}
		row.Pos = len(rows)
		rows = append(rows, row)
	}
	model.AssignKeys(rows)
	return rows, nil
}

// mozeRow makes the row of a record, but for its position and its key, which
// depend on the other rows.
func mozeRow(rec csvRecord) (model.Row, error) {
	row := model.Row{
		Line:        rec.line,
		Account:     rec.text(colAccount),
		Currency:    rec.text(colCurrency),
		Type:        model.RecordType(rec.text(colType)),
		Main:        rec.text(colMain),
		Sub:         rec.text(colSub),
		Name:        rec.text(colName),
		Merchant:    rec.text(colMerchant),
		Project:     rec.text(colProject),
		Description: rec.text(colDescription),
		Tags:        mozeTags(rec.text(colTags)),
		Party:       rec.text(colParty),
	}
	var err error
	if row.Amount, err = mozeDecimal(rec, colAmount); err != nil {
		return model.Row{}, err
	}
	if row.Fee, err = mozeDecimal(rec, colFee); err != nil {
		return model.Row{}, err
	}
	if row.Discount, err = mozeDecimal(rec, colDiscount); err != nil {
		return model.Row{}, err
	}
	if row.Date, err = mozeDate(rec, row.Type); err != nil {
		return model.Row{}, err
	}
	if row.Clock, err = mozeClock(rec); err != nil {
		return model.Row{}, err
	}
	return row, nil
}

// mozeDecimal reads a column of amounts, in which no text is zero.
func mozeDecimal(rec csvRecord, col string) (decimal.Decimal, error) {
	text := rec.text(col)
	if text == "" {
		return decimal.Zero, nil
	}
	d, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, failAt(filefailure.NotNumber, rec.line, col, text)
	}
	return d, nil
}

// mozeDate reads 日期 as a day at midnight UTC. It may be empty for the initial
// balance of an account, which has no day, and for no other record type.
func mozeDate(rec csvRecord, typ model.RecordType) (time.Time, error) {
	text := rec.text(colDate)
	if text == "" {
		if typ == model.TypeInitial {
			return time.Time{}, nil
		}
		return time.Time{}, failAt(filefailure.ValueEmpty, rec.line, colDate)
	}
	for _, layout := range mozeDateLayouts {
		if day, err := time.Parse(layout, text); err == nil {
			return day, nil
		}
	}
	return time.Time{}, failAt(filefailure.NotDay, rec.line, colDate, text, mozeDateExample)
}

// mozeClock reads 時間 as "15:04"; the seconds, if it has any, are left out.
func mozeClock(rec csvRecord) (string, error) {
	text := rec.text(colTime)
	if text == "" {
		return "", nil
	}
	for _, layout := range mozeClockLayouts {
		if t, err := time.Parse(layout, text); err == nil {
			return t.Format("15:04"), nil
		}
	}
	return "", failAt(filefailure.NotTime, rec.line, colTime, text)
}

// mozeTags reads 標籤, "#家庭;#公司", as the words of the tags: without their "#", in
// the order of the text, with none empty and none twice.
func mozeTags(text string) []string {
	var tags []string
	for part := range strings.SplitSeq(text, ";") {
		tag := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "#"))
		if tag == "" || slices.Contains(tags, tag) {
			continue
		}
		tags = append(tags, tag)
	}
	return tags
}
