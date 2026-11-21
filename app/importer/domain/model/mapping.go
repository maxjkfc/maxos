package model

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// BaseCurrencyCode is the code of the base currency, TWD.
const BaseCurrencyCode = "TWD"

// Mapping says what the things of a MOZE export are in the ledger: which account,
// category, area, project, tag and party each name stands for, and what to do
// with single rows that are not what the rest of their kind are. The user reviews
// and keeps it; the import only adds the entries it has to guess (Plan.Guessed).
//
// Every map is by the name MOZE uses.
type Mapping struct {
	// Instruments are by currency code. TWD, the base currency, is the ledger's
	// own and needs no entry.
	Instruments map[string]InstrumentMap
	// Accounts are by the name of the MOZE account. Several can be one account of
	// the ledger: a MOZE account holds one currency, a ledger account holds many.
	Accounts map[string]AccountMap
	// Projects are by the value of the 專案 column.
	Projects map[string]ProjectMap
	// Expense and Income are by "主類別/子類別", of the expenses and of the incomes.
	Expense map[string]CategoryMap
	Income  map[string]CategoryMap
	// Parties and Tags are by the name MOZE has, and give the name of the ledger.
	Parties  map[string]string
	Tags     map[string]string
	Defaults Defaults
	// Rows are by the key of a row (Row.Key), and change what is done with it.
	Rows map[string]RowOverride
}

// The kinds of instrument and of account, as the ledger has them.
var (
	InstrumentKinds = []string{"fiat", "crypto", "stock", "fund"}
	AccountKinds    = []string{"cash", "bank", "card", "exchange", "brokerage", "wallet", "loan"}
)

// InstrumentMap is an instrument the ledger is to have.
type InstrumentMap struct {
	Name       string
	Kind       string
	IsCurrency bool
	Decimals   int
	// RateAs, when set, is the code whose rates this instrument takes: USDT is
	// worth what USD is.
	RateAs string
}

// AccountMap is an account the ledger is to have.
type AccountMap struct {
	// Name is the name of the account in the ledger.
	Name string
	Kind string
	// Institution is empty for an account that has none.
	Institution string
	// ClosingDay and PaymentDueDay are the days of the statements of a card, from 1
	// to 31, and zero when not given.
	ClosingDay    int
	PaymentDueDay int
}

// What a value of the 專案 column is in the ledger.
const (
	// ProjectArea is an area of the ledger.
	ProjectArea = "area"
	// ProjectProject is a project of the ledger.
	ProjectProject = "project"
	// ProjectTag is a tag the rows carry.
	ProjectTag = "tag"
	// ProjectDealings is a party the user paid for: an expense of it is not an
	// expense but what the party owes, so it is recorded as a dealing.
	ProjectDealings = "dealings"
	// ProjectNone is nothing: the rows go to no area and no project of their own.
	ProjectNone = "none"
)

// ProjectMap is what a value of the 專案 column is.
type ProjectMap struct {
	Kind string
	// Name is the name of the area, the project or the tag in the ledger.
	Name string
	// DefaultArea is the area the transactions of a project go to, by its name in
	// the ledger.
	DefaultArea string
	// ActiveFrom and ActiveTo are the days a project is under way, both given or
	// neither (the zero time).
	ActiveFrom time.Time
	ActiveTo   time.Time
	// Party is the party of a ProjectDealings, by its name in the ledger.
	Party string
}

// What a category entry or a row override can make of the rows that have it,
// besides an expense or an income of a category.
const (
	TreatmentIncome  = "income"
	TreatmentExpense = "expense"
	TreatmentReward  = "reward"
	TreatmentDealing = "dealing"
	TreatmentSkip    = "skip"
)

// CategoryMap is what a 主類別/子類別 is.
type CategoryMap struct {
	// Category is the category of the ledger as a path: "名稱" for a top-level
	// category, "父類別/子類別" for one under another. It is not used when
	// Treatment is set.
	Category string
	// Treatment changes what is recorded: TreatmentReward records the rows as
	// rewards, TreatmentDealing as what Party gives back (an income) or is paid (an
	// expense); empty is an expense or an income of Category.
	Treatment string
	Party     string
	// Tag is a tag the rows carry, by its name in the ledger.
	Tag string
}

