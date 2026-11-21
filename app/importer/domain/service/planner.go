// Package service holds the rules of the MOZE import that look at many rows at
// once: how rows are guessed to be what they are in the ledger, how the two halves
// of a transfer are found, how an instalment is told from its rows, what the
// accounts hold by the export alone. They are pure: rows, a mapping and rates go
// in, a plan comes out.
package service

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// PlanInput is what a plan is made of.
type PlanInput struct {
	// Rows are the rows of the export, with their keys (model.AssignKeys).
	Rows []model.Row
	// Mapping is the user's: the guesses are added to it, and are in the plan.
	Mapping model.Mapping
	Rates   model.Rates
}

// BuildPlan works out what the export comes to in the ledger: the units that are
// recorded, the rows that are not and why, the adjustments that are made at
// confirmation, what had to be guessed, and what is wrong. It does not read or write
// anything.
func BuildPlan(in PlanInput) model.Plan {
	effective, guessed := complete(in.Rows, in.Mapping)
	b := &builder{
		rows:      in.Rows,
		m:         effective,
		rates:     in.Rates,
		handled:   make([]bool, len(in.Rows)),
		missing:   map[string]time.Time{},
		overrides: map[string]bool{},
		posOf:     make(map[string]int, len(in.Rows)),
	}
	for _, r := range in.Rows {
		b.posOf[r.Key] = r.Pos
	}
	b.plan = model.Plan{Rows: in.Rows, Mapping: effective, Guessed: guessed}
	b.openingDates()
	b.run()
	b.finish()
	return b.plan
}

// builder makes a plan.
type builder struct {
	rows  []model.Row
	m     model.Mapping
	rates model.Rates
	plan  model.Plan

	// handled marks the rows, by position, that something has been made of.
	handled []bool
	posOf   map[string]int
	// openings are the day each opening balance is dated.
	openings map[acctCurrency]openingDay
	// missing is the first day each currency needed a rate that the rates do not
	// have.
	missing map[string]time.Time
	// overrides are the keys of the overrides that were used.
	overrides map[string]bool
	problems  []model.Problem
}

type acctCurrency struct{ account, currency string }

type openingDay struct {
	day time.Time
	// fallback says the day is not from a row of the account of the opening.
	fallback string
}

func (b *builder) run() {
	b.instalments()
	b.transfers()
	for _, r := range b.rows {
		if b.handled[r.Pos] {
			continue
		}
		b.row(r)
	}
}

// row makes what there is to make of a row that no other rows have claimed.
func (b *builder) row(r model.Row) {
	b.handled[r.Pos] = true
	if !r.HasDate() && r.Type != model.TypeInitial {
		b.skip(model.ReasonNoDate, "", r)
		return
	}
	ov := b.override(r)
	if ov.Treatment == model.TreatmentSkip {
		b.skip(model.ReasonSkipped, "", r)
		return
	}
	switch r.Type {
	case model.TypeExpense:
		b.expense(r, ov)
	case model.TypeIncome:
		b.income(r, ov)
	case model.TypeBonus:
		b.bonus(r, ov)
	case model.TypeReceivable:
		b.receivable(r, ov)
	case model.TypeRefund:
		b.refund(r, ov)
	case model.TypeFee:
		b.feeRow(r, ov)
	case model.TypeInterest:
		b.interest(r, ov)
	case model.TypeInitial:
		b.opening(r)
	case model.TypeAdjustment:
		b.adjustment(r)
	default:
		b.skip(model.ReasonUnknownType, string(r.Type), r)
	}
	b.checkOverrideFits(r, ov)
}

