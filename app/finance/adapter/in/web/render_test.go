package web

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	catalogusecase "github.com/maxjkfc/maxos/app/catalog/usecase/port/in/usecase"
	"github.com/maxjkfc/maxos/app/finance/usecase/port/in/usecase"
)

// amount is how a page writes an amount in the markup, so that a test can look for
// it: the number, then the code of the instrument. The plus sign is the character
// reference that html/template writes for it.
func amount(text, unit string) string {
	text = strings.ReplaceAll(text, "+", "&#43;")
	return fmt.Sprintf(`%s<span class="unit"> %s</span></span>`, text, unit)
}

// part is how the table of the parts of a transaction writes the start of a row: what
// it is, and what it is to.
func part(role, target string) string {
	return `<th scope="row" role="rowheader">` + role + "</th>\n" + `<td role="cell" data-label="帳戶、類別或對象">` + target + `</td>`
}

func TestTheOverview(t *testing.T) {
	f := newFake()
	f.balances = []usecase.Balance{
		{AccountID: cash, InstrumentID: twd, Quantity: dec("1234567.5")},
		{AccountID: checking, InstrumentID: twd, Quantity: dec("-120")},
		{AccountID: checking, InstrumentID: jpy, Quantity: dec("1500")},
		{AccountID: checking, InstrumentID: btc, Quantity: dec("0.12345678")},
		{AccountID: card, InstrumentID: twd, Quantity: dec("0"), UnbilledInstalment: dec("-11000")},
		{AccountID: emptyAcct, InstrumentID: twd, Quantity: dec("0")},
		{AccountID: spareCard, InstrumentID: usd, Quantity: dec("0")},
		{AccountID: oldSaving, InstrumentID: twd, Quantity: dec("0")},
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

	t.Run("balances by institution, each instrument on its own line", func(t *testing.T) {
		wantIn(t, page,
			`<caption>好銀行</caption>`, `<caption>沒有機構</caption>`,
			// Thousands, the places of the instrument, and the sign of what is owed.
			amount("1,234,567.50", "TWD"), amount("−120.00", "TWD"), amount("1,500", "JPY"), amount("0.12345678", "BTC"),
		)
		if strings.Index(page, "好銀行") > strings.Index(page, "沒有機構") {
			t.Error("the accounts that have no institution are not last")
		}
		wantIn(t, page, `class="amt neg">−120.00`)
	})
	t.Run("the instalments that are not due, per account and as plans", func(t *testing.T) {
		wantIn(t, page,
			`未到期分期　</span><span class="amt neg">−11,000.00`,
			`<th scope="col" role="columnheader" class="num">已到期的本金</th>`,
			`<th scope="col" role="columnheader" class="num">未到期分期</th>`,
			`<a href="/app/transactions/`+seqID(77).String()+`">信用卡</a>`,
			amount("12,000.00", "TWD"), amount("1,000.00", "TWD"), amount("11,000.00", "TWD"), "2026-12-05",
			// The table of the plans is of principal, which is positive; in the balances what
			// is not due is owed, which is negative: the page says why they differ.
			"是欠機構的錢：在上面的餘額裡另外列出，是負數，餘額不含它。下表的金額是這幾筆分期的本金，所以是正數。",
		)
		wantNotIn(t, page, "已入帳的本金")
	})
	t.Run("accounts that hold nothing are folded, and archived ones left out", func(t *testing.T) {
		wantIn(t, page, `<summary>餘額皆為零的帳戶（2）</summary>`, ">空帳戶</a>", ">備用卡</a>")
		wantNotIn(t, page, "舊存摺")
	})
	t.Run("what is owed is said in words", func(t *testing.T) {
		wantIn(t, page, ">Alice</th>", amount("300.00", "TWD"), amount("−12.50", "USD"), "對方欠我", "我欠對方", `<summary>往來金額為零的對象（1）</summary>`, "<li>Bob</li>")
	})
	t.Run("the drafts that wait, and what confirming them today would change", func(t *testing.T) {
		wantIn(t, page,
			"有 <strong>3</strong> 筆草稿等著確認", `<a href="/app/drafts">查看草稿</a>`,
			"<caption>如果這些草稿今天全部確認，帳戶的餘額今天會有這些變動</caption>",
			amount("1,500.00", "TWD"), amount("200.00", "TWD"), amount("−1,300.00", "TWD"),
			"還沒到期的分期各期要到扣款日才會影響餘額",
			// What only moves between my own accounts is a line of its own.
			"自己帳戶之間的轉帳：1 筆。錢只是從一個帳戶移到另一個，不計入上面的流出和流入。",
		)
	})
	t.Run("no net worth", func(t *testing.T) {
		wantNotIn(t, page, "淨資產")
	})
}

func TestDraftsThatChangeNoBalanceToday(t *testing.T) {
	f := newFake()
	f.drafts = usecase.GetDraftSummaryOutput{Count: 2, OwnTransfers: 2}
	page := f.site(t).page("/app/")

	wantIn(t, page, "有 <strong>2</strong> 筆草稿等著確認", "如果這些草稿今天全部確認，帳戶的餘額今天不會有變動。", "自己帳戶之間的轉帳：2 筆")
	wantNotIn(t, between(page, "<h2>草稿</h2>", "</main>"), "<table")

	// With none of them moving money between my own accounts there is no line for them.
	f.drafts = usecase.GetDraftSummaryOutput{Count: 1, Amounts: []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("5"), Net: dec("-5")}}}
	wantNotIn(t, f.site(t).page("/app/"), "自己帳戶之間的轉帳")
}

func TestTheOverviewWhenThereIsNothing(t *testing.T) {
	page := newFake().site(t).page("/app/")
	wantIn(t, page, "還沒有已確認的餘額", "沒有往來。", "沒有等著確認的草稿。")
	// The accounts in use that hold nothing are only named, in a folded list.
	wantIn(t, page, "<summary>餘額皆為零的帳戶（5）</summary>")
	wantNotIn(t, page, "未到期分期", "舊存摺")
}

func TestAnArchivedAccountThatHoldsSomethingIsShown(t *testing.T) {
	f := newFake()
	f.balances = []usecase.Balance{{AccountID: oldSaving, InstrumentID: twd, Quantity: dec("5")}}
	page := f.site(t).page("/app/")
	wantIn(t, page, "舊存摺", `<span class="badge">已封存</span>`, amount("5.00", "TWD"))
}

