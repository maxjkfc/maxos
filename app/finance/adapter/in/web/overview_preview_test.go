package web

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// The overview of a ledger that has drafts waiting: the balances are what the accounts would hold
// with the drafts confirmed, which is a preview and says so, with what is confirmed of each
// beside it. The figures, what each is made of and how many drafts wait are the use case's
// (GetPreviewBalances): what these tests show is what the page makes of them, and that the page and
// the table of the drafts under it say the same.

// balanceRow is the row of the balances that is of the account, from its start tag to its end tag,
// and nothing when the overview has no such row: an account that is only named, in the list of
// those that hold nothing, has none.
func balanceRow(page string, account uuid.UUID) string {
	at := strings.Index(page, `<a href="`+accountLink(account)+`">`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(page[:at], "<tr ")
	end := strings.Index(page[at:], "</tr>")
	if start < 0 || end < 0 || strings.Contains(page[start:at], "</tr>") {
		return ""
	}
	return page[start : at+end+len("</tr>")]
}

// balanceLineMarkup is a line of a cell of the balances, as the markup has it: a balance or what is
// not yet due, and when drafts make up some of it what is confirmed of it beside it: a figure, or
// the words that none of it is.
var balanceLineMarkup = regexp.MustCompile(
	`<span class="line">(<span class="muted small">[^<]*</span>)?` + // the label of what is not a balance
		`<span class="amt[^"]*">([^<]*)<span class="unit"> ([^<]*)</span></span></span>` + // the amount
		`(?:<span class="line muted small">(?:已確認 <span class="amt[^"]*">([^<]*)<span class="unit"> [^<]*</span></span>|(尚未確認))</span>)?`) // what is confirmed of it

// numberOf reads an amount as the page writes it.
func numberOf(t *testing.T, text string) decimal.Decimal {
	t.Helper()
	n, err := decimal.NewFromString(strings.NewReplacer("−", "-", ",", "").Replace(text))
	if err != nil {
		t.Fatalf("the amount %q is not a number: %v", text, err)
	}
	return n
}

// draftsOfTheBalances adds up, for each instrument, what the balances of the markup say the drafts
// change in what the accounts hold: of each balance that has a confirmed part beside it, the amount
// less the confirmed part, which is the whole of it when none is confirmed, as a reader works it
// out. What is not yet due is not a balance, it has a label, and is not in it: the table of the
// drafts does not count the principal of instalment plans, which the page says.
func draftsOfTheBalances(t *testing.T, markup string) map[string]decimal.Decimal {
	t.Helper()
	sums := map[string]decimal.Decimal{}
	for _, line := range balanceLineMarkup.FindAllStringSubmatch(markup, -1) {
		label, total, unit, confirmed, none := line[1], line[2], line[3], line[4], line[5]
		if label != "" || (confirmed == "" && none == "") {
			continue // not a balance, or a balance that no draft changes
		}
		drafts := numberOf(t, total)
		if confirmed != "" {
			drafts = drafts.Sub(numberOf(t, confirmed))
		}
		sums[unit] = sums[unit].Add(drafts)
	}
	return sums
}

func TestTheOverviewOfLedgerWithDraftsShowsWhatTheBalancesWouldBeAndWhatIsConfirmedOfThem(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		// The cash holds 800 and a lunch of 120 waits: a balance that both make.
		{AccountID: cash, InstrumentID: twd, Held: figure("800", "-120"), Unbilled: figure("0", "0")},
		// No draft moves the bitcoin of the checking account. The drafts take 3,000 of yen from it,
		// and no yen is confirmed: it owes them, as far as the drafts go, and loses none that it
		// held. All of the TWD it holds is the drafts'. Its dollars are confirmed, and the drafts
		// take them all.
		held(checking, btc, "0.12345678"),
		{AccountID: checking, InstrumentID: jpy, Held: figure("0", "-3000"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: twd, Held: figure("0", "49000"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: usd, Held: figure("10", "-10"), Unbilled: figure("0", "0")},
		// The card owes 500 and the drafts make it 2,500; 11,000 of instalments are not yet due,
		// and a plan that is a draft makes it 17,000.
		{AccountID: card, InstrumentID: twd, Held: figure("-500", "-2000"), Unbilled: figure("-11000", "-6000")},
		// A card that nothing moves, and an account that only drafts hold anything in, which is
		// archived.
		held(spareCard, usd, "0"),
		{AccountID: oldSaving, InstrumentID: twd, Held: figure("0", "5"), Unbilled: figure("0", "0")},
	}
	// What the drafts change in what the accounts hold, as the table of them says it: the lunch, the
	// money into the checking account, the card and the old savings, the dollars and the yen.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 12, Amounts: []usecase.DraftAmount{
		{InstrumentID: twd, Outflow: dec("2120"), Inflow: dec("49005"), Net: dec("46885")},
		{InstrumentID: usd, Outflow: dec("10"), Net: dec("-10")},
		{InstrumentID: jpy, Outflow: dec("3000"), Net: dec("-3000")},
	}}
	page := f.site(t).page("/app/")
	balances := between(page, "<h2>餘額", "<h2>草稿")

	t.Run("it says it is a preview, in the heading and over the amounts, and of how many drafts", func(t *testing.T) {
		wantIn(t, page, `<h2>餘額 <span class="pill pill-draft">預覽（含 12 筆草稿）</span></h2>`)
		wantIn(t, balances, `href="/app/drafts">查看草稿</a>`, `<th scope="col" role="columnheader" class="num">餘額（預覽）</th>`)
		if strings.Index(page, "預覽（含") > strings.Index(page, "<table") {
			t.Error("the preview is not said before the first of the tables")
		}
		// Some of it is confirmed, which is said of the balances as a whole, and is not that they are drafts'.
		wantNotIn(t, balances, "全是草稿")
	})
	t.Run("a balance is what the accounts would hold, with what is confirmed of it beside it", func(t *testing.T) {
		wantCell(t, balanceRow(page, cash), "num", "680.00 TWD 已確認 800.00 TWD")
		// None of the yen and of the TWD of the checking account is confirmed, which is said in words,
		// and a balance that no draft changes has nothing said of it. A balance that the drafts take all
		// away is nothing, and what is confirmed is what it was.
		wantCell(t, balanceRow(page, checking), "num", "0.12345678 BTC −3,000 JPY 尚未確認 49,000.00 TWD 尚未確認 0.00 USD 已確認 10.00 USD")
		// What is not yet due is a line of its own, and says what is confirmed of it as a balance does:
		// the plans that are confirmed, and what a plan that is a draft makes of it.
		wantCell(t, balanceRow(page, card), "num", "−2,500.00 TWD 已確認 −500.00 TWD 未到期分期 −17,000.00 TWD 已確認 −11,000.00 TWD")
		old := balanceRow(page, oldSaving)
		wantCell(t, old, "num", "5.00 TWD 尚未確認")
		wantIn(t, old, `<span class="badge">已封存</span>`)
	})
	t.Run("only what is lost of what is confirmed is the colour of a loss", func(t *testing.T) {
		wantIn(t, balances,
			// What the card owes is a loss, with something confirmed that it is a loss of, and what is
			// confirmed beside it is read as a reference and is not.
			`<span class="amt neg">−2,500.00<span class="unit"> TWD</span></span>`,
			`已確認 <span class="amt">−500.00<span class="unit"> TWD</span></span>`,
			`未到期分期　</span><span class="amt neg">−17,000.00`,
			// What drafts alone make is no loss: nothing that was held is lost.
			`<span class="amt">−3,000<span class="unit"> JPY</span></span>`,
		)
		wantNotIn(t, balances, `已確認 <span class="amt neg">`, `<span class="amt neg">−3,000`)
	})
	t.Run("an account that holds nothing, whatever the drafts say, is only named", func(t *testing.T) {
		wantIn(t, balances, "<summary>餘額皆為零的帳戶（2）</summary>", ">備用卡</a>", ">空帳戶</a>")
		if got := balanceRow(page, spareCard); got != "" {
			t.Errorf("the card that nothing moves has a row of balances:\n%s", got)
		}
	})
	// A reader works out what the drafts change from the balances: the amounts less what is
	// confirmed of them. It has to be what the table of the drafts says, whose caption says that
	// it is what they change.
	t.Run("the balances and the table of the drafts say the same of what the drafts change", func(t *testing.T) {
		got := draftsOfTheBalances(t, balances)
		table := draftColumns(t, between(page, "<h2>草稿", "</main>"))
		if len(got) == 0 || len(table) == 0 {
			t.Fatalf("nothing to compare: %v from the balances, %v from the table", got, table)
		}
		for _, unit := range []string{"TWD", "USD", "JPY"} {
			if want := table["淨額 "+unit]; !got[unit].Equal(want) {
				t.Errorf("%s: the balances say the drafts change %s, and the table of the drafts says %s", unit, got[unit], want)
			}
		}
		// Only what the drafts change has a confirmed part beside it, or is said to be none: the
		// bitcoin, which none moves, are not among the instruments that they change.
		if _, found := got["BTC"]; found || len(got) != 3 {
			t.Errorf("the balances say the drafts change %v, want TWD, USD and JPY only", got)
		}
		// The principal of a plan is not in the table, which is of what an account holds. The page
		// says where it is, which is the line of what is not yet due.
		wantIn(t, between(page, "<h2>草稿", "</main>"), "未到期分期")
	})
	t.Run("the table of the balances keeps its roles for the phone, where it is not a table", func(t *testing.T) {
		wantIn(t, balances, `<table class="balances" role="table">`, `<tr role="row">`, `<th scope="row" role="rowheader">`, `<td role="cell" class="num">`)
	})
	// A line of a note breaks anywhere in Chinese, and a link to the drafts that is cut into 查看 and 草稿,
	// or a term that is cut in the middle, is read as two things: they are kept whole (§8 of the design).
	t.Run("the link to the drafts and the term of what is not yet due are kept whole where a line breaks", func(t *testing.T) {
		wantIn(t, balances, `<a class="nowrap" href="/app/drafts">查看草稿</a>`)
		drafts := between(page, "<h2>草稿", "</main>")
		wantIn(t, drafts, `<a class="nowrap" href="/app/drafts">查看草稿</a>`, `<span class="nowrap">「未到期分期」</span>`)
	})
}

