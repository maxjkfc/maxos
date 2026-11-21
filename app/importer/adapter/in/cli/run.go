package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// createdText names the reference data apply makes, in the order it is told.
var createdText = []struct{ key, name string }{
	{usecase.CreatedInstruments, "標的"}, {usecase.CreatedInstitutions, "機構"}, {usecase.CreatedAccounts, "帳戶"},
	{usecase.CreatedCategories, "類別"}, {usecase.CreatedAreas, "領域"}, {usecase.CreatedProjects, "專案"},
	{usecase.CreatedTags, "標籤"}, {usecase.CreatedParties, "對象"},
}

// RenderApply says what apply did.
func RenderApply(w io.Writer, out usecase.ApplyOutput) error {
	var b strings.Builder
	applyText(&b, out)
	return flush(w, &b)
}

func applyText(w *strings.Builder, out usecase.ApplyOutput) {
	var made []string
	for _, c := range createdText {
		if n := out.Created[c.key]; n > 0 {
			made = append(made, fmt.Sprintf("%s %d", c.name, n))
		}
	}
	if len(made) == 0 {
		fmt.Fprintln(w, "參考資料：新增 0 項")
	} else {
		fmt.Fprintf(w, "參考資料：新增 %s\n", strings.Join(made, "、"))
	}

	newTotal, dupTotal := sum(out.New), sum(out.Duplicate)
	fmt.Fprintf(w, "草稿：新增 %d 筆，已存在 %d 筆，失敗 %d 筆\n", newTotal, dupTotal, len(out.Failed))
	if newTotal > 0 {
		fmt.Fprintf(w, "  新增的：%s\n", kinds(out.New))
	}
	fmt.Fprintf(w, "餘額調整 %d 筆留到確認該月時處理；不需記錄 %d 筆；無法記錄的列 %d 筆（見 plan 的報告）\n",
		out.Adjustments, out.NoOps, out.Skipped)
	if out.Guessed > 0 {
		fmt.Fprintf(w, "注意：對照檔裡仍有 %d 個 # guessed 的項目，確認過就把標記拿掉\n", out.Guessed)
	}
	for _, f := range out.Failed {
		fmt.Fprintf(w, "失敗：%s %s（檔案第 %s 行）：%v\n", kindText[f.Unit.Kind], f.Unit.Date.Format(time.DateOnly), lineList(f.Lines), f.Err)
	}
}

func sum(m map[model.UnitKind]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func kinds(m map[model.UnitKind]int) string {
	var parts []string
	for _, k := range kindOrder {
		if m[k] > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", kindText[k], m[k]))
		}
	}
	return strings.Join(parts, "、")
}

func lineList(lines []int) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = fmt.Sprint(l)
	}
	return strings.Join(parts, "、")
}

// RenderBlocked says why a plan was refused.
func RenderBlocked(w io.Writer, e *usecase.BlockedError) error {
	var b strings.Builder
	blockedText(&b, e)
	return flush(w, &b)
}

func blockedText(w *strings.Builder, e *usecase.BlockedError) {
	g := e.Plan.Guessed
	fmt.Fprintln(w, "plan 還有要處理的項目，不能執行：")
	for _, c := range []struct {
		name string
		n    int
	}{
		{"標的", len(g.Instruments)}, {"帳戶", len(g.Accounts)}, {"專案值", len(g.Projects)},
		{"支出類別", len(g.Expense)}, {"收入類別", len(g.Income)}, {"對象", len(g.Parties)}, {"標籤", len(g.Tags)},
	} {
		if c.n > 0 {
			fmt.Fprintf(w, "  對照檔缺少 %s %d 個\n", c.name, c.n)
		}
	}
	for _, p := range e.Plan.Problems {
		fmt.Fprintf(w, "  %s\n", p)
	}
	fmt.Fprintln(w, "先執行 plan（它會把缺的項目寫進對照檔），確認對照檔後再來。")
}

// RenderConfirm says what confirm did and what the accounts hold at the end of the
// month against what the export says.
func RenderConfirm(w io.Writer, out usecase.ConfirmOutput) error {
	var b strings.Builder
	confirmText(&b, out)
	return flush(w, &b)
}

func confirmText(w *strings.Builder, out usecase.ConfirmOutput) {
	fmt.Fprintf(w, "%s：確認 %d 筆交易，先前已確認 %d 筆單位\n", out.Month, out.Confirmed, out.AlreadyConfirmed)
	applied, done, blocked := 0, 0, 0
	for _, a := range out.Adjustments {
		switch a.State {
		case usecase.AdjustmentApplied:
			applied++
		case usecase.AdjustmentDone:
			done++
		case usecase.AdjustmentBlocked:
			blocked++
		}
	}
	if len(out.Adjustments) > 0 {
		fmt.Fprintf(w, "餘額調整：套用 %d 筆、先前已完成 %d 筆、被擋下 %d 筆\n", applied, done, blocked)
	}
	for _, a := range out.Adjustments {
		if a.State == usecase.AdjustmentBlocked {
			fmt.Fprintf(w, "  擋下：%s %s %s %s：%v\n", a.Adjustment.Date.Format(time.DateOnly), a.Adjustment.Account, a.Adjustment.Instrument, a.Adjustment.Amount, a.Err)
		}
	}
	if out.Orphans > 0 {
		fmt.Fprintf(w, "注意：有 %d 筆匯入的草稿不在檔案裡了，沒有動它們\n", out.Orphans)
	}
	fmt.Fprintln(w)
	checkText(w, out.Check, false)
}