func TestTheTransactionsOfAMonth(t *testing.T) {
	f := newFake()
	expense := lunchTransaction(1, date(2026, time.November, 14), cash, "120")
	expense.Merchant = "巷口麵店"
	expense.AreaID, expense.ProjectID = &home, &trip
	draft := transaction(seqID(2), "income", "draft", date(2026, time.November, 13),
		accountPosting(1, checking, twd, "50000"), usecase.Posting{Seq: 2, Book: "income", CategoryID: &salary, InstrumentID: twd, Quantity: dec("-50000")})
	draft.Description = "十一月薪水"
	voided := transaction(seqID(3), "transfer", "void", date(2026, time.November, 12),
		accountPosting(1, checking, twd, "-1000"), accountPosting(2, cash, twd, "1000"))
	posted := lunchTransaction(4, date(2026, time.November, 5), card, "300")
	posted.PostedOn = new(date(2026, time.November, 7))
	foreign := transaction(seqID(5), "expense", "confirmed", date(2026, time.November, 2),
		accountPosting(1, card, usd, "-100"), expensePosting(2, food, usd, "100", "3200"))
	plan := transaction(seqID(6), "instalment", "confirmed", date(2026, time.November, 1),
		accountPosting(1, card, twd, "-1030"), usecase.Posting{Seq: 2, Book: "instalment", AccountID: &card, InstrumentID: twd, Quantity: dec("1000")},
		expensePosting(3, interst, twd, "30", "30"))
	plan.PlanSeq = new(2)
	f.listed = []usecase.Transaction{expense, draft, voided, posted, foreign, plan}
	f.hasMore = true
	s := f.site(t)
	page := s.page("/app/transactions?month=2026-11")

	t.Run("it asks for the month and the page", func(t *testing.T) {
		in := f.listedIn
		if in.From == nil || !in.From.Equal(date(2026, time.November, 1)) || in.To == nil || !in.To.Equal(date(2026, time.November, 30)) {
			t.Errorf("range = %v to %v, want the whole of November", in.From, in.To)
		}
		if in.Limit != pageSize || in.Offset != 0 || in.Status != "" || in.Kind != "" || in.AccountID != nil {
			t.Errorf("asked %+v", in)
		}
	})
	t.Run("a row has the day, the kind, the category, the memo, the account and the amount", func(t *testing.T) {
		wantIn(t, page,
			`<a href="/app/transactions/`+expense.ID.String()+`">2026-11-14</a>`,
			`<td role="cell" data-label="種類">支出</td>`, `<td role="cell" data-label="類別">午餐</td>`, `<td role="cell" data-label="說明" class="memo">午餐 1<span class="line muted small">巷口麵店</span></td>`, `<td role="cell" data-label="帳戶">現金</td>`,
			amount("−120.00", "TWD"),
			`<span class="line">家庭</span><span class="line muted small">專案：日本旅行</span>`,
			`<span class="badge badge-confirmed">已確認</span>`,
		)
	})
	t.Run("the posting date is shown when there is one", func(t *testing.T) {
		wantIn(t, page, `<td role="cell" data-label="入帳日" class="nowrap">2026-11-07</td>`)
	})
	t.Run("a draft is a row of its own look, and says it is a draft", func(t *testing.T) {
		wantIn(t, page, `<tr role="row" class="draft">`, `<span class="badge badge-draft">草稿</span>`, "十一月薪水", `<td role="cell" data-label="種類">收入</td>`, `<td role="cell" data-label="類別">薪水</td>`, amount("+50,000.00", "TWD"))
	})
	t.Run("a void transaction is marked", func(t *testing.T) {
		wantIn(t, page, `<tr role="row" class="void">`, `<span class="badge badge-void">已作廢</span>`)
	})
	t.Run("a transfer shows what each account gained or lost", func(t *testing.T) {
		wantIn(t, page, `<span class="where muted small">活期戶頭　</span>`, `<span class="where muted small">現金　</span>`, amount("−1,000.00", "TWD"), amount("+1,000.00", "TWD"), `<td role="cell" data-label="種類">轉帳</td>`)
	})
	t.Run("a foreign amount is in its own instrument", func(t *testing.T) {
		wantIn(t, page, amount("−100.00", "USD"))
	})
	t.Run("a period of an instalment plan says which", func(t *testing.T) {
		wantIn(t, page, "分期第 2 期", `<td role="cell" data-label="類別">利息</td>`)
	})
	t.Run("the months around it, and the next page", func(t *testing.T) {
		wantIn(t, page,
			`href="/app/transactions?month=2026-10" rel="prev"`, `href="/app/transactions?month=2026-12" rel="next"`,
			`href="/app/transactions?month=2026-11">回到本月`,
			`href="/app/transactions?month=2026-11&amp;page=2" rel="next">下一頁`, "第 1 頁",
		)
		wantNotIn(t, page, "上一頁")
	})
}

// rowOf is the row of the list that is of the transaction.
func rowOf(page string, t usecase.Transaction) string {
	return between(page, `<a href="/app/transactions/`+t.ID.String()+`">`, "</tr>")
}

func TestTheAmountOfARowSaysWhatItIsWhenItIsNotAnAccounts(t *testing.T) {
	f := newFake()
	// A purchase paid in instalments moves no account: it makes principal owed that is not yet
	// due, and a row that showed only −3,000.00 would read as a balance of the card that fell
	// by that much.
	origin := transaction(seqID(1), "expense", "confirmed", date(2026, time.November, 1),
		usecase.Posting{Seq: 1, Book: "instalment", AccountID: &card, InstrumentID: twd, Quantity: dec("-3000")},
		expensePosting(2, rent, twd, "3000", "3000"))
	// A dinner that Bob paid for me moves no account either: what changes is what I owe him.
	paidBy := transaction(seqID(2), "expense", "confirmed", date(2026, time.November, 2),
		dealingPosting(1, bob, twd, "-300"), expensePosting(2, lunch, twd, "300", "300"))
	// An expense on an account says only the amount: the column of the accounts says whose.
	plain := lunchTransaction(3, date(2026, time.November, 3), cash, "120")
	// A transfer has an amount for each of its accounts, and says which is which.
	transfer := transaction(seqID(4), "transfer", "confirmed", date(2026, time.November, 4),
		accountPosting(1, checking, twd, "-10"), accountPosting(2, cash, twd, "10"))
	f.listed = []usecase.Transaction{origin, paidBy, plain, transfer}
	page := f.site(t).page("/app/transactions?month=2026-11")

	t.Run("principal that is not yet due", func(t *testing.T) {
		wantIn(t, rowOf(page, origin), `<span class="where muted small">信用卡・未到期分期　</span>`, amount("−3,000.00", "TWD"))
	})
	t.Run("what I owe a party", func(t *testing.T) {
		wantIn(t, rowOf(page, paidBy), `<span class="where muted small">往來・Bob　</span>`, amount("−300.00", "TWD"))
	})
	t.Run("an account's own amount has no label", func(t *testing.T) {
		wantNotIn(t, rowOf(page, plain), `<span class="where muted small">現金`)
		wantIn(t, rowOf(page, plain), amount("−120.00", "TWD"))
	})
	t.Run("an amount of each account of a transfer is told apart", func(t *testing.T) {
		wantIn(t, rowOf(page, transfer), `<span class="where muted small">活期戶頭　</span>`, `<span class="where muted small">現金　</span>`)
	})
}

