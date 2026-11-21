package service

import (
	"slices"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// rowContext is the area, the project, the tags and the party that the 專案 and the
// tags of a row come to.
type rowContext struct {
	area    string
	project string
	tags    []string
	// dealingParty is the party the row was paid for, when its project is one that is
	// paid for a party.
	dealingParty string
}

func (c *rowContext) addTag(tag string) {
	if tag != "" && !slices.Contains(c.tags, tag) {
		c.tags = append(c.tags, tag)
	}
}

// context works out what the 專案 and the tags of a row are in the ledger.
func (b *builder) context(r model.Row) rowContext {
	var c rowContext
	for _, t := range r.Tags {
		name := b.m.Tags[t]
		if name == "" {
			name = t
		}
		c.addTag(name)
	}
	if r.Project == "" {
		return c
	}
	p := b.m.Projects[r.Project]
	switch p.Kind {
	case model.ProjectArea:
		c.area = p.Name
	case model.ProjectProject:
		c.project = p.Name
	case model.ProjectTag:
		c.addTag(p.Name)
	case model.ProjectDealings:
		c.dealingParty = p.Party
	}
	return c
}

// The words that the import writes into its descriptions around the words of the
// rows: the descriptions of an opening balance and of an adjustment, and the labels
// of the lines that say a note. They are no part of the version of a unit; to change
// one is to change what the import writes for the same inputs, which is for
// model.FormatVersion to say.
const (
	openingDescription    = "期初金額"
	adjustmentDescription = "對帳（MOZE 的餘額調整）"
	merchantLabel         = "商家："
	projectLabel          = "專案："
)

// noteLine is the line of a description that says a note: the label of its kind, and
// what it says.
func noteLine(n model.Note) string {
	switch n.Kind {
	case model.NoteMerchant:
		return merchantLabel + n.Value
	case model.NoteProject:
		return projectLabel + n.Value
	}
	return n.Value
}

// noteLines are the lines of a description that say the notes, in their order.
func noteLines(notes []model.Note) []string {
	lines := make([]string, len(notes))
	for i, n := range notes {
		lines[i] = noteLine(n)
	}
	return lines
}

// joined is what the rows that make one unit say of themselves, where the unit has
// only one place for each thing: the first merchant, area and project that any of them
// has, in the order of the rows, and the tags of all of them. What another row says
// differently has no place, and is a note of the unit.
type joined struct {
	rowContext
	merchant string
	notes    []model.Note
}

// note adds a note to the notes, unless they have it already.
func (j *joined) note(n model.Note) {
	if !slices.Contains(j.notes, n) {
		j.notes = append(j.notes, n)
	}
}

// take puts the value in a place that has none yet, and says whether the place has
// another.
func take(place *string, value string) (clash bool) {
	switch {
	case value == "":
		return false
	case *place == "":
		*place = value
		return false
	}
	return *place != value
}

// join says what the rows say of themselves as one: what they come to in the ledger,
// by their 專案 and tags, and their merchants.
func (b *builder) join(rows ...model.Row) joined {
	var j joined
	for _, r := range rows {
		ctx := b.context(r)
		for _, t := range ctx.tags {
			j.addTag(t)
		}
		if take(&j.merchant, r.Merchant) {
			j.note(model.Note{Kind: model.NoteMerchant, Value: r.Merchant})
		}
		// The area and the project both come of the 專案 of the row, which is what the
		// note says when either is another's.
		areaClash, projectClash := take(&j.area, ctx.area), take(&j.project, ctx.project)
		if areaClash || projectClash {
			j.note(model.Note{Kind: model.NoteProject, Value: r.Project})
		}
	}
	return j
}

// keepProjectsOfParties gives a unit the note of the 專案 of each of its rows that is
// paid for a party, where the unit is not a dealing with that party: it has no place
// for it, as the party is all the mapping makes of it. The description says it as it
// says the other notes.
func (b *builder) keepProjectsOfParties() {
	rows := b.plan.RowIndex()
	for i := range b.plan.Units {
		u := &b.plan.Units[i]
		for _, k := range u.Rows {
			r := rows[k]
			p := b.m.Projects[r.Project]
			if r.Project == "" || p.Kind != model.ProjectDealings || p.Party == u.Party {
				continue
			}
			if n := (model.Note{Kind: model.NoteProject, Value: r.Project}); !slices.Contains(u.Notes, n) {
				u.Notes = append(u.Notes, n)
				u.Description = joinNonEmpty("\n", u.Description, noteLine(n))
			}
		}
	}
}

// categoryNote says which category a row had in MOZE, for the transactions that have
// no category to put it in.
func categoryNote(r model.Row) string {
	switch {
	case r.Main == "" && r.Sub == "":
		return ""
	case r.Sub == "" || r.Sub == r.Main:
		return "〔" + r.Main + "〕"
	default:
		return "〔" + r.Main + "/" + r.Sub + "〕"
	}
}

// describe is the description of a transaction that has no line to put the
// description of its row on: the name, what the row says of itself, and the
// category it had.
func describe(r model.Row, extra ...string) string {
	parts := []string{r.Name, r.Description}
	parts = append(parts, extra...)
	parts = append(parts, categoryNote(r))
	return joinNonEmpty("\n", parts...)
}

// newUnit starts a unit of one row.
func (b *builder) newUnit(kind model.UnitKind, r model.Row) model.Unit {
	return model.Unit{
		Kind:  kind,
		ID:    r.Key,
		Rows:  []string{r.Key},
		Date:  r.Date,
		Clock: r.Clock,
		Order: r.Pos,
	}
}

func (b *builder) add(u model.Unit) {
	b.plan.Units = append(b.plan.Units, u)
}

// feeOf makes the fee a row carries, which is paid from the account of the row in
// its instrument.
func (b *builder) feeOf(r model.Row) []model.Fee {
	if r.Fee.IsZero() {
		return nil
	}
	amount := r.Fee.Abs()
	return []model.Fee{{
		Account:    b.ledger(r.Account),
		Instrument: r.Currency,
		Category:   b.m.Defaults.FeeCategory,
		Amount:     amount,
		Base:       b.base(r.Currency, r.Date, amount),
	}}
}

// feeLine returns the fee of the row as a line of an expense.
func (b *builder) feeLine(r model.Row) []model.Line {
	if r.Fee.IsZero() {
		return nil
	}
	amount := r.Fee.Abs()
	return []model.Line{{
		Category: b.m.Defaults.FeeCategory,
		Amount:   amount,
		Base:     b.base(r.Currency, r.Date, amount),
	}}
}

// skipWrongSign leaves out a row whose amount is not of the sign that its type needs,
// or is nothing.
func (b *builder) skipWrongSign(r model.Row) {
	reason := model.ReasonBadSign
	if r.Amount.IsZero() {
		reason = model.ReasonZeroAmount
	}
	b.skip(reason, r)
}

// priceOf is what an expense row says was spent, as a size. It is not given for a row
// that cannot be recorded as an expense, which is left out: its amount is not money
// that went out, or it, the discount or the fee has more decimal places than the
// instrument.
func (b *builder) priceOf(r model.Row) (decimal.Decimal, bool) {
	if !r.Amount.IsNegative() {
		b.skipWrongSign(r)
		return decimal.Zero, false
	}
	price := r.Amount.Neg()
	if !b.fits(r.Currency, price, r.Discount, r.Fee) {
		b.skipWith(model.ReasonTooManyDecimals, []string{r.Currency}, r)
		return decimal.Zero, false
	}
	return price, true
}

// takesIn tells whether a row is money that came into an account, in a quantity and
// with a fee that the ledger can record. A row that is not is left out.
func (b *builder) takesIn(r model.Row) bool {
	if !r.Amount.IsPositive() {
		b.skipWrongSign(r)
		return false
	}
	if !b.fits(r.Currency, r.Amount, r.Fee) {
		b.skipWith(model.ReasonTooManyDecimals, []string{r.Currency}, r)
		return false
	}
	return true
}

// expense is a 支出: an expense, or the dealing of what was paid for a party.
func (b *builder) expense(r model.Row, ov model.RowOverride) {
	price, ok := b.priceOf(r)
	if !ok {
		return
	}
	ctx := b.context(r)
	cm := b.m.Expense[model.CategoryKey(r.Main, r.Sub)]
	ctx.addTag(cm.Tag)
	ctx.addTag(ov.Tag)

	party, dealing := "", false
	switch {
	case ov.Treatment == model.TreatmentDealing:
		party, dealing = ov.Party, true
	case ov.Treatment == "" && ctx.dealingParty != "":
		party, dealing = ctx.dealingParty, true
	case ov.Treatment == "" && cm.Treatment == model.TreatmentDealing:
		party, dealing = cm.Party, true
	}
	if dealing {
		if strings.TrimSpace(party) == "" {
			b.skip(model.ReasonNoParty, r)
			return
		}
		b.paidFor(r, party, price, ctx)
		return
	}

	path := cm.Category
	if ov.Treatment == model.TreatmentExpense && ov.Category != "" {
		path = ov.Category
	}
	if parent, _ := model.SplitCategory(path); parent == "" {
		b.skip(model.ReasonNoCategory, r)
		return
	}
	b.add(b.expenseUnit(r, path, price, ctx))
}

// expenseUnit is the unit of an expense row at the price it says, in the category of
// the path, with the fee of the row as another line. A discount is paid less and is
// kept on the line; one that pays the whole price or more makes it an expense at the
// full price and a reward of the discount, which is told when it is more.
func (b *builder) expenseUnit(r model.Row, path string, price decimal.Decimal, ctx rowContext) model.Unit {
	paid := price.Sub(r.Discount)
	u := b.newUnit(model.KindExpense, r)
	u.Description, u.Merchant = r.Name, r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags

	line := model.Line{Category: path, Memo: r.Description}
	switch {
	case r.Discount.IsPositive() && !paid.IsPositive():
		// The discount paid the whole price, or more: what the account lost is the fee
		// alone. The expense is at its full price, and the discount comes back as a
		// reward of the same account, as points paid at checkout are recorded.
		line.Amount = price
		line.Base = b.base(r.Currency, r.Date, price)
		u.Kind = model.KindExpenseReward
		reward := model.RewardPart{
			Amount:      r.Discount,
			Base:        b.base(r.Currency, r.Date, r.Discount),
			Description: joinNonEmpty(" ", r.Name, "（折抵）"),
		}
		u.Reward = &reward
		if r.Discount.GreaterThan(price) {
			b.caveat(model.CaveatDiscountOverPrice, r)
		}
	case r.Discount.IsPositive():
		d := r.Discount
		line.Amount, line.Discount = paid, &d
		line.Base = b.base(r.Currency, r.Date, paid)
	default:
		line.Amount = price
		line.Base = b.base(r.Currency, r.Date, price)
	}
	u.Lines = append([]model.Line{line}, b.feeLine(r)...)
	total := decimal.Zero
	for _, l := range u.Lines {
		total = total.Add(l.Amount)
	}
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: total}
	return u
}

