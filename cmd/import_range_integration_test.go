package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	finance "github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The import works on the whole export of the history. One that ends earlier says nothing
// of the days after its last, and what the ledger has of them is told in one line, not
// removed and not asked to be voided; one that begins after the import in the ledger does
// is refused, since what it says of the ledger is not meant for it, and nothing in what the
// user is told asks to void what the ledger has right. A month in the middle that an export
// has no rows of is another matter: that is what an edited or a deleted row looks like.

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

// snapshot says what the ledger holds: every transaction with its status and its day.
func snapshot(all []finance.Transaction) string {
	lines := make([]string, 0, len(all))
	for _, tx := range all {
		lines = append(lines, fmt.Sprintf("%s %s %s", tx.IdempotencyKey, tx.Status, tx.OccurredOn.Format(time.DateOnly)))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// wholeExport puts the whole export back.
func (e importEnv) wholeExport() {
	e.t.Helper()
	if err := os.WriteFile(e.exportFile(), mozeExport(e.t), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

func TestImportWithAnExportThatEndsEarlierLeavesWhatTheLedgerHasOfLaterDaysAloneOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")
	l := e.ledger()
	count := len(e.transactions(l))
	endOfJanuary := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)

	// The export ends in January: the rows from February on are not in it. The openings,
	// which have no day, are, the one of the purse too, whose first row is in February: its
	// day, from that row, is not one that this export can say, and the ledger's is right.
	later, from, to := importedOutside(e.transactions(l), func(d time.Time) bool { return d.After(endOfJanuary) })
	if later == 0 {
		t.Fatal("the ledger has nothing of February or later")
	}
	e.editExport(keepRows(func(_, date string) bool { return date < "2026/02/01" }))
	line := fmt.Sprintf("這份匯出（2026-01-04 至 2026-01-27）沒有涵蓋帳本裡 %d 筆匯入的交易（日期 %s 至 %s），沒有動它們", later, from, to)
	out := e.must("apply")
	if !strings.Contains(out, line) || strings.Contains(out, "已移除") || strings.Contains(out, "匯出裡沒有對應的列") || strings.Contains(out, "已確認的內容與對照不同") || strings.Contains(out, "已更新的") {
		t.Errorf("apply of an export that ends in January printed:\n%s\nwant the line: %s", out, line)
	}
	if got := len(e.transactions(l)); got != count {
		t.Errorf("the ledger has %d transactions, want %d: nothing is removed", got, count)
	}
	if status := e.must("status"); !strings.Contains(status, line) {
		t.Errorf("status printed:\n%s", status)
	}
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "匯出裡沒有對應的列") || !strings.Contains(verify, "這個月只到 2026-01-27（匯出的最後一天）") {
		t.Errorf("verify printed:\n%s", verify)
	}
	e.must("confirm", "--month", "2026-01")

	// What the export has no row of, on a day it covers, is what a deleted row looks like:
	// the two drafts of March (an expense and an income) are not outside the export, and
	// are removed.
	e.wholeExport()
	if again := e.must("apply"); strings.Contains(again, "這份匯出") || !strings.Contains(again, "草稿：新增 0 筆，已更新 0 筆") {
		t.Errorf("apply of the whole export printed:\n%s", again)
	}
	e.editExport(keepRows(func(_, date string) bool { return date != "2026/03/02" && date != "2026/03/05" }))
	out = e.must("apply")
	if !strings.Contains(out, "已移除 2 筆匯入的草稿") || strings.Contains(out, "沒有涵蓋") {
		t.Errorf("apply of an export that lacks two rows of March printed:\n%s", out)
	}
}

// An export that begins after the import in the ledger does is refused by apply, confirm
// and verify before anything is done, with the sentence of the refusal and nothing else, and
// status says the sentence and nothing else. What the ledger has is not changed, and is not
// something the user is asked to void: the whole export goes on from where it was.
func TestImportRefusesAnExportThatBeginsAfterTheImportInTheLedgerDoesOnARealDatabase(t *testing.T) {
	const sentence = "這份匯出從 2026-02-05 開始，帳本裡的匯入從 2026-01-04 開始。匯入需要完整的匯出，請改用完整的匯出；不要作廢帳本裡的交易"
	for _, tt := range []struct {
		name     string
		openings bool
	}{
		{"with the rows of the opening balances", true},
		{"without them", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newImportEnv(t)
			e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
			e.review()
			e.must("apply")
			for _, month := range []string{"2026-01", "2026-02", "2026-03"} {
				e.must("confirm", "--month", month)
			}
			l := e.ledger()
			before := snapshot(e.transactions(l))

			e.editExport(keepRows(func(_, date string) bool { return date >= "2026/02/01" || date == "" && tt.openings }))
			for _, args := range [][]string{{"apply"}, {"confirm", "--month", "2026-02"}, {"verify"}} {
				out, err := e.moze(args...)
				if err == nil || err.Error() != sentence || strings.TrimSpace(out) != "Error: "+sentence {
					t.Errorf("%s: err = %v, printed:\n%s\nwant the sentence, and nothing else", args[0], err, out)
				}
				for _, advice := range []string{"把它們作廢", "作廢它們", "已確認的內容與對照不同", "匯出裡沒有對應的列", "期初餘額", "草稿：新增"} {
					if strings.Contains(out, advice) {
						t.Errorf("%s printed %q:\n%s", args[0], advice, out)
					}
				}
			}
			if status := e.must("status"); status != "注意："+sentence+"\n" {
				t.Errorf("status printed:\n%s\nwant the sentence as a warning, and nothing else", status)
			}
			if after := snapshot(e.transactions(l)); after != before {
				t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, after)
			}

			// The whole export goes on as if nothing had been tried.
			e.wholeExport()
			if again := e.must("apply"); !strings.Contains(again, "草稿：新增 0 筆，已更新 0 筆") || strings.Contains(again, "已確認的內容與對照不同") {
				t.Errorf("apply of the whole export printed:\n%s", again)
			}
			if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 4 個") {
				t.Errorf("verify of the whole export printed:\n%s", verify)
			}
		})
	}
}

