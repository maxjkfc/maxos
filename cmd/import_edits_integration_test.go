package cmd

import (
	"bytes"
	"context"
	"encoding/csv"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	catalog "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
)

// The export is made again by MOZE after the user edits a row of it, and an edited row
// is another row: these tests do it on a real database, with the ledger's own clock,
// which is what the void and the creation times that the import compares come of.

// exportFile is the export in the directory of the import.
func (e importEnv) exportFile() string {
	return filepath.Join(e.dir, "MOZE_20260401_000000.csv")
}

// editExport makes the export again with the rows the function gives, from the rows the
// file has now (the header first), in the form MOZE writes.
func (e importEnv) editExport(change func(rows [][]string) [][]string) {
	e.t.Helper()
	data, err := os.ReadFile(e.exportFile())
	if err != nil {
		e.t.Fatal(err)
	}
	reader := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\ufeff"))))
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		e.t.Fatal(err)
	}
	rows = change(rows)
	var buf bytes.Buffer
	buf.WriteString("\ufeff")
	writer := csv.NewWriter(&buf)
	writer.UseCRLF = true
	if err := writer.WriteAll(rows); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(e.exportFile(), buf.Bytes(), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// column is the place of a column of the export.
func column(name string) int {
	for i, h := range mozeHeader {
		if h == name {
			return i
		}
	}
	panic("no column " + name)
}

// amountOf changes the amount of the row of the account on the day and the time (as the
// export writes them) from one to another.
func amountOf(date, clock, account, from, to string) func(rows [][]string) [][]string {
	return func(rows [][]string) [][]string {
		for _, row := range rows[1:] {
			if row[column("日期")] == date && row[column("時間")] == clock && row[column("帳戶")] == account && row[column("金額")] == from {
				row[column("金額")] = to
				return rows
			}
		}
		panic("the export has no row of " + account + " on " + date + " for " + from)
	}
}

// withoutOverride takes the override of the row out of the mapping again.
func (e importEnv) withoutOverride(rowKey string) {
	e.t.Helper()
	path := filepath.Join(e.dir, "mapping.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		e.t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, rowKey) {
			kept = append(kept, line)
		}
	}
	text := strings.Join(kept, "\n")
	// A section with nothing left in it goes too.
	text = strings.TrimSuffix(strings.TrimRight(text, "\n"), "rows:") + "\n"
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		e.t.Fatal(err)
	}
}

// void voids the transaction that has the key.
func (e importEnv) void(l ledger, key string) {
	e.t.Helper()
	for _, tx := range e.transactions(l) {
		if tx.IdempotencyKey == key {
			if _, err := l.VoidTransaction.VoidTransaction(context.Background(), tx.ID); err != nil {
				e.t.Fatalf("VoidTransaction %s: %v", key, err)
			}
			return
		}
	}
	e.t.Fatalf("the ledger has no transaction under %q", key)
}

func TestImportOfARowEditedAfterApplyOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	l := e.ledger()

	// A row edited before its month is confirmed: the draft of the row as it was is
	// removed, and the row as it is now is recorded.
	e.editExport(amountOf("2026/01/15", "10:00", "Fake Cash", "-40", "-45"))
	out := e.must("apply")
	if !strings.Contains(out, "已移除 1 筆匯入的草稿") || !strings.Contains(out, "草稿：新增 1 筆") {
		t.Errorf("apply after the edit printed:\n%s", out)
	}
	if st := e.must("status"); strings.Contains(st, "匯出裡沒有對應的列") {
		t.Errorf("status after the draft was removed printed:\n%s", st)
	}
	e.must("confirm", "--month", "2026-01")

	// The same row edited again after its month is confirmed: the confirmed transaction
	// counts toward the balances, and is not touched. Every command names it.
	e.editExport(amountOf("2026/01/15", "10:00", "Fake Cash", "-45", "-46"))
	out, err := e.moze("apply")
	if err == nil || !strings.Contains(out, "匯出裡沒有對應的列、但已確認的交易 1 筆") || !strings.Contains(out, "2026-01-15") || !strings.Contains(out, "Fake Cash") || !strings.Contains(out, "-45") {
		t.Fatalf("apply after the edit: err = %v, printed:\n%s", err, out)
	}
	key := regexp.MustCompile(`鍵：(moze:\S+)`).FindStringSubmatch(out)
	if key == nil {
		t.Fatalf("apply names no key:\n%s", out)
	}
	if out, err := e.moze("confirm", "--month", "2026-01"); err == nil || !strings.Contains(out, "匯出裡沒有對應的列、但已確認的交易 1 筆") || !strings.Contains(out, key[1]) {
		t.Errorf("confirm: err = %v, printed:\n%s", err, out)
	}
	if out, err := e.moze("verify"); err == nil || !strings.Contains(out, key[1]) {
		t.Errorf("verify: err = %v, printed:\n%s", err, out)
	}
	if st := e.must("status"); !strings.Contains(st, key[1]) {
		t.Errorf("status printed:\n%s", st)
	}

	// The user voids it, and the month is the export's again.
	e.void(l, key[1])
	if out := e.must("apply"); strings.Contains(out, "匯出裡沒有對應的列") {
		t.Errorf("apply after the void printed:\n%s", out)
	}
	e.must("confirm", "--month", "2026-01")
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") || strings.Contains(verify, "匯出裡沒有對應的列") {
		t.Errorf("verify after the void printed:\n%s", verify)
	}
}