func TestTheTransactionsOfNoAccountAreAskedForByTheFilterThatSaysSo(t *testing.T) {
	f := newFake()
	s := f.site(t)
	page := s.page("/app/transactions?month=2026-11&account=none&status=draft")

	if in := f.listedIn; !in.WithoutAccount || in.AccountID != nil || in.Status != "draft" {
		t.Errorf("asked %+v, want the drafts of no account", in)
	}
	wantIn(t, page,
		`<option value="none" selected>沒有帳戶的交易</option>`,
		// The links around it keep the filter.
		`href="/app/transactions?account=none&amp;month=2026-10&amp;status=draft" rel="prev"`,
		"沒有符合篩選條件的交易。",
	)
	wantNotIn(t, between(page, `name="account"`, "</select>"), `<option value="" selected>`)

	// An account that is chosen is not the filter of no account, and the page without a
	// filter is neither.
	s.page("/app/transactions?month=2026-11&account=" + cash.String())
	if in := f.listedIn; in.WithoutAccount || in.AccountID == nil || *in.AccountID != cash {
		t.Errorf("asked %+v, want the account", in)
	}
	s.page("/app/transactions?month=2026-11")
	if in := f.listedIn; in.WithoutAccount || in.AccountID != nil {
		t.Errorf("asked %+v, want no account filter", in)
	}
}

// The status filter lists what has the status, the periods of a draft plan, which are
// drafts, included: a month that shows a period as a draft has it in the list of the
// drafts. The list that leaves the periods out is the one that the page of the drafts
// links to, which counts a plan once, and it says so and has a link to the whole list.
func TestThePeriodsOfPlansAreLeftOutOnlyByTheParameterThatSaysSo(t *testing.T) {
	f := newFake()
	s := f.site(t)

	// The status draft alone does not leave them out.
	page := s.page("/app/transactions?month=2026-12&status=draft")
	if in := f.listedIn; in.Status != "draft" || in.ExcludePlanPeriods {
		t.Errorf("asked %+v, want the drafts with their periods", in)
	}
	wantNotIn(t, page, "只列分期計畫的起點", `name="periods"`)

	// The parameter does, and the page says what the list leaves out, keeps the choice in
	// its form and in the links around it, and has the link to the list that has them.
	page = s.page("/app/transactions?month=2026-12&status=draft&periods=none")
	if in := f.listedIn; in.Status != "draft" || !in.ExcludePlanPeriods {
		t.Errorf("asked %+v, want the drafts, the periods of plans left out", in)
	}
	wantIn(t, page,
		"這份清單只列分期計畫的起點，各期不另外列，筆數和草稿頁一樣。",
		`<a href="/app/transactions?month=2026-12&amp;status=draft">連各期一起列</a>`,
		`<input type="hidden" name="periods" value="none">`,
		`href="/app/transactions?month=2026-11&amp;periods=none&amp;status=draft" rel="prev"`,
		`href="/app/transactions?month=2027-01&amp;periods=none&amp;status=draft"`,
		// A filter that is set, which is cleared with the others.
		`<a href="/app/transactions?month=2026-12">清除篩選</a>`,
	)

	// It is not an account or a status: it goes with any of them, and with none.
	s.page("/app/transactions?month=2026-12&periods=none")
	if in := f.listedIn; in.Status != "" || !in.ExcludePlanPeriods {
		t.Errorf("asked %+v, want every status, the periods of plans left out", in)
	}
	s.page("/app/transactions?month=2026-12")
	if in := f.listedIn; in.ExcludePlanPeriods {
		t.Errorf("asked %+v, want the periods of plans listed", in)
	}

	// Anything but none is a 400 page that does not repeat it, and so is a second value.
	for _, target := range []string{
		"/app/transactions?month=2026-12&periods=all",
		"/app/transactions?month=2026-12&periods=NONE",
		"/app/transactions?month=2026-12&periods=none&periods=none",
	} {
		clear(f.calls)
		res := s.get(target)
		if res.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d, want 400", target, res.Code)
		}
		if n := f.total(); n != 0 {
			t.Errorf("GET %s asked the use cases %d times, want none: the request does not read", target, n)
		}
	}
}

func TestTheFiltersOfTheTransactions(t *testing.T) {
	f := newFake()
	s := f.site(t)
	page := s.page("/app/transactions?month=2026-10&account=" + checking.String() + "&status=draft&kind=transfer&page=3")

	in := f.listedIn
	if in.AccountID == nil || *in.AccountID != checking || in.Status != "draft" || in.Kind != "transfer" {
		t.Errorf("asked %+v", in)
	}
	if in.Offset != 2*pageSize || in.Limit != pageSize {
		t.Errorf("page 3 = offset %d limit %d, want %d and %d", in.Offset, in.Limit, 2*pageSize, pageSize)
	}
	if in.From == nil || !in.From.Equal(date(2026, time.October, 1)) || !in.To.Equal(date(2026, time.October, 31)) {
		t.Errorf("range = %v to %v", in.From, in.To)
	}

	// What is chosen is selected in the form, and kept by the links around it.
	wantIn(t, page,
		`<option value="`+checking.String()+`" selected>活期戶頭</option>`,
		`<option value="draft" selected>草稿</option>`,
		`<option value="transfer" selected>轉帳</option>`,
		`<input type="hidden" name="month" value="2026-10">`,
		`<a href="/app/transactions?month=2026-10">清除篩選</a>`,
	)
	for _, link := range []string{
		fmt.Sprintf(`href="/app/transactions?account=%s&amp;kind=transfer&amp;month=2026-09&amp;status=draft"`, checking),
		fmt.Sprintf(`href="/app/transactions?account=%s&amp;kind=transfer&amp;month=2026-11&amp;status=draft"`, checking),
		fmt.Sprintf(`href="/app/transactions?account=%s&amp;kind=transfer&amp;month=2026-10&amp;page=2&amp;status=draft"`, checking),
	} {
		wantIn(t, page, link)
	}
	wantIn(t, page, "2026-10 沒有符合篩選條件的交易。")
	wantNotIn(t, page, "下一頁")
}

func TestAnEmptyMonthOfTransactions(t *testing.T) {
	page := newFake().site(t).page("/app/transactions")
	wantIn(t, page, "2026-11 沒有交易。")
	wantNotIn(t, page, "清除篩選", "<table")
}

func TestTextThatIsNotMarkupIsNotRunAsMarkup(t *testing.T) {
	evil := `<script>alert("x")</script>`
	img := `"><img src=x onerror=alert(1)>`
	f := newFake()
	tx := lunchTransaction(1, date(2026, time.November, 14), cash, "120")
	tx.Description, tx.Merchant = evil, img
	tx.Postings[1].Memo = evil
	tx.TagIDs = []uuid.UUID{tagA}
	f.tags = append(f.tags, catalogusecase.Tag{ID: seqID(90), Name: evil})
	tx.TagIDs = append(tx.TagIDs, seqID(90))
	f.accounts[3].Name = evil // 現金
	f.listed = []usecase.Transaction{tx}
	f.byID[tx.ID] = tx
	s := f.site(t)

	for _, target := range []string{"/app/transactions", "/app/transactions/" + tx.ID.String()} {
		page := s.page(target)
		wantIn(t, page, `&lt;script&gt;alert(&#34;x&#34;)&lt;/script&gt;`)
		wantNotIn(t, page, "<script", "<img", "onerror=alert(1)>")
	}
}