// Defaults are the categories of what has none of its own.
type Defaults struct {
	// FeeCategory is the expense category of the 手續費 column and of the rows of
	// the type 手續費.
	FeeCategory string
	// InterestCategory is the expense category of interest.
	InterestCategory string
	// RefundCategory is the income category of a refund.
	RefundCategory string
}

// RowOverride changes what the import does with one row, which is found by its key.
// It is how a row that was guessed wrong is put right before its month is
// confirmed.
type RowOverride struct {
	// Treatment is one of the Treatment values; empty leaves what the row is.
	Treatment string
	// Category is the category of the ledger, for an expense or an income.
	Category string
	// Party is the party of a dealing.
	Party string
	// Tag is a tag to add.
	Tag string
	// Quantity is how much of a non-currency instrument a trade moved, for a pair of
	// rows whose export has it as zero.
	Quantity *decimal.Decimal
}

// Empty tells whether the mapping has nothing in it.
func (m Mapping) Empty() bool {
	return len(m.Instruments)+len(m.Accounts)+len(m.Projects)+len(m.Expense)+len(m.Income)+
		len(m.Parties)+len(m.Tags)+len(m.Rows) == 0 && m.Defaults == (Defaults{})
}

// Merge returns the mapping with the entries of other that it does not have.
func (m Mapping) Merge(other Mapping) Mapping {
	out := Mapping{
		Instruments: mergeMap(m.Instruments, other.Instruments),
		Accounts:    mergeMap(m.Accounts, other.Accounts),
		Projects:    mergeMap(m.Projects, other.Projects),
		Expense:     mergeMap(m.Expense, other.Expense),
		Income:      mergeMap(m.Income, other.Income),
		Parties:     mergeMap(m.Parties, other.Parties),
		Tags:        mergeMap(m.Tags, other.Tags),
		Rows:        mergeMap(m.Rows, other.Rows),
		Defaults:    m.Defaults,
	}
	if out.Defaults.FeeCategory == "" {
		out.Defaults.FeeCategory = other.Defaults.FeeCategory
	}
	if out.Defaults.InterestCategory == "" {
		out.Defaults.InterestCategory = other.Defaults.InterestCategory
	}
	if out.Defaults.RefundCategory == "" {
		out.Defaults.RefundCategory = other.Defaults.RefundCategory
	}
	return out
}

func mergeMap[V any](a, b map[string]V) map[string]V {
	out := make(map[string]V, len(a)+len(b))
	for k, v := range b {
		out[k] = v
	}
	for k, v := range a {
		out[k] = v
	}
	return out
}

// SplitCategory splits the path of a category of the ledger into the top-level
// category and the one under it, which is empty for a top-level category.
func SplitCategory(path string) (parent, child string) {
	parent, child, found := strings.Cut(strings.TrimSpace(path), "/")
	if !found {
		return strings.TrimSpace(parent), ""
	}
	return strings.TrimSpace(parent), strings.TrimSpace(child)
}

// CategoryKey is the key of the category of a row in the mapping.
func CategoryKey(main, sub string) string {
	return main + "/" + sub
}

// Problem is something wrong with the mapping or the rates that has to be put right
// before anything is recorded.
type Problem struct {
	// Subject is what is wrong: an entry of the mapping, or a currency.
	Subject string
	Detail  string
}

func (p Problem) String() string { return p.Subject + ": " + p.Detail }