func TestImportOfAVersionVoidedToMakeRoomIsRecordedAgainOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")
	l := e.ledger()

	// The plan changes (an override of a row of a confirmed month), the user voids the
	// confirmed transaction as apply says, the new version is recorded and confirmed;
	// the plan changes back, and the user voids that as apply says.
	paid := e.rowKey("有人付款")
	e.override(paid, "{treatment: dealing, party: Amy}")
	if out, err := e.moze("apply"); err == nil || !strings.Contains(out, "已確認的內容與對照不同 1 筆") {
		t.Fatalf("apply with the override: err = %v, printed:\n%s", err, out)
	}
	e.void(l, e.ofRow(l, paid)[0].IdempotencyKey)
	if out := e.must("apply"); !strings.Contains(out, "已更新 1 筆") {
		t.Fatalf("apply after the void printed:\n%s", out)
	}
	e.must("confirm", "--month", "2026-01")
	e.withoutOverride(paid)
	if out, err := e.moze("apply"); err == nil || !strings.Contains(out, "已確認的內容與對照不同 1 筆") {
		t.Fatalf("apply with the override taken away: err = %v, printed:\n%s", err, out)
	}
	for _, tx := range e.ofRow(l, paid) {
		if tx.Status == "confirmed" {
			e.void(l, tx.IdempotencyKey)
		}
	}

	// Both versions are void now, and the plan says the first: it is recorded again,
	// under a key of its own, and said to be.
	out := e.must("apply")
	if !strings.Contains(out, "已重新記錄（先前的版本已作廢）1 筆") {
		t.Fatalf("apply after the second void printed:\n%s", out)
	}
	// The ledger lists newest first: the transactions are told by their keys.
	var again, voided []string
	for _, tx := range e.ofRow(l, paid) {
		switch {
		case tx.Status == "draft" && tx.Kind == "income" && strings.Contains(tx.IdempotencyKey, "~2"):
			again = append(again, tx.IdempotencyKey)
		case tx.Status == "void":
			voided = append(voided, tx.Kind)
		}
	}
	if len(again) != 1 || len(voided) != 2 || len(e.ofRow(l, paid)) != 3 {
		t.Fatalf("the row has %d transactions, of which %d are the first version again as a draft of a second attempt and %d are void, want 3, 1 and 2", len(e.ofRow(l, paid)), len(again), len(voided))
	}
	if repeat := e.must("apply"); strings.Contains(repeat, "已重新記錄") || !strings.Contains(repeat, "新增 0 筆，已更新 0 筆") {
		t.Errorf("a further apply printed:\n%s", repeat)
	}
	e.must("confirm", "--month", "2026-01")
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") {
		t.Errorf("verify printed:\n%s", verify)
	}

	// A void that nothing came after is the user's say: apply tells it and leaves it.
	e.void(l, again[0])
	if closed := e.must("apply"); !strings.Contains(closed, "這個版本已作廢 1 筆") || strings.Contains(closed, "已重新記錄") {
		t.Errorf("apply after the user gave the unit up printed:\n%s", closed)
	}
	if n := len(e.ofRow(l, paid)); n != 3 {
		t.Errorf("the ledger has %d transactions of the row, want 3: nothing recorded", n)
	}

}