func TestOneTransactionWithAllItsParts(t *testing.T) {
	f := newFake()
	discount := dec("50")
	base := dec("1280")
	expense := transaction(seqID(1), "expense", "confirmed", date(2026, time.November, 14),
		accountPosting(1, card, usd, "-100"),
		usecase.Posting{Seq: 2, Book: "expense", CategoryID: &food, InstrumentID: usd, Quantity: dec("40"), BaseAmount: &base, Discount: &discount, Memo: "壽司"},
		dealingPosting(3, alice, usd, "60"),
	)
	expense.Description, expense.Merchant = "聚餐", "鮨"
	expense.AreaID, expense.ProjectID = &home, &trip
	expense.TagIDs = []uuid.UUID{tagA}
	expense.PostedOn = new(date(2026, time.November, 16))
	expense.OccurredAt = new(time.Date(2026, time.November, 14, 12, 30, 0, 0, time.UTC))
	expense.ConfirmedAt = new(time.Date(2026, time.November, 14, 20, 0, 0, 0, time.UTC))
	f.byID[expense.ID] = expense

	paidFor := transaction(seqID(2), "expense", "draft", date(2026, time.November, 14),
		dealingPosting(1, bob, twd, "-300"), expensePosting(2, lunch, twd, "300", "300"))
	f.byID[paidFor.ID] = paidFor

	transfer := transaction(seqID(3), "transfer", "confirmed", date(2026, time.November, 10),
		accountPosting(1, checking, twd, "-1000"), accountPosting(2, cash, twd, "1000"),
		accountPosting(3, checking, twd, "-15"), expensePosting(4, fees, twd, "15", "15"))
	f.byID[transfer.ID] = transfer

	reconciliation := transaction(seqID(4), "reconciliation", "confirmed", date(2026, time.November, 9),
		accountPosting(1, cash, twd, "-120"),
		usecase.Posting{Seq: 2, Book: "expense", CategoryID: &food, InstrumentID: twd, Quantity: dec("120"), BaseAmount: new(dec("120"))})
	f.byID[reconciliation.ID] = reconciliation

	s := f.site(t)
	page := s.page("/app/transactions/" + expense.ID.String())

	t.Run("the facts", func(t *testing.T) {
		wantIn(t, page,
			`<h2>支出 <span class="badge badge-confirmed">已確認</span></h2>`,
			"<dt>發生日</dt><dd>2026-11-14</dd>", "<dt>入帳日</dt><dd>2026-11-16</dd>",
			// The moments are in the timezone of the user (UTC+8 here), not UTC.
			"<dt>發生時間</dt><dd>2026-11-14 20:30</dd>", "<dt>確認時間</dt><dd>2026-11-15 04:00</dd>",
			"<dt>說明</dt><dd>聚餐</dd>", "<dt>商家</dt><dd>鮨</dd>", "<dt>領域</dt><dd>家庭</dd>", "<dt>專案</dt><dd>日本旅行</dd>",
			`<span class="badge">報帳</span>`,
		)
	})
	t.Run("the parts: the main amount, the discount, the share of someone", func(t *testing.T) {
		wantIn(t, page,
			part("帳戶", "信用卡"), amount("−100.00", "USD"),
			part("主要金額", "飲食"), amount("+40.00", "USD"), "壽司",
			`<th scope="row" role="rowheader">折扣</th>`, amount("50.00", "USD"), "不影響支出",
			part("分攤", "Alice"), amount("+60.00", "USD"),
		)
	})
	t.Run("a fee, a draft and what someone paid for me", func(t *testing.T) {
		fee := s.page("/app/transactions/" + transfer.ID.String())
		wantIn(t, fee, part("手續費", "手續費"), amount("+15.00", "TWD"), amount("−1,000.00", "TWD"), amount("+1,000.00", "TWD"))

		draft := s.page("/app/transactions/" + paidFor.ID.String())
		wantIn(t, draft, `badge-draft">草稿`, "這是草稿", part("往來（我欠對方）", "Bob"), amount("−300.00", "TWD"))
	})
	t.Run("what a reconciliation found missing", func(t *testing.T) {
		wantIn(t, s.page("/app/transactions/"+reconciliation.ID.String()), `<th scope="row" role="rowheader">未記錄</th>`)
	})
}

func TestAnInstalmentPlanAndItsOtherTransactions(t *testing.T) {
	f := newFake()
	origin := transaction(seqID(10), "expense", "confirmed", date(2026, time.October, 1),
		usecase.Posting{Seq: 1, Book: "instalment", AccountID: &card, InstrumentID: twd, Quantity: dec("-3000")},
		expensePosting(2, rent, twd, "3000", "3000"))
	origin.PlanID, origin.PlanSeq = &origin.ID, new(0)
	var periods []usecase.Transaction
	for i := 1; i <= 3; i++ {
		p := transaction(seqID(10+i), "instalment", "confirmed", date(2026, time.Month(10+i-1), 15),
			accountPosting(1, card, twd, "-1010"),
			usecase.Posting{Seq: 2, Book: "instalment", AccountID: &card, InstrumentID: twd, Quantity: dec("1000")},
			expensePosting(3, interst, twd, "10", "10"))
		p.PlanID, p.PlanSeq = &origin.ID, new(i)
		periods = append(periods, p)
		f.byID[p.ID] = p
	}
	f.byID[origin.ID] = origin
	f.plan = usecase.InstalmentPlan{Origin: origin, Periods: periods}
	s := f.site(t)

	page := s.page("/app/transactions/" + periods[1].ID.String())
	wantIn(t, page,
		"<h3>分期計畫：分期第 2 期，共 3 期</h3>",
		`<a href="/app/transactions/`+origin.ID.String()+`">分期起點</a>`,
		`<a href="/app/transactions/`+periods[0].ID.String()+`">分期第 1 期</a>`,
		`<a href="/app/transactions/`+periods[2].ID.String()+`">分期第 3 期</a>`,
		`<tr role="row" class="current" aria-current="true">`, "目前這筆",
		`<th scope="row" role="rowheader">利息</th>`, `<th scope="row" role="rowheader">未到期分期</th>`,
		// The origin only makes principal owed that is not yet due.
		amount("−3,000.00", "TWD"),
	)
	// The transaction of the plan is asked of once, and the plan once.
	if f.calls["GetInstalmentPlan"] != 1 || f.calls["GetTransaction"] != 1 {
		t.Errorf("calls = %v", f.calls)
	}
}

