package web

import (
	"html"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The overview is a home screen of cards. These are the pieces its tests read it by: a card is
// found by the id of its heading, the accounts are the part of the page between their heading and
// the one of the instalments, and what a figure says is read as a reader reads it.

// cardOf is the markup of the card of the overview whose heading has the id, from the tag that
// names it to the end of the card, and fails the test when the page has none.
func cardOf(t *testing.T, page, id string) string {
	t.Helper()
	_, after, found := strings.Cut(page, `aria-labelledby="`+id+`"`)
	if !found {
		t.Fatalf("the page has no card %q:\n%s", id, page)
	}
	card, _, _ := strings.Cut(after, "</section>")
	return card
}

// hasCard is whether the page has the card whose heading has the id.
func hasCard(page, id string) bool {
	return strings.Contains(page, `aria-labelledby="`+id+`"`)
}

// accountsOf is the part of the page that lists the accounts: the groups by kind and the folded list
// of those that hold nothing.
func accountsOf(page string) string {
	return between(page, "<h2>帳戶</h2>", "<h2>未到期分期</h2>")
}

var groupHeading = regexp.MustCompile(`<h3 class="group-name">([^ <]+) <span class="muted small">(\d+) 個帳戶</span></h3>`)

// groupsOf lists the groups of accounts of the page in order, each as its name and how many accounts
// it lists.
func groupsOf(page string) []string {
	var groups []string
	for _, m := range groupHeading.FindAllStringSubmatch(page, -1) {
		groups = append(groups, m[1]+" "+m[2])
	}
	return groups
}

// groupOf is the markup of the group of accounts of the kind, from its heading to the end of its card,
// and nothing when there is none.
func groupOf(page, kind string) string {
	_, after, found := strings.Cut(page, `<h3 class="group-name">`+kind+" ")
	if !found {
		return ""
	}
	group, _, _ := strings.Cut(after, "</section>")
	return group
}

// paragraph is the text of the first paragraph of the class that the markup has, as a reader reads
// it, and nothing when it has none.
func paragraph(markup, class string) string {
	m := regexp.MustCompile(`(?s)<p class="` + class + `[^"]*">(.*?)</p>`).FindStringSubmatch(markup)
	if m == nil {
		return ""
	}
	return textOf(m[1])
}

var tileMarkup = regexp.MustCompile(`(?s)<li class="tile">(.*?)</li>`)

// tilesOf is the text of each of the figures in a row of tiles, as a reader reads them: what an
// instrument holds, and under it what is confirmed of it.
func tilesOf(markup string) []string {
	var tiles []string
	for _, m := range tileMarkup.FindAllStringSubmatch(markup, -1) {
		tiles = append(tiles, textOf(m[1]))
	}
	return tiles
}

var amountMarkup = regexp.MustCompile(`<span class="amt[^"]*">([^<]*)<span class="unit"> ([^<]*)</span></span>`)

// amountPairsOf is every amount the markup writes, as the number as a reader reads it (the plus sign is a character
// reference in the markup) and the code of its instrument, in order.
func amountPairsOf(markup string) [][2]string {
	var pairs [][2]string
	for _, m := range amountMarkup.FindAllStringSubmatch(markup, -1) {
		pairs = append(pairs, [2]string{html.UnescapeString(m[1]), m[2]})
	}
	return pairs
}

// amountsOf is every amount the markup writes, as "number CODE", in order.
func amountsOf(markup string) []string {
	var amounts []string
	for _, p := range amountPairsOf(markup) {
		amounts = append(amounts, p[0]+" "+p[1])
	}
	return amounts
}

func wantAmounts(t *testing.T, markup string, want ...string) {
	t.Helper()
	if got := amountsOf(markup); !slices.Equal(got, want) {
		t.Errorf("the amounts are %q, want %q:\n%s", got, want, markup)
	}
}

func TestTheOverview(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(cash, twd, "1234567.5"),
		held(checking, twd, "-120"),
		held(checking, jpy, "1500"),
		held(checking, btc, "0.12345678"),
		owing(card, twd, "-11000"),
		held(emptyAcct, twd, "0"),
		held(spareCard, usd, "0"),
		held(oldSaving, twd, "0"),
	}
	f.dealings = []usecase.Dealing{
		{PartyID: alice, InstrumentID: twd, Quantity: dec("300")},
		{PartyID: alice, InstrumentID: usd, Quantity: dec("-12.5")},
		{PartyID: bob, InstrumentID: twd, Quantity: dec("0")},
	}
	next := date(2026, time.December, 5)
	f.plans = []usecase.InstalmentPlanSummary{{
		PlanID: seqID(77), AccountID: card, InstrumentID: twd,
		Principal: dec("12000"), Charged: dec("1000"), Remaining: dec("11000"), NextDueOn: &next,
	}}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 3, OwnTransfers: 1, Amounts: []usecase.DraftAmount{
		{InstrumentID: twd, Outflow: dec("1500"), Inflow: dec("200"), Net: dec("-1300")},
	}}
	page := f.site(t).page("/app/")
	accounts := accountsOf(page)

	t.Run("the accounts by kind, each instrument on its own line", func(t *testing.T) {
		if got, want := groupsOf(page), []string{"銀行戶頭 1", "信用卡 1", "現金 1"}; !slices.Equal(got, want) {
			t.Errorf("the groups are %q, want %q: the kinds in their order, and only those that hold something", got, want)
		}
		wantIn(t, accounts,
			// Thousands, the places of the instrument, and the sign of what is owed.
			amount("1,234,567.50", "TWD"), amount("−120.00", "TWD"), amount("1,500", "JPY"), amount("0.12345678", "BTC"),
			`class="amt neg">−120.00`,
		)
		// The institution is under the name of an account that has one, and an account that has
		// none is not given one.
		wantIn(t, balanceRow(page, checking), `<span class="line muted small">好銀行</span>`)
		wantNotIn(t, balanceRow(page, cash), `muted small`)
	})
	t.Run("the instalments that are not due, per account and as plans", func(t *testing.T) {
		wantIn(t, page,
			`未到期分期　</span><span class="amt neg">−11,000.00`,
			`<th scope="col" role="columnheader" class="num">已到期的本金</th>`,
			`<th scope="col" role="columnheader" class="num">未到期分期</th>`,
			`<a href="/app/transactions/`+seqID(77).String()+`">信用卡</a>`,
			amount("12,000.00", "TWD"), amount("1,000.00", "TWD"), amount("11,000.00", "TWD"), "2026-12-05",
		)
		wantNotIn(t, page, "已入帳的本金")
	})
	t.Run("accounts that hold nothing are folded, and archived ones left out", func(t *testing.T) {
		wantIn(t, page, `<summary>餘額皆為零的帳戶（2）</summary>`, ">空帳戶</a>", ">備用卡</a>")
		wantNotIn(t, page, "舊存摺")
	})
	t.Run("what is owed is said in words", func(t *testing.T) {
		dealings := cardOf(t, page, "dealings-title")
		wantIn(t, dealings, `<span class="row-name">Alice</span>`, amount("300.00", "TWD"), amount("−12.50", "USD"), "對方欠我", "我欠對方",
			`<summary>往來金額為零的對象（1）</summary>`, "<li>Bob</li>")
	})
	t.Run("no net worth", func(t *testing.T) {
		wantNotIn(t, page, "淨資產")
	})
}