// paidFor is what was paid for a party: money out of an account, and what the party
// owes me grown by it. What the party owes is all that left the account for it: the
// price, less the discount, and the fee that MOZE writes in the same row, which was part
// of paying for the party, so it is in the amount of the dealing and no expense of its
// own. A dealing has no column for a discount or for a fee, so the description says both.
func (b *builder) paidFor(r model.Row, party string, price decimal.Decimal, ctx rowContext) {
	paid := price.Sub(r.Discount)
	if !paid.IsPositive() {
		b.skip(model.ReasonZeroAmount, r)
		return
	}
	u := b.newUnit(model.KindDealing, r)
	u.Description = describe(r, discountNote(r), feeNote(r))
	u.Merchant = r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	u.Party, u.Direction = party, "paid_out"
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: paid.Add(r.Fee.Abs())}
	b.add(u)
}

func discountNote(r model.Row) string {
	if !r.Discount.IsPositive() {
		return ""
	}
	return "折扣 " + r.Discount.String() + " " + r.Currency
}

// feeNote says the fee of a row that is not a fee of its own in the ledger, as the
// description of the unit says the discount.
func feeNote(r model.Row) string {
	if r.Fee.IsZero() {
		return ""
	}
	return "手續費 " + r.Fee.Abs().String() + " " + r.Currency
}

