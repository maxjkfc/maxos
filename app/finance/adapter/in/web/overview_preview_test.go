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
// beside it. The figures are the use case's (GetPreviewBalances): what these tests show is what
// the page makes of them, and that the page and the table of the drafts under it say the same.

// balanceRow is the row of the balances that is of the account, from its start tag to its end tag,
// and nothing when the overview has no such row: an account that is only named, in the list of
// those that hold nothing, has none.
func balanceRow(page string, account uuid.UUID) string {
	at := strings.Index(page, `<a href="`+accountLink(account)+`">`)
	if at < 0 {
		return ""
	}
	start := strings.LastIndex(page[:at], "<tr>")
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
// change: of each balance that has a confirmed part beside it, the amount less the confirmed part,
// which is the whole of it when none is confirmed, as a reader works it out.
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
		{AccountID: cash, InstrumentID: twd, Quantity: dec("680"), Confirmed: dec("800"), Drafts: dec("-120")},
		// The checking account holds nothing that is confirmed: all of what it holds is the drafts'.
		// Its dollars are confirmed, and the drafts take them all; no draft moves its yen.
		{AccountID: checking, InstrumentID: twd, Quantity: dec("49000"), Confirmed: dec("0"), Drafts: dec("49000")},
		{AccountID: checking, InstrumentID: usd, Quantity: dec("0"), Confirmed: dec("10"), Drafts: dec("-10")},
		held(checking, jpy, "1500"),
		// The card owes 500 and the drafts make it 2,500; 11,000 of instalments are not yet due,
		// which only confirmed plans have.
		{AccountID: card, InstrumentID: twd, Quantity: dec("-2500"), Confirmed: dec("-500"), Drafts: dec("-2000"), UnbilledInstalment: dec("-11000")},
		// A card that nothing moves, and an account that only drafts hold anything in, which is
		// archived.
		held(spareCard, usd, "0"),
		{AccountID: oldSaving, InstrumentID: twd, Quantity: dec("5"), Confirmed: dec("0"), Drafts: dec("5")},
	}
	// What the drafts change, as the table of them says it: the net of the lunch, the money into the
	// checking account, the card and the old savings, and of the dollars.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 12, Amounts: []usecase.DraftAmount{
		{InstrumentID: twd, Outflow: dec("2120"), Inflow: dec("49005"), Net: dec("46885")},
		{InstrumentID: usd, Outflow: dec("10"), Net: dec("-10")},
	}}
	page := f.site(t).page("/app/")
	balances := between(page, "<h2>餘額", "<h2>草稿")

	t.Run("it says it is a preview, of how many drafts, and that the actual balance replaces it", func(t *testing.T) {
		wantIn(t, page, `<h2>餘額 <span class="pill pill-draft">預覽（含 12 筆草稿）</span></h2>`)
		wantIn(t, balances, "草稿確認之後，這裡會換成實際的餘額。", `<a href="/app/drafts">查看草稿</a>`)
		if strings.Index(page, "預覽（含") > strings.Index(page, "<table") {
			t.Error("the preview is not said before the first of the tables")
		}
	})
	t.Run("a balance is what the accounts would hold, with what is confirmed of it beside it", func(t *testing.T) {
		wantCell(t, balanceRow(page, cash), "num", "680.00 TWD 已確認 800.00 TWD")
		// All of it is the drafts' for the checking account: none of it is confirmed, which is said
		// in words, and a balance that no draft changes has nothing said of it. A balance that the
		// drafts take all away is nothing, and what is confirmed is what it was.
		wantCell(t, balanceRow(page, checking), "num", "49,000.00 TWD 尚未確認 0.00 USD 已確認 10.00 USD 1,500 JPY")
		// What is not yet due is of confirmed plans, and has no confirmed part to show.
		wantCell(t, balanceRow(page, card), "num", "−2,500.00 TWD 已確認 −500.00 TWD 未到期分期 −11,000.00 TWD")
		old := balanceRow(page, oldSaving)
		wantCell(t, old, "num", "5.00 TWD 尚未確認")
		wantIn(t, old, `<span class="badge">已封存</span>`)
	})
	t.Run("what is confirmed is read beside the balance, and is not the colour of a loss", func(t *testing.T) {
		wantIn(t, balances,
			`<span class="amt neg">−2,500.00<span class="unit"> TWD</span></span>`,
			`已確認 <span class="amt">−500.00<span class="unit"> TWD</span></span>`,
		)
		wantNotIn(t, balances, `已確認 <span class="amt neg">`)
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
		for _, unit := range []string{"TWD", "USD"} {
			if want := table["淨額 "+unit]; !got[unit].Equal(want) {
				t.Errorf("%s: the balances say the drafts change %s, and the table of the drafts says %s", unit, got[unit], want)
			}
		}
		// Only what the drafts change has a confirmed part beside it: the yen, which none moves, are
		// not among the instruments that they change.
		if _, found := got["JPY"]; found || len(got) != 2 {
			t.Errorf("the balances say the drafts change %v, want TWD and USD only", got)
		}
	})
}

func TestAnAccountWhoseBalanceTheDraftsMakeNothingIsFoldedWithTheAccountsThatHoldNothing(t *testing.T) {
	f := newFake()
	// The empty account holds 100, and drafts take all of it: it would hold nothing.
	f.balances = []usecase.PreviewBalance{
		{AccountID: emptyAcct, InstrumentID: twd, Quantity: dec("0"), Confirmed: dec("100"), Drafts: dec("-100")},
		held(cash, twd, "30"),
	}
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1}
	page := f.site(t).page("/app/")

	wantIn(t, page, "<summary>餘額皆為零的帳戶（4）</summary>")
	if got := balanceRow(page, emptyAcct); got != "" {
		t.Errorf("the account that holds nothing once the drafts are confirmed has a row of balances:\n%s", got)
	}
	// What no draft changes has nothing beside it, in a ledger that has drafts too.
	wantCell(t, balanceRow(page, cash), "num", "30.00 TWD")
}

func TestTheOverviewOfLedgerWithNoDraftIsTheBalancesAndSaysNothingOfAPreview(t *testing.T) {
	f := newFake()
	f.balances = []usecase.PreviewBalance{
		held(cash, twd, "880"),
		{AccountID: card, InstrumentID: twd, Quantity: dec("-500"), Confirmed: dec("-500"), UnbilledInstalment: dec("-11000")},
	}
	page := f.site(t).page("/app/")

	// The heading is the heading, and a balance is its amount and nothing more.
	wantIn(t, page, "<h2>餘額</h2>", "沒有等著確認的草稿。")
	wantCell(t, balanceRow(page, cash), "num", "880.00 TWD")
	wantCell(t, balanceRow(page, card), "num", "−500.00 TWD 未到期分期 −11,000.00 TWD")
	wantNotIn(t, page, "預覽", "已確認", "尚未確認", `class="pill`)
}

func TestTheOverviewWhenDraftsWaitAndNoAccountHoldsAnythingToday(t *testing.T) {
	f := newFake()
	// Drafts that move no balance today: dated later, or of no account.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2}
	page := f.site(t).page("/app/")

	wantIn(t, page, `<span class="pill pill-draft">預覽（含 2 筆草稿）</span>`, "還沒有已確認的餘額")
	wantNotIn(t, between(page, "<h2>餘額", "<h2>草稿"), "<table")
}