var (
	// draftCell is a cell of a table of drafts that holds the amounts of one of the three
	// columns, and draftAmountInCell an amount in it: the number, then the instrument.
	draftCell         = regexp.MustCompile(`(?s)<td role="cell" data-label="(流出|流入|淨額)" class="num">(.*?)</td>`)
	draftAmountInCell = regexp.MustCompile(`<span class="amt[^"]*">([^<]*)<span class="unit"> ([^<]*)</span></span>`)
)

// draftColumns adds up the amounts of the cells of the markup that are of the columns of
// the amounts, for each column and each instrument ("流出 TWD"), as a reader adds up a
// column of the table.
func draftColumns(t *testing.T, markup string) map[string]decimal.Decimal {
	t.Helper()
	sums := map[string]decimal.Decimal{}
	for _, cell := range draftCell.FindAllStringSubmatch(markup, -1) {
		for _, a := range draftAmountInCell.FindAllStringSubmatch(cell[2], -1) {
			text := strings.NewReplacer("−", "-", "&#43;", "", "+", "", ",", "").Replace(a[1])
			n, err := decimal.NewFromString(text)
			if err != nil {
				t.Fatalf("the amount %q of the column %s is not a number: %v", a[1], cell[1], err)
			}
			key := cell[1] + " " + a[2]
			sums[key] = sums[key].Add(n)
		}
	}
	return sums
}

func TestTheDraftsByMonthAndAccount(t *testing.T) {
	f := newFake()
	noAccount := (*uuid.UUID)(nil)
	// The month has 7 drafts, of which one moves money from the checking account to the cash
	// (1,000): the legs are in the rows of the accounts, which are of their balances, and the
	// month leaves them out, as the use case says, and has the money they move in a line of its
	// own. Nothing here is the sum of anything: the page writes the numbers it is given.
	f.drafts = usecase.GetDraftSummaryOutput{
		Count:              9,
		OwnTransfers:       1,
		OwnTransferAmounts: []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("1000"), Inflow: dec("1000"), Net: dec("0")}},
		Amounts: []usecase.DraftAmount{
			{InstrumentID: twd, Outflow: dec("205"), Inflow: dec("50000"), Net: dec("49795")},
			{InstrumentID: usd, Outflow: dec("10"), Inflow: dec("0"), Net: dec("-10")},
		},
		Months: []usecase.DraftMonth{
			{
				Month: date(2026, time.November, 1), Count: 7, OwnTransfers: 1,
				OwnTransferAmounts: []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("1000"), Inflow: dec("1000"), Net: dec("0")}},
				Amounts: []usecase.DraftAmount{
					{InstrumentID: twd, Outflow: dec("200"), Inflow: dec("50000"), Net: dec("49800")},
					{InstrumentID: usd, Outflow: dec("10"), Inflow: dec("0"), Net: dec("-10")},
				},
				Accounts: []usecase.DraftAccount{
					{AccountID: &checking, Count: 4, Amounts: []usecase.DraftAmount{
						{InstrumentID: twd, Outflow: dec("1000"), Inflow: dec("50000"), Net: dec("49000")},
						{InstrumentID: usd, Outflow: dec("10"), Inflow: dec("0"), Net: dec("-10")},
					}},
					{AccountID: &cash, Count: 2, Amounts: []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("200"), Inflow: dec("1000"), Net: dec("800")}}},
					{AccountID: noAccount, Count: 1},
				},
			},
			{
				Month: date(2026, time.October, 1), Count: 2,
				Amounts:  []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("5"), Net: dec("-5")}},
				Accounts: []usecase.DraftAccount{{AccountID: &cash, Count: 2, Amounts: []usecase.DraftAmount{{InstrumentID: twd, Outflow: dec("5"), Net: dec("-5")}}}},
			},
		},
	}
	page := f.site(t).page("/app/drafts")
	november := between(page, "<h2>2026-11 ", "</section>")
	october := between(page, "<h2>2026-10 ", "</section>")

	t.Run("the count, and what the amounts are", func(t *testing.T) {
		wantIn(t, page,
			"共有 <strong>9</strong> 筆草稿等著確認",
			"分期計畫算一筆（它的起點），各期不另外算。",
			"流出、流入和淨額是如果這些草稿今天全部確認，帳戶的餘額今天會有的變動。",
			"還沒到期的分期各期要到扣款日才會影響餘額",
		)
	})
	t.Run("a month, and the links of its accounts, which list the drafts that are counted", func(t *testing.T) {
		wantIn(t, page,
			`<h2>2026-11 <span class="muted small">7 筆</span> <a class="small" href="/app/transactions?month=2026-11&amp;periods=none&amp;status=draft">查看這個月的草稿</a></h2>`,
			`<a href="/app/transactions?account=`+checking.String()+`&amp;month=2026-11&amp;periods=none&amp;status=draft">活期戶頭</a>`,
			// The drafts that are of no account are listed by the filter that says so, not by
			// all the drafts of the month; and a plan is listed by its origin, as it is counted.
			`<a href="/app/transactions?account=none&amp;month=2026-11&amp;periods=none&amp;status=draft">沒有帳戶的草稿</a>`,
			"<h2>2026-10 ",
		)
		if strings.Index(page, "2026-11 <span") > strings.Index(page, "2026-10 <span") {
			t.Error("the newest month is not first")
		}
		// The accounts are in the order the use case gave them: by name, none last.
		if strings.Index(page, ">活期戶頭<") > strings.Index(page, ">現金<") || strings.Index(page, ">現金<") > strings.Index(page, "沒有帳戶的草稿") {
			t.Error("the accounts of a month are not in the order they were given in")
		}
	})
	t.Run("the rows of the accounts have the legs of a transfer, the month does not", func(t *testing.T) {
		wantIn(t, november,
			amount("49,000.00", "TWD"), amount("−10.00", "USD"), amount("1,000.00", "TWD"), // the accounts
			"沒有帳戶的變動", "這個月合計",
			amount("50,000.00", "TWD"), amount("49,800.00", "TWD"), // the month
		)
		// The month is not the rows of the accounts added up: that would have the transfer
		// twice, as an outflow of 1,200 and an inflow of 51,000.
		wantNotIn(t, november, amount("1,200.00", "TWD"), amount("51,000.00", "TWD"))
	})
	t.Run("the transfers between my own accounts are a line that takes them out of the rows", func(t *testing.T) {
		wantIn(t, november,
			`<th scope="row" role="rowheader">自己帳戶之間的轉帳</th>`,
			`<td role="cell" data-label="筆數" class="num">1</td>`,
			// What the rows have twice, with a minus sign and not in the colour of a loss.
			`<td role="cell" data-label="流出" class="num"><span class="line"><span class="amt">`+amount("−1,000.00", "TWD"),
			`<td role="cell" data-label="流入" class="num"><span class="line"><span class="amt">`+amount("−1,000.00", "TWD"),
			"所以每一欄的各列加上它，剛好是「這個月合計」",
		)
		wantNotIn(t, october, "自己帳戶之間的轉帳")
	})
	// What the page says is what it adds up to: in each column, for each instrument, the rows
	// of the accounts and the line of the transfers between them are the total of the month.
	// A reader does that sum, and a page that has it come out wrong has told the reader that
	// it is wrong.
	t.Run("the rows and the line of the transfers add up to the total of the month", func(t *testing.T) {
		tbody := between(november, "<tbody", "</tbody>")
		ownRow, totalRow, _ := strings.Cut(between(november, "<tfoot", "</tfoot>"), "這個月合計")
		rows, own, total := draftColumns(t, tbody), draftColumns(t, ownRow), draftColumns(t, totalRow)
		if len(rows) == 0 || len(own) == 0 || len(total) == 0 {
			t.Fatalf("the sums are of nothing: %v of the rows, %v of the line, %v of the total", rows, own, total)
		}
		keys := map[string]bool{}
		for _, sums := range []map[string]decimal.Decimal{rows, own, total} {
			for key := range sums {
				keys[key] = true
			}
		}
		for key := range keys {
			if got := rows[key].Add(own[key]); !got.Equal(total[key]) {
				t.Errorf("%s: the rows %s and the transfers between my own accounts %s make %s, and the total of the month is %s", key, rows[key], own[key], got, total[key])
			}
		}
	})
}