// income is a 收入: an income, or a reward, or what a party gives back, by what the
// category of it says.
func (b *builder) income(r model.Row, ov model.RowOverride) {
	if !b.takesIn(r) {
		return
	}
	ctx := b.context(r)
	cm := b.m.Income[model.CategoryKey(r.Main, r.Sub)]
	ctx.addTag(cm.Tag)
	ctx.addTag(ov.Tag)
	if r.Sub == pendingIncomeSub {
		b.caveat(model.CaveatPendingIncome, r)
	}

	treatment, party := cm.Treatment, cm.Party
	switch ov.Treatment {
	case model.TreatmentDealing:
		treatment, party = model.TreatmentDealing, ov.Party
	case model.TreatmentReward:
		treatment = model.TreatmentReward
	case model.TreatmentIncome:
		treatment = ""
	}
	switch treatment {
	case model.TreatmentReward:
		b.rewardOf(r, ctx)
	case model.TreatmentDealing:
		if strings.TrimSpace(party) == "" {
			b.skip(model.ReasonNoParty, r)
			return
		}
		u := b.newUnit(model.KindDealing, r)
		u.Description = describe(r)
		u.Merchant = r.Merchant
		u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
		u.Party, u.Direction = party, "received"
		u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount}
		u.Fees = b.feeOf(r)
		b.add(u)
	default:
		path := cm.Category
		if ov.Treatment == model.TreatmentIncome && ov.Category != "" {
			path = ov.Category
		}
		if parent, _ := model.SplitCategory(path); parent == "" {
			b.skip(model.ReasonNoCategory, r)
			return
		}
		b.incomeOf(r, path, ctx)
	}
}

