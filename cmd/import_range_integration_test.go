package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// and verify before anything is done, with the transactions of the import that are before
// it (twenty at most), the two things it can be, and nothing else; status says the same as
// a warning. What the ledger has is not changed, and the opening balances are not among the
// transactions: the whole export goes on from where it was.
func TestImportRefusesAnExportThatBeginsAfterTheImportInTheLedgerDoesOnARealDatabase(t *testing.T) {
	const (
		sentence = "這份匯出從 2026-02-05 開始，帳本裡的匯入從 2026-01-04 開始，什麼都沒有動"
		shorter  = "這份匯出的開頭比較晚：請改用完整的匯出"
		deleted  = "你在 MOZE 刪掉或改了最前面的列：請作廢（草稿則捨棄）上面這幾筆，再執行一次"
	)
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
			n := 0
			for _, tx := range e.transactions(l) {
				if strings.HasPrefix(tx.IdempotencyKey, "moze:") && tx.Status != "void" && tx.Kind != "opening" && tx.OccurredOn.Before(time.Date(2026, 2, 5, 0, 0, 0, 0, time.UTC)) {
					n++
				}
			}
			if n <= 20 {
				t.Fatalf("the ledger has %d transactions before February, want more than a list says", n)
			}

			e.editExport(keepRows(func(_, date string) bool { return date >= "2026/02/01" || date == "" && tt.openings }))
			for _, args := range [][]string{{"apply"}, {"confirm", "--month", "2026-02"}, {"verify"}} {
				out, err := e.moze(args...)
				if err == nil || err.Error() != sentence || !strings.HasSuffix(strings.TrimSpace(out), "Error: "+sentence) {
					t.Errorf("%s: err = %v, printed:\n%s\nwant the refusal that ends in the sentence", args[0], err, out)
				}
				for _, want := range []string{
					fmt.Sprintf("這份匯出從 2026-02-05 開始，帳本裡的匯入從 2026-01-04 開始，而且帳本裡有 %d 筆匯入的交易，日期早於這份匯出的第一列：\n", n),
					fmt.Sprintf("  …另有 %d 筆\n", n-20),
					"有兩種可能：\n  " + shorter + "\n  " + deleted + "\n什麼都沒有動\n",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("%s did not print %q:\n%s", args[0], want, out)
					}
				}
				if listed := strings.Count(out, "鍵：moze:"); listed != 20 {
					t.Errorf("%s lists %d transactions, want 20", args[0], listed)
				}
				for _, not := range []string{"期初餘額", "草稿：新增", "已確認的內容與對照不同", "匯出裡沒有對應的列"} {
					if strings.Contains(out, not) {
						t.Errorf("%s printed %q:\n%s", args[0], not, out)
					}
				}
			}
			status := e.must("status")
			if !strings.HasPrefix(status, "注意：這份匯出從 2026-02-05 開始，帳本裡的匯入從 2026-01-04 開始，") ||
				!strings.Contains(status, shorter) || !strings.Contains(status, deleted) || strings.Contains(status, "Error") || strings.Contains(status, "月份") {
				t.Errorf("status printed:\n%s\nwant the refusal as a warning, and nothing else", status)
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

// The user deletes in MOZE the one row of the first day: the export then begins on a later
// day, which looks like an export that was cut short, and the transaction that was made of
// the row is before it. The refusal names that transaction, and the two things it can be;
// the user voids it (a draft is discarded), and apply, confirm and verify go on. That holds
// with months confirmed and with a ledger of drafts only.
func TestImportRefusesAnExportThatLostItsFirstRowAndGoesOnOnceWhatWasMadeOfItIsVoidedOrDiscardedOnARealDatabase(t *testing.T) {
	for _, confirmed := range []bool{true, false} {
		name := "a ledger of drafts"
		if confirmed {
			name = "a ledger that is confirmed"
		}
		t.Run(name, func(t *testing.T) {
			e := newImportEnv(t)
			e.editExport(func(rows [][]string) [][]string {
				return slices.Insert(rows, 8, r("支出", "Fake 信用卡", "TWD", "-5", "2026-01-03 09:00", "主類別", "飲食", "子類別", "午餐", "名稱", "First coffee"))
			})
			e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
			e.review()
			e.must("apply")
			months := []string{"2026-01", "2026-02", "2026-03", "2026-04"}
			if confirmed {
				for _, month := range months {
					e.must("confirm", "--month", month)
				}
			}
			l := e.ledger()
			made := e.ofRow(l, e.rowKey("First coffee"))
			if len(made) != 1 {
				t.Fatalf("the ledger has %d transactions of the first row, want 1", len(made))
			}
			first := made[0]
			before := snapshot(e.transactions(l))

			// The row is deleted in MOZE.
			e.wholeExport()
			status := map[bool]string{true: "已確認", false: "草稿"}[confirmed]
			sentence := "這份匯出從 2026-01-05 開始，帳本裡的匯入從 2026-01-03 開始，什麼都沒有動"
			for _, args := range [][]string{{"apply"}, {"confirm", "--month", "2026-01"}, {"verify"}} {
				out, err := e.moze(args...)
				if err == nil || err.Error() != sentence {
					t.Errorf("%s: err = %v, want the refusal %q", args[0], err, sentence)
				}
				for _, want := range []string{
					"而且帳本裡有 1 筆匯入的交易，日期早於這份匯出的第一列：\n  2026-01-03 支出 Fake 信用卡 TWD -5「First coffee」（" + status + "）\n    鍵：" + first.IdempotencyKey + "\n",
					"這份匯出的開頭比較晚：請改用完整的匯出",
					"你在 MOZE 刪掉或改了最前面的列：請作廢（草稿則捨棄）上面這幾筆，再執行一次",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("%s did not print %q:\n%s", args[0], want, out)
					}
				}
				if strings.Count(out, "鍵：moze:") != 1 || strings.Contains(out, "期初餘額") {
					t.Errorf("%s names more than the one transaction of the row, or an opening balance:\n%s", args[0], out)
				}
			}
			if st := e.must("status"); !strings.Contains(st, "注意：這份匯出從 2026-01-05 開始") || !strings.Contains(st, first.IdempotencyKey) {
				t.Errorf("status printed:\n%s", st)
			}
			if after := snapshot(e.transactions(l)); after != before {
				t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, after)
			}

			// The user voids it, or discards it, as the refusal says; the import goes on.
			if confirmed {
				e.void(l, first.IdempotencyKey)
			} else if err := l.DiscardDraft.DiscardDraft(context.Background(), first.ID); err != nil {
				t.Fatal(err)
			}
			if out := e.must("apply"); !strings.Contains(out, "草稿：新增 0 筆，已更新 0 筆") || strings.Contains(out, "已移除") || strings.Contains(out, "匯出裡沒有對應的列") {
				t.Errorf("apply after it printed:\n%s", out)
			}
			for _, month := range months {
				if out, err := e.moze("confirm", "--month", month); err != nil {
					t.Errorf("confirm %s after it: %v\n%s", month, err, out)
				}
			}
			if verify, err := e.moze("verify"); err != nil || !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 4 個") {
				t.Errorf("verify after it: err = %v, printed:\n%s", err, verify)
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

// What a plan of instalments that the export ends in the middle of is made of are its own
// rows: the conversion, the payments on the days its periods fall due and the interest of
// those days. An interest of the same account on another day is recorded beside the plan,
// as the expense it is, and an export that ends in the middle of the plan does not take it
// for the plan's: it is not an orphan to void.
func TestImportKeepsAnInterestThatIsNotThePlansWhenTheExportEndsInTheMiddleOfThePlanOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.editExport(func(rows [][]string) [][]string {
		at := slices.IndexFunc(rows, func(row []string) bool { return row[column("日期")] == "2026/04/15" })
		return slices.Insert(rows, at, r("利息", "Fake 信用卡分期", "TWD", "-3", "2026-03-20 10:00", "主類別", "利息支出", "子類別", "利息支出"))
	})
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	for _, month := range []string{"2026-01", "2026-02", "2026-03"} {
		e.must("confirm", "--month", month)
	}
	l := e.ledger()
	before := snapshot(e.transactions(l))

	e.editExport(keepRows(func(_, date string) bool { return date <= "2026/03/25" }))
	out := e.must("apply")
	if !strings.Contains(out, "草稿：新增 0 筆，已更新 0 筆") ||
		strings.Contains(out, "匯出裡沒有對應的列") || strings.Contains(out, "已移除") || strings.Contains(out, "擋住") || strings.Contains(out, "已確認的內容與對照不同") {
		t.Errorf("apply of an export that ends on 25 March printed:\n%s", out)
	}
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "匯出裡沒有對應的列") {
		t.Errorf("verify printed:\n%s", verify)
	}
	if after := snapshot(e.transactions(l)); after != before {
		t.Errorf("the ledger changed:\nbefore %s\nafter  %s", before, after)
	}
}

// An export that has no rows of the opening balances says nothing of them, and they are part
// of the history it describes all the same, which the accounts started from. The ones the
// ledger has as drafts are confirmed with the month they are dated in, like any other
// transaction, and do not hold the reconciliations of the month up.
func TestImportConfirmsTheOpeningDraftsTheLedgerHasWhenTheExportHasNoOpeningRowsOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	l := e.ledger()
	e.editExport(keepRows(func(_, date string) bool { return date != "" }))

	for _, month := range []string{"2026-01", "2026-02", "2026-03", "2026-04"} {
		if out, err := e.moze("confirm", "--month", month); err != nil || strings.Contains(out, "擋下：") {
			t.Errorf("confirm %s: err = %v, printed:\n%s", month, err, out)
		}
	}
	openings := 0
	for _, tx := range e.transactions(l) {
		if tx.Kind != "opening" {
			continue
		}
		openings++
		if tx.Status != "confirmed" {
			t.Errorf("the opening balance of %s is %s, want it confirmed with its month", tx.OccurredOn.Format(time.DateOnly), tx.Status)
		}
	}
	if openings == 0 {
		t.Fatal("the ledger has no opening balance")
	}
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || !strings.Contains(verify, "已確認的月份 4 個") {
		t.Errorf("verify printed:\n%s", verify)
	}
}