// Validate checks that what the mapping says can be made of it, and returns every
// problem it finds: kinds that are not kinds, names that are empty, a card day out
// of range, an area that no entry makes, an account that two entries make in two
// ways.
func (m Mapping) Validate() []Problem {
	var problems []Problem
	add := func(subject, format string, args ...any) {
		problems = append(problems, Problem{Subject: subject, Detail: fmt.Sprintf(format, args...)})
	}

	for _, code := range sortedKeys(m.Instruments) {
		in := m.Instruments[code]
		subject := "instruments." + code
		if strings.TrimSpace(in.Name) == "" {
			add(subject, "name is empty")
		}
		if !slices.Contains(InstrumentKinds, in.Kind) {
			add(subject, "kind %q is not one of %s", in.Kind, strings.Join(InstrumentKinds, ", "))
		}
		if in.Decimals < 0 || in.Decimals > 18 {
			add(subject, "decimals %d is not from 0 to 18", in.Decimals)
		}
	}

	type shape struct{ kind, institution string }
	byName := map[string]shape{}
	for _, name := range sortedKeys(m.Accounts) {
		a := m.Accounts[name]
		subject := "accounts." + name
		if strings.TrimSpace(a.Name) == "" {
			add(subject, "name is empty")
			continue
		}
		if !slices.Contains(AccountKinds, a.Kind) {
			add(subject, "kind %q is not one of %s", a.Kind, strings.Join(AccountKinds, ", "))
		}
		if a.ClosingDay != 0 && (a.ClosingDay < 1 || a.ClosingDay > 31) {
			add(subject, "closing_day %d is not from 1 to 31", a.ClosingDay)
		}
		if a.PaymentDueDay != 0 && (a.PaymentDueDay < 1 || a.PaymentDueDay > 31) {
			add(subject, "payment_due_day %d is not from 1 to 31", a.PaymentDueDay)
		}
		if (a.ClosingDay != 0 || a.PaymentDueDay != 0) && a.Kind != "card" {
			add(subject, "only a card has closing_day and payment_due_day")
		}
		if a.PaymentDueDay != 0 && a.ClosingDay == 0 {
			add(subject, "payment_due_day needs closing_day")
		}
		// MOZE accounts that are one account of the ledger must agree on what it is.
		s := shape{a.Kind, a.Institution}
		if prev, ok := byName[a.Name]; ok && prev != s {
			add(subject, "is the account %q of the ledger, which another entry makes a %s of %q: kind and institution must agree",
				a.Name, prev.kind, prev.institution)
		}
		byName[a.Name] = s
	}

	areas := map[string]bool{}
	for _, value := range sortedKeys(m.Projects) {
		if p := m.Projects[value]; p.Kind == ProjectArea {
			areas[p.Name] = true
		}
	}
	for _, value := range sortedKeys(m.Projects) {
		p := m.Projects[value]
		subject := "projects." + value
		switch p.Kind {
		case ProjectArea, ProjectProject, ProjectTag:
			if strings.TrimSpace(p.Name) == "" {
				add(subject, "name is empty")
			}
		case ProjectDealings:
			if strings.TrimSpace(p.Party) == "" {
				add(subject, "a dealings project needs the party it is paid for")
			}
		case ProjectNone:
		default:
			add(subject, "kind %q is not one of area, project, tag, dealings, none", p.Kind)
		}
		if p.Kind == ProjectProject && p.DefaultArea != "" && !areas[p.DefaultArea] {
			add(subject, "default_area %q is not an area of the mapping", p.DefaultArea)
		}
		if p.ActiveFrom.IsZero() != p.ActiveTo.IsZero() {
			add(subject, "active_period needs both from and to")
		}
		if !p.ActiveTo.IsZero() && p.ActiveTo.Before(p.ActiveFrom) {
			add(subject, "active_period ends before it starts")
		}
		if p.Kind != ProjectProject && (p.DefaultArea != "" || !p.ActiveFrom.IsZero()) {
			add(subject, "only a project has a default_area and an active_period")
		}
	}

	checkCategories := func(section string, entries map[string]CategoryMap) {
		for _, key := range sortedKeys(entries) {
			c := entries[key]
			subject := "categories." + section + "." + key
			switch c.Treatment {
			case "", TreatmentReward, TreatmentDealing:
			default:
				add(subject, "treatment %q is not one of reward, dealing", c.Treatment)
			}
			if c.Treatment == TreatmentDealing && strings.TrimSpace(c.Party) == "" {
				add(subject, "a dealing needs its party")
			}
			if c.Treatment == "" {
				if parent, _ := SplitCategory(c.Category); parent == "" {
					add(subject, "category is empty")
				}
			}
		}
	}
	checkCategories("expense", m.Expense)
	checkCategories("income", m.Income)

	for _, key := range sortedKeys(m.Rows) {
		o := m.Rows[key]
		subject := "rows." + key
		switch o.Treatment {
		case "", TreatmentIncome, TreatmentExpense, TreatmentReward, TreatmentDealing, TreatmentSkip:
		default:
			add(subject, "treatment %q is not one of income, expense, reward, dealing, skip", o.Treatment)
		}
		if o.Treatment == TreatmentDealing && strings.TrimSpace(o.Party) == "" {
			add(subject, "a dealing needs its party")
		}
	}
	return problems
}

// sortedKeys returns the keys of m in order.
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