// RenderCheck writes the balances of a month by the ledger and by the export. With
// onlyDifferences it writes the lines that differ alone.
func RenderCheck(w io.Writer, c usecase.MonthCheck, onlyDifferences bool) error {
	var b strings.Builder
	checkText(&b, c, onlyDifferences)
	return flush(w, &b)
}

func checkText(w *strings.Builder, c usecase.MonthCheck, onlyDifferences bool) {
	lines := c.Lines
	if onlyDifferences {
		lines = c.Differences()
	}
	fmt.Fprintf(w, "%s 月底（%s）帳本餘額與 CSV 預期餘額：", c.Month, c.End.Format(time.DateOnly))
	if n := len(c.Differences()); n == 0 {
		fmt.Fprintln(w, "全部吻合")
	} else {
		fmt.Fprintf(w, "%d 項有差異，其中 %d 項無法解釋\n", n, c.Unexplained())
	}
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(w)
	rows := make([][]string, len(lines))
	for i, l := range lines {
		d := c.Decimals[l.Instrument]
		note := ""
		switch {
		case l.Unexplained().IsZero() && !l.Difference().IsZero():
			note = "已說明：有不匯入的列"
		case !l.Unexplained().IsZero():
			note = "無法解釋"
		}
		rows[i] = []string{
			l.Account, l.Instrument, money(l.Ledger, d), money(l.Expected, d),
			signed(l.Difference(), d), signed(l.Unrecorded, d), note,
		}
	}
	table(w, []string{"帳戶", "標的", "帳本", "CSV 預期", "差額", "不匯入的列", "說明"}, rows)
}

// RenderVerify says, for every month that is confirmed, whether the ledger holds what
// the export says.
func RenderVerify(w io.Writer, out usecase.VerifyOutput) error {
	var b strings.Builder
	verifyText(&b, out)
	return flush(w, &b)
}

func verifyText(w *strings.Builder, out usecase.VerifyOutput) {
	if len(out.Months) == 0 {
		fmt.Fprintln(w, "還沒有確認完的月份，沒有東西可以比對")
		if out.Open != "" {
			fmt.Fprintf(w, "最早還沒確認完的月份：%s\n", out.Open)
		}
		return
	}
	fmt.Fprintf(w, "已確認的月份 %d 個（%s 至 %s）\n", len(out.Months), out.Months[0].Month, out.Months[len(out.Months)-1].Month)
	if out.Open != "" {
		fmt.Fprintf(w, "下一個還沒確認完的月份：%s\n", out.Open)
	}
	fmt.Fprintln(w)
	for _, m := range out.Months {
		if len(m.Differences()) == 0 {
			fmt.Fprintf(w, "%s：全部吻合（%d 個帳戶與標的）\n", m.Month, len(m.Lines))
			continue
		}
		checkText(w, m, true)
	}
	fmt.Fprintln(w)
	if out.Unexplained == 0 {
		fmt.Fprintln(w, "沒有無法解釋的差異")
	} else {
		fmt.Fprintf(w, "無法解釋的差異共 %d 項\n", out.Unexplained)
	}
}

// RenderStatus writes the state of the import by month.
func RenderStatus(w io.Writer, out usecase.StatusOutput) error {
	var b strings.Builder
	rows := make([][]string, 0, len(out.Months))
	for _, m := range out.Months {
		rows = append(rows, []string{
			m.Month, fmt.Sprint(m.Drafts), fmt.Sprint(m.Confirmed), fmt.Sprint(m.Skipped),
			fmt.Sprint(m.AdjustmentsPending), fmt.Sprint(m.NotApplied), fmt.Sprint(m.Voided),
		})
	}
	table(&b, []string{"月份", "草稿", "已確認", "略過", "待套用的餘額調整", "尚未匯入", "已作廢"}, rows)
	if out.Orphans > 0 {
		fmt.Fprintf(&b, "有 %d 筆匯入的草稿不在檔案裡了\n", out.Orphans)
	}
	return flush(w, &b)
}

// Problem says, in plain terms, what the use cases refuse with when it is something
// the user can put right: a plan that has something to map, and a month that comes
// after one that is not confirmed. It returns whether it said anything.
func Problem(w io.Writer, err error) (bool, error) {
	var blocked *usecase.BlockedError
	var order *usecase.MonthOrderError
	var b strings.Builder
	switch {
	case errors.As(err, &blocked):
		blockedText(&b, blocked)
	case errors.As(err, &order):
		fmt.Fprintf(&b, "不能確認 %s：%s 還有 %d 筆草稿、%d 筆尚未匯入的列、%d 筆餘額調整沒處理完。請先確認 %s。\n",
			order.Month, order.Open, order.Drafts, order.NotApplied, order.Adjustments, order.Open)
	default:
		return false, nil
	}
	return true, flush(w, &b)
}
