package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// An export that ends earlier, or begins later, than the one that was applied before says
// nothing of the days it does not reach: what the ledger has of them is told in one line,
// not removed and not asked to be voided. A month inside what it covers that it has no
// rows of is another matter: that is what an edited or a deleted row looks like.

// keepRows keeps the rows of the export (the header first) that the function says to, by
// the account and the date of the row; the date is as the export writes it, "2026/01/05",
// and empty for an opening.
func keepRows(keep func(account, date string) bool) func(rows [][]string) [][]string {
	return func(rows [][]string) [][]string {
		out := rows[:1:1]
		for _, row := range rows[1:] {
			if keep(row[column("帳戶")], row[column("日期")]) {
				out = append(out, row)
			}
		}
		return out
	}
}

// importedOutside are the transactions of the import that are not void and are of days
// that the filter says, and the first and the last of those days, as the output says them.
func importedOutside(all []finance.Transaction, outside func(time.Time) bool) (n int, from, to string) {
	var first, last time.Time
	for _, tx := range all {
		if !strings.HasPrefix(tx.IdempotencyKey, "moze:") || tx.Status == "void" || !outside(tx.OccurredOn) {
			continue
		}
		n++
		if first.IsZero() || tx.OccurredOn.Before(first) {
			first = tx.OccurredOn
		}
		if last.IsZero() || tx.OccurredOn.After(last) {
			last = tx.OccurredOn
		}
	}
	return n, first.Format(time.DateOnly), last.Format(time.DateOnly)
}

func TestImportWithAnExportThatDoesNotCoverWhatTheLedgerHasOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")
	l := e.ledger()
	count := len(e.transactions(l))
	endOfJanuary := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	// The export ends in January: the rows from February on are not in it. The openings,
	// which have no day, are, and are dated from the first row of their account; the
	// purse, which has rows only from February on, is left out with them, or its opening
	// would be dated again from another row.
	later, from, to := importedOutside(e.transactions(l), func(d time.Time) bool { return d.After(endOfJanuary) })
	if later == 0 {
		t.Fatal("the ledger has nothing of February or later")
	}
	e.editExport(keepRows(func(account, date string) bool { return date < "2026/02/01" && account != "Fake Purse" }))
	line := fmt.Sprintf("這份匯出（2026-01-04 至 2026-01-27）沒有涵蓋帳本裡 %d 筆匯入的交易（日期 %s 至 %s），沒有動它們", later, from, to)
	out := e.must("apply")
	if !strings.Contains(out, line) || strings.Contains(out, "已移除") || strings.Contains(out, "匯出裡沒有對應的列") {
		t.Errorf("apply of an export that ends in January printed:\n%s\nwant the line: %s", out, line)
	}
	if got := len(e.transactions(l)); got != count {
		t.Errorf("the ledger has %d transactions, want %d: nothing is removed", got, count)
	}
	if status := e.must("status"); !strings.Contains(status, line) {
		t.Errorf("status printed:\n%s", status)
	}
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "匯出裡沒有對應的列") {
		t.Errorf("verify printed:\n%s", verify)
	}
	e.must("confirm", "--month", "2026-01")

	// The whole export again changes nothing; then one that begins in February, with only the
	// opening of the purse (whose first row is in February) of the openings: what the ledger
	// has of January is left alone, and February is confirmed, not refused for it. Its
	// balances are not the ledger's, as they cannot be from an export that has not the
	// months before, and confirm says so: that is not what is looked at here.
	if err := os.WriteFile(e.exportFile(), mozeExport(t), 0o600); err != nil {
		t.Fatal(err)
	}
	if again := e.must("apply"); strings.Contains(again, "這份匯出") || !strings.Contains(again, "草稿：新增 0 筆，已更新 0 筆") {
		t.Errorf("apply of the whole export printed:\n%s", again)
	}
	earlier, from, to := importedOutside(e.transactions(l), func(d time.Time) bool { return d.Before(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)) })
	e.editExport(keepRows(func(account, date string) bool {
		return date >= "2026/02/01" || date == "" && account == "Fake Purse"
	}))
	line = fmt.Sprintf("這份匯出（2026-02-05 至 2026-04-15）沒有涵蓋帳本裡 %d 筆匯入的交易（日期 %s 至 %s），沒有動它們", earlier, from, to)
	out = e.must("apply")
	if !strings.Contains(out, line) || strings.Contains(out, "已移除") || strings.Contains(out, "匯出裡沒有對應的列") {
		t.Errorf("apply of an export that begins in February printed:\n%s\nwant the line: %s", out, line)
	}
	if got := len(e.transactions(l)); got != count {
		t.Errorf("the ledger has %d transactions, want %d: nothing is removed", got, count)
	}
	if february, _ := e.moze("confirm", "--month", "2026-02"); !strings.Contains(february, "2026-02：確認") || strings.Contains(february, "匯出裡沒有對應的列") {
		t.Errorf("confirm February printed:\n%s", february)
	}

	// What the export has no row of, on a day it covers, is what a deleted row looks like:
	// the two drafts of March (an expense and an income) are not outside the export, and
	// are removed.
	if err := os.WriteFile(e.exportFile(), mozeExport(t), 0o600); err != nil {
		t.Fatal(err)
	}
	e.editExport(keepRows(func(_, date string) bool { return date != "2026/03/02" && date != "2026/03/05" }))
	out = e.must("apply")
	if !strings.Contains(out, "已移除 2 筆匯入的草稿") || strings.Contains(out, "沒有涵蓋") {
		t.Errorf("apply of an export that lacks two rows of March printed:\n%s", out)
	}
}