// An account whose drafts make its balance nothing is not an account that holds nothing: it holds
// what is confirmed of it, which the drafts would take. It is listed with what is confirmed, and is
// not folded into the accounts that have nothing to say, where what is confirmed would be gone.
func TestAnAccountWhoseBalanceTheDraftsMakeNothingIsListedWithWhatIsConfirmedOfIt(t *testing.T) {
	f := newFake()
	// The empty account holds 100, and drafts take all of it: it would hold nothing.
	f.balances = []usecase.PreviewBalance{
		{AccountID: emptyAcct, InstrumentID: twd, Held: figure("100", "-100"), Unbilled: figure("0", "0")},
		held(cash, twd, "30"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	page := f.site(t).page("/app/")

	wantCell(t, balanceRow(page, emptyAcct), "num", "0.00 TWD 已確認 100.00 TWD")
	// What no draft changes has nothing beside it, in a ledger that has drafts too.
	wantCell(t, balanceRow(page, cash), "num", "30.00 TWD")
	// The checking account, the card and the spare card have nothing, and are all that is folded.
	wantIn(t, page, "<summary>餘額皆為零的帳戶（3）</summary>")
	wantNotIn(t, between(page, "<summary>", "</details>"), "空帳戶")
}

// The quantity of an account that the drafts make nothing is still a line when it also owes in
// instalments that are not yet due: what is confirmed is not left out for the line of the
// instalments to be the only one.
func TestAnAccountOwingInInstalmentsWhoseQuantityTheDraftsMakeNothingStillSaysWhatIsConfirmed(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		{AccountID: card, InstrumentID: twd, Held: figure("500", "-500"), Unbilled: figure("-11000", "0")},
		// With nothing confirmed and nothing taken, what the card holds has nothing to say, and the
		// line of the instalments is the only one.
		owing(checking, twd, "-3000"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	page := f.site(t).page("/app/")

	wantCell(t, balanceRow(page, card), "num", "0.00 TWD 已確認 500.00 TWD 未到期分期 −11,000.00 TWD")
	wantCell(t, balanceRow(page, checking), "num", "未到期分期 −3,000.00 TWD")
}

// A ledger whose transactions are all drafts has no balance of its own: every number the page shows
// is a draft's, and it says so, once, where the balances are introduced and not as a figure of
// nothing on each of the lines, and nothing is red, for nothing is lost.
func TestTheOverviewOfLedgerOfDraftsOnlySaysThatEveryNumberIsADraftsAndIsRedNowhere(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		{AccountID: cash, InstrumentID: twd, Held: figure("0", "-900"), Unbilled: figure("0", "0")},
		{AccountID: checking, InstrumentID: twd, Held: figure("0", "49000"), Unbilled: figure("0", "0")},
		// A purchase in instalments that is a draft: it owes the principal, and holds nothing.
		{AccountID: card, InstrumentID: twd, Held: figure("0", "-30"), Unbilled: figure("0", "-6000")},
		{AccountID: emptyAcct, InstrumentID: twd, Held: figure("0", "0"), Unbilled: figure("0", "-4000")},
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 4}
	page := f.site(t).page("/app/")
	balances := between(page, "<h2>餘額", "<h2>草稿")

	wantIn(t, page, `<span class="pill pill-draft">預覽（含 4 筆草稿）</span>`)
	// The note says that none of it is confirmed, in place of the one that says what is added to
	// what is confirmed.
	wantIn(t, balances, "全是草稿", `href="/app/drafts">查看草稿</a>`)
	wantNotIn(t, balances, "加上這些草稿今天全部確認後會有的變動")
	wantCell(t, balanceRow(page, cash), "num", "−900.00 TWD 尚未確認")
	wantCell(t, balanceRow(page, checking), "num", "49,000.00 TWD 尚未確認")
	wantCell(t, balanceRow(page, card), "num", "−30.00 TWD 尚未確認 未到期分期 −6,000.00 TWD 尚未確認")
	wantCell(t, balanceRow(page, emptyAcct), "num", "未到期分期 −4,000.00 TWD 尚未確認")
	// A column of reds is a loss, and there is none: every amount is drafts', and none is confirmed.
	wantNotIn(t, balances, `class="amt neg"`, "已確認 <span")

	// The first account whose balance is confirmed: the balances are no longer all drafts', and the
	// note says what is added to what is confirmed. The lines of drafts alone are the same.
	f.balances = append(f.balances, held(spareCard, usd, "100"))
	page = f.site(t).page("/app/")
	balances = between(page, "<h2>餘額", "<h2>草稿")
	wantNotIn(t, balances, "全是草稿")
	wantCell(t, balanceRow(page, cash), "num", "−900.00 TWD 尚未確認")
	wantCell(t, balanceRow(page, spareCard), "num", "100.00 USD")
}

func TestTheOverviewOfLedgerWithNoDraftIsTheBalancesAndSaysNothingOfAPreview(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(cash, twd, "880"),
		{AccountID: card, InstrumentID: twd, Held: figure("-500", "0"), Unbilled: figure("-11000", "0")},
	}
	next := date(2026, 12, 5)
	f.plans = []usecase.InstalmentPlanSummary{{PlanID: seqID(77), AccountID: card, InstrumentID: twd, Principal: dec("12000"), Charged: dec("1000"), Remaining: dec("11000"), NextDueOn: &next}}
	page := f.site(t).page("/app/")

	// The heading is the heading, and a balance is its amount and nothing more.
	wantIn(t, page, "<h2>餘額</h2>", `<th scope="col" role="columnheader" class="num">餘額</th>`, "沒有等著確認的草稿。")
	wantCell(t, balanceRow(page, cash), "num", "880.00 TWD")
	wantCell(t, balanceRow(page, card), "num", "−500.00 TWD 未到期分期 −11,000.00 TWD")
	wantNotIn(t, page, "預覽", "已確認 <span", "尚未確認", `class="pill`, "只列已確認的分期", "全是草稿")
}

func TestTheOverviewWhenDraftsWaitAndNoAccountHoldsAnythingToday(t *testing.T) {
	f := newFake()
	// Drafts that move no balance today: dated later, or of no account.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2}
	page := f.site(t).page("/app/")
	balances := between(page, "<h2>餘額", "<h2>草稿")

	wantIn(t, page, `<span class="pill pill-draft">預覽（含 2 筆草稿）</span>`, "還沒有已確認的餘額")
	// There is no balance to say anything of: no note that introduces them, no table.
	wantNotIn(t, balances, "<table", "全是草稿", "查看草稿")
}