// finish puts the units and the rest in order, works out what every row does to a
// balance, and adds what is wrong.
func (b *builder) finish() {
	p := &b.plan

	sort.SliceStable(p.Units, func(i, j int) bool { return b.unitBefore(p.Units[i], p.Units[j]) })
	for i := range p.Units {
		p.Units[i].Order = i
	}
	sort.SliceStable(p.Adjustments, func(i, j int) bool {
		a, c := p.Adjustments[i], p.Adjustments[j]
		if !a.Date.Equal(c.Date) {
			return a.Date.Before(c.Date)
		}
		if a.Clock != c.Clock {
			return a.Clock < c.Clock
		}
		return b.posOf[a.Row] < b.posOf[c.Row]
	})
	for i := range p.Adjustments {
		p.Adjustments[i].Order = i
	}
	sort.SliceStable(p.Skipped, func(i, j int) bool { return b.posOf[p.Skipped[i].Rows[0]] < b.posOf[p.Skipped[j].Rows[0]] })

	skipped := map[string]bool{}
	for _, s := range p.Skipped {
		for _, k := range s.Rows {
			skipped[k] = true
		}
	}
	for _, r := range b.rows {
		day := r.Date
		if r.Type == model.TypeInitial {
			day = b.openings[acctCurrency{r.Account, r.Currency}].day
		}
		e := model.Effect{
			Account:    b.ledger(r.Account),
			Instrument: r.Currency,
			Date:       day,
			Quantity:   r.Effect(),
			Row:        r.Key,
		}
		if e.Quantity.IsZero() || day.IsZero() {
			continue
		}
		p.Effects = append(p.Effects, e)
		if skipped[r.Key] {
			p.Unrecorded = append(p.Unrecorded, e)
		}
	}
	b.investmentCaveats(skipped)

	for _, key := range sortedStringKeys(b.m.Rows) {
		if b.overrides[key] {
			continue
		}
		if _, known := b.posOf[key]; known {
			p.Warnings = append(p.Warnings, fmt.Sprintf("rows.%s: nothing changes what the import does with this row, which is part of a plan or of a pair it does not take an override for", key))
			continue
		}
		p.Warnings = append(p.Warnings, fmt.Sprintf("rows.%s: the file has no row with this key", key))
	}
	p.Problems = append(p.Problems, b.m.Validate()...)
	p.Problems = append(p.Problems, b.problems...)
	for _, code := range sortedStringKeys(b.missing) {
		p.Problems = append(p.Problems, model.Problem{
			Subject: "rates." + code,
			Detail:  fmt.Sprintf("there is no rate of %s on or before %s", code, b.missing[code].Format(time.DateOnly)),
		})
	}
}

func (b *builder) unitBefore(x, y model.Unit) bool {
	if !x.Date.Equal(y.Date) {
		return x.Date.Before(y.Date)
	}
	if x.Clock != y.Clock {
		return x.Clock < y.Clock
	}
	return x.Order < y.Order
}

// ledger is the name of the account of the ledger that an account of MOZE is.
func (b *builder) ledger(mozeAccount string) string {
	if a, ok := b.m.Accounts[mozeAccount]; ok && a.Name != "" {
		return a.Name
	}
	return mozeAccount
}

func (b *builder) isCurrency(code string) bool {
	if code == model.BaseCurrencyCode {
		return true
	}
	return b.m.Instruments[code].IsCurrency
}

func (b *builder) decimals(code string) int {
	return b.plan.Decimals(code)
}

// override returns what the mapping says of the row, and notes that it was used.
func (b *builder) override(r model.Row) model.RowOverride {
	ov, ok := b.m.Rows[r.Key]
	if ok {
		b.overrides[r.Key] = true
	}
	return ov
}

// checkOverrideFits reports an override that asks for what the type of the row
// cannot be.
func (b *builder) checkOverrideFits(r model.Row, ov model.RowOverride) {
	if ov.Treatment == "" || ov.Treatment == model.TreatmentSkip {
		return
	}
	ok := false
	switch r.Type {
	case model.TypeExpense:
		ok = ov.Treatment == model.TreatmentExpense || ov.Treatment == model.TreatmentDealing
	case model.TypeIncome:
		ok = ov.Treatment == model.TreatmentIncome || ov.Treatment == model.TreatmentDealing || ov.Treatment == model.TreatmentReward
	case model.TypeReceivable:
		ok = ov.Treatment == model.TreatmentDealing
	case model.TypeBonus:
		ok = ov.Treatment == model.TreatmentReward
	}
	if !ok {
		b.problems = append(b.problems, model.Problem{
			Subject: "rows." + r.Key,
			Detail:  fmt.Sprintf("treatment %q does not fit a row of the type %s", ov.Treatment, r.Type),
		})
	}
}

