package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// createdText names the reference data apply makes, in the order it is told.
var createdText = []struct{ key, name string }{
	{usecase.CreatedInstruments, "標的"}, {usecase.CreatedInstitutions, "機構"}, {usecase.CreatedAccounts, "帳戶"},
	{usecase.CreatedCategories, "類別"}, {usecase.CreatedAreas, "領域"}, {usecase.CreatedProjects, "專案"},
	{usecase.CreatedTags, "標籤"}, {usecase.CreatedParties, "對象"},
}

// ledgerKindText names the kinds of transaction of the ledger, as the glossary does.
var ledgerKindText = map[string]string{
	"expense":        "支出",
	"income":         "收入",
	"transfer":       "轉帳",
	"exchange":       "換匯",
	"trade":          "買賣",
	"reward":         "回饋",
	"dealing":        "往來增減",
	"instalment":     "分期",
	"reconciliation": "對帳",
	"opening":        "期初餘額",
}

// RenderWarnings says what is odd in the mapping, one line each. A command says it
// before anything else, whatever the command goes on to do.
func RenderWarnings(w io.Writer, warnings []model.Warning) error {
	var b strings.Builder
	for _, warning := range warnings {
		fmt.Fprintf(&b, "注意：%s\n", warningText(warning))
	}
	return flush(w, &b)
}

// RenderApply says what apply did.
func RenderApply(w io.Writer, out usecase.ApplyOutput) error {
	var b strings.Builder
	applyText(&b, out)
	return flush(w, &b)
}

func applyText(w *strings.Builder, out usecase.ApplyOutput) {
	for _, warning := range out.Warnings {
		fmt.Fprintf(w, "注意：%s\n", warningText(warning))
	}
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
	writeApplied(w, out)
	writeHeldBack(w, out)
	writeUnaccounted(w, out)
	for _, d := range out.ProjectDifferences {
		fmt.Fprintf(w, "注意：專案「%s」在帳本的預設領域是「%s」，對照檔寫的是「%s」；匯入沿用帳本的，沒有改\n",
			d.Project, orNone(d.Ledger), orNone(d.Mapping))
	}
	fmt.Fprintf(w, "MOZE 的餘額調整 %d 筆留到確認該月時對帳；不需記錄 %d 筆；無法記錄的列 %d 筆（見 plan 的報告）\n",
		out.Adjustments, out.NoOps, out.Skipped)
	for _, f := range out.Failed {
		fmt.Fprintf(w, "失敗：%s %s（檔案第 %s 行），帳本的回覆：%v\n", kindText[f.Unit.Kind], f.Unit.Date.Format(time.DateOnly), lineList(f.Lines), f.Err)
	}
}

// writeApplied counts the units recorded, updated, recorded again, and the ones the
// ledger had, with what kinds they are.
func writeApplied(w *strings.Builder, out usecase.ApplyOutput) {
	newTotal, updatedTotal, revivedTotal, dupTotal := sum(out.New), sum(out.Updated), sum(out.Revived), sum(out.Duplicate)
	fmt.Fprintf(w, "草稿：新增 %d 筆，已更新 %d 筆，", newTotal, updatedTotal)
	if revivedTotal > 0 {
		fmt.Fprintf(w, "已重新記錄（先前的版本已作廢）%d 筆，", revivedTotal)
	}
	fmt.Fprintf(w, "已存在 %d 筆，失敗 %d 筆\n", dupTotal, len(out.Failed))
	if newTotal > 0 {
		fmt.Fprintf(w, "  新增的：%s\n", kinds(out.New))
	}
	if updatedTotal > 0 {
		fmt.Fprintf(w, "  已更新的（對照改了，原來的草稿已捨棄、依新的對照重記）：%s\n", kinds(out.Updated))
	}
	if revivedTotal > 0 {
		fmt.Fprintf(w, "  已重新記錄的（對照改回先前的樣子，先前的版本已作廢，所以用新的鍵重記）：%s\n", kinds(out.Revived))
	}
}