func TestNoDrafts(t *testing.T) {
	page := newFake().site(t).page("/app/drafts")
	wantIn(t, page, "沒有等著確認的草稿。")
	wantNotIn(t, page, "<table")
}

func TestTheCards(t *testing.T) {
	f := newFake()
	// Twenty statements, the newest first, the third of them being the one that is open
	// today (the 21st of November): closing on the 5th of December.
	f.statements = nil
	for i := range 20 {
		closing := date(2026, time.December, 5).AddDate(0, -i, 0)
		s := usecase.CardStatementSummary{
			From:      closing.AddDate(0, -1, 1),
			ClosingOn: closing,
			Closed:    closing.Before(date(2026, time.November, 21)),
			DueOn:     new(closing.AddDate(0, 0, 15)),
			Figures: []usecase.StatementFigures{
				{InstrumentID: twd, Previous: dec("-100"), Charges: dec("2500"), Credits: dec("100"), Closing: dec("-2500")},
				{InstrumentID: usd, Previous: dec("0"), Charges: dec("10"), Credits: dec("0"), Closing: dec("-10")},
			},
		}
		f.statements = append(f.statements, s)
	}
	// One statement after the open one has not begun, and is not shown.
	f.statements = append([]usecase.CardStatementSummary{{
		From: date(2026, time.December, 6), ClosingOn: date(2027, time.January, 5),
	}}, f.statements...)
	page := f.site(t).page("/app/cards")

	if len(f.statementIn) != 1 {
		t.Fatalf("%d calls for the statements, want one for the one card that has a closing day", len(f.statementIn))
	}
	in := f.statementIn[0]
	if in.AccountID != card || !in.From.Equal(date(2025, time.October, 21)) || !in.To.Equal(date(2026, time.December, 22)) {
		t.Errorf("asked %+v", in)
	}

	t.Run("the open statement and the last twelve closed ones", func(t *testing.T) {
		if n := strings.Count(page, `<th scope="row" role="rowheader" class="nowrap">`); n != 13 {
			t.Errorf("%d statements, want 13", n)
		}
		wantIn(t, page,
			`<tr role="row" class="current">`, `<span class="badge">未結帳</span>`,
			`href="/app/cards/`+card.String()+`?closing=2026-12-05">2026-12-05</a>`,
			`href="/app/cards/`+card.String()+`?closing=2025-12-05">2025-12-05</a>`,
			"2026-11-06 至 2026-12-05", "2026-12-20",
			amount("−100.00", "TWD"), amount("2,500.00", "TWD"), amount("−10.00", "USD"),
		)
		wantNotIn(t, page, "2027-01-05", "closing=2025-11-05", "closing=2025-10-05")
	})
	t.Run("the days of the card", func(t *testing.T) {
		wantIn(t, page, "結帳日：每月 5 日；繳款日：每月 20 日")
	})
	t.Run("a card with no closing day is listed, with a note", func(t *testing.T) {
		wantIn(t, page, "<h2>沒有結帳日的信用卡</h2>", "備用卡：沒有設定結帳日，所以不產生帳單。")
	})
}

func TestNoCards(t *testing.T) {
	f := newFake()
	f.accounts = f.accounts[3:]
	wantIn(t, f.site(t).page("/app/cards"), "沒有信用卡。")
}

