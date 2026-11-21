package service

import (
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/shopspring/decimal"

	"github.com/maxjkfc/maxos/app/importer/domain/model"
)

// The words of the user's MOZE that the guesses know. Everything a guess gives is
// marked as one when it is written, and the user reviews it.
const (
	pendingIncomeSub     = "收款"
	pendingIncomeName    = "收款（待確認）"
	pendingIncomeTag     = "待確認"
	creditCardOffsetSub  = "信用卡抵扣"
	companyCollectSuffix = "代收款"
	exchangeSub          = "兌換"
	investmentSub        = "投資"
	genericIncomeMain    = "收入"
)

// tagProjects are the values of 專案 that are a tag more than a part of life.
var tagProjects = map[string]bool{"募資商品": true}

// instrumentGuesses are the instruments a guess knows by their code; anything else
// is a cryptocurrency that is not a currency.
var instrumentGuesses = map[string]model.InstrumentMap{
	"USD":  {Name: "美元", Kind: "fiat", IsCurrency: true, Decimals: 2},
	"JPY":  {Name: "日圓", Kind: "fiat", IsCurrency: true, Decimals: 0},
	"EUR":  {Name: "歐元", Kind: "fiat", IsCurrency: true, Decimals: 2},
	"GBP":  {Name: "英鎊", Kind: "fiat", IsCurrency: true, Decimals: 2},
	"CNY":  {Name: "人民幣", Kind: "fiat", IsCurrency: true, Decimals: 2},
	"HKD":  {Name: "港幣", Kind: "fiat", IsCurrency: true, Decimals: 2},
	"KRW":  {Name: "韓元", Kind: "fiat", IsCurrency: true, Decimals: 0},
	"USDT": {Name: "Tether USDT", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
	"USDC": {Name: "USD Coin", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
	"DAI":  {Name: "Dai", Kind: "crypto", IsCurrency: true, Decimals: 2, RateAs: "USD"},
}

// GuessInstrument guesses what the instrument with the code is.
func GuessInstrument(code string) model.InstrumentMap {
	if in, ok := instrumentGuesses[code]; ok {
		return in
	}
	return model.InstrumentMap{Name: code, Kind: "crypto", IsCurrency: false, Decimals: 8}
}

// kindWords are the words of an account's name that say what kind it is, in the
// order they are tried.
var kindWords = []struct {
	kind  string
	words *regexp.Regexp
}{
	{"loan", regexp.MustCompile(`貸款|(?i)loan`)},
	{"brokerage", regexp.MustCompile(`證卷|證券|證劵|券商|(?i)brokerage`)},
	{"card", regexp.MustCompile(`信用卡|卡$|(?i)card$`)},
	{"exchange", regexp.MustCompile(`交易所|(?i)exchange`)},
	{"wallet", regexp.MustCompile(`(?i)wallet|metamask|ledger|trezor`)},
	{"cash", regexp.MustCompile(`現金|錢包|(?i)cash`)},
	{"bank", regexp.MustCompile(`銀行|(?i)bank|帳戶|活存|活期|存款|郵局`)},
}

// institutionStop is where the name of an institution ends in the name of an account:
// at a word that says what the account is, or of what it holds.
var institutionStop = regexp.MustCompile(`信用卡|銀行|證卷|證券|證劵|活期|帳戶|美金|日幣|外幣|主帳戶|卡$|\s`)

// GuessAccountHolding guesses what an account of MOZE is from its name and from the
// currencies it holds: the kind from what the name says it is, and the institution
// from what comes before that. A name that says nothing of what the account is, on an
// account that holds only cryptocurrencies, is an exchange.
func GuessAccountHolding(name string, currencies []string) model.AccountMap {
	kind, named := "bank", false
	for _, k := range kindWords {
		if k.words.MatchString(name) {
			kind, named = k.kind, true
			break
		}
	}
	if !named && len(currencies) > 0 && allCrypto(currencies) {
		kind = "exchange"
	}
	account := model.AccountMap{Name: name, Kind: kind}
	switch kind {
	case "bank", "card", "brokerage", "loan", "exchange":
		account.Institution = guessInstitution(name)
	}
	return account
}

// allCrypto tells whether every code is of a cryptocurrency.
func allCrypto(codes []string) bool {
	for _, c := range codes {
		if c == model.BaseCurrencyCode || GuessInstrument(c).Kind != "crypto" {
			return false
		}
	}
	return true
}

// shareInstitutions gives the accounts whose names start with the same two Chinese
// characters that institution: the name of a bank is what its accounts begin with,
// and the guess of each alone only knows where its name stops.
func shareInstitutions(accounts map[string]model.AccountMap) {
	groups := map[string][]string{}
	for name, a := range accounts {
		if a.Institution == "" {
			continue
		}
		runes := []rune(name)
		if len(runes) >= 2 && unicode.Is(unicode.Han, runes[0]) && unicode.Is(unicode.Han, runes[1]) {
			groups[string(runes[:2])] = append(groups[string(runes[:2])], name)
		}
	}
	for prefix, names := range groups {
		if len(names) < 2 {
			continue
		}
		for _, name := range names {
			a := accounts[name]
			a.Institution = prefix
			accounts[name] = a
		}
	}
}

// guessInstitution is the start of the name of an account, up to the word that says
// what it is, or to where Latin letters follow Chinese ones.
func guessInstitution(name string) string {
	end := len(name)
	if loc := institutionStop.FindStringIndex(name); loc != nil {
		end = loc[0]
	}
	var out []rune
	prevHan := false
	for i, r := range name {
		if i >= end {
			break
		}
		if prevHan && r < unicode.MaxASCII && unicode.IsLetter(r) {
			break
		}
		prevHan = unicode.Is(unicode.Han, r)
		out = append(out, r)
	}
	return strings.TrimSpace(string(out))
}

var (
	datePrefix     = regexp.MustCompile(`^\d{4}([-/]\d{1,2}([-/]\d{1,2})?)?\s*`)
	projectSuffix  = regexp.MustCompile(`專案$`)
	spaces         = regexp.MustCompile(`\s+`)
	tripWords      = regexp.MustCompile(`之旅|旅行|遊記|(?i)trip`)
	tripAreaWords  = regexp.MustCompile(`旅|遊`)
	careAreaWords  = regexp.MustCompile(`開刀|醫|病|手術`)
	dealingProject = regexp.MustCompile(`^(.+)代付$`)
)

// CleanName makes the name of an area or a project of the value of 專案: without the
// symbols and the emoji MOZE lets one write, without the date it starts with, and
// without the word 專案.
func CleanName(value string) string {
	var b strings.Builder
	for _, r := range value {
		if unicode.Is(unicode.So, r) || unicode.Is(unicode.Sk, r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Cf, r) {
			continue
		}
		b.WriteRune(r)
	}
	name := strings.TrimSpace(spaces.ReplaceAllString(b.String(), " "))
	name = datePrefix.ReplaceAllString(name, "")
	name = projectSuffix.ReplaceAllString(name, "")
	name = strings.TrimSpace(name)
	if name == "" {
		return strings.TrimSpace(value)
	}
	return name
}

// guessProject guesses what a value of the 專案 column is, from its name. It never
// gives a project an active period, however clear the days of its rows are: the
// ledger puts every transaction that has no project of its own into the project that
// is active on its day, which would pull in transactions that are no part of it.
func guessProject(value string) model.ProjectMap {
	name := CleanName(value)
	if m := dealingProject.FindStringSubmatch(value); m != nil {
		return model.ProjectMap{Kind: model.ProjectDealings, Party: strings.TrimSpace(CleanName(m[1]))}
	}
	if tagProjects[value] {
		return model.ProjectMap{Kind: model.ProjectTag, Name: name}
	}
	startsWithDate := datePrefix.FindString(value) != "" && datePrefix.FindString(value) != value
	if startsWithDate || tripWords.MatchString(value) || projectSuffix.MatchString(value) {
		return model.ProjectMap{Kind: model.ProjectProject, Name: name}
	}
	return model.ProjectMap{Kind: model.ProjectArea, Name: name}
}

// guessDefaultArea guesses the area a project goes to by default: leisure for what
// is a trip, the family for what is care, and only if the mapping has such an area.
func guessDefaultArea(project model.ProjectMap, areas map[string]bool) string {
	for _, c := range []struct {
		words *regexp.Regexp
		area  string
	}{
		{careAreaWords, "家庭"},
		{tripAreaWords, "玩樂"},
	} {
		if c.words.MatchString(project.Name) && areas[c.area] {
			return c.area
		}
	}
	return ""
}

// evidentPeriod finds when a project was under way from the days of its rows: the
// longest run of consecutive days that holds 80% of the rows or more, if there is one
// of two days or longer. A trip that was paid for in advance has rows long before it;
// a project that is not a run of days (a surgery with its visits) has none.
func evidentPeriod(days []time.Time) (from, to time.Time) {
	if len(days) < 2 {
		return time.Time{}, time.Time{}
	}
	count := map[time.Time]int{}
	var distinct []time.Time
	for _, d := range days {
		if count[d] == 0 {
			distinct = append(distinct, d)
		}
		count[d]++
	}
	slices.SortFunc(distinct, time.Time.Compare)

	bestFrom, bestTo, bestRows := time.Time{}, time.Time{}, 0
	for i := 0; i < len(distinct); {
		j, rows := i, 0
		for ; j < len(distinct); j++ {
			if j > i && distinct[j].Sub(distinct[j-1]) > 24*time.Hour {
				break
			}
			rows += count[distinct[j]]
		}
		if rows > bestRows {
			bestFrom, bestTo, bestRows = distinct[i], distinct[j-1], rows
		}
		i = j
	}
	if bestTo.Equal(bestFrom) || bestRows*10 < len(days)*8 {
		return time.Time{}, time.Time{}
	}
	return bestFrom, bestTo
}

// projectEvidence says, for every value of 專案 that the rows have, whatever the
// mapping makes of it, how many rows with a day carry it, the first and the last of
// those days, and the period they would suggest. It is for the report alone.
func projectEvidence(rows []model.Row) []model.ProjectEvidence {
	days := map[string][]time.Time{}
	for _, r := range rows {
		if r.Project == "" {
			continue
		}
		list := days[r.Project] // nothing yet for a value that only rows with no day have
		if r.HasDate() {
			list = append(list, r.Date)
		}
		days[r.Project] = list
	}
	var evidence []model.ProjectEvidence
	for _, value := range sortedStringKeys(days) {
		list := days[value]
		e := model.ProjectEvidence{Value: value, Rows: len(list)}
		if len(list) > 0 {
			e.First = slices.MinFunc(list, time.Time.Compare)
			e.Last = slices.MaxFunc(list, time.Time.Compare)
			e.From, e.To = evidentPeriod(list)
		}
		evidence = append(evidence, e)
	}
	return evidence
}

// expensePath guesses the category of the ledger of an expense of MOZE: the two
// levels it has, or one when the two are the same word.
func expensePath(main, sub string) string {
	if sub == "" || sub == main {
		return main
	}
	return main + "/" + sub
}

// incomePath is expensePath for an income, where the main category of MOZE is just
// the word 收入 and says nothing more than the direction.
func incomePath(main, sub string) string {
	switch {
	case sub == "":
		return main
	case main == genericIncomeMain || main == sub:
		return sub
	default:
		return main + "/" + sub
	}
}

// guessIncome guesses what an income category of MOZE is.
func guessIncome(main, sub string) model.CategoryMap {
	switch {
	case sub == pendingIncomeSub:
		return model.CategoryMap{Category: pendingIncomeName, Tag: pendingIncomeTag}
	case sub == creditCardOffsetSub:
		return model.CategoryMap{Treatment: model.TreatmentReward}
	case strings.HasSuffix(sub, companyCollectSuffix) && sub != companyCollectSuffix:
		return model.CategoryMap{Treatment: model.TreatmentDealing, Party: strings.TrimSuffix(sub, companyCollectSuffix)}
	}
	return model.CategoryMap{Category: incomePath(main, sub)}
}

// complete returns the mapping the plan is made with, which is the user's with the
// guess for each thing in the rows that it has no entry for, and the guesses alone.
func complete(rows []model.Row, user model.Mapping) (effective, guessed model.Mapping) {
	defaults, guessedDefaults := guessDefaults(rows, user)
	guessed = model.Mapping{
		Instruments: guessInstruments(rows, user),
		Accounts:    guessAccounts(rows, user),
		Projects:    guessProjects(rows, user),
		Parties:     guessParties(rows, user),
		Tags:        guessTags(rows, user),
		Defaults:    guessedDefaults,
		Rows:        map[string]model.RowOverride{},
	}
	guessed.Expense, guessed.Income = guessCategories(rows, user)
	effective = user.Merge(guessed)
	effective.Defaults = defaults
	return effective, guessed
}

// guessInstruments guesses the instruments of the rows that the mapping has no entry
// for. The base currency has none to guess.
func guessInstruments(rows []model.Row, user model.Mapping) map[string]model.InstrumentMap {
	guessed := map[string]model.InstrumentMap{}
	for _, r := range rows {
		if _, ok := user.Instruments[r.Currency]; !ok && r.Currency != model.BaseCurrencyCode {
			guessed[r.Currency] = GuessInstrument(r.Currency)
		}
	}
	return guessed
}

// guessAccounts guesses the accounts of the rows that the mapping has no entry for:
// by their names and by what they hold, with the institution that accounts of alike
// names share; as the user's, when the ledger account they are is one the user made;
// and as one, when they are the accounts of the rows of one instalment plan.
func guessAccounts(rows []model.Row, user model.Mapping) map[string]model.AccountMap {
	held := map[string][]string{}
	for _, r := range rows {
		if _, ok := user.Accounts[r.Account]; !ok {
			held[r.Account] = append(held[r.Account], r.Currency)
		}
	}
	guessed := make(map[string]model.AccountMap, len(held))
	for name, currencies := range held {
		guessed[name] = GuessAccountHolding(name, currencies)
	}
	shareInstitutions(guessed)
	takeWhatTheUserSaid(guessed, user.Accounts)
	alignInstalments(guessed, user.Accounts, rows)
	return guessed
}

// takeWhatTheUserSaid makes an account of MOZE that the guess puts in an account of the
// ledger that the user has made already that account, with what the user said of it.
func takeWhatTheUserSaid(guessed, user map[string]model.AccountMap) {
	own := map[string]model.AccountMap{}
	for _, n := range sortedStringKeys(user) {
		if _, seen := own[user[n].Name]; !seen {
			own[user[n].Name] = user[n]
		}
	}
	for name, g := range guessed {
		if mine, ok := own[g.Name]; ok {
			guessed[name] = mine
		}
	}
}

// alignInstalments puts the rows of a conversion of a bill into instalments in one
// account of the ledger, as the plan is of one: where they are in accounts of MOZE
// that the user has not mapped yet, the guess is the account the bill was converted
// in.
func alignInstalments(guessed, user map[string]model.AccountMap, rows []model.Row) {
	groups, _ := findInstalments(rows)
	for _, g := range groups {
		main := g.mainAccount()
		entry, ok := user[main]
		if !ok {
			entry, ok = guessed[main]
		}
		if !ok {
			continue
		}
		for _, r := range g.rows() {
			if _, own := user[r.Account]; !own && r.Account != main {
				guessed[r.Account] = entry
			}
		}
	}
}

// guessProjects guesses the values of 專案 that the mapping has no entry for, and
// for the projects among them the default area of their own kind of thing, when the
// mapping has the area.
func guessProjects(rows []model.Row, user model.Mapping) map[string]model.ProjectMap {
	guessed := map[string]model.ProjectMap{}
	for _, r := range rows {
		if _, known := user.Projects[r.Project]; r.Project == "" || known {
			continue
		}
		if _, done := guessed[r.Project]; !done {
			guessed[r.Project] = guessProject(r.Project)
		}
	}
	areas := map[string]bool{}
	for _, projects := range []map[string]model.ProjectMap{user.Projects, guessed} {
		for _, p := range projects {
			if p.Kind == model.ProjectArea {
				areas[p.Name] = true
			}
		}
	}
	for value, p := range guessed {
		if p.Kind == model.ProjectProject && p.DefaultArea == "" {
			p.DefaultArea = guessDefaultArea(p, areas)
			guessed[value] = p
		}
	}
	return guessed
}

// guessCategories guesses the categories of the expenses and of the incomes of the rows
// that the mapping has no entry for.
func guessCategories(rows []model.Row, user model.Mapping) (expense, income map[string]model.CategoryMap) {
	expense, income = map[string]model.CategoryMap{}, map[string]model.CategoryMap{}
	for _, r := range rows {
		key := model.CategoryKey(r.Main, r.Sub)
		switch r.Type {
		case model.TypeExpense:
			if _, ok := user.Expense[key]; !ok {
				expense[key] = model.CategoryMap{Category: expensePath(r.Main, r.Sub)}
			}
		case model.TypeIncome:
			if _, ok := user.Income[key]; !ok {
				income[key] = guessIncome(r.Main, r.Sub)
			}
		}
	}
	return expense, income
}

// guessParties guesses that the parties of the rows that the mapping has no entry for
// are called in the ledger what MOZE calls them.
func guessParties(rows []model.Row, user model.Mapping) map[string]string {
	guessed := map[string]string{}
	for _, r := range rows {
		if _, ok := user.Parties[r.Party]; r.Party != "" && !ok {
			guessed[r.Party] = r.Party
		}
	}
	return guessed
}

// guessTags guesses that the tags of the rows that the mapping has no entry for are
// called in the ledger what MOZE calls them.
func guessTags(rows []model.Row, user model.Mapping) map[string]string {
	guessed := map[string]string{}
	for _, r := range rows {
		for _, t := range r.Tags {
			if _, ok := user.Tags[t]; !ok {
				guessed[t] = t
			}
		}
	}
	return guessed
}

// guessDefaults gives the categories of what has none of its own: the user's, and for
// each that the user has not set, a guess, which is also returned alone. The guess for
// interest is the category of the first interest the rows have.
func guessDefaults(rows []model.Row, user model.Mapping) (defaults, guessed model.Defaults) {
	defaults = user.Defaults
	if defaults.FeeCategory == "" {
		defaults.FeeCategory = "手續費"
		guessed.FeeCategory = defaults.FeeCategory
	}
	if defaults.InterestCategory == "" {
		defaults.InterestCategory = "利息"
		for _, r := range rows {
			if path := expensePath(r.Main, r.Sub); r.Type == model.TypeInterest && path != "" {
				defaults.InterestCategory = path
				break
			}
		}
		guessed.InterestCategory = defaults.InterestCategory
	}
	if defaults.RefundCategory == "" {
		defaults.RefundCategory = "退款"
		guessed.RefundCategory = defaults.RefundCategory
	}
	return defaults, guessed
}

// quantityDecimals tells whether a quantity has no more decimal places than allowed.
func quantityDecimals(q decimal.Decimal, decimals int) bool {
	return q.Round(int32(decimals)).Equal(q)
}