// writeHeldBack says the units that were not recorded: those whose confirmed version is
// not what the plan says, those that a plan which is gone holds back, and those that
// the user voided.
func writeHeldBack(w *strings.Builder, out usecase.ApplyOutput) {
	if len(out.Stale) > 0 {
		fmt.Fprintf(w, "已確認的內容與對照不同 %d 筆；不會動它們，也不會在旁邊再記一筆：\n", len(out.Stale))
		for _, s := range out.Stale {
			writeUnit(w, s.Unit, s.Lines)
		}
		fmt.Fprintln(w, "  把它們作廢之後再執行一次 apply，會依現在的對照重記成草稿。")
	}
	writeBlocked(w, out.Blocked)
	if len(out.Voided) > 0 {
		fmt.Fprintf(w, "這個版本已作廢 %d 筆；匯入不會再把它們記回去：\n", len(out.Voided))
		for _, v := range out.Voided {
			writeUnit(w, v.Unit, v.Lines)
		}
	}
}

// writeUnaccounted says what the ledger has of the import that no unit of the plan
// accounts for: the drafts that were removed and the confirmed transactions that were
// not, on the days the export covers, and what is of the days it does not.
func writeUnaccounted(w *strings.Builder, out usecase.ApplyOutput) {
	if len(out.Removed) > 0 {
		fmt.Fprintf(w, "已移除 %d 筆匯入的草稿（匯出裡已經沒有對應的列）：\n", len(out.Removed))
		writeOrphans(w, out.Removed)
	}
	if len(out.Orphans) > 0 {
		fmt.Fprintf(w, "匯出裡沒有對應的列、但已確認的交易 %d 筆（列被改過、刪掉，或現在被略過）；不會動它們：\n", len(out.Orphans))
		writeOrphans(w, out.Orphans)
		fmt.Fprintln(w, "  把它們作廢，再執行 apply 與 confirm。")
	}
	writeUncovered(w, out.Uncovered)
}

// writeBlocked says the units that a plan of instalments which is gone holds back, by
// the plan: it is the plan that the user voids, and the units are recorded after it.
func writeBlocked(w *strings.Builder, blocked []usecase.BlockedUnit) {
	var plans []string
	byPlan := map[string][]usecase.BlockedUnit{}
	for _, b := range blocked {
		if _, ok := byPlan[b.Plan]; !ok {
			plans = append(plans, b.Plan)
		}
		byPlan[b.Plan] = append(byPlan[b.Plan], b)
	}
	for _, plan := range plans {
		units := byPlan[plan]
		fmt.Fprintf(w, "被已不存在的計畫 %s 擋住、沒有記的單位 %d 筆（它還計著這些單位的利息，記在旁邊同一筆利息會算兩次）：\n", plan, len(units))
		for _, b := range units {
			writeUnit(w, b.Unit, b.Lines)
		}
		fmt.Fprintf(w, "  要作廢的是這個計畫（%s），不是這些單位；作廢之後再執行一次 apply。\n", plan)
	}
}

// writeUncovered says, in one line, what the ledger has of the import on days that the
// export does not cover, which was left alone; it says nothing when there is none.
func writeUncovered(w *strings.Builder, u usecase.Uncovered) {
	if u.Count == 0 {
		return
	}
	covers := "這份匯出沒有任何日期，"
	if !u.First.IsZero() {
		covers = fmt.Sprintf("這份匯出（%s 至 %s）", u.First.Format(time.DateOnly), u.Last.Format(time.DateOnly))
	}
	fmt.Fprintf(w, "注意：%s沒有涵蓋帳本裡 %d 筆匯入的交易（日期 %s 至 %s），沒有動它們\n",
		covers, u.Count, u.From.Format(time.DateOnly), u.To.Format(time.DateOnly))
}

// writeUnit says a unit by its kind, its day, the lines of the export it is made of and
// the start of its description.
func writeUnit(w *strings.Builder, u model.Unit, lines []int) {
	fmt.Fprintf(w, "  %s %s（檔案第 %s 行）%s\n", kindText[u.Kind], u.Date.Format(time.DateOnly), lineList(lines), clip(u.Description, 24))
}

// writeOrphans says transactions of the import by their day, their kind, the first
// account they move and how much, and the key they were recorded under, which is what
// the user finds them by.
func writeOrphans(w *strings.Builder, orphans []usecase.Orphan) {
	writeTransactions(w, orphans, false)
}

// statusText says whether a transaction of the import is a draft or confirmed.
var statusText = map[string]string{"draft": "（草稿）", "confirmed": "（已確認）"}