// incomeOf is an income of the category, from the row.
func (b *builder) incomeOf(r model.Row, path string, ctx rowContext) {
	u := b.newUnit(model.KindIncome, r)
	u.Description, u.Merchant = r.Name, r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	u.Lines = []model.Line{{
		Category: path,
		Amount:   r.Amount,
		Base:     b.base(r.Currency, r.Date, r.Amount),
		Memo:     r.Description,
	}}
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount}
	u.Fees = b.feeOf(r)
	b.add(u)
}

// rewardOf is a reward of the account of the row.
func (b *builder) rewardOf(r model.Row, ctx rowContext) {
	u := b.newUnit(model.KindReward, r)
	u.Description = describe(r)
	u.Merchant = r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount}
	u.Base = b.base(r.Currency, r.Date, r.Amount)
	u.Fees = b.feeOf(r)
	b.add(u)
}

// bonus is a 紅利回饋: a reward.
func (b *builder) bonus(r model.Row, ov model.RowOverride) {
	if !b.takesIn(r) {
		return
	}
	ctx := b.context(r)
	ctx.addTag(ov.Tag)
	// A reward of what was paid for a party is still a reward, and is mine: it goes to
	// no area or project of its own.
	if ctx.dealingParty != "" {
		ctx = rowContext{tags: ctx.tags}
	}
	b.rewardOf(r, ctx)
}

// receivable is a 應收款項: what a party owes me, changed by money that went out
// of an account or came into it.
func (b *builder) receivable(r model.Row, ov model.RowOverride) {
	if r.Amount.IsZero() {
		b.skip(model.ReasonZeroAmount, r)
		return
	}
	if !b.fits(r.Currency, r.Amount, r.Fee) {
		b.skipWith(model.ReasonTooManyDecimals, []string{r.Currency}, r)
		return
	}
	party := b.m.Parties[r.Party]
	if party == "" {
		party = r.Party
	}
	if ov.Treatment == model.TreatmentDealing && ov.Party != "" {
		party = ov.Party
	}
	ctx := b.context(r)
	ctx.addTag(ov.Tag)
	u := b.newUnit(model.KindDealing, r)
	u.Description = describe(r)
	u.Merchant = r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	if strings.TrimSpace(party) == "" {
		b.skip(model.ReasonNoParty, r)
		return
	}
	u.Party = party
	u.Direction = "paid_out"
	if r.Amount.IsPositive() {
		u.Direction = "received"
	}
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount.Abs()}
	u.Fees = b.feeOf(r)
	b.add(u)
}

// refund is a 退款: the ledger has no kind of its own for it, so it is an income of
// the category of refunds, with the category it had in MOZE kept in the memo.
func (b *builder) refund(r model.Row, ov model.RowOverride) {
	if !b.takesIn(r) {
		return
	}
	ctx := b.context(r)
	ctx.addTag(ov.Tag)
	b.caveat(model.CaveatRefund, r)
	u := b.newUnit(model.KindIncome, r)
	u.Description, u.Merchant = r.Name, r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	u.Lines = []model.Line{{
		Category: b.m.Defaults.RefundCategory,
		Amount:   r.Amount,
		Base:     b.base(r.Currency, r.Date, r.Amount),
		Memo:     joinNonEmpty("\n", r.Description, categoryNote(r)),
	}}
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount}
	u.Fees = b.feeOf(r)
	b.add(u)
}

