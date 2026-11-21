package model

import (
	"slices"
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

// UnitKind is what a unit of the plan is recorded as in the ledger.
type UnitKind string

// The kinds of unit. Each is recorded with the use case that has its name.
const (
	// KindExpense is RecordExpense.
	KindExpense UnitKind = "expense"
	// KindExpenseReward is an expense at its full price that the discount paid for
	// as a whole, which is RecordExpense and RecordReward: two transactions, as
	// points paid at checkout are recorded.
	KindExpenseReward UnitKind = "expense_reward"
	// KindIncome is RecordIncome.
	KindIncome UnitKind = "income"
	// KindReward is RecordReward.
	KindReward UnitKind = "reward"
	// KindDealing is RecordDealing.
	KindDealing UnitKind = "dealing"
	// KindTransfer is RecordTransfer.
	KindTransfer UnitKind = "transfer"
	// KindExchange is RecordExchange.
	KindExchange UnitKind = "exchange"
	// KindTrade is RecordTrade.
	KindTrade UnitKind = "trade"
	// KindOpening is SetOpeningBalance.
	KindOpening UnitKind = "opening"
	// KindBillInstalment is RecordBillInstalment: a plan, with its periods.
	KindBillInstalment UnitKind = "bill_instalment"
)

// RewardKeySuffix follows the key of a KindExpenseReward unit to make the key of its
// second transaction, the reward.
const RewardKeySuffix = "#reward"

// Leg is an amount of an instrument in an account of the ledger, by their names.
type Leg struct {
	Account    string
	Instrument string
	// Amount is a size, greater than zero: what leaves or comes in, by the unit's
	// kind. For the instrument of a trade that is not a currency it is the quantity.
	Amount decimal.Decimal
}

// Line is a part of an expense or of an income, that goes to one category.
type Line struct {
	// Category is the path of a category of the ledger.
	Category string
	// Amount is what was paid, after the discount, or received.
	Amount decimal.Decimal
	// Base is Amount in the base currency, for a currency that is not it.
	Base     *decimal.Decimal
	Discount *decimal.Decimal
	Memo     string
}

// Fee is a charge that came with a unit, paid from an account in an instrument.
type Fee struct {
	Account    string
	Instrument string
	// Category is the path of an expense category.
	Category string
	Amount   decimal.Decimal
	Base     *decimal.Decimal
	// Memo is the text that came with the fee: what the row it is of says.
	Memo string
}

// Period is one period of an instalment plan.
type Period struct {
	DueOn     time.Time
	Principal decimal.Decimal
	Interest  *decimal.Decimal
}

// RewardPart is the reward of a KindExpenseReward unit.
type RewardPart struct {
	Amount      decimal.Decimal
	Base        *decimal.Decimal
	Description string
}

// NoteKind says what a unit has no place for.
type NoteKind string

// The things a unit can have no place for.
const (
	// NoteMerchant is the merchant of a row of the unit, where the unit has the merchant
	// of another row.
	NoteMerchant NoteKind = "merchant"
	// NoteProject is a value of 專案 of a row of the unit that the unit has no place for:
	// another row's area or project is the unit's, or the 專案 is paid for a party and
	// the unit is not a dealing with that party.
	NoteProject NoteKind = "project"
)

// Note is something that a row of a unit says and that the unit has no single place
// for. The plan keeps it as a line of the Description.
//
// Which notes a unit has is not in its rows alone: whether two rows say different
// areas or projects is for the mapping to say. So a note is a value of its own, and
// the Version is made of it, not of the line that the plan writes for it.
type Note struct {
	Kind NoteKind
	// Value is what the row says: the merchant, or the value of 專案.
	Value string
}

// Unit is one thing the plan records in the ledger: one transaction, or two for a
// KindExpenseReward, or a whole instalment plan. It is made of one row of the
// export, or of the rows that are halves of one transaction, and says it by the names
// of the mapping, so that it can be recorded once the ledger has what it names.
//
// A unit is told from the others by its ID, which comes of the rows it is made of,
// and from its own earlier versions by its Version, which comes of its inputs: the
// rows it is made of, and what the mapping, the row overrides and the rates make of
// them. A change of any of those is a new version of the same unit, and so another
// idempotency key (see Key and Fingerprint).
type Unit struct {
	Kind UnitKind
	// ID is made of the keys of the rows: the key of the row for a unit of one row,
	// "<out>+<in>" for the two halves of a transfer, an exchange or a trade, and
	// "plan-<first row>" for an instalment plan.
	ID string
	// Version is the fingerprint of the unit's inputs (Fingerprint); the plan sets it
	// when it is made.
	Version string
	// Rows are the keys of the rows of the export it is made of.
	Rows []string
	// Date is the day it happened on, at midnight UTC; Clock is the time of day, or
	// empty.
	Date  time.Time
	Clock string
	// Order is its place in the order the plan records and confirms things: by day,
	// time of day and then the file.
	Order int

	// Description and Merchant are the text of the transaction: words of the rows the
	// unit is made of, and the lines the plan writes around them (see Notes). They are
	// not in the Version, which has the rows whole instead, and FormatVersion for the
	// wording.
	Description string
	Merchant    string
	// Area and Project are by their names in the ledger, empty for none. Tags too.
	Area    string
	Project string
	Tags    []string
	// Notes are what the rows of the unit say that it has no single place for, which the
	// plan also writes as lines of the Description. They are in the Version.
	Notes []Note

	// Main is what the unit does to the account it is of: the account an expense is
	// paid from, an income comes into, the account a transfer or an exchange leaves,
	// the account that holds what a trade buys or sells. For an expense its amount is
	// what the account pays, the lines and the fees together.
	Main Leg
	// Other is the second leg of a transfer, an exchange or a trade: where the money
	// arrives, or for a trade the account and the currency it is paid in.
	Other *Leg
	Lines []Line
	// Direction is "paid_out" or "received" for a dealing and "buy" or "sell" for a
	// trade.
	Direction string
	Party     string
	// Base is the amount of a reward in the base currency.
	Base *decimal.Decimal
	// Reward is the second transaction of a KindExpenseReward.
	Reward *RewardPart
	Fees   []Fee
	// Periods and InterestCategory make a KindBillInstalment; its principal is
	// Main.Amount and its day is Date.
	Periods          []Period
	InterestCategory string
}

// Month is the month the unit happened in.
func (u Unit) Month() string { return MonthOf(u.Date) }

// Adjustment is a 餘額調整 of MOZE: what MOZE added to or took from an account to
// make it say what the account really held. The import does not record it with the
// rest: it reconciles the account (對帳) when the month of the adjustment is
// confirmed, once everything of the account before it is.
type Adjustment struct {
	// Key is the idempotency key of the reconciliation, and Row the key of the row.
	Key string
	Row string
	// Date and Clock are as the row has them.
	Date  time.Time
	Clock string
	Order int
	// Account and Instrument are by name and by code.
	Account    string
	Instrument string
	// Amount is what the row adds to the account: not zero.
	Amount decimal.Decimal
	// Base is the amount in the base currency, for a currency that is not it.
	Base *decimal.Decimal
	// Description, Merchant, Area, Project and Tags are what the row says of itself,
	// as a Unit has them.
	Description string
	Merchant    string
	Area        string
	Project     string
	Tags        []string
}

// Month is the month the adjustment is in.
func (a Adjustment) Month() string { return MonthOf(a.Date) }

// Reason says why rows cannot be recorded. Skipped.Args are the values its text fills
// in, which each reason says; none is given for a reason that does not.
type Reason string

// The reasons a row, or the rows of a transaction, are not recorded.
const (
	// ReasonUnknownType is a record type the import does not know. Args: the type.
	ReasonUnknownType Reason = "unknown_type"
	// ReasonBadSign is an amount of the wrong sign for its record type.
	ReasonBadSign Reason = "bad_sign"
	// ReasonZeroAmount is an amount that is nothing where the ledger needs some.
	ReasonZeroAmount Reason = "zero_amount"
	// ReasonTooManyDecimals is a quantity with more decimal places than its
	// instrument has. Args: the codes of the instruments, one or two.
	ReasonTooManyDecimals Reason = "too_many_decimals"
	// ReasonZeroQuantity is the purchase or sale of an instrument that is not a
	// currency whose quantity the export has as zero: it cannot be a trade. Args: the
	// code of the instrument.
	ReasonZeroQuantity Reason = "zero_quantity"
	// ReasonNotCurrencies is a pair whose two sides are not currencies and not one
	// currency and one other instrument: nothing records it. Args: the two codes.
	ReasonNotCurrencies Reason = "not_currencies"
	// ReasonUnpaired is a 轉出 or 轉入 that has no other half: a minute with more of
	// one than of the other. Args: how many 轉出 and how many 轉入 the minute has.
	ReasonUnpaired Reason = "unpaired"
	// ReasonNoPairing is 轉出 and 轉入 of one minute, as many of each, whose amounts
	// fit no way of pairing them.
	ReasonNoPairing Reason = "no_pairing"
	// ReasonAmbiguousPair is 轉出 and 轉入 of one minute that can be paired in more
	// than one way. Args: how many 轉出 and how many 轉入 the minute has.
	ReasonAmbiguousPair Reason = "ambiguous_pair"
	// ReasonAmountMismatch is a 轉出 and a 轉入 of one instrument whose amounts
	// differ.
	ReasonAmountMismatch Reason = "amount_mismatch"
	// ReasonSameAccount is a transfer whose two sides are one account of the ledger.
	// Args: the account.
	ReasonSameAccount Reason = "same_account"
	// ReasonInstalmentUnmatched is a row of an instalment (帳單分期, 分期還款, 利息)
	// that no plan accounts for.
	ReasonInstalmentUnmatched Reason = "instalment_unmatched"
	// ReasonInstalmentAccounts is an instalment whose rows reconcile but are in
	// accounts the mapping does not make one. Args: the accounts of the ledger.
	ReasonInstalmentAccounts Reason = "instalment_accounts"
	// ReasonSkipped is a row the user told the import to leave out.
	ReasonSkipped Reason = "skipped"
	// ReasonNoDate is a row that needs a day and has none: an opening balance of an
	// account with no row at all to put it before.
	ReasonNoDate Reason = "no_date"
	// ReasonNoParty is a dealing whose party is not known.
	ReasonNoParty Reason = "no_party"
	// ReasonNoCategory is an expense or an income whose category is not known.
	ReasonNoCategory Reason = "no_category"
)

// Skipped is what is not recorded: the rows, and why.
type Skipped struct {
	Reason Reason
	// Rows are the keys of the rows, in the order of the file.
	Rows []string
	// Args say more of why, for the reasons that have values to fill in.
	Args []string
}

// NoOpReason says why a row has nothing to record.
type NoOpReason string

// The rows that need nothing recorded.
const (
	// NoOpZeroOpening is an opening balance of nothing.
	NoOpZeroOpening NoOpReason = "zero_opening"
	// NoOpZeroAdjustment is a 餘額調整 that adds nothing.
	NoOpZeroAdjustment NoOpReason = "zero_adjustment"
)

// NoOp is a row that needs nothing recorded.
type NoOp struct {
	Reason NoOpReason
	Row    string
}

// CaveatKind says what to know of a row that is recorded.
type CaveatKind string

// The things the plan tells of rows that it does record.
const (
	// CaveatInvestment is an expense or an income of the category 投資: not a trade,
	// since the export has no quantities, so recorded as the expense or the income
	// it is in the export.
	CaveatInvestment CaveatKind = "investment"
	// CaveatBrokerage is a row of an account that is a brokerage: it holds what it
	// is worth in the base currency, with no quantities.
	CaveatBrokerage CaveatKind = "brokerage"
	// CaveatDiscountOverPrice is a discount that is more than the price: recorded as
	// the expense at the price and a reward of the discount.
	CaveatDiscountOverPrice CaveatKind = "discount_over_price"
	// CaveatSameInstrumentExchange is a 兌換 between two accounts in one instrument,
	// which cannot be an exchange: recorded as a transfer.
	CaveatSameInstrumentExchange CaveatKind = "same_instrument_exchange"
	// CaveatPendingIncome is an income 收款 whose source is not known: recorded as
	// the mapping says, by default as an income of a category to be checked.
	CaveatPendingIncome CaveatKind = "pending_income"
	// CaveatRefund is a 退款, which the ledger has no kind for: recorded as an income.
	CaveatRefund CaveatKind = "refund"
	// CaveatOpeningDate is an opening balance of an account that has no row of its
	// own to put it before. Args: where its day comes from, OpeningFromLedgerAccount
	// or OpeningFromFile.
	CaveatOpeningDate CaveatKind = "opening_date"
)

// Caveat is a remark about a row that is recorded.
type Caveat struct {
	Kind CaveatKind
	Row  string
	// Args are the values the text of the remark fills in; see the kind.
	Args []string
}

// Effect is what one row does to the balance of an account in an instrument, on its
// day.
type Effect struct {
	Account    string
	Instrument string
	Date       time.Time
	Quantity   decimal.Decimal
	// Row is the key of the row.
	Row string
}

// Pair is an account of the ledger and an instrument, by name and code.
type Pair struct {
	Account    string
	Instrument string
}

// Balances are what accounts hold of instruments.
type Balances map[Pair]decimal.Decimal

// Plan is what the import makes of an export, a mapping and rates: what it records,
// what it does not and why, what it had to guess, and what the balances of the
// accounts are by the export alone.
type Plan struct {
	Rows []Row
	// Mapping is the mapping the plan was made with: the user's, and the entries
	// that were guessed.
	Mapping Mapping
	// Guessed is what the user's mapping lacked and the import guessed: the entries
	// to add to it. A plan that has any is not one to record from.
	Guessed Mapping
	// Problems are what is wrong with the mapping or the rates; a plan that has any
	// is not one to record from either.
	Problems []Problem
	// Warnings are what is odd and does not stop anything.
	Warnings []Warning

	// Units are what is recorded, in order.
	Units []Unit
	// Adjustments are reconciled when their month is confirmed.
	Adjustments []Adjustment
	// NoOps are the rows that need nothing.
	NoOps []NoOp
	// Skipped are the rows that cannot be recorded.
	Skipped []Skipped
	Caveats []Caveat

	// Effects are what every row does to a balance, those that are not recorded as
	// well as those that are.
	Effects []Effect
	// Unrecorded are the effects of the rows in Skipped: what the ledger will not
	// have.
	Unrecorded []Effect
	// Differences are the units whose effect, as the ledger will have it, is not what
	// their rows say: a fee or a discount the ledger has no place for. What they come
	// to is in Unrecorded as well, so that a difference of the balance that they make
	// is one that the plan explains.
	Differences []Difference
	// Projects say what the rows of every value of 專案 come to, with the period the
	// import would suggest for a project; see ProjectEvidence.
	Projects []ProjectEvidence
}

// Blocked tells whether the plan is not one to record from: it has something to
// map, or something wrong.
func (p Plan) Blocked() bool {
	return !p.Guessed.Empty() || len(p.Problems) > 0
}

// RowIndex returns the rows by their keys.
func (p Plan) RowIndex() map[string]Row {
	idx := make(map[string]Row, len(p.Rows))
	for _, r := range p.Rows {
		idx[r.Key] = r
	}
	return idx
}

// Decimals is how many decimal places the instrument has.
func (p Plan) Decimals(code string) int {
	if code == BaseCurrencyCode {
		return BaseDecimals
	}
	if in, ok := p.Mapping.Instruments[code]; ok {
		return in.Decimals
	}
	return 2
}

// Months lists the months the plan has anything in, from the first to the last,
// each once, as "2006-01": a month has something in it when a row has an effect in
// it, or a unit or an adjustment is dated in it. (A month whose rows all come to
// nothing, as an expense that a discount paid in full does, still has drafts to
// confirm.)
func (p Plan) Months() []string {
	var first, last time.Time
	note := func(day time.Time) {
		if day.IsZero() {
			return
		}
		if first.IsZero() || day.Before(first) {
			first = day
		}
		if last.IsZero() || day.After(last) {
			last = day
		}
	}
	for _, e := range p.Effects {
		note(e.Date)
	}
	for _, u := range p.Units {
		note(u.Date)
	}
	for _, a := range p.Adjustments {
		note(a.Date)
	}
	if first.IsZero() {
		return nil
	}
	var months []string
	for m := firstOfMonth(first); !m.After(last); m = m.AddDate(0, 1, 0) {
		months = append(months, MonthOf(m))
	}
	return months
}

// Days are the first and the last day that a row of the export has: an opening balance
// has none, and the day an opening balance is dated from its account is not one of
// them. Both are zero when no row has a day.
func (p Plan) Days() (first, last time.Time) {
	for _, r := range p.Rows {
		if !r.HasDate() {
			continue
		}
		if first.IsZero() || r.Date.Before(first) {
			first = r.Date
		}
		if last.IsZero() || r.Date.After(last) {
			last = r.Date
		}
	}
	return first, last
}

// MonthEnd is the last day of the month "2006-01", at midnight UTC.
func MonthEnd(month string) (time.Time, error) {
	first, err := time.Parse("2006-01", month)
	if err != nil {
		return time.Time{}, err
	}
	return first.AddDate(0, 1, -1), nil
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

// ExpectedAt is what the accounts hold on the day by the export alone: the effects
// of every row up to it, the opening balances in their place. A pair with nothing
// up to the day is not in it.
func (p Plan) ExpectedAt(day time.Time) Balances {
	return sumUntil(p.Effects, day)
}

// UnrecordedAt is what the rows that are not recorded add to the accounts up to the
// day: what the ledger lacks, and so what explains a difference from ExpectedAt.
func (p Plan) UnrecordedAt(day time.Time) Balances {
	return sumUntil(p.Unrecorded, day)
}

func sumUntil(effects []Effect, day time.Time) Balances {
	out := Balances{}
	for _, e := range effects {
		if e.Date.After(day) {
			continue
		}
		k := Pair{e.Account, e.Instrument}
		out[k] = out[k].Add(e.Quantity)
	}
	return out
}

// Pairs lists the pairs of the balances in order.
func (b Balances) Pairs() []Pair {
	pairs := make([]Pair, 0, len(b))
	for k := range b {
		pairs = append(pairs, k)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Account != pairs[j].Account {
			return pairs[i].Account < pairs[j].Account
		}
		return pairs[i].Instrument < pairs[j].Instrument
	})
	return pairs
}

// Accounts lists the accounts of the ledger the plan uses, in order, each once.
func (p Plan) Accounts() []string {
	seen := map[string]bool{}
	for _, a := range p.Mapping.Accounts {
		seen[a.Name] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}