// writeTransactions says them so, and with whether each is a draft or confirmed when the
// user is to do one thing with the drafts and another with the confirmed ones.
func writeTransactions(w *strings.Builder, ts []usecase.Orphan, withStatus bool) {
	for _, o := range ts {
		kind := ledgerKindText[o.Kind]
		if kind == "" {
			kind = o.Kind
		}
		fmt.Fprintf(w, "  %s %s %s %s %s", o.Date.Format(time.DateOnly), kind, o.Account, o.Instrument, signedQuantity(o.Amount))
		if o.Description != "" {
			fmt.Fprintf(w, "「%s」", clip(o.Description, 24))
		}
		if withStatus {
			fmt.Fprint(w, statusText[o.Status])
		}
		fmt.Fprintf(w, "\n    鍵：%s\n", o.Key)
	}
}

// signedQuantity writes a quantity with the sign of what it is not negative.
func signedQuantity(q decimal.Decimal) string {
	if q.IsPositive() {
		return "+" + q.String()
	}
	return q.String()
}

func orNone(area string) string {
	if area == "" {
		return "無"
	}
	return area
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

func blockedText(w *strings.Builder, e *usecase.BlockedError) {
	fmt.Fprintln(w, "對照檔還有要處理的項目，不能繼續：")
	if missing := e.Missing(); len(missing) > 0 {
		fmt.Fprintf(w, "  對照檔缺少 %d 個項目：\n", len(missing))
		writeMissing(w, missing, "    ")
	}
	if e.Guessed > 0 {
		fmt.Fprintf(w, "  對照檔裡還有 %d 個標了 # guessed 的項目：逐項看過、改好之後，把標記拿掉\n", e.Guessed)
	}
	if len(e.Plan.Problems) > 0 {
		fmt.Fprintf(w, "  有問題的項目 %d 個：\n", len(e.Plan.Problems))
		for _, p := range e.Plan.Problems {
			fmt.Fprintf(w, "    %s\n", problemText(p))
		}
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
	for _, warning := range out.Warnings {
		fmt.Fprintf(w, "注意：%s\n", warningText(warning))
	}
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
		fmt.Fprintf(w, "對帳（MOZE 的餘額調整）：套用 %d 筆、先前已完成 %d 筆、被擋下 %d 筆\n", applied, done, blocked)
	}
	for _, a := range out.Adjustments {
		if a.State == usecase.AdjustmentBlocked {
			fmt.Fprintf(w, "  擋下：%s %s %s %s，帳本的回覆：%v\n", a.Adjustment.Date.Format(time.DateOnly), a.Adjustment.Account, a.Adjustment.Instrument, a.Adjustment.Amount, a.Err)
		}
	}
	if len(out.Orphans) > 0 {
		fmt.Fprintf(w, "注意：有 %d 筆匯入的草稿，匯出裡已經沒有對應的列，沒有動它們（apply 會移除）：\n", len(out.Orphans))
		writeOrphans(w, out.Orphans)
	}
	fmt.Fprintln(w)
	checkText(w, out.Check, false)
}

// checkText writes the balances of a month by the ledger and by the export. With
// onlyDifferences it writes the lines that differ alone.
func checkText(w *strings.Builder, c model.MonthCheck, onlyDifferences bool) {
	lines := c.Lines
	if onlyDifferences {
		lines = c.Differences()
	}
	if c.Partial {
		fmt.Fprintf(w, "%s 這個月只到 %s（匯出的最後一天），帳本餘額與 CSV 預期餘額：", c.Month, c.End.Format(time.DateOnly))
	} else {
		fmt.Fprintf(w, "%s 月底（%s）帳本餘額與 CSV 預期餘額：", c.Month, c.End.Format(time.DateOnly))
	}
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
			note = "已說明：有不匯入的部分"
		case !l.Unexplained().IsZero():
			note = "無法解釋"
		}
		rows[i] = []string{
			l.Account, l.Instrument, money(l.Ledger, d), money(l.Expected, d),
			signed(l.Difference(), d), signed(l.Unrecorded, d), note,
		}
	}
	table(w, []string{"帳戶", "標的", "帳本", "CSV 預期", "差額", "不匯入的部分", "說明"}, rows)
}

// RenderVerify says, for every month that is confirmed, whether the ledger holds what
// the export says.
func RenderVerify(w io.Writer, out usecase.VerifyOutput) error {
	var b strings.Builder
	verifyText(&b, out)
	return flush(w, &b)
}

