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
	paidForSuffix        = "代付"
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

// GuessAccount guesses what an account of MOZE is from its name: the kind from what
// the name says it is, the institution from what comes before that.
func GuessAccount(name string) model.AccountMap {
	return GuessAccountHolding(name, nil)
}

// GuessAccountHolding is GuessAccount for an account that holds the currencies: a
// name that says nothing of what the account is, and holds only cryptocurrencies, is
// an exchange.
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

// guessProject guesses what a value of the 專案 column is, from its name and the
// days of the rows that carry it.
func guessProject(value string, days []time.Time) model.ProjectMap {
	name := CleanName(value)
	if m := dealingProject.FindStringSubmatch(value); m != nil {
		return model.ProjectMap{Kind: model.ProjectDealings, Party: strings.TrimSpace(CleanName(m[1]))}
	}
	if tagProjects[value] {
		return model.ProjectMap{Kind: model.ProjectTag, Name: name}
	}
	startsWithDate := datePrefix.FindString(value) != "" && datePrefix.FindString(value) != value
	if startsWithDate || tripWords.MatchString(value) || projectSuffix.MatchString(value) {
		p := model.ProjectMap{Kind: model.ProjectProject, Name: name}
		p.ActiveFrom, p.ActiveTo = evidentPeriod(days)
		return p
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
	guessed = model.Mapping{
		Instruments: map[string]model.InstrumentMap{},
		Accounts:    map[string]model.AccountMap{},
		Projects:    map[string]model.ProjectMap{},
		Expense:     map[string]model.CategoryMap{},
		Income:      map[string]model.CategoryMap{},
		Parties:     map[string]string{},
		Tags:        map[string]string{},
		Rows:        map[string]model.RowOverride{},
	}

	held := map[string][]string{}
	projectDays := map[string][]time.Time{}
	interestPath := ""
	for _, r := range rows {
		if r.Currency != model.BaseCurrencyCode {
			if _, ok := user.Instruments[r.Currency]; !ok {
				guessed.Instruments[r.Currency] = GuessInstrument(r.Currency)
			}
		}
		if _, ok := user.Accounts[r.Account]; !ok {
			held[r.Account] = append(held[r.Account], r.Currency)
		}
		if r.Project != "" {
			if r.HasDate() {
				projectDays[r.Project] = append(projectDays[r.Project], r.Date)
			} else if _, seen := projectDays[r.Project]; !seen {
				projectDays[r.Project] = nil
			}
		}
		switch r.Type {
		case model.TypeExpense:
			key := model.CategoryKey(r.Main, r.Sub)
			if _, ok := user.Expense[key]; !ok {
				guessed.Expense[key] = model.CategoryMap{Category: expensePath(r.Main, r.Sub)}
			}
		case model.TypeIncome:
			key := model.CategoryKey(r.Main, r.Sub)
			if _, ok := user.Income[key]; !ok {
				guessed.Income[key] = guessIncome(r.Main, r.Sub)
			}
		case model.TypeInterest:
			if interestPath == "" {
				interestPath = expensePath(r.Main, r.Sub)
			}
		}
		if r.Party != "" {
			if _, ok := user.Parties[r.Party]; !ok {
				guessed.Parties[r.Party] = r.Party
			}
		}
		for _, t := range r.Tags {
			if _, ok := user.Tags[t]; !ok {
				guessed.Tags[t] = t
			}
		}
	}
	for value, days := range projectDays {
		if _, ok := user.Projects[value]; !ok {
			guessed.Projects[value] = guessProject(value, days)
		}
	}

	for name, currencies := range held {
		guessed.Accounts[name] = GuessAccountHolding(name, currencies)
	}
	shareInstitutions(guessed.Accounts)
	// An account of MOZE that the guess puts in an account of the ledger that the user
	// has made already is that account, and takes what the user said of it.
	userNames := make([]string, 0, len(user.Accounts))
	for n := range user.Accounts {
		userNames = append(userNames, n)
	}
	slices.Sort(userNames)
	own := map[string]model.AccountMap{}
	for _, n := range userNames {
		if _, seen := own[user.Accounts[n].Name]; !seen {
			own[user.Accounts[n].Name] = user.Accounts[n]
		}
	}
	for name, g := range guessed.Accounts {
		if mine, ok := own[g.Name]; ok {
			guessed.Accounts[name] = mine
		}
	}

	// A conversion of a bill into instalments is one plan of one account in the ledger:
	// where its rows are in accounts of MOZE that the user has not mapped yet, the
	// guess puts them in the account the bill was converted in.
	groups, _ := findInstalments(rows)
	for _, g := range groups {
		main := g.mainAccount()
		entry, ok := user.Accounts[main]
		if !ok {
			entry, ok = guessed.Accounts[main]
		}
		if !ok {
			continue
		}
		for _, r := range g.rows() {
			if _, own := user.Accounts[r.Account]; !own && r.Account != main {
				guessed.Accounts[r.Account] = entry
			}
		}
	}

	// A project goes to the default area of its own kind of thing, when the mapping
	// has the area.
	areas := map[string]bool{}
	for _, p := range user.Projects {
		if p.Kind == model.ProjectArea {
			areas[p.Name] = true
		}
	}
	for _, p := range guessed.Projects {
		if p.Kind == model.ProjectArea {
			areas[p.Name] = true
		}
	}
	for value, p := range guessed.Projects {
		if p.Kind == model.ProjectProject && p.DefaultArea == "" {
			p.DefaultArea = guessDefaultArea(p, areas)
			guessed.Projects[value] = p
		}
	}

	defaults := user.Defaults
	if defaults.FeeCategory == "" {
		defaults.FeeCategory = "手續費"
		guessed.Defaults.FeeCategory = defaults.FeeCategory
	}
	if defaults.InterestCategory == "" {
		defaults.InterestCategory = "利息"
		if interestPath != "" {
			defaults.InterestCategory = interestPath
		}
		guessed.Defaults.InterestCategory = defaults.InterestCategory
	}
	if defaults.RefundCategory == "" {
		defaults.RefundCategory = "退款"
		guessed.Defaults.RefundCategory = defaults.RefundCategory
	}
	effective = user.Merge(guessed)
	effective.Defaults = defaults
	return effective, guessed
}

// quantityDecimals tells whether a quantity has no more decimal places than allowed.
func quantityDecimals(q decimal.Decimal, decimals int) bool {
	return q.Round(int32(decimals)).Equal(q)
}