func TestImportOfAPlanOfInstalmentsTheExportNoLongerHasOnARealDatabase(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()
	e.must("apply")
	e.must("confirm", "--month", "2026-01")
	e.must("confirm", "--month", "2026-02")
	l := e.ledger()
	count := len(e.transactions(l))

	// A payment of the plan is edited, so that the payments no longer add up to what was
	// converted: the plan the ledger has is not in the export any more, and the interest
	// rows that are left must not be recorded beside it.
	e.editExport(amountOf("2026/03/15", "23:59", "Fake 信用卡分期", "-200", "-190"))
	out, err := e.moze("apply")
	if err == nil || !strings.Contains(out, "已確認的內容與對照不同 3 筆") || !strings.Contains(out, "匯出裡沒有對應的列、但已確認的交易 1 筆") || !strings.Contains(out, "2026-02-05 分期") {
		t.Fatalf("apply after the edit: err = %v, printed:\n%s", err, out)
	}
	if got := len(e.transactions(l)); got != count {
		t.Errorf("the ledger has %d transactions, want %d: nothing is recorded beside the plan", got, count)
	}
	key := regexp.MustCompile(`鍵：(moze:plan-\S+)`).FindStringSubmatch(out)
	if key == nil {
		t.Fatalf("apply names no plan:\n%s", out)
	}
	if out, err := e.moze("confirm", "--month", "2026-03"); err == nil || !strings.Contains(out, "已確認的內容與對照不同") {
		t.Errorf("confirm: err = %v, printed:\n%s", err, out)
	}

	// The user voids the plan, which voids it as a whole, and the interest is recorded as
	// the expenses the export has now.
	e.void(l, key[1])
	out, err = e.moze("apply")
	if err != nil || !strings.Contains(out, "草稿：新增 3 筆") {
		t.Fatalf("apply after the void: err = %v, printed:\n%s", err, out)
	}
	e.must("confirm", "--month", "2026-02")
	e.must("confirm", "--month", "2026-03")
	if verify := e.must("verify"); !strings.Contains(verify, "沒有無法解釋的差異") {
		t.Errorf("verify printed:\n%s", verify)
	}
}

func TestImportSaysThatAnActivePeriodIsIgnoredInEveryCommandAndLeavesTheFileAlone(t *testing.T) {
	e := newImportEnv(t)
	e.must("plan", "--out", filepath.Join(e.dir, "report.md"))
	e.review()

	// The first version of plan wrote the period of a project on the line of its entry.
	path := filepath.Join(e.dir, "mapping.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trip := "  2026-02 Fake Trip: {kind: project, name: Fake Trip}"
	if !strings.Contains(string(data), trip) {
		t.Fatalf("the mapping has no entry of the trip:\n%s", data)
	}
	withPeriod := strings.Replace(string(data), trip, "  2026-02 Fake Trip: {kind: project, name: Fake Trip, active_period: {from: 2026-02-18, to: 2026-02-20}}", 1)
	if err := os.WriteFile(path, []byte(withPeriod), 0o600); err != nil {
		t.Fatal(err)
	}
	line := 1 + strings.Count(withPeriod[:strings.Index(withPeriod, "active_period")], "\n")

	warning := "`active_period` 已不再使用，已忽略（專案不設定進行期間）"
	for _, command := range [][]string{
		{"plan"},
		{"apply"},
		{"status"},
		{"confirm", "--month", "2026-01"},
		{"verify"},
	} {
		out, err := e.moze(command...)
		if err != nil || !strings.Contains(out, warning) || !strings.Contains(out, "第 "+strconv.Itoa(line)+" 行") {
			t.Errorf("%s: err = %v, want it to say the period is ignored, at line %d:\n%s", command[0], err, line, out)
		}
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != withPeriod {
		t.Errorf("the mapping was rewritten: %v\n%s", err, after)
	}

	// The project has no period in the ledger, as the import never sets one.
	projects, err := e.ledger().ListProjects.ListProjects(context.Background(), catalog.ListProjectsInput{})
	if err != nil || len(projects.Projects) != 1 || projects.Projects[0].ActivePeriod != nil {
		t.Errorf("projects = %+v, %v, want the trip with no period", projects.Projects, err)
	}

	// With a mark of a guess on the same line, the entry is still a guess nobody looked
	// at: apply does not go on, and the warning is said all the same.
	marked := strings.Replace(withPeriod, "to: 2026-02-20}}", "to: 2026-02-20}}  # guessed", 1)
	if err := os.WriteFile(path, []byte(marked), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := e.moze("apply"); err == nil || !strings.Contains(out, "1 個標了 # guessed") || !strings.Contains(out, warning) {
		t.Errorf("apply with the mark: err = %v, printed:\n%s", err, out)
	}
	if again := e.must("plan"); !strings.Contains(again, warning) || strings.Contains(again, "已建立對照檔") {
		t.Errorf("plan with the mark printed:\n%s", again)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != marked {
		t.Errorf("the mapping was rewritten: %v\n%s", err, after)
	}
}