func verifyText(w *strings.Builder, out usecase.VerifyOutput) {
	for _, warning := range out.Warnings {
		fmt.Fprintf(w, "注意：%s\n", warningText(warning))
	}
	if len(out.Months) == 0 {
		fmt.Fprintln(w, "還沒有確認完的月份，沒有東西可以比對")
		if out.Open != "" {
			fmt.Fprintf(w, "最早還沒確認完的月份：%s\n", out.Open)
		}
	} else {
		fmt.Fprintf(w, "已確認的月份 %d 個（%s 至 %s）\n", len(out.Months), out.Months[0].Month, out.Months[len(out.Months)-1].Month)
		if out.Open != "" {
			fmt.Fprintf(w, "下一個還沒確認完的月份：%s\n", out.Open)
		}
		fmt.Fprintln(w)
		for _, m := range out.Months {
			if len(m.Differences()) == 0 {
				fmt.Fprintf(w, "%s：全部吻合（%d 個帳戶與標的）%s\n", m.Month, len(m.Lines), partialText(m))
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
	if len(out.Stale) > 0 {
		fmt.Fprintf(w, "已確認的內容與對照不同 %d 筆（作廢它們，再執行 apply 依現在的對照重記）：\n", len(out.Stale))
		for _, s := range out.Stale {
			writeUnit(w, s.Unit, s.Lines)
		}
	}
	writeBlocked(w, out.Blocked)
	if len(out.Orphans) > 0 {
		fmt.Fprintf(w, "匯出裡沒有對應的列的交易 %d 筆（已確認的會算進餘額，作廢它們；草稿由 apply 移除）：\n", len(out.Orphans))
		writeOrphans(w, out.Orphans)
	}
}

// RenderStatus writes the state of the import by month.
func RenderStatus(w io.Writer, out usecase.StatusOutput) error {
	var b strings.Builder
	if out.LaterStart != nil {
		writeLaterStart(&b, *out.LaterStart, "注意：")
		return flush(w, &b)
	}
	for _, warning := range out.Warnings {
		fmt.Fprintf(&b, "注意：%s\n", warningText(warning))
	}
	rows := make([][]string, 0, len(out.Months))
	for _, m := range out.Months {
		rows = append(rows, []string{
			m.Month, fmt.Sprint(m.Drafts), fmt.Sprint(m.Confirmed), fmt.Sprint(m.Skipped),
			fmt.Sprint(m.AdjustmentsPending), fmt.Sprint(m.NotApplied), fmt.Sprint(m.Voided), fmt.Sprint(m.Stale), fmt.Sprint(m.Blocked),
		})
	}
	table(&b, []string{"月份", "草稿", "已確認", "略過", "待對帳", "尚未匯入", "已作廢", "已確認但與對照不同", "被已不存在的計畫擋住"}, rows)
	if len(out.Orphans) > 0 {
		fmt.Fprintf(&b, "匯出裡沒有對應的列的交易 %d 筆（已確認的會算進餘額，作廢它們；草稿由 apply 移除）：\n", len(out.Orphans))
		writeOrphans(&b, out.Orphans)
	}
	writeUncovered(&b, out.Uncovered)
	return flush(w, &b)
}

// Refusal says, in plain terms, what the use cases refuse with when it is something the
// user can put right: an export that begins after the import in the ledger does, a
// mapping that is not ready, a month that comes after one that is not confirmed, a
// confirmed transaction that is not what the plan says. It returns the error for the
// command to fail with, in words the user reads, or the error as it is for anything
// else (the ledger's own errors are English, as everything of the ledger is).
func Refusal(w io.Writer, err error) error {
	var later *usecase.LaterStartError
	var blocked *usecase.BlockedError
	var order *usecase.MonthOrderError
	var stale *usecase.StaleError
	var orphan *usecase.OrphanError
	var b strings.Builder
	var out error
	switch {
	case errors.As(err, &later):
		writeLaterStart(&b, later.LaterStart, "")
		out = fmt.Errorf("這份匯出從 %s 開始，帳本裡的匯入從 %s 開始，什麼都沒有動",
			later.From.Format(time.DateOnly), later.LedgerFrom.Format(time.DateOnly))
	case errors.As(err, &blocked):
		blockedText(&b, blocked)
		out = errors.New("對照檔還有要處理的項目，什麼都沒有寫入")
		if len(blocked.Missing()) == 0 && len(blocked.Plan.Problems) == 0 {
			out = fmt.Errorf("對照檔裡還有 %d 個標了 # guessed 的項目，看過、改好之後把標記拿掉，再執行 apply", blocked.Guessed)
		}
	case errors.As(err, &order):
		fmt.Fprintf(&b, "不能確認 %s：%s 還有 %d 筆草稿、%d 筆尚未匯入的列、%d 筆對帳沒處理完。請先確認 %s。\n",
			order.Month, order.Open, order.Drafts, order.NotApplied, order.Adjustments, order.Open)
		out = fmt.Errorf("%s 之前的月份還沒確認完，什麼都沒有確認", order.Month)
	case errors.As(err, &stale):
		fmt.Fprintf(&b, "不能確認 %s：已確認的內容與對照不同 %d 筆：\n", stale.Month, len(stale.Units))
		for _, s := range stale.Units {
			writeUnit(&b, s.Unit, s.Lines)
		}
		fmt.Fprintln(&b, "把它們作廢，執行 apply 依現在的對照重記，再確認。")
		out = fmt.Errorf("有 %d 筆已確認的交易與對照不同，什麼都沒有確認", len(stale.Units))
	case errors.As(err, &orphan):
		fmt.Fprintf(&b, "不能確認 %s：匯出裡沒有對應的列、但已確認的交易 %d 筆（列被改過、刪掉，或現在被略過）：\n", orphan.Month, len(orphan.Orphans))
		writeOrphans(&b, orphan.Orphans)
		fmt.Fprintln(&b, "把它們作廢，執行 apply 依現在的對照重記，再確認。")
		out = fmt.Errorf("有 %d 筆已確認的交易在匯出裡找不到對應的列，什麼都沒有確認", len(orphan.Orphans))
	case errors.Is(err, usecase.ErrUnknownMonth):
		return errors.New("匯出裡沒有這個月份的資料")
	case errors.Is(err, usecase.ErrNotApplied):
		return errors.New("這個月有列還沒有 apply 進帳本，請先執行 apply")
	default:
		if text, ok := failureText(err); ok {
			return errors.New(text)
		}
		return err
	}
	if werr := flush(w, &b); werr != nil {
		return werr
	}
	return out
}

// laterStartListLimit is how many of the transactions that are before an export are said.
const laterStartListLimit = 20

// writeLaterStart says, as the refusal and as the warning of status, that the export
// begins after the import in the ledger does, with the transactions of the import that
// are before it, the first of them, and the two things it can be: the export is not the
// whole one, which the user replaces; or the user deleted or changed its first rows in
// MOZE, and voids (discards, for drafts) the transactions that were made of them. The
// opening balances are not among them, and nothing was changed.
func writeLaterStart(w *strings.Builder, l usecase.LaterStart, prefix string) {
	fmt.Fprintf(w, "%s這份匯出從 %s 開始，帳本裡的匯入從 %s 開始，而且帳本裡有 %d 筆匯入的交易，日期早於這份匯出的第一列：\n",
		prefix, l.From.Format(time.DateOnly), l.LedgerFrom.Format(time.DateOnly), len(l.Before))
	writeTransactions(w, l.Before[:min(len(l.Before), laterStartListLimit)], true)
	if more := len(l.Before) - laterStartListLimit; more > 0 {
		fmt.Fprintf(w, "  …另有 %d 筆\n", more)
	}
	fmt.Fprintln(w, "有兩種可能：")
	fmt.Fprintln(w, "  這份匯出的開頭比較晚：請改用完整的匯出")
	fmt.Fprintln(w, "  你在 MOZE 刪掉或改了最前面的列：請作廢（草稿則捨棄）上面這幾筆，再執行一次")
	fmt.Fprintln(w, "什麼都沒有動")
}

// partialText says of the month that the export stops in that it is compared only up to
// the last day of the export; it says nothing for any other month.
func partialText(c model.MonthCheck) string {
	if !c.Partial {
		return ""
	}
	return fmt.Sprintf("，這個月只到 %s（匯出的最後一天）", c.End.Format(time.DateOnly))
}