func TestAStatement(t *testing.T) {
	f := newFake()
	closing := date(2026, time.November, 5)
	food1 := transaction(seqID(1), "expense", "confirmed", date(2026, time.October, 8),
		accountPosting(1, card, twd, "-500"), expensePosting(2, lunch, twd, "300", "300"), expensePosting(3, rent, twd, "200", "200"))
	food1.Description = "全聯"
	shared := transaction(seqID(2), "expense", "confirmed", date(2026, time.October, 20),
		accountPosting(1, card, twd, "-900"), expensePosting(2, food, twd, "300", "300"),
		dealingPosting(3, alice, twd, "300"), dealingPosting(4, bob, twd, "300"))
	payment := transaction(seqID(3), "transfer", "confirmed", date(2026, time.October, 25),
		accountPosting(1, checking, twd, "800"), accountPosting(2, card, twd, "800"))
	late := transaction(seqID(4), "expense", "confirmed", date(2026, time.November, 4),
		accountPosting(1, card, usd, "-20"), expensePosting(2, food, usd, "20", "640"))
	late.PostedOn = new(date(2026, time.November, 5))
	f.statement = usecase.CardStatement{
		CardStatementSummary: usecase.CardStatementSummary{
			From: date(2026, time.October, 6), ClosingOn: closing, DueOn: new(date(2026, time.November, 20)), Closed: true,
			Figures: []usecase.StatementFigures{
				{InstrumentID: twd, Previous: dec("-1000"), Charges: dec("1400"), Credits: dec("800"), Closing: dec("-1600")},
				{InstrumentID: usd, Charges: dec("20"), Closing: dec("-20")},
			},
		},
		Transactions: []usecase.StatementTransaction{
			{Transaction: food1, StatementDate: food1.OccurredOn, Amounts: []usecase.StatementAmount{{InstrumentID: twd, Quantity: dec("-500")}}},
			{Transaction: shared, StatementDate: shared.OccurredOn, Amounts: []usecase.StatementAmount{{InstrumentID: twd, Quantity: dec("-900")}}},
			{Transaction: payment, StatementDate: payment.OccurredOn, Amounts: []usecase.StatementAmount{{InstrumentID: twd, Quantity: dec("800")}}},
			{Transaction: late, StatementDate: date(2026, time.November, 5), Amounts: []usecase.StatementAmount{{InstrumentID: usd, Quantity: dec("-20")}}},
		},
		Drafts: 3,
		// What the use case divided the charges into: 300 + 300 of food (the lunch is under it),
		// 200 of rent, and the shares of Alice and Bob.
		Charges: []usecase.StatementCharges{
			{InstrumentID: twd, Categories: []usecase.CategoryCharge{{CategoryID: food, Amount: dec("600")}, {CategoryID: rent, Amount: dec("200")}}, Shares: dec("600")},
			{InstrumentID: usd, Categories: []usecase.CategoryCharge{{CategoryID: food, Amount: dec("20")}}},
		},
	}
	s := f.site(t)
	page := s.page("/app/cards/" + card.String() + "?closing=2026-11-05")

	if f.cardIn.AccountID != card || f.cardIn.ClosingOn == nil || !f.cardIn.ClosingOn.Equal(closing) {
		t.Errorf("asked %+v", f.cardIn)
	}
	t.Run("the days and the figures", func(t *testing.T) {
		wantIn(t, page,
			"<h2>信用卡</h2>", "2026-10-06 至 2026-11-05", "<dt>繳款日期</dt><dd>2026-11-20</dd>",
			amount("−1,000.00", "TWD"), amount("1,400.00", "TWD"), amount("800.00", "TWD"), amount("−1,600.00", "TWD"), amount("−20.00", "USD"),
		)
		wantNotIn(t, page, `<span class="badge">未結帳</span>`)
	})
	t.Run("the statements around it", func(t *testing.T) {
		wantIn(t, page,
			`href="/app/cards/`+card.String()+`?closing=2026-10-05" rel="prev"`,
			`href="/app/cards/`+card.String()+`?closing=2026-11-06" rel="next"`,
		)
	})
	t.Run("the transactions in the order of the use case, oldest first", func(t *testing.T) {
		wantIn(t, page, ">2026-10-08</a>", ">2026-10-20</a>", ">2026-10-25</a>", amount("−500.00", "TWD"), amount("+800.00", "TWD"), "全聯")
		if strings.Index(page, "2026-10-08") > strings.Index(page, "2026-10-25") {
			t.Error("the lines are not in the order they were given in")
		}
		// A transaction that happened on another day than it was posted says so.
		wantIn(t, page, `<td role="cell" data-label="發生日" class="nowrap">2026-11-04</td>`)
	})
	t.Run("the drafts that would change it", func(t *testing.T) {
		wantIn(t, page, "還有 <strong>3</strong> 筆草稿沒有確認", `<a href="/app/drafts">查看草稿</a>`)
	})
	t.Run("the charges by top-level category: a chart and the same numbers in a table", func(t *testing.T) {
		wantIn(t, page,
			"<h3>本期消費依類別</h3>", `<svg `, `class="chart chart-hbars"`, `id="charges-TWD-title"`, `id="charges-USD-title"`,
			"<caption>TWD</caption>",
		)
		table := between(page, "<caption>TWD</caption>", "</table>")
		wantIn(t, table,
			`<th scope="row">飲食</th><td class="num"><span class="amt">600.00`,
			`<th scope="row">房租</th><td class="num"><span class="amt">200.00`,
			`<th scope="row">分攤（我替別人付）</th><td class="num"><span class="amt">600.00`,
			// The total is the Charges of the figures, which the use case divided up.
			`<th scope="row">本期消費</th><td class="num"><span class="amt">1,400.00`,
		)
		if strings.Contains(table, "其他") {
			t.Errorf("a charge that is explained to the last cent has a row called other:\n%s", table)
		}
		if strings.Index(table, "飲食") > strings.Index(table, "房租") {
			t.Errorf("the lines are not in the order the use case gave them:\n%s", table)
		}
	})
}

// between is what is in text between the first from and the first to after it.
func between(text, from, to string) string {
	_, after, _ := strings.Cut(text, from)
	inside, _, _ := strings.Cut(after, to)
	return inside
}

func TestTheStatementThatIsOpen(t *testing.T) {
	f := newFake()
	f.statement = usecase.CardStatement{CardStatementSummary: usecase.CardStatementSummary{
		From: date(2026, time.November, 6), ClosingOn: date(2026, time.December, 5),
	}}
	page := f.site(t).page("/app/cards/" + card.String())
	if f.cardIn.ClosingOn != nil {
		t.Errorf("asked for %v: no day means the open statement, which is for the use case to find by today", f.cardIn.ClosingOn)
	}
	wantIn(t, page, `<span class="badge">未結帳</span>`, "<dt>繳款日期</dt><dd>—</dd>", "這一期沒有款項", "這一期沒有已確認的交易", "這一期沒有消費")
	wantNotIn(t, page, "草稿沒有確認")
}

func TestTheBudgets(t *testing.T) {
	f := newFake()
	f.areaBudgets = usecase.GetAreaBudgetsOutput{Areas: []usecase.AreaBudgetStatus{
		{
			AreaID: home, Budget: new(dec("10000")), Spent: dec("12500"), SpentWithProjects: dec("15000"), Over: true,
			Allocations: []usecase.AllocationStatus{
				{CategoryID: food, Amount: dec("4000"), Spent: dec("4500"), Over: true},
				{CategoryID: rent, Amount: dec("5000"), Spent: dec("5000")},
			},
			Unallocated: dec("3000"),
		},
		{
			AreaID: work, Budget: new(dec("8000")), Spent: dec("2000"), SpentWithProjects: dec("2000"),
			// The use case says whether an allocation is over, and says it is not: the page does
			// not compare the two numbers again, so it shows no badge for this one.
			Allocations: []usecase.AllocationStatus{{CategoryID: fees, Amount: dec("1000"), Spent: dec("1500")}},
			Unallocated: dec("2000"),
		},
		{AreaID: seqID(50), Spent: dec("300"), SpentWithProjects: dec("300"), Unallocated: dec("300")},
		{AreaID: seqID(51), Budget: new(dec("0")), Spent: dec("50"), SpentWithProjects: dec("50"), Unallocated: dec("50"), Over: true},
	}}
	f.projectBudgets = usecase.GetProjectBudgetsOutput{Projects: []usecase.ProjectBudgetStatus{{
		ProjectID: trip, Budget: new(dec("100000")), Spent: dec("25000"), Unallocated: dec("25000"),
	}}}
	page := f.site(t).page("/app/budgets?month=2026-11")

	if !f.budgetMonth.Equal(date(2026, time.November, 1)) {
		t.Errorf("asked for %v", f.budgetMonth)
	}
	t.Run("an area over its budget is marked in words and in the picture", func(t *testing.T) {
		home := between(page, "<h3>家庭</h3>", "</section>")
		wantIn(t, home,
			`<span class="badge badge-over">超支 2,500.00 TWD</span>`,
			"<dt>預算</dt>", amount("10,000.00", "TWD"),
			"<dt>已花（不含專案）</dt>", amount("12,500.00", "TWD"), "<dt>已花（含專案）</dt>", amount("15,000.00", "TWD"),
			`chart-fill-over`, `chart-limit`, "已花預算的 125.0%",
			`<th scope="row" class="nowrap">飲食</th>`, `<span class="badge badge-over">超過分配</span>`,
			`<th scope="row" class="nowrap">未分配</th>`, amount("3,000.00", "TWD"),
		)
		if n := strings.Count(home, "超過分配"); n != 1 {
			t.Errorf("%d allocations are over, want 1: spending the whole of one is not over", n)
		}
	})
	t.Run("an area within it has what is left", func(t *testing.T) {
		work := between(page, "<h3>工作</h3>", "</section>")
		wantIn(t, work, `badge-ok">沒有超支`, "<dt>還剩</dt>", amount("6,000.00", "TWD"), "已花預算的 25.0%", `class="chart-fill"`)
		wantNotIn(t, work, "badge-over", "chart-fill-over")
	})
	t.Run("an area with spending and no budget says so and shows no bar", func(t *testing.T) {
		unknown := between(page, "<h3>"+seqID(50).String()+"</h3>", "</section>")
		wantIn(t, unknown, "沒有預算", amount("300.00", "TWD"))
		wantNotIn(t, unknown, "<svg", "已花預算的")
	})
	t.Run("a budget of nothing: what is spent is over it, and has no share to speak of", func(t *testing.T) {
		none := between(page, "<h3>"+seqID(51).String()+"</h3>", "</section>")
		wantIn(t, none, `<span class="badge badge-over">超支 50.00 TWD</span>`, "這個預算是 0：花任何錢都算超支。", `chart-fill-over`)
		wantNotIn(t, none, "已花預算的")
	})
	t.Run("the projects, the same way, with a note that they do not follow the month", func(t *testing.T) {
		project := between(page, "<h3>日本旅行</h3>", "</section>")
		wantIn(t, project, amount("100,000.00", "TWD"), amount("25,000.00", "TWD"), "已花預算的 25.0%")
		wantNotIn(t, project, "已花（含專案）")
		wantIn(t, page, "專案的預算和已花是整個專案的總額，不分月份，不會隨上面切換的月份改變。")
	})
}