// The month that an export stops in, before its end, is compared as of the last day of the
// export, which is all it says of the month: what the ledger has of the days after it is
// not what it is compared with, and the output says the month is partial.
func TestImportComparesTheMonthAnExportStopsInAsOfItsLastDayOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")

	e.editExport(keepRows(func(_, date string) bool { return date <= "2026/01/20" }))
	verify, err := e.moze("verify")
	if err != nil || !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "2026-01：全部吻合") || !strings.Contains(verify, "這個月只到 2026-01-20（匯出的最後一天）") {
		t.Errorf("verify of the export to 20 January: err = %v, printed:\n%s", err, verify)
	}
	confirm, err := e.moze("confirm", "--month", "2026-01")
	if err != nil || !strings.Contains(confirm, "2026-01 這個月只到 2026-01-20（匯出的最後一天），帳本餘額與 CSV 預期餘額：全部吻合") {
		t.Errorf("confirm of January of the export to 20 January: err = %v, printed:\n%s", err, confirm)
	}

	// The whole export says January to its end, and is compared to it.
	e.wholeExport()
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "這個月只到 2026-01") {
		t.Errorf("verify of the whole export printed:\n%s", verify)
	}
}

// The plan of instalments is of the 5th of February, with periods on the 15th of February,
// March and April. An export that ends on the 15th of March has the rows of the conversion
// and of the two periods, which do not add up to what was converted, so the plan is not in
// it: the plan is the ledger's, not an orphan to void, and its rows are not recorded beside
// it. Everything the export says agrees with the ledger.
func TestImportLeavesAPlanOfInstalmentsThatRunsPastTheEndOfTheExportAloneOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	for _, month := range []string{"2026-01", "2026-02", "2026-03"} {
		e.must("confirm", "--month", month)
	}
	l := e.ledger()
	before := snapshot(e.transactions(l))

	e.editExport(keepRows(func(_, date string) bool { return date <= "2026/03/15" }))
	line := "這份匯出（2026-01-04 至 2026-03-15）沒有涵蓋帳本裡 1 筆匯入的交易（日期 2026-02-05 至 2026-04-15），沒有動它們"
	out := e.must("apply")
	if !strings.Contains(out, line) || !strings.Contains(out, "草稿：新增 0 筆，已更新 0 筆") ||
		strings.Contains(out, "匯出裡沒有對應的列") || strings.Contains(out, "已移除") || strings.Contains(out, "擋住") || strings.Contains(out, "已確認的內容與對照不同") {
		t.Errorf("apply of an export that ends on 15 March printed:\n%s\nwant the line: %s", out, line)
	}
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "匯出裡沒有對應的列") || !strings.Contains(verify, "這個月只到 2026-03-15（匯出的最後一天）") {
		t.Errorf("verify printed:\n%s", verify)
	}
	if confirm := e.must("confirm", "--month", "2026-03"); !strings.Contains(confirm, "其中 0 項無法解釋") {
		t.Errorf("confirm of March printed:\n%s", confirm)
	}
	if status := e.must("status"); !strings.Contains(status, line) || strings.Contains(status, "匯出裡沒有對應的列") {
		t.Errorf("status printed:\n%s", status)
	}
	if after := snapshot(e.transactions(l)); after != before {
		t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, after)
	}
}
