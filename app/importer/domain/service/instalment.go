package service

import (
	"slices"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// instalmentGroup is a conversion of a bill into instalments as the rows of an
// export have it: the 帳單分期 rows MOZE writes at once, the 分期還款 rows that pay
// the principal back, and the 利息 rows of the same days.
type instalmentGroup struct {
	// origin are the 帳單分期 rows of the conversion, which MOZE writes in the same
	// minute, and their sum is what was converted.
	origin []model.Row
	net    decimal.Decimal
	// payments are the 分期還款 rows of the periods, in order, and interest the 利息
	// rows of each, which are the rows of the same account on the same day.
	payments []model.Row
	interest [][]model.Row
}

// rows are all the rows the group is made of.
func (g instalmentGroup) rows() []model.Row {
	rows := slices.Clone(g.origin)
	rows = append(rows, g.payments...)
	for _, in := range g.interest {
		rows = append(rows, in...)
	}
	return rows
}

// findInstalments tells the instalments in the rows. A conversion is one when the
// principals that are paid back after it, one period after the other, add up to what
// it converted exactly: the periods are then those payments, with the interest of the
// day of each. It returns the groups that reconcile, and the rows of the conversions
// that do not.
func findInstalments(rows []model.Row) (groups []instalmentGroup, unmatched []model.Row) {
	var origins, payments, interests []model.Row
	for _, r := range rows {
		switch r.Type {
		case model.TypeBillInstalment:
			origins = append(origins, r)
		case model.TypeInstalmentPayment:
			payments = append(payments, r)
		case model.TypeInterest:
			interests = append(interests, r)
		}
	}
	if len(origins) == 0 {
		return nil, nil
	}

	// The rows of a conversion are written together, in one minute.
	var clusters [][]model.Row
	for _, r := range origins {
		if n := len(clusters); n > 0 && sameMinute(clusters[n-1][0], r) {
			clusters[n-1] = append(clusters[n-1], r)
			continue
		}
		clusters = append(clusters, []model.Row{r})
	}

	claimed := map[int]bool{}
	for _, cluster := range clusters {
		g, ok := reconcileCluster(cluster, payments, interests, claimed)
		if !ok {
			unmatched = append(unmatched, cluster...)
			continue
		}
		for _, r := range g.rows() {
			claimed[r.Pos] = true
		}
		groups = append(groups, g)
	}
	return groups, unmatched
}

func sameMinute(a, b model.Row) bool {
	return a.Date.Equal(b.Date) && a.Clock == b.Clock
}

// reconcileCluster looks for the payments that make a cluster of 帳單分期 rows a plan.
func reconcileCluster(cluster, payments, interests []model.Row, claimed map[int]bool) (instalmentGroup, bool) {
	currency := cluster[0].Currency
	net := decimal.Zero
	accounts := map[string]bool{}
	for _, r := range cluster {
		if r.Currency != currency {
			return instalmentGroup{}, false
		}
		net = net.Add(r.Amount)
		accounts[r.Account] = true
	}
	if !net.IsPositive() {
		return instalmentGroup{}, false
	}

	var candidates []model.Row
	for _, p := range payments {
		if claimed[p.Pos] || !accounts[p.Account] || p.Currency != currency || !p.Amount.IsNegative() {
			continue
		}
		if p.Date.Before(cluster[0].Date) {
			continue
		}
		candidates = append(candidates, p)
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].Before(candidates[j]) })

	sum := decimal.Zero
	end := -1
	for i, p := range candidates {
		sum = sum.Add(p.Amount.Neg())
		if sum.Equal(net) {
			end = i + 1
			break
		}
		if sum.GreaterThan(net) {
			break
		}
	}
	if end < 0 {
		return instalmentGroup{}, false
	}
	g := instalmentGroup{origin: cluster, net: net, payments: candidates[:end]}
	for i, p := range g.payments {
		// The periods are one after the other, each on a later day, the first not before
		// the conversion.
		if i > 0 && !p.Date.After(g.payments[i-1].Date) {
			return instalmentGroup{}, false
		}
		var own []model.Row
		for _, in := range interests {
			if !claimed[in.Pos] && in.Account == p.Account && in.Currency == currency && in.Date.Equal(p.Date) && in.Amount.IsNegative() {
				own = append(own, in)
			}
		}
		g.interest = append(g.interest, own)
	}
	return g, true
}