// A ledger with no balance at all, and no draft, spent nothing and owes nothing: each card is there
// and says so plainly, without a figure of nothing; the cards that are of something there is not (the
// dealings, the cards to be paid) are not there.
func TestTheOverviewWhenThereIsNothing(t *testing.T) {
	f := newFake()
	page := f.site(t).page("/app/")

	wantIn(t, cardOf(t, page, "hero-title"), "還沒有已確認的餘額")
	wantNotIn(t, cardOf(t, page, "hero-title"), `class="hero-figure"`, "預覽")
	wantIn(t, cardOf(t, page, "spending-title"), "這個月還沒有已確認的支出")
	wantIn(t, cardOf(t, page, "drafts-title"), "沒有等著確認的草稿")
	wantIn(t, cardOf(t, page, "trend-title"), "還沒有已確認的支出")
	wantIn(t, cardOf(t, page, "top-title"), "這個月還沒有已確認的支出")
	// No chart of twelve columns that are all nothing, and no bars of nothing.
	wantNotIn(t, page, "<svg")
	for _, absent := range []string{"dealings-title", "payments-title"} {
		if hasCard(page, absent) {
			t.Errorf("the page has the card %q, which is of nothing there is", absent)
		}
	}
	// The accounts in use that hold nothing are only named, in a folded list, and there is no group.
	wantIn(t, page, "<summary>餘額皆為零的帳戶（5）</summary>")
	wantNotIn(t, page, `class="group-name"`, "舊存摺", "未到期分期")
}

func TestAnArchivedAccountThatHoldsSomethingIsShown(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{held(oldSaving, twd, "5")}
	page := f.site(t).page("/app/")
	wantIn(t, page, "舊存摺", `<span class="badge">已封存</span>`, amount("5.00", "TWD"))
}