// feeRow is a row of the type 手續費: an expense of the category of fees.
func (b *builder) feeRow(r model.Row, ov model.RowOverride) {
	b.expenseOf(r, ov, b.m.Defaults.FeeCategory)
}

// interest is a 利息 that no instalment plan accounts for: an expense of the
// category of interest, on the day it was charged.
func (b *builder) interest(r model.Row, ov model.RowOverride) {
	b.expenseOf(r, ov, b.m.Defaults.InterestCategory)
}

// expenseOf is an expense of one category, of the row.
func (b *builder) expenseOf(r model.Row, ov model.RowOverride, path string) {
	price, ok := b.priceOf(r)
	if !ok {
		return
	}
	ctx := b.context(r)
	ctx.addTag(ov.Tag)
	b.add(b.expenseUnit(r, path, price, ctx))
}

// opening is a 初始金額: what an account held before its first row.
func (b *builder) opening(r model.Row) {
	if r.Amount.IsZero() {
		b.plan.NoOps = append(b.plan.NoOps, model.NoOp{Reason: model.NoOpZeroOpening, Row: r.Key})
		return
	}
	day, ok := b.openings[acctCurrency{r.Account, r.Currency}]
	if !ok {
		b.skip(model.ReasonNoDate, r)
		return
	}
	if !b.fits(r.Currency, r.Amount) {
		b.skipWith(model.ReasonTooManyDecimals, []string{r.Currency}, r)
		return
	}
	if day.fallback != "" {
		b.caveat(model.CaveatOpeningDate, r, day.fallback)
	}
	ctx := b.context(r)
	u := b.newUnit(model.KindOpening, r)
	u.Date, u.Clock = day.day, ""
	u.Description = joinNonEmpty("\n", openingDescription, r.Name, r.Description)
	u.Merchant = r.Merchant
	u.Area, u.Project, u.Tags = ctx.area, ctx.project, ctx.tags
	u.Main = model.Leg{Account: b.ledger(r.Account), Instrument: r.Currency, Amount: r.Amount}
	b.add(u)
}

// adjustment is a 餘額調整, which is not recorded with the rest but when its month is
// confirmed.
func (b *builder) adjustment(r model.Row) {
	adds := r.Effect()
	if adds.IsZero() {
		b.plan.NoOps = append(b.plan.NoOps, model.NoOp{Reason: model.NoOpZeroAdjustment, Row: r.Key})
		return
	}
	if !b.fits(r.Currency, r.Amount, r.Discount, r.Fee) {
		b.skipWith(model.ReasonTooManyDecimals, []string{r.Currency}, r)
		return
	}
	ctx := b.context(r)
	a := model.Adjustment{
		Key:         model.KeyPrefix + r.Key,
		Row:         r.Key,
		Date:        r.Date,
		Clock:       r.Clock,
		Account:     b.ledger(r.Account),
		Instrument:  r.Currency,
		Amount:      adds,
		Description: joinNonEmpty("\n", adjustmentDescription, r.Name, r.Description),
		Merchant:    r.Merchant,
		Area:        ctx.area,
		Project:     ctx.project,
		Tags:        ctx.tags,
	}
	// Only a currency has an amount in the base currency to give; the quantity of any
	// other instrument is all a reconciliation takes.
	if b.isCurrency(r.Currency) {
		a.Base = b.base(r.Currency, r.Date, adds)
	}
	b.plan.Adjustments = append(b.plan.Adjustments, a)
}

// investmentCaveats notes the rows that are recorded and are an investment, or of a
// brokerage, which the ledger cannot have as trades: the export has no quantities,
// so they are the expenses, the incomes and the transfers they are in it.
func (b *builder) investmentCaveats(skipped map[string]bool) {
	for _, r := range b.rows {
		if skipped[r.Key] {
			continue
		}
		isInvestment := r.Type == model.TypeExpense || r.Type == model.TypeIncome
		if isInvestment && (r.Sub == investmentSub || r.Main == investmentSub) {
			b.caveat(model.CaveatInvestment, r)
		}
		if b.m.Accounts[r.Account].Kind == "brokerage" {
			b.caveat(model.CaveatBrokerage, r)
		}
	}
}
