package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
	"github.com/maxjkfc/maxos/app/importer/usecase/port/in/usecase"
)

// planView is what the report is written from: the plan, with its rows by key.
type planView struct {
	plan model.Plan
	rows map[string]model.Row
}

func newPlanView(plan model.Plan) planView {
	return planView{plan: plan, rows: plan.RowIndex()}
}

// rowLabel names a row: when, where, what and how much, and the key to override it by.
func (v planView) rowLabel(key string) []string {
	r := v.rows[key]
	when := r.Date.Format(time.DateOnly)
	if !r.HasDate() {
		when = "（無日期）"
	}
	if r.Clock != "" {
		when += " " + r.Clock
	}
	return []string{
		"`" + key + "`", when, r.Account, string(r.Type),
		r.Currency + " " + money(r.Amount, v.plan.Decimals(r.Currency)),
		clip(r.Name, 24),
	}
}

var rowHeaders = []string{"key", "時間", "帳戶", "類型", "金額", "名稱"}

// effectOf is what the rows with the keys do to the balances, as text.
func (v planView) effectOf(keys ...string) string {
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	sums := map[model.Pair]decimal.Decimal{}
	for _, e := range v.plan.Effects {
		if want[e.Row] {
			k := model.Pair{Account: e.Account, Instrument: e.Instrument}
			sums[k] = sums[k].Add(e.Quantity)
		}
	}
	var parts []string
	for _, p := range model.Balances(sums).Pairs() {
		if sums[p].IsZero() {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s %s %s", p.Account, p.Instrument, signed(sums[p], v.plan.Decimals(p.Instrument))))
	}
	if len(parts) == 0 {
		return "無"
	}
	return strings.Join(parts, "；")
}

// RenderPlanSummary writes the counts of a plan: what it comes to, in a few lines.
func RenderPlanSummary(w io.Writer, out usecase.PlanOutput, reportPath string) error {
	var b strings.Builder
	planSummary(&b, out, reportPath)
	return flush(w, &b)
}

func planSummary(w *strings.Builder, out usecase.PlanOutput, reportPath string) {
	v := newPlanView(out.Plan)
	p := v.plan
	fmt.Fprintf(w, "資料列 %d 筆", len(p.Rows))
	if months := p.Months(); len(months) > 0 {
		fmt.Fprintf(w, "，%s 至 %s", months[0], months[len(months)-1])
	}
	fmt.Fprintln(w)

	if out.MappingCreated {
		fmt.Fprintf(w, "已建立對照檔，共 %d 個項目，都標了 # guessed，請逐項確認\n", out.MappingAdded)
	} else if out.MappingAdded > 0 {
		fmt.Fprintf(w, "對照檔新增 %d 個猜測項目（標了 # guessed）\n", out.MappingAdded)
	}
	if out.Guessed > 0 {
		fmt.Fprintf(w, "對照檔裡仍有 %d 個 # guessed 的項目\n", out.Guessed)
	}
	if !out.RatesFound {
		fmt.Fprintln(w, "找不到匯率檔：外幣的列沒有本位幣金額")
	}

	fmt.Fprintln(w, "\n記錄方式（單位 = 一筆交易，或一個分期計畫）：")
	counts := map[model.UnitKind]int{}
	for _, u := range p.Units {
		counts[u.Kind]++
	}
	for _, k := range kindOrder {
		if counts[k] > 0 {
			fmt.Fprintf(w, "  %-8s %d\n", kindText[k], counts[k])
		}
	}
	fmt.Fprintf(w, "  餘額調整  %d（確認該月時才對帳記錄）\n", len(p.Adjustments))
	zeroOpenings, zeroAdjustments := 0, 0
	for _, n := range p.NoOps {
		if n.Reason == model.NoOpZeroOpening {
			zeroOpenings++
		} else {
			zeroAdjustments++
		}
	}
	fmt.Fprintf(w, "  不需記錄  %d（期初為 0：%d、調整為 0：%d）\n", len(p.NoOps), zeroOpenings, zeroAdjustments)

	reasons, pairing := map[model.Reason]int{}, 0
	for _, s := range p.Skipped {
		reasons[s.Reason] += len(s.Rows)
		if pairingReasons[s.Reason] {
			pairing++
		}
	}
	fmt.Fprintf(w, "\n無法記錄的列 %d 筆（配對問題 %d 組）：\n", countRows(p.Skipped), pairing)
	for _, r := range sortedReasons(reasons) {
		fmt.Fprintf(w, "  %-4d %s\n", reasons[r], reasonText[r].Why)
	}

	caveats := map[model.CaveatKind]int{}
	for _, c := range p.Caveats {
		caveats[c.Kind]++
	}
	if len(caveats) > 0 {
		fmt.Fprintln(w, "\n有記錄、但要知道的列：")
		for _, k := range sortedCaveats(caveats) {
			fmt.Fprintf(w, "  %-4d %s\n", caveats[k], caveatText[k].Title)
		}
	}

	if len(p.Problems) > 0 {
		fmt.Fprintf(w, "\n要先處理的問題 %d 項：\n", len(p.Problems))
		for _, pr := range p.Problems {
			fmt.Fprintf(w, "  %s\n", pr)
		}
	}
	for _, warning := range p.Warnings {
		fmt.Fprintf(w, "注意：%s\n", warning)
	}
	if reportPath != "" {
		fmt.Fprintf(w, "\n完整報告：%s\n", reportPath)
	}
}

func countRows(skipped []model.Skipped) int {
	n := 0
	for _, s := range skipped {
		n += len(s.Rows)
	}
	return n
}

func sortedReasons(m map[model.Reason]int) []model.Reason {
	out := make([]model.Reason, 0, len(m))
	for r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func sortedCaveats(m map[model.CaveatKind]int) []model.CaveatKind {
	out := make([]model.CaveatKind, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// RenderPlan writes the whole report of a plan, in Markdown: the counts, what is not
// recorded and why, what has been guessed, what to know of the rows that are
// recorded, and what the accounts hold at the end of each month by the export alone.
func RenderPlan(w io.Writer, out usecase.PlanOutput) error {
	var b strings.Builder
	planReport(&b, out)
	return flush(w, &b)
}

func planReport(w *strings.Builder, out usecase.PlanOutput) {
	v := newPlanView(out.Plan)
	fmt.Fprintln(w, "# MOZE 匯入計畫")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "```")
	planSummary(w, out, "")
	fmt.Fprintln(w, "```")
	fmt.Fprintln(w)

	v.renderBlocking(w)
	v.renderSkipped(w)
	v.renderCaveats(w)
	v.renderProjects(w)
	v.renderExpected(w)
}

// renderBlocking lists what stands in the way of recording: what has to be mapped and
// what is wrong.
func (v planView) renderBlocking(w *strings.Builder) {
	g := v.plan.Guessed
	if g.Empty() && len(v.plan.Problems) == 0 {
		return
	}
	fmt.Fprintln(w, "## 先處理，才能 apply")
	fmt.Fprintln(w)
	if !g.Empty() {
		fmt.Fprintln(w, "對照檔缺少下列項目。它們已加進對照檔（標了 `# guessed`），請逐項確認、改好，再執行一次 plan。")
		fmt.Fprintln(w)
		counts := []struct {
			name string
			n    int
		}{
			{"標的", len(g.Instruments)}, {"帳戶", len(g.Accounts)}, {"專案值", len(g.Projects)},
			{"支出類別", len(g.Expense)}, {"收入類別", len(g.Income)}, {"對象", len(g.Parties)}, {"標籤", len(g.Tags)},
		}
		for _, c := range counts {
			if c.n > 0 {
				fmt.Fprintf(w, "- %s：%d 個\n", c.name, c.n)
			}
		}
		fmt.Fprintln(w)
	}
	if len(v.plan.Problems) > 0 {
		fmt.Fprintln(w, "有問題的項目：")
		fmt.Fprintln(w)
		for _, p := range v.plan.Problems {
			fmt.Fprintf(w, "- %s\n", p)
		}
		fmt.Fprintln(w)
	}
}

// renderSkipped lists the rows that are not recorded, by reason, with what each does
// to the balance of its account and what to do about it.
func (v planView) renderSkipped(w *strings.Builder) {
	if len(v.plan.Skipped) == 0 {
		return
	}
	fmt.Fprintln(w, "## 無法記錄的列")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "這些列不會匯入，也不會被默默丟掉：每一組都列出原因、對帳戶餘額的影響（帳本會比 CSV 少掉這些）與建議的處理。`verify` 把這個影響視為已說明的差異。")
	fmt.Fprintln(w)

	byReason := map[model.Reason][]model.Skipped{}
	for _, s := range v.plan.Skipped {
		byReason[s.Reason] = append(byReason[s.Reason], s)
	}
	for _, reason := range sortedReasons(countByReason(v.plan.Skipped)) {
		groups := byReason[reason]
		title := reasonText[reason].Why
		if pairingReasons[reason] {
			title = "配對問題：" + title
		}
		fmt.Fprintf(w, "### %s（%d 組，%d 列）\n\n建議：%s\n\n", title, len(groups), countRows(groups), reasonText[reason].Proposal)
		var rows [][]string
		for _, g := range groups {
			for i, key := range g.Rows {
				label := v.rowLabel(key)
				effect, detail := "", ""
				if i == 0 {
					effect = v.effectOf(g.Rows...)
					detail = g.Detail
				}
				rows = append(rows, append(label, effect, detail))
			}
		}
		table(w, append(rowHeaders, "這組對餘額的影響", "說明"), rows)
	}
}

func countByReason(skipped []model.Skipped) map[model.Reason]int {
	out := map[model.Reason]int{}
	for _, s := range skipped {
		out[s.Reason] += len(s.Rows)
	}
	return out
}

// renderCaveats lists the rows that are recorded and need a word: the 收款 that are
// to be checked one by one, the investments, and the rest.
func (v planView) renderCaveats(w *strings.Builder) {
	if len(v.plan.Caveats) == 0 {
		return
	}
	by := map[model.CaveatKind][]model.Caveat{}
	counts := map[model.CaveatKind]int{}
	for _, c := range v.plan.Caveats {
		by[c.Kind] = append(by[c.Kind], c)
		counts[c.Kind]++
	}
	fmt.Fprintln(w, "## 有記錄、但要知道的列")
	fmt.Fprintln(w)
	for _, k := range sortedCaveats(counts) {
		fmt.Fprintf(w, "### %s（%d 列）\n\n%s\n\n", caveatText[k].Title, counts[k], caveatText[k].Note)
		var rows [][]string
		for _, c := range by[k] {
			rows = append(rows, append(v.rowLabel(c.Row), v.effectOf(c.Row), c.Detail))
		}
		table(w, append(rowHeaders, "對餘額的影響", "說明"), rows)
	}
}

// renderProjects proposes what the values of the 專案 column are, with what is in the
// rows that have them, for the ones the user has not decided: every one that was
// guessed.
func (v planView) renderProjects(w *strings.Builder) {
	g := v.plan.Guessed.Projects
	if len(g) == 0 {
		return
	}
	type tally struct {
		rows        int
		first, last time.Time
	}
	tallies := map[string]*tally{}
	for _, r := range v.plan.Rows {
		if r.Project == "" || !r.HasDate() {
			continue
		}
		t := tallies[r.Project]
		if t == nil {
			t = &tally{first: r.Date, last: r.Date}
			tallies[r.Project] = t
		}
		t.rows++
		if r.Date.Before(t.first) {
			t.first = r.Date
		}
		if r.Date.After(t.last) {
			t.last = r.Date
		}
	}
	empty := 0
	for _, r := range v.plan.Rows {
		if r.Project == "" && (r.Type == model.TypeExpense || r.Type == model.TypeIncome || r.Type == model.TypeBonus) {
			empty++
		}
	}

	fmt.Fprintln(w, "## 專案值的建議")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "專案欄的每個值要對到領域、專案、標籤、往來的對象，或什麼都不是。下面是依名稱與列的日期猜的建議（已寫進對照檔，標了 `# guessed`）：")
	fmt.Fprintln(w)
	var rows [][]string
	values := make([]string, 0, len(g))
	for value := range g {
		values = append(values, value)
	}
	sort.Strings(values)
	kindName := map[string]string{
		model.ProjectArea: "領域", model.ProjectProject: "專案", model.ProjectTag: "標籤",
		model.ProjectDealings: "往來（代付）", model.ProjectNone: "不歸類",
	}
	for _, value := range values {
		p := g[value]
		t := tallies[value]
		rowCount, span := 0, ""
		if t != nil {
			rowCount = t.rows
			span = t.first.Format(time.DateOnly) + " ～ " + t.last.Format(time.DateOnly)
		}
		extra := p.Name
		switch {
		case p.Kind == model.ProjectDealings:
			extra = "對象 " + p.Party
		case p.Kind == model.ProjectProject && !p.ActiveFrom.IsZero():
			extra += "；進行期間 " + p.ActiveFrom.Format(time.DateOnly) + " ～ " + p.ActiveTo.Format(time.DateOnly)
		case p.Kind == model.ProjectProject && p.DefaultArea != "":
			extra += "；預設領域 " + p.DefaultArea
		}
		if p.Kind == model.ProjectProject && p.DefaultArea != "" && !p.ActiveFrom.IsZero() {
			extra += "；預設領域 " + p.DefaultArea
		}
		rows = append(rows, []string{value, fmt.Sprint(rowCount), span, kindName[p.Kind], extra})
	}
	rows = append(rows, []string{"（空）", fmt.Sprint(empty), "", "不歸類", "沒有專案的列不帶領域；交易日期落在專案的進行期間時，帳本會自動歸入該專案"})
	table(w, []string{"專案值", "列數", "日期範圍", "建議", "說明"}, rows)
}

// renderExpected writes what each account holds of each instrument at the end of each
// month, by the export alone.
func (v planView) renderExpected(w *strings.Builder) {
	months := v.plan.Months()
	if len(months) == 0 {
		return
	}
	fmt.Fprintln(w, "## 每月底的預期餘額（只由 CSV 算出）")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "含餘額調整的金額，期初餘額從它的日期起算；確認該月之後，`confirm` 與 `verify` 拿帳本的餘額與這裡比對。")
	fmt.Fprintln(w)
	for _, month := range months {
		end, err := model.MonthEnd(month)
		if err != nil {
			continue
		}
		expected := v.plan.ExpectedAt(end)
		var rows [][]string
		for _, p := range expected.Pairs() {
			rows = append(rows, []string{p.Account, p.Instrument, money(expected[p], v.plan.Decimals(p.Instrument))})
		}
		fmt.Fprintf(w, "### %s（%s）\n\n", month, end.Format(time.DateOnly))
		table(w, []string{"帳戶", "標的", "餘額"}, rows)
	}
}