// The accounts are listed under the kind they are, in the order that a person goes through them (what is
// in a bank, what is owed on a card, what is in the pocket, what is invested, what is held elsewhere), and
// a kind that is not known is last: the page does not drop what it has no word for.
func TestTheAccountsAreGroupedByKindInTheOrderOfTheKinds(t *testing.T) {
	f := newFake()
	kinds := []string{"wallet", "exchange", "brokerage", "cash", "card", "bank", "loan", "future"}
	f.accounts, f.balances = nil, nil
	for i, kind := range kinds {
		id := seqID(300 + i)
		f.accounts = append(f.accounts, usecase.Account{ID: id, Name: "帳戶 " + kind, Kind: kind})
		f.balances = append(f.balances, held(id, twd, "10"))
	}
	// A second account of a kind is listed in its group, which says how many it lists.
	second := seqID(399)
	f.accounts = append(f.accounts, usecase.Account{ID: second, Name: "帳戶 bank 2", Kind: "bank"})
	f.balances = append(f.balances, held(second, twd, "20"))

	page := f.site(t).page("/app/")

	want := []string{"銀行戶頭 2", "信用卡 1", "現金 1", "投資帳戶 1", "交易所 1", "錢包 1", "貸款 1", "future 1"}
	if got := groupsOf(page); !slices.Equal(got, want) {
		t.Errorf("the groups are %q, want %q", got, want)
	}
}

// What an instrument of a kind of account adds up to is the use case's to say, and is only written, for each
// instrument and never across them, in the head of a group that lists more than one account: a group of one
// account is that account, and says it once.
func TestTheHeadOfAGroupSaysWhatItsAccountsAddUpToAnInstrumentAtATime(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(checking, twd, "1000"),
		held(checking, usd, "10"),
		held(emptyAcct, twd, "500"),
		held(emptyAcct, jpy, "3000"),
		held(cash, twd, "75"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	page := f.site(t).page("/app/")

	bank := groupOf(page, "銀行戶頭")
	head := between(bank, "<ul", "</ul>")
	// Each instrument has a figure of its own, by code, and none is the sum of two.
	if got, want := tilesOf(head), []string{"3,000 JPY", "1,500.00 TWD", "10.00 USD"}; !slices.Equal(got, want) {
		t.Errorf("the head of the group of banks says %q, want %q", got, want)
	}
	// The group of the one account that holds something has no figure of its own in its head.
	wantNotIn(t, groupOf(page, "現金"), `<ul class="tiles"`)
}

// The accounts of a kind that hold nothing between them add up to nothing, which a head does not say.
func TestTheHeadOfAGroupSaysNothingOfAnInstrumentThatAddsUpToNothing(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(checking, twd, "1000"),
		held(emptyAcct, twd, "-1000"),
		held(checking, usd, "5"),
		held(emptyAcct, usd, "-5"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	page := f.site(t).page("/app/")

	wantNotIn(t, groupOf(page, "銀行戶頭"), `<ul class="tiles"`)
	// The rows say what each holds.
	wantIn(t, balanceRow(page, checking), amount("1,000.00", "TWD"))
	wantIn(t, balanceRow(page, emptyAcct), `class="amt neg">−1,000.00`)
}

// A group's head is written by the rule of its rows: red only where something that is confirmed is lost. The head of
// a group of cards that owe is red when something is confirmed of what they owe, and is not where drafts alone make it.
func TestTheHeadOfAGroupFollowsTheRuleOfTheRedOfItsRows(t *testing.T) {
	f := newFake()
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2}
	// Both cards owe because of drafts alone: nothing that was held is lost.
	f.balances = []usecase.PreviewBalance{
		{AccountID: card, InstrumentID: twd, Held: figure("0", "-900"), Unbilled: figure("0", "0")},
		{AccountID: spareCard, InstrumentID: twd, Held: figure("0", "-100"), Unbilled: figure("0", "0")},
	}
	page := f.site(t).page("/app/")
	wantNotIn(t, groupOf(page, "信用卡"), `class="amt neg"`)
	if got, want := tilesOf(between(groupOf(page, "信用卡"), "<ul", "</ul>")), []string{"−1,000.00 TWD 尚未確認"}; !slices.Equal(got, want) {
		t.Errorf("the head of the group of cards says %q, want %q", got, want)
	}

	// One of them owes of what is confirmed, and the head is of both.
	f.balances = []usecase.PreviewBalance{
		{AccountID: card, InstrumentID: twd, Held: figure("-500", "-900"), Unbilled: figure("0", "0")},
		{AccountID: spareCard, InstrumentID: twd, Held: figure("0", "-100"), Unbilled: figure("0", "0")},
	}
	page = f.site(t).page("/app/")
	head := between(groupOf(page, "信用卡"), "<ul", "</ul>")
	wantIn(t, head, `<span class="amt neg">−1,500.00<span class="unit"> TWD</span></span>`)
	if got, want := tilesOf(head), []string{"−1,500.00 TWD 已確認 −500.00 TWD"}; !slices.Equal(got, want) {
		t.Errorf("the head of the group of cards says %q, want %q", got, want)
	}
}