// skip leaves rows out, and says why.
func (b *builder) skip(reason model.Reason, detail string, rows ...model.Row) {
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
		b.handled[r.Pos] = true
	}
	b.plan.Skipped = append(b.plan.Skipped, model.Skipped{Reason: reason, Rows: keys, Detail: detail})
}

func (b *builder) caveat(kind model.CaveatKind, r model.Row, detail string) {
	b.plan.Caveats = append(b.plan.Caveats, model.Caveat{Kind: kind, Row: r.Key, Detail: detail})
}

// base returns the amount in the base currency of an amount of a currency on a
// day, or nil for the base currency. A rate the rates lack is noted, and gives a
// minimum amount so that the unit is still made: the plan is not one to record from
// until the rates are filled in.
func (b *builder) base(code string, day time.Time, amount decimal.Decimal) *decimal.Decimal {
	if code == model.BaseCurrencyCode {
		return nil
	}
	rateCode := code
	if as := b.m.Instruments[code].RateAs; as != "" {
		rateCode = as
	}
	rate, ok := b.rates.Lookup(rateCode, day)
	if !ok {
		if first, seen := b.missing[rateCode]; !seen || day.Before(first) {
			b.missing[rateCode] = day
		}
		minimum := decimal.New(1, -model.BaseDecimals)
		return &minimum
	}
	base := model.BaseAmountOf(rate, amount)
	if base.IsZero() {
		// An amount too small to be worth a cent still has to be above nothing.
		base = decimal.New(1, -model.BaseDecimals)
	}
	return &base
}

// fits tells whether the quantities have no more decimal places than the instrument.
func (b *builder) fits(code string, quantities ...decimal.Decimal) bool {
	d := b.decimals(code)
	for _, q := range quantities {
		if !quantityDecimals(q, d) {
			return false
		}
	}
	return true
}

// openingDates works out the day of the opening balance of each account and
// currency: the day before the first row with a day of that MOZE account in that
// currency. Where the account has no such row it is the day before the first row of
// the account of the ledger it is, and at last the day before the first row of the
// file.
func (b *builder) openingDates() {
	b.openings = map[acctCurrency]openingDay{}
	own := map[acctCurrency]time.Time{}
	ledgerFirst := map[string]time.Time{}
	var fileFirst time.Time
	for _, r := range b.rows {
		if !r.HasDate() {
			continue
		}
		k := acctCurrency{r.Account, r.Currency}
		if first, ok := own[k]; !ok || r.Date.Before(first) {
			own[k] = r.Date
		}
		l := b.ledger(r.Account)
		if first, ok := ledgerFirst[l]; !ok || r.Date.Before(first) {
			ledgerFirst[l] = r.Date
		}
		if fileFirst.IsZero() || r.Date.Before(fileFirst) {
			fileFirst = r.Date
		}
	}
	for _, r := range b.rows {
		if r.Type != model.TypeInitial {
			continue
		}
		k := acctCurrency{r.Account, r.Currency}
		switch {
		case !own[k].IsZero():
			b.openings[k] = openingDay{day: own[k].AddDate(0, 0, -1)}
		case !ledgerFirst[b.ledger(r.Account)].IsZero():
			b.openings[k] = openingDay{day: ledgerFirst[b.ledger(r.Account)].AddDate(0, 0, -1), fallback: "the first row of its account of the ledger"}
		case !fileFirst.IsZero():
			b.openings[k] = openingDay{day: fileFirst.AddDate(0, 0, -1), fallback: "the first row of the file"}
		}
	}
}

func sortedStringKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// joinNonEmpty joins the parts that are not empty.
func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, strings.TrimSpace(p))
		}
	}
	return strings.Join(kept, sep)
}