// instalments records the conversions of bills into instalments that the rows
// reconcile as plans, and leaves out the rows of those that cannot be recorded: what
// is converted and what is paid back are in accounts the mapping does not make one,
// or the rows do not add up. What is left of the interest is an expense of its own.
func (b *builder) instalments() {
	groups, unmatched := findInstalments(b.rows)

	for _, g := range groups {
		all := g.rows()
		ledgers := map[string]bool{}
		var names []string
		for _, r := range all {
			if name := b.ledger(r.Account); !ledgers[name] {
				ledgers[name] = true
				names = append(names, name)
			}
		}
		if len(names) > 1 {
			slices.Sort(names)
			b.skip(model.ReasonInstalmentAccounts, strings.Join(names, "、"), all...)
			continue
		}
		if !b.instalmentFits(g) {
			b.skip(model.ReasonTooManyDecimals, g.origin[0].Currency, all...)
			continue
		}
		b.addInstalment(g, names[0])
	}
	if len(unmatched) > 0 {
		b.skip(model.ReasonInstalmentUnmatched, "", unmatched...)
	}
	// What no conversion has is left: the payments that nothing converted, which
	// nothing records, and the interest, which is an expense.
	for _, r := range b.rows {
		if !b.handled[r.Pos] && r.Type == model.TypeInstalmentPayment {
			b.skip(model.ReasonInstalmentUnmatched, "", r)
		}
	}
}

func (b *builder) instalmentFits(g instalmentGroup) bool {
	code := g.origin[0].Currency
	if !b.fits(code, g.net) {
		return false
	}
	for i, p := range g.payments {
		if !b.fits(code, p.Amount) {
			return false
		}
		for _, in := range g.interest[i] {
			if !b.fits(code, in.Amount) {
				return false
			}
		}
	}
	return true
}

// addInstalment makes the unit of a plan from its rows.
func (b *builder) addInstalment(g instalmentGroup, account string) {
	first := g.origin[0]
	keys := make([]string, 0, len(g.rows()))
	var names []string
	for _, r := range g.rows() {
		keys = append(keys, r.Key)
		b.handled[r.Pos] = true
	}
	for _, r := range g.origin {
		if r.Name != "" && !slices.Contains(names, r.Name) {
			names = append(names, r.Name)
		}
	}
	u := model.Unit{
		Kind:        model.KindBillInstalment,
		Key:         model.KeyPrefix + "plan-" + keys[0],
		Rows:        keys,
		Date:        first.Date,
		Clock:       first.Clock,
		Order:       first.Pos,
		Description: joinNonEmpty("\n", strings.Join(names, " / "), categoryNote(first)),
		Main:        model.Leg{Account: account, Instrument: first.Currency, Amount: g.net},
	}
	hasInterest := false
	for i, p := range g.payments {
		period := model.Period{DueOn: p.Date, Principal: p.Amount.Neg()}
		interest := decimal.Zero
		for _, in := range g.interest[i] {
			interest = interest.Add(in.Amount.Neg())
		}
		if interest.IsPositive() {
			period.Interest = &interest
			hasInterest = true
		}
		u.Periods = append(u.Periods, period)
	}
	if hasInterest {
		u.InterestCategory = b.m.Defaults.InterestCategory
	}
	b.add(u)
}

// mainAccount is the account of MOZE the conversion was made in: the one of its
// rows that the most was converted in.
func (g instalmentGroup) mainAccount() string {
	sums := map[string]decimal.Decimal{}
	var order []string
	for _, r := range g.origin {
		if _, seen := sums[r.Account]; !seen {
			order = append(order, r.Account)
		}
		sums[r.Account] = sums[r.Account].Add(r.Amount)
	}
	best := order[0]
	for _, a := range order[1:] {
		if sums[a].GreaterThan(sums[best]) {
			best = a
		}
	}
	return best
}