func TestNoBudgets(t *testing.T) {
	page := newFake().site(t).page("/app/budgets")
	wantIn(t, page, "2026-11 沒有設預算的領域，也沒有領域花錢。", "還沒有專案設了預算或花過錢。")
}

func TestTheSpending(t *testing.T) {
	f := newFake()
	// The use case has done the adding up: each top-level category has its total, what was
	// spent on it itself and its subcategories, the largest total first. The page lays it out.
	f.spent = usecase.GetSpendingOutput{
		Total: dec("10000"),
		TopLevel: []usecase.TopLevelSpending{
			{CategoryID: rent, Total: dec("5500"), Own: dec("5500")},
			{CategoryID: food, Total: dec("4000"), Own: dec("1000"), Children: []usecase.CategorySpending{{CategoryID: lunch, Amount: dec("3000")}}},
			{CategoryID: fees, Total: dec("500"), Own: dec("500")},
		},
	}
	months := []usecase.MonthSpending{}
	for m := date(2025, time.December, 1); !m.After(date(2026, time.November, 1)); m = m.AddDate(0, 1, 0) {
		months = append(months, usecase.MonthSpending{Month: m, Total: dec(fmt.Sprint(1000 * (len(months) + 1)))})
	}
	f.trend = usecase.GetSpendingTrendOutput{Months: months}
	s := f.site(t)
	page := s.page("/app/spending?month=2026-09")

	t.Run("by top-level category, the largest first, with the subcategories", func(t *testing.T) {
		table := between(page, "<caption>各類別的支出和子類別</caption>", "</table>")
		wantIn(t, table,
			`<th scope="row">房租</th><td class="num"><span class="amt">5,500.00`, "55.0%",
			`<th scope="row">飲食</th><td class="num"><span class="amt">4,000.00`, "40.0%",
			`<th scope="row" class="indent">午餐</th><td class="num"><span class="amt">3,000.00`, "30.0%",
			`<th scope="row" class="indent">其他（直接記在這個類別）</th><td class="num"><span class="amt">1,000.00`,
			`<th scope="row">手續費</th><td class="num"><span class="amt">500.00`,
		)
		if strings.Index(table, "房租") >= strings.Index(table, "飲食") || strings.Index(table, "飲食") >= strings.Index(table, "手續費") {
			t.Errorf("not the largest first:\n%s", table)
		}
		wantIn(t, page, "合計 <strong>", amount("10,000.00", "TWD"), `class="chart chart-hbars"`, `id="by-category-title"`)
	})
	t.Run("the twelve months, the selected one highlighted, and the numbers in a table", func(t *testing.T) {
		wantIn(t, page, `class="chart chart-columns"`, `chart-bar chart-bar-hl`,
			`<tr class="current" aria-current="true"><th scope="row"><a href="/app/spending?month=2026-09">2026-09</a></th>`)
		if n := strings.Count(page, "chart-bar-hl"); n != 1 {
			t.Errorf("%d bars are highlighted, want 1", n)
		}
		table := between(page, "<caption>每月的支出合計</caption>", "</table>")
		if n := strings.Count(table, "<tr"); n != 13 { // the header and the months
			t.Errorf("%d rows in the table of the months, want 13", n)
		}
		wantIn(t, table, amount("1,000.00", "TWD"), amount("12,000.00", "TWD"))
	})
	t.Run("the window of the chart is asked for, and the heading says which months it is", func(t *testing.T) {
		if !f.trendIn.From.Equal(date(2025, time.December, 1)) || !f.trendIn.To.Equal(date(2026, time.November, 1)) {
			t.Errorf("the trend was asked from %v to %v, want the twelve months that end with this one", f.trendIn.From, f.trendIn.To)
		}
		wantIn(t, page, "<h2>最近十二個月每月的支出</h2>", `id="trend-title"`)
	})
	t.Run("a month that is out of the window ends it, and the heading is not about the last twelve months", func(t *testing.T) {
		old := s.page("/app/spending?month=2024-03")
		if !f.trendIn.From.Equal(date(2023, time.April, 1)) || !f.trendIn.To.Equal(date(2024, time.March, 1)) {
			t.Errorf("the trend was asked from %v to %v, want the twelve months that end with March 2024", f.trendIn.From, f.trendIn.To)
		}
		wantIn(t, old, "<h2>2023-04 到 2024-03 每月的支出</h2>")
		wantNotIn(t, old, "最近十二個月")
		s.page("/app/spending?month=2027-02")
		if !f.trendIn.To.Equal(date(2027, time.February, 1)) {
			t.Errorf("the trend ends %v, want February 2027", f.trendIn.To)
		}
	})
	t.Run("the definition of spent is said", func(t *testing.T) {
		wantIn(t, page, "統計已確認的支出，以本位幣金額計，算到今天為止")
	})
}

func TestNothingSpent(t *testing.T) {
	page := newFake().site(t).page("/app/spending")
	wantIn(t, page, "2026-11 沒有支出。", `class="chart chart-columns"`)
	wantNotIn(t, page, "chart-hbars")
}
