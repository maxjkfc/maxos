package model

import (
	"slices"
	"strconv"
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
	// OpeningDate is the day the opening balances of the account are dated, at
	// midnight UTC; the zero time leaves it to the rows: the day before the first row
	// of the account. It is how an opening balance is kept where it was when the
	// first row of the account has changed.
	OpeningDate time.Time
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

// ProjectMap is what a value of the 專案 column is. It has no active period: the
// import never sets one, since the ledger puts every transaction that has no project
// of its own into the project that is active on its day.
type ProjectMap struct {
	Kind string
	// Name is the name of the area, the project or the tag in the ledger.
	Name string
	// DefaultArea is the area the transactions of a project go to, by its name in
	// the ledger.
	DefaultArea string
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

// The sections of a mapping, as the file names them, which EntryRef says an entry is
// in.
const (
	SectionInstruments = "instruments"
	SectionAccounts    = "accounts"
	SectionProjects    = "projects"
	SectionExpense     = "categories.expense"
	SectionIncome      = "categories.income"
	SectionParties     = "parties"
	SectionTags        = "tags"
	SectionDefaults    = "defaults"
	SectionRows        = "rows"
)

// The keys of the defaults section.
const (
	DefaultFeeCategory      = "fee_category"
	DefaultInterestCategory = "interest_category"
	DefaultRefundCategory   = "refund_category"
)

// EntryRef names an entry of a mapping: the section it is in, and its key there (for
// the defaults, which of them it is).
type EntryRef struct {
	Section string
	Key     string
}

func (e EntryRef) String() string { return e.Section + "." + e.Key }

// Items lists every entry of the mapping, section by section in the order of the
// file, each section in the order of its keys. A default is an entry when it is set.
// It is the one way entries are counted and listed: what Plan has to add to a
// mapping, and what a refusal tells the user to look at, are made of it.
func (m Mapping) Items() []EntryRef {
	var out []EntryRef
	add := func(section string, keys []string) {
		for _, k := range keys {
			out = append(out, EntryRef{Section: section, Key: k})
		}
	}
	add(SectionInstruments, sortedKeys(m.Instruments))
	add(SectionAccounts, sortedKeys(m.Accounts))
	add(SectionProjects, sortedKeys(m.Projects))
	add(SectionExpense, sortedKeys(m.Expense))
	add(SectionIncome, sortedKeys(m.Income))
	add(SectionParties, sortedKeys(m.Parties))
	add(SectionTags, sortedKeys(m.Tags))
	for _, d := range []struct{ key, value string }{
		{DefaultFeeCategory, m.Defaults.FeeCategory},
		{DefaultInterestCategory, m.Defaults.InterestCategory},
		{DefaultRefundCategory, m.Defaults.RefundCategory},
	} {
		if d.value != "" {
			out = append(out, EntryRef{Section: SectionDefaults, Key: d.key})
		}
	}
	add(SectionRows, sortedKeys(m.Rows))
	return out
}

// Entries is how many entries the mapping has: len(Items()).
func (m Mapping) Entries() int { return len(m.Items()) }

// Empty tells whether the mapping has nothing in it.
func (m Mapping) Empty() bool { return m.Entries() == 0 }

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

// ProblemCode says what is wrong with the mapping or the rates. The text is the
// business of whoever shows it, not of the model: a problem is a code, where it is,
// and the values the text fills in, which each code says.
type ProblemCode string

// The problems of a mapping and of rates.
const (
	// ProblemNameEmpty is an entry that has no name.
	ProblemNameEmpty ProblemCode = "name_empty"
	// ProblemKindInvalid is a kind that is not one the ledger has. Args: the kind.
	ProblemKindInvalid ProblemCode = "kind_invalid"
	// ProblemDecimalsRange is a number of decimals that is not from 0 to 18. Args: it.
	ProblemDecimalsRange ProblemCode = "decimals_range"
	// ProblemDayRange is a day of a card's statement that is not from 1 to 31. Args:
	// the key (closing_day or payment_due_day) and the day.
	ProblemDayRange ProblemCode = "day_range"
	// ProblemCardOnlyDays is a closing or payment due day on an account that is not
	// a card.
	ProblemCardOnlyDays ProblemCode = "card_only_days"
	// ProblemDueNeedsClosing is a payment due day with no closing day.
	ProblemDueNeedsClosing ProblemCode = "due_needs_closing"
	// ProblemAccountConflict is an account of the ledger that two entries make in two
	// ways. Args: its name, and the kind and the institution the other entry gave.
	ProblemAccountConflict ProblemCode = "account_conflict"
	// ProblemDealingsNeedsParty is a dealings project, or a dealing, with no party.
	ProblemDealingsNeedsParty ProblemCode = "dealings_needs_party"
	// ProblemDefaultAreaUnknown is a default area that no entry of the mapping makes.
	// Args: the area.
	ProblemDefaultAreaUnknown ProblemCode = "default_area_unknown"
	// ProblemOnlyProjectHasArea is a default area on what is not a project.
	ProblemOnlyProjectHasArea ProblemCode = "only_project_has_area"
	// ProblemTreatmentInvalid is a treatment that is not one that entry can have.
	// Args: the treatment.
	ProblemTreatmentInvalid ProblemCode = "treatment_invalid"
	// ProblemCategoryEmpty is a category entry that makes no category.
	ProblemCategoryEmpty ProblemCode = "category_empty"
	// ProblemOverrideNotFit is a row override that asks for what the type of its row
	// cannot be. Args: the treatment and the type of the row.
	ProblemOverrideNotFit ProblemCode = "override_not_fit"
	// ProblemRateMissing is a currency with no rate on or before the first day it
	// is needed. Args: the code of the rates and the day.
	ProblemRateMissing ProblemCode = "rate_missing"
)

// Problem is something wrong with the mapping or the rates that has to be put right
// before anything is recorded.
type Problem struct {
	Code ProblemCode
	// Subject is where it is: the entry of the mapping, as the file names it
	// ("accounts.Name"), or the rates ("rates.USD").
	Subject string
	// Args are the values the text of the problem fills in; see the code.
	Args []string
}

// Validate checks that what the mapping says can be made of it, and returns every
// problem it finds: kinds that are not kinds, names that are empty, a card day out
// of range, an area that no entry makes, an account that two entries make in two
// ways.
func (m Mapping) Validate() []Problem {
	var problems []Problem
	add := func(subject string, code ProblemCode, args ...string) {
		problems = append(problems, Problem{Code: code, Subject: subject, Args: args})
	}
	m.validateInstruments(add)
	m.validateAccounts(add)
	m.validateProjects(add)
	validateCategories(SectionExpense, m.Expense, add)
	validateCategories(SectionIncome, m.Income, add)
	m.validateRows(add)
	return problems
}

type addProblem func(subject string, code ProblemCode, args ...string)

func (m Mapping) validateInstruments(add addProblem) {
	for _, code := range sortedKeys(m.Instruments) {
		in := m.Instruments[code]
		subject := SectionInstruments + "." + code
		if strings.TrimSpace(in.Name) == "" {
			add(subject, ProblemNameEmpty)
		}
		if !slices.Contains(InstrumentKinds, in.Kind) {
			add(subject, ProblemKindInvalid, in.Kind)
		}
		if in.Decimals < 0 || in.Decimals > 18 {
			add(subject, ProblemDecimalsRange, strconv.Itoa(in.Decimals))
		}
	}
}

func (m Mapping) validateAccounts(add addProblem) {
	type shape struct{ kind, institution string }
	byName := map[string]shape{}
	for _, name := range sortedKeys(m.Accounts) {
		a := m.Accounts[name]
		subject := SectionAccounts + "." + name
		if strings.TrimSpace(a.Name) == "" {
			add(subject, ProblemNameEmpty)
			continue
		}
		if !slices.Contains(AccountKinds, a.Kind) {
			add(subject, ProblemKindInvalid, a.Kind)
		}
		for _, day := range []struct {
			key string
			n   int
		}{{"closing_day", a.ClosingDay}, {"payment_due_day", a.PaymentDueDay}} {
			if day.n != 0 && (day.n < 1 || day.n > 31) {
				add(subject, ProblemDayRange, day.key, strconv.Itoa(day.n))
			}
		}
		if (a.ClosingDay != 0 || a.PaymentDueDay != 0) && a.Kind != "card" {
			add(subject, ProblemCardOnlyDays)
		}
		if a.PaymentDueDay != 0 && a.ClosingDay == 0 {
			add(subject, ProblemDueNeedsClosing)
		}
		// MOZE accounts that are one account of the ledger must agree on what it is.
		s := shape{a.Kind, a.Institution}
		if prev, ok := byName[a.Name]; ok && prev != s {
			add(subject, ProblemAccountConflict, a.Name, prev.kind, prev.institution)
		}
		byName[a.Name] = s
	}
}

func (m Mapping) validateProjects(add addProblem) {
	areas := map[string]bool{}
	for _, value := range sortedKeys(m.Projects) {
		if p := m.Projects[value]; p.Kind == ProjectArea {
			areas[p.Name] = true
		}
	}
	for _, value := range sortedKeys(m.Projects) {
		p := m.Projects[value]
		subject := SectionProjects + "." + value
		switch p.Kind {
		case ProjectArea, ProjectProject, ProjectTag:
			if strings.TrimSpace(p.Name) == "" {
				add(subject, ProblemNameEmpty)
			}
		case ProjectDealings:
			if strings.TrimSpace(p.Party) == "" {
				add(subject, ProblemDealingsNeedsParty)
			}
		case ProjectNone:
		default:
			add(subject, ProblemKindInvalid, p.Kind)
		}
		if p.Kind == ProjectProject && p.DefaultArea != "" && !areas[p.DefaultArea] {
			add(subject, ProblemDefaultAreaUnknown, p.DefaultArea)
		}
		if p.Kind != ProjectProject && p.DefaultArea != "" {
			add(subject, ProblemOnlyProjectHasArea)
		}
	}
}

func validateCategories(section string, entries map[string]CategoryMap, add addProblem) {
	for _, key := range sortedKeys(entries) {
		c := entries[key]
		subject := section + "." + key
		switch c.Treatment {
		case "", TreatmentReward, TreatmentDealing:
		default:
			add(subject, ProblemTreatmentInvalid, c.Treatment)
		}
		if c.Treatment == TreatmentDealing && strings.TrimSpace(c.Party) == "" {
			add(subject, ProblemDealingsNeedsParty)
		}
		if c.Treatment == "" {
			if parent, _ := SplitCategory(c.Category); parent == "" {
				add(subject, ProblemCategoryEmpty)
			}
		}
	}
}

func (m Mapping) validateRows(add addProblem) {
	for _, key := range sortedKeys(m.Rows) {
		o := m.Rows[key]
		subject := SectionRows + "." + key
		switch o.Treatment {
		case "", TreatmentIncome, TreatmentExpense, TreatmentReward, TreatmentDealing, TreatmentSkip:
		default:
			add(subject, ProblemTreatmentInvalid, o.Treatment)
		}
		if o.Treatment == TreatmentDealing && strings.TrimSpace(o.Party) == "" {
			add(subject, ProblemDealingsNeedsParty)
		}
	}
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