// The plan of instalments is made on the 5th of February: an export that begins on the
// 10th has its payments and its interest and not the conversion. The plan is of a day the
// export does not cover, so it is not an orphan; the interest rows that come to alone are
// held back by it all the same, and recording them beside it would count them twice. Every
// command that has to say it fails, by the plan, which is what the user voids.
func TestImportWithAnExportThatBeginsInTheMiddleOfAPlanOfInstalmentsHoldsBackItsInterestOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")
	e.must("confirm", "--month", "2026-02")
	l := e.ledger()
	count := len(e.transactions(l))
	var key string
	for _, tx := range e.transactions(l) {
		if strings.HasPrefix(tx.IdempotencyKey, "moze:plan-") {
			key = tx.IdempotencyKey
		}
	}
	if key == "" {
		t.Fatal("the ledger has no plan of instalments")
	}

	// The opening of the purse, whose first row is in February, is kept with the rows from
	// the 10th.
	e.editExport(keepRows(func(account, date string) bool {
		return date >= "2026/02/10" || date == "" && account == "Fake Purse"
	}))
	out, err := e.moze("apply")
	if err == nil || !strings.Contains(err.Error(), "有 3 個單位被已不存在的計畫擋住：作廢那個計畫（"+key+"）") {
		t.Fatalf("apply: err = %v, printed:\n%s", err, out)
	}
	for _, want := range []string{
		"被已不存在的計畫 " + key + " 擋住、沒有記的單位 3 筆",
		"要作廢的是這個計畫（" + key + "），不是這些單位",
		"沒有涵蓋帳本裡",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("apply does not say %q; it printed:\n%s", want, out)
		}
	}
	if strings.Contains(out, "已移除") || strings.Contains(out, "已確認的內容與對照不同") || strings.Contains(out, "匯出裡沒有對應的列") {
		t.Errorf("apply removed something, or tells the units held back as confirmed transactions to void, or the plan as an orphan:\n%s", out)
	}
	if got := len(e.transactions(l)); got != count {
		t.Errorf("the ledger has %d transactions, want %d: nothing is recorded beside the plan", got, count)
	}
	if verify, err := e.moze("verify"); err == nil || !strings.Contains(err.Error(), "個單位被已不存在的計畫擋住：作廢那個計畫（"+key+"）") || !strings.Contains(verify, "被已不存在的計畫 "+key+" 擋住") {
		t.Errorf("verify: err = %v, printed:\n%s", err, verify)
	}
	if status := e.must("status"); !strings.Contains(status, "被已不存在的計畫擋住") {
		t.Errorf("status printed:\n%s", status)
	}

	// The user voids the plan: what the export has is recorded, the interest as expenses.
	e.void(l, key)
	if out := e.must("apply"); !strings.Contains(out, "草稿：新增 3 筆") || strings.Contains(out, "擋住") {
		t.Errorf("apply after the void printed:\n%s", out)
	}
}
